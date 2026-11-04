// Package lazyinit 提供「失败可重试」的懒初始化原语。
//
// sync.Once 的语义是「只执行一次」，不区分成功还是失败。初始化函数一旦失败，
// once 已经完成，后续调用不会再执行；如果调用方把 (nil, nil) 当成成功，
// 就会在 nil 上解引用。这个坑在 MinIO、ES、Neo4j、Milvus、Eino 的初始化里
// 都出现过，所以统一收敛到这里。
//
// 语义：
//   - 成功结果永久复用，不重复探测依赖；
//   - 失败结果只缓存一个冷却窗口，窗口过后重新尝试，依赖恢复后能自愈；
//   - 并发调用串行化，同一时刻只有一个探测在跑；
//   - 失败时返回零值 + 错误，永远不会返回 (nil, nil)。
package lazyinit

import (
	"sync"
	"time"
)

// Options 控制失败冷却与时钟。零值表示使用 time.Now。
type Options struct {
	// RetryInterval 是失败后的冷却窗口；<=0 表示下一次调用立即重试。
	RetryInterval time.Duration
	// Now 可注入时钟，便于测试冷却窗口；为 nil 时使用 time.Now。
	Now func() time.Time
}

// Resource 是一个并发安全、失败可重试的懒初始化容器。
type Resource[T any] struct {
	load          func() (T, error)
	retryInterval time.Duration
	now           func() time.Time

	mu      sync.Mutex
	ready   bool
	value   T
	lastErr error
	retryAt time.Time
}

// New 构造一个 Resource。load 不能为 nil。
func New[T any](opts Options, load func() (T, error)) *Resource[T] {
	if load == nil {
		panic("lazyinit: load must not be nil")
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Resource[T]{
		load:          load,
		retryInterval: opts.RetryInterval,
		now:           now,
	}
}

// Get 返回已初始化的值；尚未成功时执行（或在冷却结束后重新执行）load。
func (r *Resource[T]) Get() (T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.ready {
		return r.value, nil
	}
	if r.lastErr != nil && r.now().Before(r.retryAt) {
		var zero T
		return zero, r.lastErr
	}

	value, err := r.load()
	if err != nil {
		var zero T
		r.value = zero
		r.lastErr = err
		r.retryAt = r.now().Add(r.retryInterval)
		return zero, err
	}

	r.value = value
	r.ready = true
	r.lastErr = nil
	return value, nil
}

// Ready 报告当前是否已有可复用的成功值，不会触发初始化。
func (r *Resource[T]) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}
