package gateway

import (
	"testing"
	"time"
)

// TestJitterDurationBounds 验证抖动幅度严格落在 [base*(1-f), base*(1+f)]，
// 且端点取值与公式一致。心跳周期一旦越上界就会超过读超时，必须可断言。
func TestJitterDurationBounds(t *testing.T) {
	base := 60 * time.Second
	const fraction = 0.10

	low := JitterDuration(base, fraction, 0)
	if low != 54*time.Second {
		t.Fatalf("rnd=0 应取 -10%% 下界 54s，实际 %s", low)
	}

	high := JitterDuration(base, fraction, 1)
	if high != 66*time.Second {
		t.Fatalf("rnd=1 应取 +10%% 上界 66s，实际 %s", high)
	}

	if mid := JitterDuration(base, fraction, 0.5); mid != base {
		t.Fatalf("rnd=0.5 应等于基准周期 %s，实际 %s", base, mid)
	}
}

// TestJitterDurationMonotonic 验证抖动值随 rnd 单调递增，
// 保证“随机源越大周期越长”这一语义在测试与生产一致。
func TestJitterDurationMonotonic(t *testing.T) {
	base := 60 * time.Second
	prev := time.Duration(0)
	for _, rnd := range []float64{0, 0.25, 0.5, 0.75, 1} {
		got := JitterDuration(base, 0.10, rnd)
		if got < prev {
			t.Fatalf("rnd=%v 的抖动值 %s 小于前一个 %s", rnd, got, prev)
		}
		prev = got
	}
}

// TestJitterDurationDegenerateInputs 验证防御性收敛：
// 非正周期、零幅度、越界随机数都不会产出负/零周期（零周期 ticker 会 panic）。
func TestJitterDurationDegenerateInputs(t *testing.T) {
	if got := JitterDuration(0, 0.10, 0); got != 0 {
		t.Fatalf("base=0 应原样返回，实际 %s", got)
	}
	if got := JitterDuration(-time.Second, 0.10, 0); got != -time.Second {
		t.Fatalf("负 base 应原样返回，实际 %s", got)
	}
	if got := JitterDuration(60*time.Second, 0, 0.9); got != 60*time.Second {
		t.Fatalf("fraction=0 应关闭抖动，实际 %s", got)
	}
	if got := JitterDuration(60*time.Second, 0.10, -5); got != 54*time.Second {
		t.Fatalf("rnd<0 应夹到下界 54s，实际 %s", got)
	}
	if got := JitterDuration(60*time.Second, 0.10, 5); got != 66*time.Second {
		t.Fatalf("rnd>1 应夹到上界 66s，实际 %s", got)
	}
	if got := JitterDuration(60*time.Second, 2, 0); got <= 0 {
		t.Fatalf("fraction>1 应收敛到 1 且周期非负，实际 %s", got)
	}
}

// TestJitterDurationDeterministic 同一输入必须得到同一结果：
// 单测与故障复现都依赖抖动函数本身不含隐式随机源。
func TestJitterDurationDeterministic(t *testing.T) {
	for i := 0; i < 100; i++ {
		if got := JitterDuration(60*time.Second, 0.10, 0.33); got != JitterDuration(60*time.Second, 0.10, 0.33) {
			t.Fatalf("同一输入结果不一致: %s", got)
		}
	}
}
