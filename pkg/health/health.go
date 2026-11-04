// Package health 提供进程级「存活 / 就绪」检查。
//
// 两者语义必须分开：
//   - 存活（live）只回答「进程还在不在」，永远不查依赖。依赖挂了不是重启进程的理由，
//     否则数据库抖动会让所有副本一起被编排系统重启，把一次故障放大成雪崩；
//   - 就绪（ready）回答「现在能不能接流量」，依赖不可用时应主动摘除，让请求落到
//     仍然健康的副本，而不是继续接收后每条都超时。
//
// 探针本身也会成为故障源：探测必须带超时，并发探测要合并成一次，结果要短暂缓存。
// 否则 10 个探针 × 每秒多次 × 下游已经变慢，会把「依赖慢」变成「依赖被打死」。
package health

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrTimeout 表示检查在预算内没有完成。
var ErrTimeout = errors.New("health: check timed out")

// CheckFunc 执行一次依赖检查。返回 nil 表示健康。
// 实现必须尊重 ctx：探测预算是硬上限，不能被下游的默认超时放大。
type CheckFunc func(ctx context.Context) error

type check struct {
	name string
	fn   CheckFunc
	soft bool
}

// Registry 管理一组就绪检查。
//
// 零值不可用，必须用 New 构造。Registry 可被多个 HTTP handler / gRPC 探针并发调用。
type Registry struct {
	mu      sync.RWMutex
	checks  []check
	timeout time.Duration
	ttl     time.Duration
	cache   *resultCache
}

// Option 调整 Registry 行为。
type Option func(*Registry)

