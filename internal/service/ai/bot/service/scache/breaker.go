package scache

import (
	"sync"
	"time"
)

// breaker 是一个只放行「一个探测请求」的熔断器。
//
// 语义缓存的三条外部依赖（Redis / embedding / 判官）失败模式完全不同，
// 但处理原则一致：连续失败到阈值后停止调用，冷却期结束只放一个请求
// 去探测，成功则恢复，失败则重新计时。
type breaker struct {
	mu        sync.Mutex
	fails     int
	threshold int
	cooldown  time.Duration
	openUntil time.Time
	probing   bool
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	if threshold <= 0 {
		threshold = 1
	}
	if cooldown < 0 {
		cooldown = 0
	}
	return &breaker{threshold: threshold, cooldown: cooldown}
}

// allow 判断本次调用是否放行。
//
// 冷却期结束后不是立刻全部放行，而是只允许一个探测请求：
// 下游刚恢复时最脆弱，几百个并发请求同时打过去会把故障放大成雪崩。
func (b *breaker) allow(now time.Time) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.openUntil.IsZero() {
		return true
	}
	if now.After(b.openUntil) {
		if !b.probing {
			b.probing = true
			return true
		}
		return false
	}
	return false
}

// success 关闭熔断并清零失败计数。
func (b *breaker) success() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fails = 0
	b.openUntil = time.Time{}
	b.probing = false
}

// failure 记录一次失败；达到阈值后打开熔断。
func (b *breaker) failure(now time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	b.fails++
	if b.fails >= b.threshold {
		b.openUntil = now.Add(b.cooldown)
	}
}

// state 只用于日志与测试，不参与判定。
func (b *breaker) state(now time.Time) (fails int, open bool) {
	if b == nil {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fails, !b.openUntil.IsZero() && !now.After(b.openUntil)
}
