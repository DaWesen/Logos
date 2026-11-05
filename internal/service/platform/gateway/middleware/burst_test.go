package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeBurstCache struct {
	mu          sync.Mutex
	values      map[string]int64
	ttls        map[string]time.Duration
	incrErr     error
	existsErr   error
	expireErr   error
	setErr      error
	incrCalls   int
	deleteCalls int
}

func newFakeBurstCache() *fakeBurstCache {
	return &fakeBurstCache{
		values: make(map[string]int64),
		ttls:   make(map[string]time.Duration),
	}
}

func (f *fakeBurstCache) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.existsErr != nil {
		return false, f.existsErr
	}
	_, ok := f.values[key]
	return ok, nil
}

func (f *fakeBurstCache) IncrBy(_ context.Context, key string, value int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.incrErr != nil {
		return 0, f.incrErr
	}
	f.incrCalls++
	f.values[key] += value
	return f.values[key], nil
}

func (f *fakeBurstCache) Expire(_ context.Context, key string, expiration time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.expireErr != nil {
		return f.expireErr
	}
	f.ttls[key] = expiration
	return nil
}

func (f *fakeBurstCache) Set(_ context.Context, key string, _ interface{}, expiration time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.values[key] = 1
	f.ttls[key] = expiration
	return nil
}

func (f *fakeBurstCache) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	delete(f.values, key)
	delete(f.ttls, key)
	return nil
}

func (f *fakeBurstCache) ttl(key string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ttls[key]
}

func (f *fakeBurstCache) delete(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.values, key)
}

// TestBurstGuard_BlockedKeyShortCircuits 已封禁的 IP 必须在自增计数前就被拒绝，
// 否则封禁期间仍在消耗 Redis 写入。
func TestBurstGuard_BlockedKeyShortCircuits(t *testing.T) {
	fake := newFakeBurstCache()
	fake.values["blocked:10.0.0.1"] = 1
	g := newBurstGuard(fake, 1000, 5*time.Minute)

	allowed, degraded := g.allow(context.Background(), "10.0.0.1")
	if allowed || degraded {
		t.Fatalf("已封禁 IP 应直接拒绝，实际 allowed=%v degraded=%v", allowed, degraded)
	}
	if fake.incrCalls != 0 {
		t.Fatalf("封禁期间不应再自增计数，实际调用 %d 次", fake.incrCalls)
	}
}

// TestBurstGuard_SetsBlockAfterThreshold 超限后必须写入封禁键，
// 且封禁时长为窗口的两倍。
func TestBurstGuard_SetsBlockAfterThreshold(t *testing.T) {
	fake := newFakeBurstCache()
	g := newBurstGuard(fake, 2, 5*time.Minute)

	for i := 0; i < 2; i++ {
		if allowed, _ := g.allow(context.Background(), "10.0.0.2"); !allowed {
			t.Fatalf("第 %d 次请求不应被拒绝", i+1)
		}
	}
	if allowed, _ := g.allow(context.Background(), "10.0.0.2"); allowed {
		t.Fatal("超过阈值后应拒绝")
	}
	if fake.values["blocked:10.0.0.2"] != 1 {
		t.Fatal("超限后应写入 blocked 键")
	}
	if got := fake.ttl("blocked:10.0.0.2"); got != 10*time.Minute {
		t.Fatalf("封禁时长 = %s, want 10m", got)
	}
}

// TestBurstGuard_BlockSurvivesBurstWindow 回归测试：
// 旧实现只写 blocked 键、从不读取，burst 计数窗口一过期封禁就失效。
func TestBurstGuard_BlockSurvivesBurstWindow(t *testing.T) {
	fake := newFakeBurstCache()
	g := newBurstGuard(fake, 1, time.Minute)

	if allowed, _ := g.allow(context.Background(), "10.0.0.3"); !allowed {
		t.Fatal("第一次请求应放行")
	}
	if allowed, _ := g.allow(context.Background(), "10.0.0.3"); allowed {
		t.Fatal("超限后应拒绝")
	}

	// 模拟 burst 计数窗口过期，但封禁键仍在有效期内。
	fake.delete("burst:10.0.0.3")
	if allowed, _ := g.allow(context.Background(), "10.0.0.3"); allowed {
		t.Fatal("封禁键仍存在时必须继续拒绝，否则封禁形同虚设")
	}
}

