package ctxmem

import "testing"

func TestDeriveHistoryBudget_MatchesSpecExamples(t *testing.T) {
	tests := []struct {
		name      string
		window    int
		system    int
		wantTotal int
	}{
		{name: "8k", window: 8000, system: 1200, wantTotal: 4288},
		{name: "32k", window: 32000, system: 2400, wantTotal: 20000},
		{name: "128k", window: 128000, system: 3200, wantTotal: 86400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveHistoryBudget(tt.window, tt.system)
			if got.Total != tt.wantTotal {
				t.Fatalf("Total = %d, want %d", got.Total, tt.wantTotal)
			}
			if got.Reserve <= 0 || got.Reserve >= tt.window {
				t.Fatalf("Reserve = %d, want a positive safety margin below the window", got.Reserve)
			}
		})
	}
}

func TestDeriveHistoryBudget_NeverNegative(t *testing.T) {
	if got := DeriveHistoryBudget(0, 0); got.Total != 0 {
		t.Fatalf("zero window Total = %d, want 0", got.Total)
	}
	if got := DeriveHistoryBudget(8000, 100000); got.Total != 0 {
		t.Fatalf("oversized system prompt Total = %d, want 0", got.Total)
	}
}

// TestAllocate_WorkingQuotaGrowsWithWindow 是 T2 的核心：
// 同一个 Bot 从 8k 换到 128k 窗口时，历史额度必须跟着涨，而不是继续吃 6000。
func TestAllocate_WorkingQuotaGrowsWithWindow(t *testing.T) {
	previous := 0
	for _, window := range []int{8000, 32000, 128000} {
		h := DeriveHistoryBudget(window, 1200).Total
		budget := Allocate(h, DefaultQuotas(h, [4]int{h, h, h, h}))

		if budget.Working <= previous {
			t.Fatalf("window %d: Working = %d, want > %d", window, budget.Working, previous)
		}
		previous = budget.Working
	}
}

func TestAllocate_NeverExceedsTotal(t *testing.T) {
	for _, h := range []int{1, 64, 256, 4240, 20000, 86400} {
		quotas := DefaultQuotas(h, [4]int{h, h, h, h})
		budget := Allocate(h, quotas)

		if budget.Sum() > h {
			t.Fatalf("h=%d: Sum = %d, want <= %d", h, budget.Sum(), h)
		}
		if budget.Total != h {
			t.Fatalf("h=%d: Total = %d, want %d", h, budget.Total, h)
		}
		for layer, got := range []int{budget.Working, budget.Episodic, budget.Semantic, budget.Retrieval} {
			if got < 0 {
				t.Fatalf("h=%d: layer %d got negative quota %d", h, layer, got)
			}
		}
	}
}

// TestAllocate_ScalesDownWhenMinimumsOverflow 小窗口下保底之和会超过 H，
// 必须等比压缩而不是让某一层拿到负数或把总额撑爆。
func TestAllocate_ScalesDownWhenMinimumsOverflow(t *testing.T) {
	const h = 100
	budget := Allocate(h, DefaultQuotas(h, [4]int{h, h, h, 0}))

	if budget.Sum() != h {
		t.Fatalf("Sum = %d, want %d after scale-down", budget.Sum(), h)
	}
	if budget.Semantic <= 0 || budget.Working <= 0 {
		t.Fatalf("scale-down starved a core layer: %+v", budget)
	}
}

func TestDefaultQuotas_SemanticMaxNeverBelowMin(t *testing.T) {
	for _, h := range []int{1, 32, 100, 640} {
		quotas := DefaultQuotas(h, [4]int{})
		if quotas[LayerSemantic].Max < quotas[LayerSemantic].Min {
			t.Fatalf("h=%d: Semantic Max %d < Min %d",
				h, quotas[LayerSemantic].Max, quotas[LayerSemantic].Min)
		}
	}
}
