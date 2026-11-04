package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestBuildMessages_FiltersInvalidHistory 历史里的 nil 与空消息必须被剔除：
// nil 会被 SDK 解引用 panic，空 content 会被供应商 400，
// 但「带 tool_calls 的空 assistant 消息」是合法的，必须保留。
func TestBuildMessages_FiltersInvalidHistory(t *testing.T) {
	a := &BaseBotAgent{systemPrompt: "sys"}

	history := []*schema.Message{
		nil,
		{},                     // role/content 全空
		schema.UserMessage(""), // 空内容
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "c1", Function: schema.FunctionCall{Name: "t"},
		}}}, // 合法：只带工具调用
		schema.UserMessage("hello"),
	}

	got := a.buildMessages("now", history)

	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(got), got)
	}
	if got[0].Role != schema.System || got[0].Content != "sys" {
		t.Fatalf("system prompt 必须位于首位: %+v", got[0])
	}
	if got[1].Role != schema.Assistant || len(got[1].ToolCalls) != 1 {
		t.Fatalf("带 tool_calls 的消息被误删: %+v", got[1])
	}
	if got[2].Content != "hello" {
		t.Fatalf("历史顺序被打乱: %+v", got[2])
	}
	if got[3].Role != schema.User || got[3].Content != "now" {
		t.Fatalf("当前消息必须在最后: %+v", got[3])
	}
}

// TestChat_RejectsRequestWithoutConversation 只有 system prompt 的请求在本地拦下，
// 不要把必然 400 的请求发到供应商。
func TestChat_RejectsRequestWithoutConversation(t *testing.T) {
	called := false
	a := &BaseBotAgent{
		name:         "t",
		systemPrompt: "sys",
		run: func(context.Context, []*schema.Message) agentEventIter {
			called = true
			return &fakeIter{}
		},
	}

	if _, err := a.chatWithMessages(context.Background(), a.buildMessages("", nil)); !errors.Is(err, ErrEmptyRequest) {
		t.Fatalf("want ErrEmptyRequest, got %v", err)
	}
	if called {
		t.Fatal("空请求不应触发实际运行")
	}
}

// TestChatStream_RejectsRequestWithoutConversation 流式路径同样要拦。
func TestChatStream_RejectsRequestWithoutConversation(t *testing.T) {
	a := &BaseBotAgent{
		name:         "t",
		systemPrompt: "sys",
		stream: func(context.Context, []*schema.Message) agentEventIter {
			t.Fatal("空请求不应触发实际运行")
			return nil
		},
	}

	err := a.chatStreamWithMessages(context.Background(), a.buildMessages("", nil), func(string) error { return nil })
	if !errors.Is(err, ErrEmptyRequest) {
		t.Fatalf("want ErrEmptyRequest, got %v", err)
	}
}
