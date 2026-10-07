package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	botmodel "Logos/internal/service/ai/bot/model"
)

// makeMsgs 构造按时间倒序的消息列表（最新在前），与 repo.GetMessages 的返回顺序一致
func makeMsgs(n int, contentLen int) []*botmodel.Message {
	msgs := make([]*botmodel.Message, n)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = &botmodel.Message{
			ID:      fmt.Sprintf("msg-%03d", n-i), // 最新的消息 ID 最大
			Role:    role,
			Content: strings.Repeat("x", contentLen),
		}
	}
	return msgs
}

func TestPackMessagesByBudget_AllWithinBudget(t *testing.T) {
	msgs := makeMsgs(4, 100) // 4 条，每条 ~100 tokens
	kept, overflow := packMessagesByBudget(msgs, 5000)
	if len(kept) != 4 {
		t.Fatalf("expected all 4 kept, got %d", len(kept))
	}
	if len(overflow) != 0 {
		t.Fatalf("expected no overflow, got %d", len(overflow))
	}
	// kept 按时间正序（最早在前）
	if kept[0].ID != "msg-001" || kept[3].ID != "msg-004" {
		t.Errorf("kept should be chronological, got %s..%s", kept[0].ID, kept[3].ID)
	}
}

func TestPackMessagesByBudget_OverflowsOldest(t *testing.T) {
	// 每条 ~800 tokens（3000 字节 × 0.25 + 50），10 条 = 8000 > 6000 预算
	msgs := makeMsgs(10, 3000)
	kept, overflow := packMessagesByBudget(msgs, historyTokenBudget)

	if len(kept) < 2 {
		t.Fatalf("expected at least 2 kept, got %d", len(kept))
	}
	if len(overflow) == 0 {
		t.Fatal("expected overflow when total size exceeds budget")
	}
	if len(kept)+len(overflow) != 10 {
		t.Fatalf("message lost: kept=%d overflow=%d", len(kept), len(overflow))
	}
	// 溢出的是更早的消息（ID 更小）
	if overflow[0].ID >= kept[0].ID {
		t.Errorf("overflow should contain older messages: overflow[0]=%s kept[0]=%s", overflow[0].ID, kept[0].ID)
	}
	// kept 的最新消息保留
	newest := kept[len(kept)-1]
	if newest.ID != "msg-010" {
		t.Errorf("newest message must be kept, got %s", newest.ID)
	}
}

func TestPackMessagesByBudget_SingleHugeMessage(t *testing.T) {
	// 单条消息超预算：仍保留（最新消息不能丢）
	msgs := makeMsgs(1, 10000)
	kept, _ := packMessagesByBudget(msgs, 100)
	if len(kept) != 1 {
		t.Fatal("single newest message must always be kept")
	}
}

func TestBuildContextMessages_ShortHistoryNoSummary(t *testing.T) {
	cc := NewConversationCompressor()
	msgs := makeMsgs(4, 100)

	fetch := func() ([]*botmodel.Message, error) { return msgs, nil }
	chatCalled := false
	chat := func(ctx context.Context, prompt string) (string, error) {
		chatCalled = true
		return "summary", nil
	}

	out := cc.BuildContextMessages(t.Context(), "conv-1", fetch, chat)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages (no background), got %d", len(out))
	}
	if chatCalled {
		t.Error("short history should not trigger summarization")
	}
}

func TestBuildContextMessages_LongHistoryGetsBackground(t *testing.T) {
	cc := NewConversationCompressor()
	// 40 条 × ~1050 tokens，远超 6000 预算 → 大量溢出
	msgs := makeMsgs(40, 1000)

	fetch := func() ([]*botmodel.Message, error) { return msgs, nil }
	chat := func(ctx context.Context, prompt string) (string, error) {
		return "用户与助手讨论了项目架构。", nil
	}

	out := cc.BuildContextMessages(t.Context(), "conv-1", fetch, chat)

	// 开头两轮是「对话背景」
	if len(out) < 3 {
		t.Fatalf("expected background + history, got %d messages", len(out))
	}
	first := out[0].Content
	if !strings.Contains(first, "对话此前的内容摘要") || !strings.Contains(first, "项目架构") {
		t.Errorf("background message missing or wrong: %q", first)
	}

	// 摘要被缓存：第二次调用不再触发 chat（除非有新溢出）
	chatCalls := 0
	chat2 := func(ctx context.Context, prompt string) (string, error) {
		chatCalls++
		return "用户与助手讨论了项目架构。", nil
	}
	cc.BuildContextMessages(t.Context(), "conv-1", fetch, chat2)
	if chatCalls != 0 {
		t.Errorf("cached summary should avoid re-summarization, chat called %d times", chatCalls)
	}
}

func TestBuildContextMessages_SummaryFailureDegradesGracefully(t *testing.T) {
	cc := NewConversationCompressor()
	msgs := makeMsgs(40, 1000)

	fetch := func() ([]*botmodel.Message, error) { return msgs, nil }
	chat := func(ctx context.Context, prompt string) (string, error) {
		return "", fmt.Errorf("LLM unavailable")
	}

	out := cc.BuildContextMessages(t.Context(), "conv-1", fetch, chat)
	// 摘要失败 → 无背景前缀，仅保留预算内历史（降级为直接截断）
	for _, m := range out {
		if strings.Contains(m.Content, "对话此前的内容摘要") {
			t.Fatal("failed summary must not inject background")
		}
	}
	if len(out) == 0 {
		t.Fatal("history must still be present after summary failure")
	}
}

func TestBuildContextMessages_FetchErrorReturnsNil(t *testing.T) {
	cc := NewConversationCompressor()
	fetch := func() ([]*botmodel.Message, error) { return nil, fmt.Errorf("db error") }
	if out := cc.BuildContextMessages(t.Context(), "conv-1", fetch, nil); out != nil {
		t.Errorf("fetch error should return nil, got %d messages", len(out))
	}
}

func TestToSchemaMessages_SkipsEmptyAssistant(t *testing.T) {
	msgs := []*botmodel.Message{
		{ID: "1", Role: "user", Content: "hi"},
		{ID: "2", Role: "assistant", Content: ""},
		{ID: "3", Role: "assistant", Content: "hello"},
		{ID: "4", Role: "user", Content: "again"},
	}
	out := toSchemaMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("empty assistant should be skipped, got %d", len(out))
	}
}

func TestRenderMessages_TruncatesLongContent(t *testing.T) {
	msgs := []*botmodel.Message{
		{ID: "1", Role: "user", Content: strings.Repeat("y", 1000)},
	}
	out := renderMessages(msgs)
	if len(out) > 600 {
		t.Errorf("long message should be truncated in summary input, got %d chars", len(out))
	}
	if !strings.Contains(out, "...") {
		t.Error("truncated content should carry ellipsis")
	}
}
