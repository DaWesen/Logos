package toolbudget

import (
	"context"
	"sync"
	"testing"
)

// TestTakeSequential 预算按调用顺序扣减，超出的部分只批剩余额度。
func TestTakeSequential(t *testing.T) {
	_, turn := WithTurn(context.Background(), 100)

	if got := turn.Take(40); got != 40 {
		t.Fatalf("第一次 Take = %d, want 40", got)
	}
	if got := turn.Take(80); got != 60 {
		t.Fatalf("第二次 Take = %d, want 60（只剩 60）", got)
	}
	if got := turn.Take(10); got != 0 {
		t.Fatalf("额度耗尽后 Take = %d, want 0", got)
	}
	if rem := turn.Remaining(); rem != 0 {
		t.Fatalf("剩余 = %d, want 0", rem)
	}
	if used := turn.Used(); used != 100 {
		t.Fatalf("已用 = %d, want 100", used)
	}
}

// TestTakeZeroOrNegative 非正数申请不消耗额度（调用方可能传入空结果长度）。
func TestTakeZeroOrNegative(t *testing.T) {
	_, turn := WithTurn(context.Background(), 10)
	for _, n := range []int{0, -1, -100} {
		if got := turn.Take(n); got != 0 {
			t.Fatalf("Take(%d) = %d, want 0", n, got)
		}
	}
	if rem := turn.Remaining(); rem != 10 {
		t.Fatalf("剩余 = %d, want 10", rem)
	}
}

// TestTakeConcurrent 并发扣减不能超发：总额度是所有工具轮次共享的硬上限。
func TestTakeConcurrent(t *testing.T) {
	const (
		budget = 10000
		reqs   = 40
		size   = 500
	)
	_, turn := WithTurn(context.Background(), budget)

	var mu sync.Mutex
	grantedTotal := 0
	var wg sync.WaitGroup
	for n := 0; n < reqs; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := turn.Take(size)
			mu.Lock()
			grantedTotal += g
			mu.Unlock()
		}()
	}
	wg.Wait()

	if grantedTotal != budget {
		t.Fatalf("并发获批总量 = %d, want %d（不多不少）", grantedTotal, budget)
	}
}

// TestWithTurnDefault 预算为 0/负数时回落到默认值，避免调用方传错导致「零预算」。
func TestWithTurnDefault(t *testing.T) {
	for _, b := range []int{0, -1} {
		_, turn := WithTurn(context.Background(), b)
		if turn.Budget() != DefaultTurnBudget {
			t.Fatalf("budget=%d 时总额 = %d, want %d", b, turn.Budget(), DefaultTurnBudget)
		}
	}
}

// TestFromContextAbsent 没有挂预算时返回 nil，调用方必须能安全退化。
func TestFromContextAbsent(t *testing.T) {
	if turn := FromContext(context.Background()); turn != nil {
		t.Fatalf("空 ctx 不应有预算，got %+v", turn)
	}
	if turn := FromContext(nil); turn != nil {
		t.Fatalf("nil ctx 不应 panic 也不应有预算")
	}
}

// TestNilTurnSafe nil 预算句柄上的方法安全可用（日志/观测代码可能拿到零值）。
func TestNilTurnSafe(t *testing.T) {
	var turn *Turn
	if got := turn.Take(10); got != 0 {
		t.Fatalf("nil Turn Take = %d, want 0", got)
	}
	if turn.Remaining() != 0 || turn.Budget() != 0 || turn.Used() != 0 {
		t.Fatalf("nil Turn 读接口应返回 0")
	}
}

// TestWithTurnReplaces 同一 ctx 重复挂预算以最后一次为准，对应「一轮一份」。
func TestWithTurnReplaces(t *testing.T) {
	ctx, first := WithTurn(context.Background(), 10)
	_, second := WithTurn(ctx, 20)

	if turn := FromContext(ctx); turn != first {
		t.Fatalf("原 ctx 上的预算不应被修改")
	}
	if got := second.Take(20); got != 20 {
		t.Fatalf("新预算 Take = %d, want 20", got)
	}
}
