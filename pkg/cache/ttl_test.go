package cache

import (
	"testing"
	"time"
)

func TestJitterTTL(t *testing.T) {
	const ttl = time.Hour
	if got := JitterTTL(ttl, 0.1, 0); got != 54*time.Minute {
		t.Fatalf("下界 = %v, want 54m", got)
	}
	if got := JitterTTL(ttl, 0.1, 0.5); got != ttl {
		t.Fatalf("中点 = %v, want %v", got, ttl)
	}
	// rnd 只会取 [0,1)，上界用 0.999999 避免刚好等于 1+ratio。
	if got := JitterTTL(ttl, 0.1, 0.999999); got <= ttl || got > 66*time.Minute {
		t.Fatalf("上界 = %v, 应落在 (%v, 66m]", got, ttl)
	}

	// ratio 非法值收敛：负数等于不抖动，>1 按 1 处理（即 [0, 2*ttl)）。
	if got := JitterTTL(ttl, -1, 0.3); got != ttl {
		t.Fatalf("ratio<0 应保持原值, got %v", got)
	}
	if got := JitterTTL(ttl, 5, 0); got != 0 {
		t.Fatalf("ratio>1 应被收敛为 1, got %v", got)
	}
	if got := JitterTTL(0, 0.1, 0.5); got != 0 {
		t.Fatalf("ttl=0 应原样返回, got %v", got)
	}
}
