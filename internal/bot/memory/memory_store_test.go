package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
)

// failDeleteRepo 让指定 ID 的删除失败，用于验证清理任务不再吞错。
type failDeleteRepo struct {
	*mockMemoryRepo
	failID string
}

func (r *failDeleteRepo) DeleteUserMemoryByID(ctx context.Context, id string) error {
	if id == r.failID {
		return errors.New("db down")
	}
	return r.mockMemoryRepo.DeleteUserMemoryByID(ctx, id)
}

// TestBuildMemoryPrompt_SkipsNilMemories 仓储返回 nil 元素时不能 panic。
func TestBuildMemoryPrompt_SkipsNilMemories(t *testing.T) {
	repo := &mockMemoryRepo{memories: []*botmodel.UserMemory{
		nil,
		{Key: "k", Value: "v", Category: "preference", Confidence: 0.9},
	}}
	m := newTestManager(repo)

	got := m.BuildMemoryPrompt(context.Background(), "u1", "b1")
	if !strings.Contains(got, "v") {
		t.Fatalf("有效记忆未被注入: %q", got)
	}
}

// TestBuildMemoryPrompt_TieOrderStable 同置信度的记忆必须按确定顺序输出，
// 否则数据库返回顺序一变，同一用户的 prompt 就变。
func TestBuildMemoryPrompt_TieOrderStable(t *testing.T) {
	now := time.Now()
	repo := &mockMemoryRepo{memories: []*botmodel.UserMemory{
		{ID: "m3", Key: "k3", Value: "v3", Category: "fact", Confidence: 0.9, UpdatedAt: now},
		{ID: "m1", Key: "k1", Value: "v1", Category: "fact", Confidence: 0.9, UpdatedAt: now},
		{ID: "m2", Key: "k2", Value: "v2", Category: "fact", Confidence: 0.9, UpdatedAt: now},
	}}
	m := newTestManager(repo)

	first := m.BuildMemoryPrompt(context.Background(), "u1", "b1")
	if !(strings.Index(first, "v1") < strings.Index(first, "v2") &&
		strings.Index(first, "v2") < strings.Index(first, "v3")) {
		t.Fatalf("同分记忆未按 ID 升序: %q", first)
	}

	// 打乱输入顺序，输出必须保持一致
	repo.memories = []*botmodel.UserMemory{repo.memories[1], repo.memories[2], repo.memories[0]}
	second := m.BuildMemoryPrompt(context.Background(), "u1", "b1")
	if first != second {
		t.Fatal("输入顺序变化导致 prompt 变化")
	}
}

// TestSortMemories_ConfidenceFirst 置信度是主键，UpdatedAt/ID 只在同分时生效。
func TestSortMemories_ConfidenceFirst(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		{ID: "a", Confidence: 0.6, UpdatedAt: now},
		{ID: "b", Confidence: 0.9, UpdatedAt: now.Add(-time.Hour)},
		{ID: "c", Confidence: 0.6, UpdatedAt: now.Add(time.Minute)},
	}
	sortMemories(mems)

	if mems[0].ID != "b" {
		t.Fatalf("置信度最高的应排第一，got %s", mems[0].ID)
	}
	if mems[1].ID != "c" || mems[2].ID != "a" {
		t.Fatalf("同分应按 UpdatedAt 降序，got %s,%s", mems[1].ID, mems[2].ID)
	}
}

// TestCleanupOldMemories_ReportsDeleteErrors 单条删除失败要带回来，
// 但不应中断其余条目的清理。
func TestCleanupOldMemories_ReportsDeleteErrors(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	repo := &failDeleteRepo{
		mockMemoryRepo: &mockMemoryRepo{memories: []*botmodel.UserMemory{
			{ID: "bad", Source: "auto_extract", Confidence: 0.3, UpdatedAt: old},
			{ID: "ok", Source: "auto_extract", Confidence: 0.3, UpdatedAt: old},
		}},
		failID: "bad",
	}
	m := newTestManager(repo)

	err := m.CleanupOldMemories(context.Background(), "u1", "b1", 24*time.Hour)
	if err == nil {
		t.Fatal("删除失败必须返回 error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Fatalf("错误应包含失败的记忆 ID: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "ok" {
		t.Fatalf("成功条目应继续删除，deleted=%v", repo.deleted)
	}
}

// TestCleanupOldMemories_KeepsManualAndFresh 手工记忆与未过期记忆不能被删。
func TestCleanupOldMemories_KeepsManualAndFresh(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	repo := &mockMemoryRepo{memories: []*botmodel.UserMemory{
		{ID: "manual", Source: "manual", Confidence: 0.3, UpdatedAt: old},
		{ID: "fresh", Source: "auto_extract", Confidence: 0.3, UpdatedAt: time.Now()},
		{ID: "confident", Source: "auto_extract", Confidence: 0.9, UpdatedAt: old},
	}}
	m := newTestManager(repo)

	if err := m.CleanupOldMemories(context.Background(), "u1", "b1", 24*time.Hour); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("不应删除任何记忆，deleted=%v", repo.deleted)
	}
}

// TestGetMemoriesByCategory_SortedAndSkipsNil 管理页同样需要稳定顺序。
func TestGetMemoriesByCategory_SortedAndSkipsNil(t *testing.T) {
	repo := &mockMemoryRepo{memories: []*botmodel.UserMemory{
		nil,
		{ID: "z", Category: "preference", Confidence: 0.5},
		{ID: "a", Category: "preference", Confidence: 0.9},
		{ID: "x", Category: "fact", Confidence: 0.9},
	}}
	m := newTestManager(repo)

	got, err := m.GetMemoriesByCategory(context.Background(), "u1", "b1", "preference")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "z" {
		t.Fatalf("应按置信度降序且跳过 nil，got %+v", got)
	}
}

var _ dao.BotRepository = (*failDeleteRepo)(nil)
