package ratelimit

import (
	"strings"
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

// TestTokenBucket_HighFrequencyStillRefills 回归测试：
// 旧实现用 math.floor(elapsed*rate) 且每次把 last_refill 重置为 now，
// 100ms 一次、rate=1/s 时每次只补到 0.1 个令牌，floor 后恒为 0，
// 分数部分被永久丢弃——桶打空后再也不会放行。
// 修复后分数令牌会累积，20s 至少应放行 20 次。
func TestTokenBucket_HighFrequencyStillRefills(t *testing.T) {
	const (
		rate     = 1
		capacity = 2
	)
	tokens := float64(capacity)
	last := int64(0)
	allowed := 0

	// 模拟 20 秒内每 100ms 一次请求。
	for i := 1; i <= 200; i++ {
		now := int64(i) * 100_000 // 100ms
		tokens = refillTokens(tokens, last, now, rate, capacity)
		last = now
		if tokens >= 1 {
			tokens -= 1
			allowed++
		}
	}

	if allowed < 20 {
		t.Fatalf("20s 内只放行 %d 次，令牌补充仍被丢弃（期望约 22 次）", allowed)
	}
}

// TestRefillTokens_CapsAtCapacity 空闲再久也不能超过桶容量，否则会放大突发流量。
func TestRefillTokens_CapsAtCapacity(t *testing.T) {
	got := refillTokens(0, 0, int64(time.Hour/time.Microsecond), 10, 5)
	if got != 5 {
		t.Fatalf("补充后令牌 = %v, want 5（容量上限）", got)
	}
}

// TestRefillTokens_ClockBackwards 时钟回拨不能产生负补充量，否则令牌会被扣掉。
func TestRefillTokens_ClockBackwards(t *testing.T) {
	got := refillTokens(3, 1_000_000, 500_000, 10, 5)
	if got != 3 {
		t.Fatalf("时钟回拨后令牌 = %v, want 3（不补充也不扣减）", got)
	}
}

// TestTokenBucketTTL 桶键过期时间不能短于「补满两次」的耗时，
// 否则空闲用户的桶会提前重置为满，凭空多出突发额度。
func TestTokenBucketTTL(t *testing.T) {
	if got := tokenBucketTTL(10, 5); got < time.Hour {
		t.Fatalf("TTL = %s，不应低于 1h", got)
	}
	// 极低速率 + 大容量时，补满时间应主导 TTL。
	if got := tokenBucketTTL(1, 7200); got < 4*time.Hour {
		t.Fatalf("TTL = %s，未覆盖补满所需时间", got)
	}
}

// TestTokenBucketScript_NoFloorRefill 防止分数令牌逻辑被改回 floor：
// 脚本里一旦重新出现 math.floor 作用于补充量，高频场景就会退化。
func TestTokenBucketScript_NoFloorRefill(t *testing.T) {
	if strings.Contains(tokenBucketScript, "math.floor") {
		t.Fatal("令牌补充不能取整，否则分数时间会被丢弃")
	}
	if !strings.Contains(tokenBucketScript, "elapsed * rate") {
		t.Fatal("令牌补充应使用浮点 elapsed * rate")
	}
}

// TestNewTokenBucketLimiter_Normalizes 非法配置必须收敛到可用值，
// 不能让 rate=0 变成永久限流。
func TestNewTokenBucketLimiter_Normalizes(t *testing.T) {
	l := NewTokenBucketLimiter(nil, 0, 0)
	if l.rate < 1 || l.capacity < 1 {
		t.Fatalf("rate/capacity 未归一化: rate=%d capacity=%d", l.rate, l.capacity)
	}
}

// TestParseTokenBucketState_ScientificNotation Redis Lua 的 tostring 会把
// 16 位微秒时间戳写成科学计数法，必须能解析，否则 GetRemaining 会静默不补充。
func TestParseTokenBucketState_ScientificNotation(t *testing.T) {
	tokens, last, hasTime, ok := parseTokenBucketState(map[string]string{
		"tokens":      "3.5",
		"last_refill": "1.7623456789012e+15",
	})
	if !ok || !hasTime {
		t.Fatalf("应能解析科学计数法，实际 ok=%v hasTime=%v", ok, hasTime)
	}
	if tokens != 3.5 {
		t.Errorf("tokens = %v, want 3.5", tokens)
	}
	if last != 1762345678901200 {
		t.Errorf("last_refill = %d, want 1762345678901200", last)
	}
}

// TestParseTokenBucketState_MissingTime 缺少时间时不能按 now-0 补满整个桶。
func TestParseTokenBucketState_MissingTime(t *testing.T) {
	tokens, _, hasTime, ok := parseTokenBucketState(map[string]string{"tokens": "2"})
	if !ok || hasTime {
		t.Fatalf("缺少时间时 ok=%v hasTime=%v，want true/false", ok, hasTime)
	}
	if tokens != 2 {
		t.Errorf("tokens = %v, want 2", tokens)
	}
}

// TestParseTokenBucketState_Invalid 脏数据应返回 ok=false，由调用方回落到容量。
func TestParseTokenBucketState_Invalid(t *testing.T) {
	if _, _, _, ok := parseTokenBucketState(map[string]string{"tokens": "abc"}); ok {
		t.Fatal("非法 tokens 应返回 ok=false")
	}
	if _, _, _, ok := parseTokenBucketState(nil); ok {
		t.Fatal("空状态应返回 ok=false")
	}
}
