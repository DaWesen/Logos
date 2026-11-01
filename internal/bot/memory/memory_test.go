package memory

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// TestBuildDialogText_PreservesOriginalSeq 截断后序号必须仍是原始下标：
// 模型引用 [k] 时，resolveEvidenceIDs 会拿它当原切片下标，平移即错挂证据。
func TestBuildDialogText_PreservesOriginalSeq(t *testing.T) {
	var messages []*botmodel.Message
	for i := 0; i < 10; i++ {
		messages = append(messages, &botmodel.Message{
			ID:      "m" + strconv.Itoa(i+1),
			Role:    "user",
			Content: strings.Repeat("内容", 60), // 每条约 130 rune，必然触发预算
		})
	}

	got := buildDialogText(messages, 400)
	lines := strings.Split(got, "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("未生成任何对话文本: %q", got)
	}

	first := strings.Index(lines[0], "[")
	if first != 0 {
		t.Fatalf("行首序号被截断: %q", lines[0])
	}
	var seq int
	if _, err := fmt.Sscanf(lines[0], "[%d]", &seq); err != nil {
		t.Fatalf("解析序号失败: %v, line=%q", err, lines[0])
	}
	if seq != 10-len(lines)+1 {
		t.Fatalf("序号平移了：首行 [%d]，共 %d 行（应为 [%d]）", seq, len(lines), 10-len(lines)+1)
	}
}

// TestBuildDialogText_ValidUTF8AndBudget 按 rune 计预算：中文不能被切成半个字，总量不超预算。
func TestBuildDialogText_ValidUTF8AndBudget(t *testing.T) {
	messages := []*botmodel.Message{
		{ID: "1", Role: "user", Content: "我平时喜欢用Go写后端"},
		{ID: "2", Role: "assistant", Content: strings.Repeat("答复", 2000)}, // 单条就超预算
	}

	got := buildDialogText(messages, 200)
	if !utf8.ValidString(got) {
		t.Fatal("截断后产生非法 UTF-8")
	}
	if n := utf8.RuneCountInString(got); n > 200 {
		t.Fatalf("超出预算: %d runes", n)
	}
	if !strings.Contains(got, "[2]") {
		t.Fatalf("超长消息的序号丢失: %q", got)
	}
	if !strings.Contains(got, "答复") {
		t.Fatalf("应保留最新消息的尾部内容: %q", got)
	}
}

// TestBuildDialogText_WithinBudget 预算内不做任何裁剪，序号从 1 连续。
func TestBuildDialogText_WithinBudget(t *testing.T) {
	messages := []*botmodel.Message{
		{ID: "1", Role: "user", Content: "你好"},
		{ID: "2", Role: "assistant", Content: "你好，有什么可以帮你"},
	}
	got := buildDialogText(messages, 4000)
	if !strings.HasPrefix(got, "[1] 用户: 你好") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "[2] 助手: 你好，有什么可以帮你") {
		t.Fatalf("got %q", got)
	}
}

func TestBuildDialogText_Empty(t *testing.T) {
	if got := buildDialogText(nil, 4000); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := buildDialogText([]*botmodel.Message{{Content: "x"}}, 0); got != "" {
		t.Fatalf("预算为 0 应返回空，got %q", got)
	}
}
