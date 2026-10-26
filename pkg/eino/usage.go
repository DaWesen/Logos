package eino

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// ==================== 真实 Token 用量采集 ====================
//
// eino 不提供离线 tokenizer：真实 token 数来自模型供应商在响应里返回的
// schema.ResponseMeta.Usage（OpenAI 兼容接口的 usage 字段，eino-ext 的
// openai ChatModel 已解析到 schema.TokenUsage）。本文件提供按请求维度的
// 采集器，把一次请求内（含 ReAct 多轮、planner、反思等）所有模型调用的
// 用量累加起来，供计费与监控使用。
//
// 供应商未返回 usage 时（如部分自建 OpenAI 兼容网关），采集器拿不到数据，
// 调用方需退化到本地估算并标记来源。

type usageRecorderKey struct{}

// UsageRecorder 汇总一次请求内的 token 用量，并发安全。
type UsageRecorder struct {
	mu    sync.Mutex
	usage schema.TokenUsage
	calls int
}

// WithUsageRecorder 在 ctx 上挂一个用量采集器。
// 之后同一条 ctx 链路上的模型调用（agent / coordinator / einoManager）会自动累加。
func WithUsageRecorder(ctx context.Context) (context.Context, *UsageRecorder) {
	recorder := &UsageRecorder{}
	return context.WithValue(ctx, usageRecorderKey{}, recorder), recorder
}

// RecordUsageFrom 从模型返回消息中采集 ResponseMeta.Usage。
// 不是模型输出消息、或供应商未返回 usage 时是空操作。
func RecordUsageFrom(ctx context.Context, msg *schema.Message) {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	recorder, ok := ctx.Value(usageRecorderKey{}).(*UsageRecorder)
	if !ok || recorder == nil {
		return
	}
	recorder.add(*msg.ResponseMeta.Usage)
}

func (r *UsageRecorder) add(usage schema.TokenUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage.PromptTokens += usage.PromptTokens
	r.usage.CompletionTokens += usage.CompletionTokens
	r.usage.TotalTokens += usage.TotalTokens
	r.calls++
}

// Snapshot 返回累计用量与模型调用次数。
func (r *UsageRecorder) Snapshot() (schema.TokenUsage, int) {
	if r == nil {
		return schema.TokenUsage{}, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage, r.calls
}

// UsageSource 标记用量来源：eino 真实用量或本地估算。
const (
	UsageSourceEino     = "eino"
	UsageSourceEstimate = "estimate"
)
