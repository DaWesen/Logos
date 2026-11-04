package agent

import (
	"context"
	"testing"

	"Logos/internal/bot/toolbudget"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// TestChatAttachesTurnBudget 每一轮 Run 的 ctx 上必须挂一份工具结果预算：
// 工具执行器靠它跨工具累计扣减，漏挂就等于所有工具各自按单条硬上限进 prompt。
func TestChatAttachesTurnBudget(t *testing.T) {
	var seen *toolbudget.Turn
	agent := &BaseBotAgent{
		name: "test-agent",
		run: func(ctx context.Context, _ []*schema.Message) agentEventIter {
			seen = toolbudget.FromContext(ctx)
			return &fakeIter{events: []*adk.AgentEvent{textEvent("ok")}}
		},
	}

	if _, err := agent.Chat(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen == nil {
		t.Fatal("Chat 未在 ctx 上挂工具结果预算")
	}
	if seen.Budget() != toolbudget.DefaultTurnBudget {
		t.Fatalf("预算 = %d, want %d", seen.Budget(), toolbudget.DefaultTurnBudget)
	}
}

// TestStreamAttachesTurnBudget 流式路径同样要挂预算，否则工具结果在流式回答里不受限。
func TestStreamAttachesTurnBudget(t *testing.T) {
	var seen *toolbudget.Turn
	agent := &BaseBotAgent{
		name: "test-agent",
		stream: func(ctx context.Context, _ []*schema.Message) agentEventIter {
			seen = toolbudget.FromContext(ctx)
			return &fakeIter{events: []*adk.AgentEvent{streamEvent(schema.AssistantMessage("ok", nil))}}
		},
	}

	if err := agent.ChatStream(context.Background(), "hi", func(string) error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen == nil || seen.Budget() != toolbudget.DefaultTurnBudget {
		t.Fatalf("流式未挂正确预算: %+v", seen)
	}
}

// TestTurnBudgetFreshPerCall 轮次之间必须重新计数：
// 上一轮用光预算不能影响下一轮，否则长对话里工具结果会越用越少。
func TestTurnBudgetFreshPerCall(t *testing.T) {
	var (
		turns   []*toolbudget.Turn
		atStart []int
	)
	agent := &BaseBotAgent{
		name: "test-agent",
		run: func(ctx context.Context, _ []*schema.Message) agentEventIter {
			turn := toolbudget.FromContext(ctx)
			turns = append(turns, turn)
			atStart = append(atStart, turn.Remaining())
			// 模拟工具在本轮吃掉全部预算
			turn.Take(toolbudget.DefaultTurnBudget)
			return &fakeIter{events: []*adk.AgentEvent{textEvent("ok")}}
		},
	}

	for i := 0; i < 2; i++ {
		if _, err := agent.Chat(context.Background(), "hi"); err != nil {
			t.Fatalf("第 %d 轮失败: %v", i+1, err)
		}
	}
	if len(turns) != 2 {
		t.Fatalf("应产生 2 轮预算，实际 %d", len(turns))
	}
	if turns[0] == turns[1] {
		t.Fatal("两轮复用了同一份预算，上一轮的消耗会挤占下一轮")
	}
	for i, rem := range atStart {
		if rem != toolbudget.DefaultTurnBudget {
			t.Fatalf("第 %d 轮起手剩余 = %d, want 满额 %d", i+1, rem, toolbudget.DefaultTurnBudget)
		}
	}
}
