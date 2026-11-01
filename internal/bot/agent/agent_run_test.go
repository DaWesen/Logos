package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// fakeIter 用一个切片事件源替掉 adk.AsyncIterator，让「事件 → 输出」这段逻辑
// 不依赖真实模型即可单测。beforeNext 在返回第 i 个事件前回调，
// 用来断言「消费是增量的」——事件还没产出时，上一段内容必须已经推给上层。
type fakeIter struct {
	events     []*adk.AgentEvent
	idx        int
	beforeNext func(i int)
}

func (f *fakeIter) Next() (*adk.AgentEvent, bool) {
	if f.idx >= len(f.events) {
		return nil, false
	}
	if f.beforeNext != nil {
		f.beforeNext(f.idx)
	}
	event := f.events[f.idx]
	f.idx++
	return event, true
}

func textEvent(content string) *adk.AgentEvent {
	return adk.EventFromMessage(schema.AssistantMessage(content, nil), nil, schema.Assistant, "")
}

func streamEvent(chunks ...*schema.Message) *adk.AgentEvent {
	return adk.EventFromMessage(nil, schema.StreamReaderFromArray(chunks), schema.Assistant, "")
}

func toolCallEvent(content, toolName, args string) *adk.AgentEvent {
	return adk.EventFromMessage(schema.AssistantMessage(content, []schema.ToolCall{
		{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: toolName, Arguments: args}},
	}), nil, schema.Assistant, "")
}

func newTestAgent(t *testing.T, events []*adk.AgentEvent, hook func(i int)) *BaseBotAgent {
	t.Helper()
	iter := &fakeIter{events: events, beforeNext: hook}
	return &BaseBotAgent{
		name: "test-agent",
		run:  func(context.Context, []*schema.Message) agentEventIter { return iter },
	}
}

func newTestStreamAgent(t *testing.T, events []*adk.AgentEvent, hook func(i int)) *BaseBotAgent {
	t.Helper()
	iter := &fakeIter{events: events, beforeNext: hook}
	return &BaseBotAgent{
		name:   "test-agent",
		stream: func(context.Context, []*schema.Message) agentEventIter { return iter },
	}
}

