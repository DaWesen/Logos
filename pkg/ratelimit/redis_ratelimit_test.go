package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// TestNextSlidingMemberUnique 同一毫秒内的多次请求必须生成互不相同的成员，
// 否则 ZADD 只更新分数不新增成员，窗口计数会被锁死（旧的「毫秒-n」实现缺陷）。
func TestNextSlidingMemberUnique(t *testing.T) {
	const calls = 2000
	seen := make(map[string]struct{}, calls)
	for i := 0; i < calls; i++ {
		member := nextSlidingMember(1700000000000)
		if _, ok := seen[member]; ok {
			t.Fatalf("成员重复: %s", member)
		}
		seen[member] = struct{}{}
	}
}

// TestGetGlobalLimiterConcurrent 限流中间件每请求都会取 limiter，
// 并发场景下必须安全，且同名配置复用同一实例。配合 -race 运行。
func TestGetGlobalLimiterConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "user_" + string(rune('a'+i%8))
			GetGlobalLimiter(name, "fixed", nil, 50, time.Minute)
			GetGlobalLimiter("global_api", "sliding", nil, 600, time.Minute)
		}(i)
	}
	wg.Wait()

	first := GetGlobalLimiter("global_api", "sliding", nil, 600, time.Minute)
	second := GetGlobalLimiter("global_api", "sliding", nil, 600, time.Minute)
	if first != second {
		t.Fatal("同一配置应复用同一个 limiter 实例")
	}
}
