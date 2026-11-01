package coordinator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestMain(m *testing.M) {
	_, file, _, _ := runtime.Caller(0)
	fixture := filepath.Join(filepath.Dir(file), "..", "..", "..", "config", "testdata", "config.yaml")
	if abs, err := filepath.Abs(fixture); err == nil {
		fixture = abs
	}
	os.Setenv("LOGOS_CONFIG_PATH", fixture)
	os.Exit(m.Run())
}

type fakeIter struct {
	events     []*adk.AgentEvent
	idx        int
	consumed   *int32
	beforeNext func(i int, consumed int32)
}

func (f *fakeIter) Next() (*adk.AgentEvent, bool) {
	if f.idx >= len(f.events) {
		return nil, false
	}
	if f.beforeNext != nil {
		f.beforeNext(f.idx, atomic.LoadInt32(f.consumed))
	}
	event := f.events[f.idx]
	f.idx++
	return event, true
}

func streamEvent(chunks ...*schema.Message) *adk.AgentEvent {
	return adk.EventFromMessage(nil, schema.StreamReaderFromArray(chunks), schema.Assistant, "")
}

func textEvent(content string) *adk.AgentEvent {
	return adk.EventFromMessage(schema.AssistantMessage(content, nil), nil, schema.Assistant, "")
}

func toolResultEvent(content string) *adk.AgentEvent {
	return adk.EventFromMessage(schema.ToolMessage(content, "call_1"), nil, schema.Tool, "knowledge_search")
}

func newTestCoordinator(events []*adk.AgentEvent, consumed *int32) *Coordinator {
	iter := &fakeIter{events: events, consumed: consumed}
	src := func(context.Context, []*schema.Message) eventIterator { return iter }
	return &Coordinator{einoMgr: nil, run: src, stream: src}
}

// TestChatStreamIsIncremental 流式接口必须用流式事件源并边收边推：
// 第二段产生时第一段必须已经到达上层（复用非流式 Runner 时这里恒为空）。
func TestChatStreamIsIncremental(t *testing.T) {
	var consumed int32
	var got []string

	c := newTestCoordinator([]*adk.AgentEvent{
		streamEvent(schema.AssistantMessage("第一段", nil)),
		streamEvent(schema.AssistantMessage("第二段", nil)),
	}, &consumed)

	// 用 beforeNext 只看事件顺序：第二段被消费前，第一段必须已推送
	iter := &fakeIter{
		consumed: &consumed,
		events: []*adk.AgentEvent{
			streamEvent(schema.AssistantMessage("第一段", nil)),
			streamEvent(schema.AssistantMessage("第二段", nil)),
		},
		beforeNext: func(i int, seen int32) {
			if i == 1 && seen == 0 {
				t.Fatalf("第二段产出前第一段必须已推送")
			}
		},
	}
	c.stream = func(context.Context, []*schema.Message) eventIterator { return iter }

	err := c.ChatStream(context.Background(), "hi", func(chunk string) error {
		atomic.AddInt32(&consumed, 1)
		got = append(got, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, "|") != "第一段|第二段" {
		t.Fatalf("chunks = %v", got)
	}
}

// TestChatStreamSkipsToolResult 工具结果（Role=Tool）是给模型看的原始数据，不能推给用户。
func TestChatStreamSkipsToolResult(t *testing.T) {
	var got []string
	c := newTestCoordinator([]*adk.AgentEvent{
		toolResultEvent(`{"hits":[{"text":"原始检索结果"}]}`),
		textEvent("整合后的回答"),
	}, nil)

	if err := c.ChatStream(context.Background(), "hi", func(chunk string) error {
		got = append(got, chunk)
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(got, "|") != "整合后的回答" {
		t.Fatalf("工具结果泄漏给用户，chunks = %v", got)
	}
}

// TestChatStreamPropagatesCallbackError 客户端断开必须立刻停止。
func TestChatStreamPropagatesCallbackError(t *testing.T) {
	sentinel := errors.New("client gone")
	calls := 0
	c := newTestCoordinator([]*adk.AgentEvent{
		streamEvent(
			schema.AssistantMessage("a", nil),
			schema.AssistantMessage("b", nil),
			schema.AssistantMessage("c", nil),
		),
	}, nil)

	err := c.ChatStream(context.Background(), "hi", func(string) error {
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
		t.Fatalf("报错后仍在推送，calls = %d", calls)
	}
}

// TestChatWrapsEventError 模型报错时不能返回空答案 + nil。
func TestChatWrapsEventError(t *testing.T) {
	boom := errors.New("model 500")
	c := newTestCoordinator([]*adk.AgentEvent{{Err: boom}}, nil)

	_, err := c.Chat(context.Background(), "hi")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrap of boom", err)
	}
}

// TestChatSkipsToolCallTurn 带 tool_calls 的中间消息不是答案。
func TestChatSkipsToolCallTurn(t *testing.T) {
	c := newTestCoordinator([]*adk.AgentEvent{
		adk.EventFromMessage(schema.AssistantMessage("我先查一下", []schema.ToolCall{
			{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "knowledge_search"}},
		}), nil, schema.Assistant, ""),
		textEvent("最终答案"),
	}, nil)

	got, err := c.Chat(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "最终答案" {
		t.Fatalf("got %q", got)
	}
}

// TestChatEmptyResponseErrors 零正文必须是错误，不能是成功的空回答。
func TestChatEmptyResponseErrors(t *testing.T) {
	c := newTestCoordinator([]*adk.AgentEvent{toolResultEvent("only tool output")}, nil)
	if _, err := c.Chat(context.Background(), "hi"); err == nil {
		t.Fatal("empty coordinator response should error")
	}
}
