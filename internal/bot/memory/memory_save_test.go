package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"
)

// failingRepo 允许按方法注入错误，验证「查询失败不能被当成未命中」。
type failingRepo struct {
	dao.BotRepository

	memories []*botmodel.UserMemory

	byKeyErr  error
	byUserErr error
	setErr    error

	byKeyCalls int
	setCalls   []*botmodel.UserMemory
}

func (r *failingRepo) GetUserMemoryByKey(ctx context.Context, userID, botID, key string) (*botmodel.UserMemory, error) {
	r.byKeyCalls++
	if r.byKeyErr != nil {
		return nil, r.byKeyErr
	}
	for _, m := range r.memories {
		if m.Key == key {
			return m, nil
		}
	}
	return nil, nil
}

func (r *failingRepo) GetUserMemoriesByUser(ctx context.Context, userID, botID string) ([]*botmodel.UserMemory, error) {
	if r.byUserErr != nil {
		return nil, r.byUserErr
	}
	return r.memories, nil
}

func (r *failingRepo) SetUserMemory(ctx context.Context, memory *botmodel.UserMemory) error {
	r.setCalls = append(r.setCalls, memory)
	if r.setErr != nil {
		return r.setErr
	}
	for i, m := range r.memories {
		if m.Key == memory.Key {
			r.memories[i] = memory
			return nil
		}
	}
	r.memories = append(r.memories, memory)
	return nil
}

func saveMessages() []*botmodel.Message {
	return []*botmodel.Message{
		{ID: "msg-1", Content: "我每天早上都喝一杯咖啡"},
		{ID: "msg-2", Content: "不加糖"},
	}
}

func newSaveManager(repo dao.BotRepository) *MemoryManager {
	return &MemoryManager{repo: repo, now: time.Now}
}

// 查询已有记忆失败时不能退化成「新建」：否则会给同一个 key 插重复行。
func TestSaveExtractedMemories_KeyLookupErrorSkipsWrite(t *testing.T) {
	repo := &failingRepo{byKeyErr: errors.New("db unavailable")}
	mgr := newSaveManager(repo)

	stats := mgr.saveExtractedMemories(context.Background(), "u1", "b1", []ExtractedMemory{
		{Key: "喜欢喝咖啡", Value: "每天一杯", Confidence: 0.9},
	}, saveMessages())

	if stats.Failed != 1 || stats.Saved != 0 {
		t.Fatalf("查询失败应记为失败且不写入，实际 %+v", stats)
	}
	if len(repo.setCalls) != 0 {
		t.Fatalf("查询失败时不得写入任何记忆，实际写入 %d 条", len(repo.setCalls))
	}
}

// 快照加载失败时无法判断是否有近似条目，宁可跳过也不插重复。
func TestSaveExtractedMemories_SnapshotErrorSkipsWrite(t *testing.T) {
	repo := &failingRepo{byUserErr: errors.New("db unavailable")}
	mgr := newSaveManager(repo)

	stats := mgr.saveExtractedMemories(context.Background(), "u1", "b1", []ExtractedMemory{
		{Key: "喜欢喝咖啡", Value: "每天一杯", Confidence: 0.9},
	}, saveMessages())

	if stats.Failed != 1 || stats.Saved != 0 {
		t.Fatalf("快照失败应记为失败且不写入，实际 %+v", stats)
	}
	if len(repo.setCalls) != 0 {
		t.Fatalf("快照失败时不得写入任何记忆，实际写入 %d 条", len(repo.setCalls))
	}
}

func TestSaveExtractedMemories_MergesExactKey(t *testing.T) {
	repo := &failingRepo{memories: []*botmodel.UserMemory{
		{ID: "m1", Key: "喜欢喝咖啡", Value: "喜欢喝咖啡", Category: "preference",
			Source: "auto_extract", Confidence: 0.6, UpdatedAt: time.Now()},
	}}
	mgr := newSaveManager(repo)

	stats := mgr.saveExtractedMemories(context.Background(), "u1", "b1", []ExtractedMemory{
		{Key: "喜欢喝咖啡", Value: "每天一杯", Category: "preference", Confidence: 0.9},
	}, saveMessages())

	if stats.Saved != 1 || stats.Failed != 0 {
		t.Fatalf("精确键应合并成功，实际 %+v", stats)
	}
	if len(repo.memories) != 1 {
		t.Fatalf("合并不应新增行，实际 %d 行", len(repo.memories))
	}
}

func TestSaveExtractedMemories_MergesSimilarKey(t *testing.T) {
	repo := &failingRepo{memories: []*botmodel.UserMemory{
		{ID: "m1", Key: "喜欢喝咖啡", Value: "喜欢喝咖啡", Category: "preference",
			Source: "auto_extract", Confidence: 0.7, UpdatedAt: time.Now()},
	}}
	mgr := newSaveManager(repo)

	stats := mgr.saveExtractedMemories(context.Background(), "u1", "b1", []ExtractedMemory{
		{Key: "喜欢喝咖啡呢", Value: "每天一杯", Category: "preference", Confidence: 0.9},
	}, saveMessages())

	if stats.Merged != 1 || stats.Saved != 0 {
		t.Fatalf("近似键应走相似合并，实际 %+v", stats)
	}
	if len(repo.memories) != 1 {
		t.Fatalf("相似合并不应新增行，实际 %d 行", len(repo.memories))
	}
}

func TestSaveExtractedMemories_RespectsBatchLimit(t *testing.T) {
	repo := &failingRepo{}
	mgr := newSaveManager(repo)

	candidates := make([]ExtractedMemory, 0, maxMemoriesPerExtraction+5)
	for i := 0; i < maxMemoriesPerExtraction+5; i++ {
		candidates = append(candidates, ExtractedMemory{
			Key: "事实" + string(rune('a'+i)), Value: "值", Category: "fact", Confidence: 0.9,
		})
	}

	stats := mgr.saveExtractedMemories(context.Background(), "u1", "b1", candidates, saveMessages())

	if stats.Processed != maxMemoriesPerExtraction {
		t.Fatalf("单次最多处理 %d 条，实际 %d", maxMemoriesPerExtraction, stats.Processed)
	}
	if len(repo.memories) != maxMemoriesPerExtraction {
		t.Fatalf("单次最多写入 %d 条，实际 %d", maxMemoriesPerExtraction, len(repo.memories))
	}
}
