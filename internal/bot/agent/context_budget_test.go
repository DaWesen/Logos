package agent

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestTrimHistory_UnderBudgetKeepsAll(t *testing.T) {
	history := []*schema.Message{
		schema.UserMessage("hi"),
		schema.AssistantMessage("hello", nil),
	}

	kept, omitted := trimHistory(history, 100)
	if omitted != 0 || len(kept) != len(history) {
		t.Fatalf("未超预算不应裁剪，omitted=%d kept=%d", omitted, len(kept))
	}
}

func TestTrimHistory_KeepsRecentMessages(t *testing.T) {
	history := []*schema.Message{
		schema.UserMessage(strings.Repeat("a", 100)),
		schema.AssistantMessage(strings.Repeat("b", 100), nil),
		schema.UserMessage("hi"),
	}

	kept, omitted := trimHistory(history, 10)
	if omitted != 2 {
		t.Fatalf("omitted = %d, want 2", omitted)
	}
	if len(kept) != 1 || kept[0].Content != "hi" {
		t.Fatalf("应只保留最近一条，got %+v", kept)
	}
}

// TestTrimHistory_DropsOrphanToolResult 截断边界不能留下孤立的 tool 消息：
// 它对应的 assistant tool_calls 已被截掉，供应商会拒绝整个请求。
func TestTrimHistory_DropsOrphanToolResult(t *testing.T) {
	history := []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "c1", Function: schema.FunctionCall{Name: "t", Arguments: "{}"},
		}}},
		{Role: schema.Tool, Content: "ok", ToolCallID: "c1"},
		schema.UserMessage("hi"),
	}

	// 预算刚好容纳 tool + user，强制边界落在 tool 上
	kept, omitted := trimHistory(history, 4)

	if len(kept) != 1 || kept[0].Role != schema.User {
		t.Fatalf("边界孤儿 tool 结果必须被丢弃，got %+v", kept)
	}
	if omitted != 2 {
		t.Fatalf("omitted = %d, want 2", omitted)
	}
}

// TestTrimHistory_CountsToolCallArguments 工具参数同样占 prompt 空间，
// 不计入预算会让长参数的工具轮次永远裁不掉。
func TestTrimHistory_CountsToolCallArguments(t *testing.T) {
	history := []*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: "c1", Function: schema.FunctionCall{Name: "t", Arguments: strings.Repeat("x", 200)},
		}}},
		schema.UserMessage("hi"),
	}

	kept, omitted := trimHistory(history, 10)
	if omitted != 1 || len(kept) != 1 || kept[0].Content != "hi" {
		t.Fatalf("含大参数的工具消息应被裁掉，omitted=%d kept=%+v", omitted, kept)
	}
}

// TestBuildMessages_AddsTruncationNotice 截断必须在 system prompt 里说明，
// 否则模型会确信「早期不存在」并给出错误回答。
func TestBuildMessages_AddsTruncationNotice(t *testing.T) {
	a := &BaseBotAgent{systemPrompt: "sys"}

	history := []*schema.Message{
		schema.UserMessage(strings.Repeat("a", historyRuneBudget)),
		schema.UserMessage("now"),
	}

	got := a.buildMessages("latest", history)
	if got[0].Role != schema.System {
		t.Fatalf("首条必须是 system，got %+v", got[0])
	}
	if !strings.Contains(got[0].Content, "已省略") {
		t.Fatalf("system prompt 缺少截断说明: %q", got[0].Content)
	}
}

// TestBuildMessages_NoNoticeWhenNotTrimmed 未截断时不能出现多余提示。
func TestBuildMessages_NoNoticeWhenNotTrimmed(t *testing.T) {
	a := &BaseBotAgent{systemPrompt: "sys"}
	got := a.buildMessages("hi", nil)

	if strings.Contains(got[0].Content, "已省略") {
		t.Fatalf("未截断不应出现提示: %q", got[0].Content)
	}
}
