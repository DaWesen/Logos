package agent

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// ToolCallObserver 工具调用观察者：ReAct 循环中 LLM 每次决定调用工具时回调。
// 用于把 Agent 的中间决策（调用了什么工具）透传给上层（如流式推送推理过程）。
//
// 通过 context 传递而非 Agent 字段：Agent 实例被 AgentManager 缓存、
// 被并发请求共享，per-request 的观察者放上下文里才不会串线。
type ToolCallObserver func(toolName, arguments string)

type toolCallObserverKey struct{}

// WithToolCallObserver 把观察者挂到 context（每次请求独立）
func WithToolCallObserver(ctx context.Context, obs ToolCallObserver) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, toolCallObserverKey{}, obs)
}

// notifyToolCalls 通知观察者一批工具调用（无观察者时为 no-op）
func notifyToolCalls(ctx context.Context, calls []schema.ToolCall) {
	obs, ok := ctx.Value(toolCallObserverKey{}).(ToolCallObserver)
	if !ok || obs == nil {
		return
	}
	for _, tc := range calls {
		if tc.Function.Name != "" {
			obs(tc.Function.Name, tc.Function.Arguments)
		}
	}
}