// WithTimeout 设置单次探测总预算（所有检查并发执行，共享同一个 deadline）。
func WithTimeout(d time.Duration) Option {
	return func(r *Registry) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// WithCacheTTL 设置探测结果缓存时长。TTL<=0 表示不缓存。
//
// 缓存的目的不是「省一次查询」，而是给探针本身限流：K8s 默认每 10s 探测一次，
// 多副本 + 多层探针叠加时，依赖会被大量重复的 Ping 淹没。
func WithCacheTTL(d time.Duration) Option {
	return func(r *Registry) {
		if d > 0 {
			r.ttl = d
		}
	}
}

// DefaultTimeout 是单次探测总预算。
const DefaultTimeout = 2 * time.Second

// DefaultCacheTTL 是探测结果缓存时长。
const DefaultCacheTTL = 2 * time.Second

// New 创建 Registry。
func New(opts ...Option) *Registry {
	r := &Registry{
		timeout: DefaultTimeout,
		ttl:     DefaultCacheTTL,
		cache:   newResultCache(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Register 注册一个具名检查。重复注册同名检查会覆盖旧实现，
// 便于测试替换依赖而不必重建整个 Registry。
func (r *Registry) Register(name string, fn CheckFunc) {
	r.register(name, fn, false)
}

// RegisterOptional 注册「降级不摘流量」的检查。
//
// 典型场景是网关的 Redis 限流：Redis 不可用时还有单机限流兜底，核心收发消息不受影响。
// 如果把这类依赖也算成就绪失败，一次缓存抖动会让整个网关被摘除，故障反而被放大。
func (r *Registry) RegisterOptional(name string, fn CheckFunc) {
	r.register(name, fn, true)
}

func (r *Registry) register(name string, fn CheckFunc, soft bool) {
	if name == "" || fn == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.checks {
		if r.checks[i].name == name {
			r.checks[i].fn = fn
			r.checks[i].soft = soft
			return
		}
	}
	r.checks = append(r.checks, check{name: name, fn: fn, soft: soft})
}

// Unregister 移除一个检查，返回是否存在。
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.checks {
		if r.checks[i].name == name {
			r.checks = append(r.checks[:i], r.checks[i+1:]...)
			return true
		}
	}
	return false
}

// Names 返回已注册的检查名（按字典序，便于断言与日志稳定）。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.checks))
	for _, c := range r.checks {
		names = append(names, c.name)
	}
	sort.Strings(names)
	return names
}

// Result 是一次就绪探测的聚合结果。
type Result struct {
	Ready     bool              `json:"ready"`
	Checks    map[string]string `json:"checks,omitempty"`
	Degraded  []string          `json:"degraded,omitempty"`
	CheckedAt time.Time         `json:"checked_at"`
	Cached    bool              `json:"cached"`
}

// Failed 返回导致未就绪的硬依赖检查名，按字典序。
func (r Result) Failed() []string {
	names := make([]string, 0, len(r.Checks))
	for name, status := range r.Checks {
		if status != "ok" && status != "degraded" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Ready 执行全部检查。任何一项失败即整体未就绪。
//
// 所有检查并发执行并共享一个 deadline：串行探测的耗时是各项之和，
// 依赖越多越容易超过编排系统的 probe timeout，表现为「健康检查超时 = 服务不健康」。
func (r *Registry) Ready(ctx context.Context) Result {
	start := time.Now()
	r.mu.RLock()
	checks := make([]check, len(r.checks))
	copy(checks, r.checks)
	timeout := r.timeout
	ttl := r.ttl
	r.mu.RUnlock()

	if len(checks) == 0 {
		return Result{
			Ready:     true,
			Checks:    map[string]string{},
			CheckedAt: start,
		}
	}

	if ttl > 0 {
		if cached, ok := r.cache.get(ttl); ok {
			cached.Checks = cloneStatuses(cached.Checks)
			cached.Degraded = append([]string(nil), cached.Degraded...)
			cached.Cached = true
			return cached
		}
	}

	return r.cache.do(ctx, func(ctx context.Context) Result {
		return r.run(ctx, checks, timeout, start)
	})
}

func (r *Registry) run(ctx context.Context, checks []check, timeout time.Duration, start time.Time) Result {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		name string
		err  error
		soft bool
	}
	outcomes := make(chan outcome, len(checks))
	var wg sync.WaitGroup

	for _, c := range checks {
		wg.Add(1)
		go func(c check) {
			defer wg.Done()
			err := runCheck(probeCtx, c.fn)
			outcomes <- outcome{name: c.name, err: err, soft: c.soft}
		}(c)
	}

	go func() {
		wg.Wait()
		close(outcomes)
	}()

	statuses := make(map[string]string, len(checks))
	degraded := make([]string, 0, len(checks))
	ready := true
	for {
		select {
		case o, ok := <-outcomes:
			if !ok {
				// 所有检查都返回了，但总预算可能已经耗尽：
				// 剩余未写入的检查一律按超时处理，不能因为「还没轮到」就算健康。
				for _, c := range checks {
					if _, seen := statuses[c.name]; !seen {
						statuses[c.name] = ErrTimeout.Error()
						if !c.soft {
							ready = false
						}
					}
				}
				return Result{
					Ready:     ready,
					Checks:    statuses,
					Degraded:  degraded,
					CheckedAt: start,
				}
			}
			if o.err != nil {
				if o.soft {
					statuses[o.name] = "degraded"
					degraded = append(degraded, o.name)
				} else {
					ready = false
					statuses[o.name] = o.err.Error()
				}
			} else {
				statuses[o.name] = "ok"
			}
		case <-probeCtx.Done():
			// 硬超时：不再等待慢检查，按当前已知结果立即判定未就绪。
			// 这也是探针应有的语义——探测超时本身就是「不健康」。
			for _, c := range checks {
				if _, seen := statuses[c.name]; !seen {
					if c.soft {
						statuses[c.name] = "degraded"
						degraded = append(degraded, c.name)
					} else {
						statuses[c.name] = ErrTimeout.Error()
						ready = false
					}
				}
			}
			return Result{
				Ready:     ready,
				Checks:    statuses,
				Degraded:  degraded,
				CheckedAt: start,
			}
		}
	}
}

// runCheck 把 panic 也当作失败：一个写错的探针不应该让整个 /readyz 返回 500，
// 更不应该把进程打崩。
func runCheck(ctx context.Context, fn CheckFunc) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("health: check panic: %v", rec)
		}
	}()
	return fn(ctx)
}

func cloneStatuses(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// resultCache 合并并发探测，并在 TTL 内复用结果。
//
// 与 singleflight 的区别：这里需要「失败也缓存」。依赖挂掉时每个探针都去重试，
// 会让已经过载的下游雪上加霜；缓存失败结果可以把探测频率压到 TTL 一次。
type resultCache struct {
	mu       sync.Mutex
	inflight chan struct{}
	last     Result
	at       time.Time
}

func newResultCache() *resultCache {
	return &resultCache{}
}

func (c *resultCache) get(ttl time.Duration) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at.IsZero() || time.Since(c.at) > ttl {
		return Result{}, false
	}
	return c.last, true
}

func (c *resultCache) do(ctx context.Context, fn func(context.Context) Result) Result {
	c.mu.Lock()
	if c.inflight != nil {
		ch := c.inflight
		c.mu.Unlock()
		select {
		case <-ch:
			c.mu.Lock()
			res := c.last
			c.mu.Unlock()
			return res
		case <-ctx.Done():
			return Result{Ready: false, Checks: map[string]string{"probe": ctx.Err().Error()}, CheckedAt: time.Now()}
		}
	}
	ch := make(chan struct{})
	c.inflight = ch
	c.mu.Unlock()

	res := fn(ctx)

	c.mu.Lock()
	c.last = res
	c.at = time.Now()
	c.inflight = nil
	c.mu.Unlock()
	close(ch)
	return res
}
