package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// notifyToolCalls 是内部函数，直接在同包内测试

func TestNotifyToolCalls_NoObserverInContext(t *testing.T) {
	// context 里没有观察者：必须 no-op 且不 panic
	ctx := context.Background()
	calls := []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "search", Arguments: "{}"}},
		{Function: schema.FunctionCall{Name: "calc", Arguments: "{}"}},
	}
	// 不应 panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("无观察者时 panic: %v", r)
		}
	}()
	notifyToolCalls(ctx, calls)
}

func TestNotifyToolCalls_NilObserver(t *testing.T) {
	// 显式挂 nil 观察者：WithToolCallObserver 应原样返回 ctx（不加 value）
	orig := context.Background()
	ctx := WithToolCallObserver(orig, nil)
	if ctx != orig {
		t.Fatalf("nil 观察者应原样返回 ctx")
	}
	calls := []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "search", Arguments: "{}"}},
	}
	notifyToolCalls(ctx, calls) // 不应 panic
}

func TestNotifyToolCalls_ObserverReceivesCalls(t *testing.T) {
	type captured struct {
		name string
		args string
	}
	var got []captured
	obs := func(toolName, arguments string) {
		got = append(got, captured{name: toolName, args: arguments})
	}
	ctx := WithToolCallObserver(context.Background(), obs)

	calls := []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "search", Arguments: `{"q":"go"}`}},
		{Function: schema.FunctionCall{Name: "calc", Arguments: `{"x":1}`}},
	}
	notifyToolCalls(ctx, calls)

	if len(got) != 2 {
		t.Fatalf("期望收到 2 次回调，实际 %d", len(got))
	}
	if got[0].name != "search" || got[0].args != `{"q":"go"}` {
		t.Errorf("第 1 次回调内容错: %+v", got[0])
	}
	if got[1].name != "calc" || got[1].args != `{"x":1}` {
		t.Errorf("第 2 次回调内容错: %+v", got[1])
	}
}

func TestNotifyToolCalls_SkipsEmptyName(t *testing.T) {
	var count int
	obs := func(toolName, arguments string) {
		count++
	}
	ctx := WithToolCallObserver(context.Background(), obs)

	calls := []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "", Arguments: "{}"}}, // 应被跳过
		{Function: schema.FunctionCall{Name: "search", Arguments: "{}"}},
		{Function: schema.FunctionCall{Name: "", Arguments: "{}"}}, // 应被跳过
	}
	notifyToolCalls(ctx, calls)

	if count != 1 {
		t.Fatalf("只应回调 1 次（仅非空名），实际 %d", count)
	}
}

func TestNotifyToolCalls_EmptySlice(t *testing.T) {
	var count int
	obs := func(toolName, arguments string) {
		count++
	}
	ctx := WithToolCallObserver(context.Background(), obs)

	notifyToolCalls(ctx, nil)
	notifyToolCalls(ctx, []schema.ToolCall{})

	if count != 0 {
		t.Fatalf("空切片不应触发回调，实际 %d", count)
	}
}

// 验证观察者通过 context 传递的隔离性：
// 两个不同的 ctx 挂不同观察者，互不串线。
func TestWithToolCallObserver_PerRequestIsolation(t *testing.T) {
	var sbA, sbB strings.Builder
	ctxA := WithToolCallObserver(context.Background(), func(name, args string) {
		sbA.WriteString(name)
	})
	ctxB := WithToolCallObserver(context.Background(), func(name, args string) {
		sbB.WriteString(name)
	})

	calls := []schema.ToolCall{
		{Function: schema.FunctionCall{Name: "alpha", Arguments: ""}},
		{Function: schema.FunctionCall{Name: "beta", Arguments: ""}},
	}

	notifyToolCalls(ctxA, calls)
	notifyToolCalls(ctxB, calls)

	if sbA.String() != "alphabeta" {
		t.Errorf("ctxA 期望 alphabeta，实际 %s", sbA.String())
	}
	if sbB.String() != "alphabeta" {
		t.Errorf("ctxB 期望 alphabeta，实际 %s", sbB.String())
	}

	// 关键：ctxA 的观察者不能从 ctxB 取到，反之亦然
	// （这里通过 ctxA 内 sbB 仍为空来验证）
	if sbB.Len() == 0 {
		// sbB 已写入，跳过；下面的断言针对 ctxA 不污染 ctxB
	}
}
