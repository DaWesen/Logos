package gateway

import (
	"testing"
	"time"
)

// fakeClock 可推进的注入时钟，避免单测 sleep。
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
}

// TestTokenBucketAllowsBurstThenDenies 验证桶容量就是允许的瞬时突发上限：
// 满桶时第 burst+1 帧必须被拒，且不因为时间没走而「凭空」补令牌。
func TestTokenBucketAllowsBurstThenDenies(t *testing.T) {
	clock := newFakeClock()
	const burst = 60
	bucket := NewTokenBucketWithClock(30, burst, clock.Now)

	for i := 0; i < burst; i++ {
		if !bucket.Allow() {
			t.Fatalf("第 %d 帧不应被限流（桶容量 %d）", i+1, burst)
		}
	}
	if bucket.Allow() {
		t.Fatalf("第 %d 帧应被限流：桶已耗尽", burst+1)
	}
	if got := bucket.Tokens(); got != 0 {
		t.Fatalf("耗尽后剩余令牌应为 0，实际 %d", got)
	}
}

// TestTokenBucketRefillsByRate 验证按速率连续补充，而不是按整秒跳跃补充。
func TestTokenBucketRefillsByRate(t *testing.T) {
	clock := newFakeClock()
	bucket := NewTokenBucketWithClock(30, 60, clock.Now)

	// 打空桶
	for i := 0; i < 60; i++ {
		bucket.Allow()
	}
	if bucket.Allow() {
		t.Fatal("桶应已耗尽")
	}

	// 过 1/3 秒，30/s 的速率应补充 10 个令牌（连续补充，不是等到 1 秒才补 30）
	clock.Advance(333 * time.Millisecond)
	allowed := 0
	for i := 0; i < 10; i++ {
		if bucket.Allow() {
			allowed++
		}
	}
	if allowed != 9 && allowed != 10 {
		t.Fatalf("1/3 秒应补充约 10 个令牌，实际放行 %d", allowed)
	}
	if bucket.Allow() {
		t.Fatal("补充的令牌用尽后应再次拒绝")
	}
}

// TestTokenBucketLongTermRate 验证长时间尺度上的平均速率符合配置。
func TestTokenBucketLongTermRate(t *testing.T) {
	clock := newFakeClock()
	// 小容量桶，避免容量上限掩盖补充量
	bucket := NewTokenBucketWithClock(10, 10, clock.Now)

	// 先打空
	for i := 0; i < 10; i++ {
		bucket.Allow()
	}

	// 10 秒后应恰好补满 10 个（容量 10），而不是无限累积
	clock.Advance(10 * time.Second)
	allowed := 0
	for i := 0; i < 20; i++ {
		if bucket.Allow() {
			allowed++
		}
	}
	if allowed != 10 {
		t.Fatalf("10 秒 × 10/s 应补满容量 10 个令牌，实际放行 %d", allowed)
	}
	if bucket.Allow() {
		t.Fatal("容量耗尽后应继续拒绝")
	}
}

// TestTokenBucketClockRollbackDoesNotGrantTokens 验证时钟回拨不会补令牌，也不会把桶锁死。
func TestTokenBucketClockRollbackDoesNotGrantTokens(t *testing.T) {
	clock := newFakeClock()
	bucket := NewTokenBucketWithClock(10, 5, clock.Now)

	for i := 0; i < 5; i++ {
		bucket.Allow()
	}
	if bucket.Allow() {
		t.Fatal("桶应已耗尽")
	}

	clock.Advance(-time.Hour) // 时间倒流
	if bucket.Allow() {
		t.Fatal("时钟回拨不应凭空补令牌")
	}

	// 回拨后继续正常前进，补充应恢复
	clock.Advance(time.Second)
	allowed := 0
	for i := 0; i < 10; i++ {
		if bucket.Allow() {
			allowed++
		}
	}
	if allowed == 0 {
		t.Fatal("时钟回拨后正向时间应继续补充令牌")
	}
}

// TestNilTokenBucketAllows 验证未配置限流器时一律放行，避免 nil 检查散落在调用点。
func TestNilTokenBucketAllows(t *testing.T) {
	var bucket *TokenBucket
	for i := 0; i < 10; i++ {
		if !bucket.Allow() {
			t.Fatal("nil 限流器应放行")
		}
	}
	if got := bucket.Tokens(); got != 0 {
		t.Fatalf("nil 限流器 Tokens() 应为 0，实际 %d", got)
	}
}

// TestInboundRateLimitDefaults 把默认值的关系钉成测试：
// 突发必须大于等于速率（否则连 1 秒的正常脉冲都会被拒），
// 速率保持在「正常人打不满、脚本能打爆」的区间。
func TestInboundRateLimitDefaults(t *testing.T) {
	if InboundBurst < InboundRatePerSecond {
		t.Fatalf("突发(%d)应不小于每秒速率(%d)，否则一秒内的正常活动就会被限流",
			InboundBurst, InboundRatePerSecond)
	}
	if InboundRatePerSecond < 10 {
		t.Fatalf("每秒速率(%d)过低，会误伤正常聊天", InboundRatePerSecond)
	}
	if InboundBurst > 200 {
		t.Fatalf("突发(%d)过高，限制形同虚设", InboundBurst)
	}
}

// TestNoteInboundRateLimitCounts 验证计数与采样日志路径不会重复计数。
func TestNoteInboundRateLimitCounts(t *testing.T) {
	before := InboundRateLimitRejections()
	// 跨过 50 的采样边界，确保采样分支也被执行
	for i := 0; i < 60; i++ {
		NoteInboundRateLimit("websocket", "s1", "u1")
	}
	if got := InboundRateLimitRejections() - before; got != 60 {
		t.Fatalf("应累计 60 次拒绝，实际 %d", got)
	}
}
