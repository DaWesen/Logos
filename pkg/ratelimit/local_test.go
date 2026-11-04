package ratelimit

import (
	"testing"
	"time"
)

// TestLocalLimiter_FixedWindow 固定窗口的核心语义：
// 窗口内超过配额拒绝，窗口切换后重新放行。
func TestLocalLimiter_FixedWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := NewLocalLimiter()
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if !l.Allow("k", 3, time.Minute) {
			t.Fatalf("第 %d 次请求应在配额内", i+1)
		}
	}
	if l.Allow("k", 3, time.Minute) {
		t.Fatal("第 4 次请求应被拒绝")
	}

	now = now.Add(time.Minute)
	if !l.Allow("k", 3, time.Minute) {
		t.Fatal("新窗口应重新放行")
	}
}

// TestLocalLimiter_IndependentKeys 不同 key 的配额互不影响。
func TestLocalLimiter_IndependentKeys(t *testing.T) {
	l := NewLocalLimiter()

	if !l.Allow("user-1", 1, time.Minute) {
		t.Fatal("user-1 首个请求应放行")
	}
	if l.Allow("user-1", 1, time.Minute) {
		t.Fatal("user-1 第二个请求应被拒绝")
	}
	if !l.Allow("user-2", 1, time.Minute) {
		t.Fatal("user-2 不应受 user-1 影响")
	}
}

// TestLocalLimiter_Disabled 非法配额按「该层未启用」处理，不能误伤流量。
func TestLocalLimiter_Disabled(t *testing.T) {
	l := NewLocalLimiter()
	for i := 0; i < 10; i++ {
		if !l.Allow("k", 0, time.Minute) || !l.Allow("k", -1, 0) {
			t.Fatal("limit/window 非正时应直接放行")
		}
	}
}

// TestLocalLimiter_Bounded 兜底器必须有内存上限：
// 大量一次性 key 不能把 map 撑到无界。
func TestLocalLimiter_Bounded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := NewLocalLimiter()
	l.maxEntries = 8
	l.now = func() time.Time { return now }

	for i := 0; i < 100; i++ {
		l.Allow(string(rune('a'+i%26))+string(rune('0'+i%10)), 10, time.Minute)
	}

	l.mu.Lock()
	size := len(l.buckets)
	l.mu.Unlock()
	if size > l.maxEntries {
		t.Fatalf("bucket 数 = %d，超过上限 %d", size, l.maxEntries)
	}
}