// TestChatStreamIsIncremental 钉死「真流式」：第二段尚未产出时，第一段必须已经到达上层。
// 旧实现先 ConcatMessageStream 读完再回放，这个断言会失败（回调直到循环结束才被调用）。
func TestChatStreamIsIncremental(t *testing.T) {
	var got []string
	agent := newTestStreamAgent(t, []*adk.AgentEvent{
		streamEvent(schema.AssistantMessage("第一段", nil)),
		streamEvent(schema.AssistantMessage("第二段", nil)),
	}, func(i int) {
		if i == 1 && len(got) == 0 {
			t.Fatalf("第二段产出前第一段必须已经推送，实际 got=%v", got)
		}
	})

	err := agent.ChatStream(context.Background(), "hi", func(chunk string) error {
		got = append(got, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"第一段", "第二段"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("chunks = %v, want %v", got, want)
	}
}

// TestChatStreamPropagatesCallbackError 客户端断开时 onChunk 报错，必须立刻终止而不是继续跑模型。
func TestChatStreamPropagatesCallbackError(t *testing.T) {
	sentinel := errors.New("client gone")
	calls := 0
	agent := newTestStreamAgent(t, []*adk.AgentEvent{
		streamEvent(
			schema.AssistantMessage("a", nil),
			schema.AssistantMessage("b", nil),
			schema.AssistantMessage("c", nil),
		),
	}, nil)

	err := agent.ChatStream(context.Background(), "hi", func(string) error {
		calls++
		if calls == 2 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
	if calls != 2 {
		t.Fatalf("回调报错后仍在继续推送，calls = %d", calls)
	}
}

// TestChatStreamErrorsWhenAllEventsFail 整轮只有错误事件时必须上抛，不能返回 ("", nil)。
func TestChatStreamErrorsWhenAllEventsFail(t *testing.T) {
	boom := errors.New("model 500")
	agent := newTestStreamAgent(t, []*adk.AgentEvent{{Err: boom}}, nil)

	err := agent.ChatStream(context.Background(), "hi", func(string) error { return nil })
	if err == nil {
		t.Fatal("全部事件失败却返回 nil error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrap of boom", err)
	}
}

// TestChatStreamPartialContentSurvivesEventError 已有正文时，尾部错误只记录不推翻结果。
func TestChatStreamPartialContentSurvivesEventError(t *testing.T) {
	agent := newTestStreamAgent(t, []*adk.AgentEvent{
		streamEvent(schema.AssistantMessage("部分答案", nil)),
		{Err: errors.New("stream interrupted")},
	}, nil)

	var got []string
	err := agent.ChatStream(context.Background(), "hi", func(chunk string) error {
		got = append(got, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("已产出正文不应返回错误，got %v", err)
	}
	if len(got) != 1 || got[0] != "部分答案" {
		t.Fatalf("chunks = %v", got)
	}
}

// TestChatStreamEmptyReturnsErrEmptyResponse 零事件时必须是可识别的空响应错误。
func TestChatStreamEmptyReturnsErrEmptyResponse(t *testing.T) {
	agent := newTestStreamAgent(t, nil, nil)
	err := agent.ChatStream(context.Background(), "hi", func(string) error { return nil })
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("err = %v, want ErrEmptyResponse", err)
	}
}

// TestChatStreamCanceledContext 调用方取消后，空结果错误应指向 ctx 而不是「模型没说话」。
func TestChatStreamCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	agent := newTestStreamAgent(t, nil, nil)
	err := agent.ChatStream(ctx, "hi", func(string) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestChatStreamNotifiesToolCalls 流式 tool_calls 是分片下发的，拼接后必须只通知完整调用。
func TestChatStreamNotifiesToolCalls(t *testing.T) {
	var observed []string
	ctx := WithToolCallObserver(context.Background(), func(name, _ string) {
		observed = append(observed, name)
	})

	agent := newTestStreamAgent(t, []*adk.AgentEvent{
		streamEvent(schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "knowledge_search", Arguments: `{"q":"x"}`}},
		})),
		streamEvent(schema.AssistantMessage("答案", nil)),
	}, nil)

	var chunks []string
	if err := agent.ChatStream(ctx, "hi", func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(observed) != 1 || observed[0] != "knowledge_search" {
		t.Fatalf("observed = %v", observed)
	}
	if len(chunks) != 1 || chunks[0] != "答案" {
		t.Fatalf("chunks = %v，工具调用轮的内容不应推给用户", chunks)
	}
}

// TestChatSkipsToolCallTurn 非流式：带 tool_calls 的中间消息不能当成最终答案。
func TestChatSkipsToolCallTurn(t *testing.T) {
	agent := newTestAgent(t, []*adk.AgentEvent{
		toolCallEvent("我先查一下资料", "knowledge_search", `{"q":"x"}`),
		textEvent("最终答案"),
	}, nil)

	got, err := agent.Chat(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "最终答案" {
		t.Fatalf("got %q, want 最终答案", got)
	}
}

// TestChatReturnsEmptyResponseError 非流式：零正文同样不能返回 ("", nil)。
func TestChatReturnsEmptyResponseError(t *testing.T) {
	agent := newTestAgent(t, nil, nil)
	if _, err := agent.Chat(context.Background(), "hi"); !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("err = %v, want ErrEmptyResponse", err)
	}
}

// TestChatWrapsEventError 非流式：事件错误要能还原根因（errors.Is 可判定）。
func TestChatWrapsEventError(t *testing.T) {
	boom := fmt.Errorf("quota exceeded")
	agent := newTestAgent(t, []*adk.AgentEvent{{Err: boom}}, nil)

	_, err := agent.Chat(context.Background(), "hi")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrap of boom", err)
	}
}

// TestChatWithoutRunner 未初始化的事件源要返回明确错误，而不是空结果。
func TestChatWithoutRunner(t *testing.T) {
	agent := &BaseBotAgent{name: "no-runner"}
	if _, err := agent.Chat(context.Background(), "hi"); err == nil {
		t.Fatal("missing runner should error")
	}
	if err := agent.ChatStream(context.Background(), "hi", func(string) error { return nil }); err == nil {
		t.Fatal("missing stream runner should error")
	}
}
