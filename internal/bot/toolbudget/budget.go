// Package toolbudget 限制「一次对话轮次内所有工具结果」进入 prompt 的总字符量。
//
// 单工具硬上限（tools 包 32000 字符）只保证「一条结果不爆」，挡不住
// 「模型一轮并行调用 8 个工具」：8 × 32000 = 25 万字符，足以把上下文窗口
// 和成本一起吃掉，还会把真正的关键结果挤到后面。
//
// 预算挂在 context 上，由 Agent 每次 Run 前创建：一轮一次，轮与轮互不影响；
// 工具执行器只做原子扣减，不关心属于哪个会话。
package toolbudget

import (
	"context"
	"sync"
)

// DefaultTurnBudget 单轮对话所有工具结果的字符预算。
//
// 取舍：历史消息单独有 12000 字符预算（agent.historyRuneBudget），
// 再加上 system prompt 与模型回答，48000 字符（约 2.4 万~4.8 万 token）
// 对主流 128k 上下文仍留有余量；再大就会让长检索结果把回答空间挤没。
const DefaultTurnBudget = 48000

type ctxKey struct{}

// Turn 一轮对话内共享的可扣减预算。
//
// 并发安全：同一轮的多个工具会在 goroutine 里并行执行，扣减必须原子，
// 否则两个大结果都能看到「剩余足够」而一起通过。
type Turn struct {
	mu        sync.Mutex
	budget    int
	remaining int
}

// WithTurn 在 ctx 上挂一个新的轮次预算，返回派生 ctx 与预算句柄。
// budget<=0 使用 DefaultTurnBudget；同一 ctx 重复调用会覆盖旧预算，
// 这正是「一轮一个」的语义。
func WithTurn(ctx context.Context, budget int) (context.Context, *Turn) {
	if ctx == nil {
		ctx = context.Background()
	}
	if budget <= 0 {
		budget = DefaultTurnBudget
	}
	turn := &Turn{budget: budget, remaining: budget}
	return context.WithValue(ctx, ctxKey{}, turn), turn
}

// FromContext 取出当前轮次预算；没有挂预算时返回 nil（调用方应退化为无预算行为）。
func FromContext(ctx context.Context) *Turn {
	if ctx == nil {
		return nil
	}
	turn, _ := ctx.Value(ctxKey{}).(*Turn)
	return turn
}

// Take 申请 n 个字符额度，返回实际获批额度（可能小于 n，甚至为 0）。
//
// 先到先得而非均分：工具结果的价值取决于模型当时的决策，
// 执行层无法预知「后面还有几个工具、各要多少」，均分反而会切碎每条结果；
// 先到先得至少保证早返回的结果完整，晚返回的被截断时有明确标记。
func (t *Turn) Take(n int) int {
	if t == nil || n <= 0 {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if n >= t.remaining {
		granted := t.remaining
		t.remaining = 0
		return granted
	}
	t.remaining -= n
	return n
}

// Remaining 返回剩余额度（观测/日志用）。
func (t *Turn) Remaining() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.remaining
}

// Budget 返回本轮总预算。
func (t *Turn) Budget() int {
	if t == nil {
		return 0
	}
	return t.budget
}

// Used 返回本轮已消耗额度。
func (t *Turn) Used() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.budget - t.remaining
}
