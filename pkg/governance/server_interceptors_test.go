package governance

import (
	"sync"
	"testing"
	"time"
)

func TestTokenBucketLimiter_AllowsUpToCapacity(t *testing.T) {
	// rate=5 表示桶容量 5、每秒补充 5 个令牌
	l := NewTokenBucketLimiter(5)
	for i := 0; i < 5; i++ {
		if !l.Allow() {
			t.Fatalf("request %d should be allowed within capacity", i+1)
		}
	}
	if l.Allow() {
		t.Fatal("request beyond capacity should be rejected")
	}
}

func TestTokenBucketLimiter_RefillsOverTime(t *testing.T) {
	l := NewTokenBucketLimiter(1)
	if !l.Allow() {
		t.Fatal("first request should be allowed")
	}
	if l.Allow() {
		t.Fatal("second immediate request should be rejected")
	}
	// 模拟时间流逝：等待令牌补充（rate=1/s）
	time.Sleep(1100 * time.Millisecond)
	if !l.Allow() {
		t.Fatal("request should be allowed after refill")
	}
}

func TestTokenBucketLimiter_ConcurrentAccess(t *testing.T) {
	// 配合 -race 检测数据竞争：修复前该测试会触发 race detector
	l := NewTokenBucketLimiter(1000)
	var wg sync.WaitGroup
	allowed := make([]int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if l.Allow() {
					allowed[idx]++
				}
			}
		}(i)
	}
	wg.Wait()

	total := 0
	for _, n := range allowed {
		total += n
	}
	// 容量 1000，补充极小，总放行数不应显著超过容量
	if total > 1100 {
		t.Fatalf("allowed %d requests, expected at most ~1000", total)
	}
}
