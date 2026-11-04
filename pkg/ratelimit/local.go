package ratelimit

import (
	"sync"
	"time"
)

// LocalLimiter 是 Redis 不可用时的单机兜底限流器（固定窗口）。
//
// 它不追求与 Redis 侧一致：Redis 看到的是全集群流量，本地只能看到本进程流量，
// 所以这里的目标是「依赖挂掉时不至于完全裸奔」，而不是精确复刻全局限流。
// 阈值仍按原有单层配额设置，宁可短时误伤，也不让攻击面完全敞开。
type LocalLimiter struct {
	mu         sync.Mutex
	buckets    map[string]localBucket
	now        func() time.Time
	lastPrune  time.Time
	maxEntries int
}

type localBucket struct {
	windowStart time.Time
	window      time.Duration
	count       int
}

// NewLocalLimiter 创建单机兜底限流器。
// maxEntries 是内存上限：兜底器不能因为 key 无限增长把进程撑爆。
func NewLocalLimiter() *LocalLimiter {
	return &LocalLimiter{
		buckets:    make(map[string]localBucket),
		now:        time.Now,
		maxEntries: 100_000,
	}
}

// Allow 判断 key 在 window 内是否还有 limit 次配额。
// limit <= 0 表示该层未启用，直接放行。
func (l *LocalLimiter) Allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 || window <= 0 {
		return true
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(now, window)

	b, ok := l.buckets[key]
	if !ok || b.window != window || now.Sub(b.windowStart) >= window {
		l.buckets[key] = localBucket{windowStart: now, window: window, count: 1}
		return true
	}
	if b.count >= limit {
		return false
	}
	b.count++
	l.buckets[key] = b
	return true
}

// pruneLocked 清理过期窗口，并在 key 数触顶时整体重置。
// 整体重置是有意为之：兜底限流的价值是「挡住突发」，不是精确计费；
// 在极端 key 膨胀下，保留可用性比保留旧计数更重要。
func (l *LocalLimiter) pruneLocked(now time.Time, window time.Duration) {
	if len(l.buckets) < l.maxEntries && now.Sub(l.lastPrune) < window {
		return
	}
	for key, b := range l.buckets {
		if now.Sub(b.windowStart) >= b.window {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) >= l.maxEntries {
		l.buckets = make(map[string]localBucket)
	}
	l.lastPrune = now
}