// TestBurstGuard_DegradesToLocal Redis 不可用时退化为单机窗口，
// 窗口内超限同样拒绝，不能变成无限流。
func TestBurstGuard_DegradesToLocal(t *testing.T) {
	fake := newFakeBurstCache()
	fake.incrErr = errors.New("redis down")
	g := newBurstGuard(fake, 2, time.Minute)

	for i := 0; i < 2; i++ {
		allowed, degraded := g.allow(context.Background(), "10.0.0.4")
		if !allowed || !degraded {
			t.Fatalf("第 %d 次请求应放行并标记降级，实际 allowed=%v degraded=%v", i+1, allowed, degraded)
		}
	}
	allowed, degraded := g.allow(context.Background(), "10.0.0.4")
	if allowed || !degraded {
		t.Fatalf("降级窗口内超限应拒绝，实际 allowed=%v degraded=%v", allowed, degraded)
	}
}

// TestBurstGuard_SetsBurstTTL 第一次自增时必须设置窗口过期时间，
// 否则 burst 计数会永久累积。
func TestBurstGuard_SetsBurstTTL(t *testing.T) {
	fake := newFakeBurstCache()
	g := newBurstGuard(fake, 10, 5*time.Minute)

	if allowed, _ := g.allow(context.Background(), "10.0.0.5"); !allowed {
		t.Fatal("首次请求应放行")
	}
	if got := fake.ttl("burst:10.0.0.5"); got != 5*time.Minute {
		t.Fatalf("burst TTL = %s, want 5m", got)
	}
}

// TestBurstGuard_ExpireFailureResetsCounterKey 计数键没有 TTL 会从「短期封禁」
// 退化成「永久封禁」：Expire 失败后必须删键重开窗口，而不是留下无过期计数器。
func TestBurstGuard_ExpireFailureResetsCounterKey(t *testing.T) {
	fake := newFakeBurstCache()
	fake.expireErr = errors.New("redis down")
	g := newBurstGuard(fake, 3, time.Minute)

	allowed, degraded := g.allow(context.Background(), "10.0.0.6")
	if !allowed || degraded {
		t.Fatalf("首次请求应放行且不降级，实际 allowed=%v degraded=%v", allowed, degraded)
	}
	if fake.deleteCalls != 1 {
		t.Fatalf("Expire 失败后必须删除计数键，Delete 调用 %d 次", fake.deleteCalls)
	}
	if _, ok := fake.values["burst:10.0.0.6"]; ok {
		t.Fatal("删除后计数键不应残留，否则仍可能永久累积")
	}
}

// TestBurstGuard_BlockWriteFailureKeepsDenying 封禁键写失败时当前请求仍要拒绝；
// 只要计数还超阈值，后续请求继续被拒，不能因为写失败就放行。
func TestBurstGuard_BlockWriteFailureKeepsDenying(t *testing.T) {
	fake := newFakeBurstCache()
	fake.setErr = errors.New("redis down")
	g := newBurstGuard(fake, 2, time.Minute)

	for i := 0; i < 2; i++ {
		if allowed, _ := g.allow(context.Background(), "10.0.0.7"); !allowed {
			t.Fatalf("第 %d 次请求未超过阈值，应放行", i+1)
		}
	}
	if allowed, _ := g.allow(context.Background(), "10.0.0.7"); allowed {
		t.Fatal("超过阈值必须拒绝，即使封禁键写入失败")
	}
	if _, ok := fake.values["blocked:10.0.0.7"]; ok {
		t.Fatal("Set 失败不应留下封禁键")
	}
	if allowed, _ := g.allow(context.Background(), "10.0.0.7"); allowed {
		t.Fatal("计数仍超阈值时后续请求应继续拒绝")
	}
}

// TestBurstGuard_BlockedLookupFailureFallsThrough 封禁状态查询失败不能中断
// 计数路径，也不能把错误当成「已封禁」误伤所有流量。
func TestBurstGuard_BlockedLookupFailureFallsThrough(t *testing.T) {
	fake := newFakeBurstCache()
	fake.existsErr = errors.New("redis down")
	g := newBurstGuard(fake, 2, time.Minute)

	if allowed, _ := g.allow(context.Background(), "10.0.0.8"); !allowed {
		t.Fatal("封禁状态查询失败时应继续走计数并放行首次请求")
	}
	if got := fake.ttl("burst:10.0.0.8"); got != time.Minute {
		t.Fatalf("计数值仍应正常写入并带 TTL，实际 %s", got)
	}
}
