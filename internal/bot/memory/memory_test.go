package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/ai/bot/dao"
	botmodel "Logos/internal/service/ai/bot/model"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	logger.SetLogger(zap.NewNop())
	os.Exit(m.Run())
}

// mockMemoryRepo 嵌入接口实现最小 mock，未覆盖的方法一旦调用即 panic
type mockMemoryRepo struct {
	dao.BotRepository
	memories []*botmodel.UserMemory
	deleted  []string
}

func (r *mockMemoryRepo) GetUserMemoriesByUser(ctx context.Context, userID, botID string) ([]*botmodel.UserMemory, error) {
	return r.memories, nil
}

func (r *mockMemoryRepo) GetUserMemoryByKey(ctx context.Context, userID, botID, key string) (*botmodel.UserMemory, error) {
	for _, m := range r.memories {
		if m.Key == key {
			return m, nil
		}
	}
	return nil, nil
}

func (r *mockMemoryRepo) SetUserMemory(ctx context.Context, memory *botmodel.UserMemory) error {
	for i, m := range r.memories {
		if m.Key == memory.Key {
			r.memories[i] = memory
			return nil
		}
	}
	r.memories = append(r.memories, memory)
	return nil
}

func (r *mockMemoryRepo) DeleteUserMemoryByID(ctx context.Context, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func newTestManager(repo dao.BotRepository) *MemoryManager {
	return &MemoryManager{
		repo:       repo,
		processing: make(map[string]bool),
	}
}

func TestBuildMemoryPrompt_Empty(t *testing.T) {
	m := newTestManager(&mockMemoryRepo{})
	if got := m.BuildMemoryPrompt(context.Background(), "u1", "b1"); got != "" {
		t.Errorf("expected empty prompt for no memories, got %q", got)
	}
}

func TestBuildMemoryPrompt_GroupsByCategory(t *testing.T) {
	repo := &mockMemoryRepo{
		memories: []*botmodel.UserMemory{
			{Key: "favorite_language", Value: "Go", Category: "preference", Confidence: 0.9},
			{Key: "pet_name", Value: "小花", Category: "fact", Confidence: 0.9},
			{Key: "work_style", Value: "喜欢简洁", Category: "", Confidence: 0.9},
		},
	}
	m := newTestManager(repo)

	got := m.BuildMemoryPrompt(context.Background(), "u1", "b1")
	if got == "" {
		t.Fatal("expected non-empty prompt")
	}

	// 空分类应归入 other
	if !strings.Contains(got, "【其他】") {
		t.Errorf("empty category should be grouped as 其他, got %q", got)
	}
	// 内容应包含键值
	for _, want := range []string{"favorite_language", "Go", "pet_name", "小花", "work_style"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt should contain %q, got %q", want, got)
		}
	}
	// 分类标题
	if !strings.Contains(got, "【用户偏好】") || !strings.Contains(got, "【用户信息】") {
		t.Errorf("prompt should contain category headers, got %q", got)
	}
}

func TestGetMemoriesByCategory(t *testing.T) {
	repo := &mockMemoryRepo{
		memories: []*botmodel.UserMemory{
			{Key: "a", Value: "1", Category: "preference"},
			{Key: "b", Value: "2", Category: "fact"},
			{Key: "c", Value: "3", Category: "preference"},
		},
	}
	m := newTestManager(repo)

	got, err := m.GetMemoriesByCategory(context.Background(), "u1", "b1", "preference")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 preference memories, got %d", len(got))
	}
}

func TestCleanupOldMemories_ProtectsManualAndHighConfidence(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	repo := &mockMemoryRepo{
		memories: []*botmodel.UserMemory{
			// 应清理：自动提取 + 低置信度 + 过期
			{ID: "1", Key: "k1", Source: "auto_extract", Confidence: 0.3, UpdatedAt: old},
			// 应保留：手动添加（即使过期）
			{ID: "2", Key: "k2", Source: "manual", Confidence: 0.3, UpdatedAt: old},
			// 应保留：高置信度（即使过期）
			{ID: "3", Key: "k3", Source: "auto_extract", Confidence: 0.9, UpdatedAt: old},
			// 应保留：未过期
			{ID: "4", Key: "k4", Source: "auto_extract", Confidence: 0.3, UpdatedAt: time.Now()},
		},
	}
	m := newTestManager(repo)

	if err := m.CleanupOldMemories(context.Background(), "u1", "b1", time.Hour); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repo.deleted) != 1 || repo.deleted[0] != "1" {
		t.Fatalf("expected only memory '1' to be deleted, got %v", repo.deleted)
	}
}

func TestCleanupOldMemories_EmptyRepo(t *testing.T) {
	m := newTestManager(&mockMemoryRepo{})
	if err := m.CleanupOldMemories(context.Background(), "u1", "b1", time.Hour); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTrimJSON(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"  \n{\"a\":1}\n", `{"a":1}`},
		{"前置文字 {\"a\":1} 后置文字", `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"no json here", "no json here"},
		{"{", "{"},
	}
	for _, tt := range tests {
		if got := trimJSON(tt.in); got != tt.want {
			t.Errorf("trimJSON(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
