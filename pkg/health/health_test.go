package health

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_NoChecksIsReady(t *testing.T) {
	r := New()
	res := r.Ready(context.Background())
	if !res.Ready {
		t.Fatal("没有注册检查时应视为就绪")
	}
	if len(res.Checks) != 0 {
		t.Fatalf("checks = %v, want empty", res.Checks)
	}
}

func TestRegistry_HardDependencyFailureBlocksReady(t *testing.T) {
	r := New(WithCacheTTL(0))
	r.Register("postgres", func(context.Context) error {
		return errors.New("connection refused")
	})
	r.Register("redis", func(context.Context) error { return nil })

	res := r.Ready(context.Background())
	if res.Ready {
		t.Fatal("关键依赖失败时不应就绪")
	}
	if got := res.Failed(); len(got) != 1 || got[0] != "postgres" {
		t.Fatalf("failed = %v, want [postgres]", got)
	}
	if res.Checks["redis"] != "ok" {
		t.Fatalf("redis status = %q, want ok", res.Checks["redis"])
	}
}

// TestRegistry_OptionalDependencyDegrades 可选依赖失败只降级，不摘流量。
//
// 网关的 Redis 限流有单机兜底，Redis 抖动不应该让整个网关被判为不可用；
// 但状态必须显式暴露为 degraded，否则「降级」会变成无人知晓的静默状态。
func TestRegistry_OptionalDependencyDegrades(t *testing.T) {
	r := New(WithCacheTTL(0))
	r.Register("postgres", func(context.Context) error { return nil })
	r.RegisterOptional("redis", func(context.Context) error {
		return errors.New("redis timeout")
	})

	res := r.Ready(context.Background())
	if !res.Ready {
		t.Fatal("可选依赖失败不应阻止就绪")
	}
	if res.Checks["redis"] != "degraded" {
		t.Fatalf("redis status = %q, want degraded", res.Checks["redis"])
	}
	if len(res.Degraded) != 1 || res.Degraded[0] != "redis" {
		t.Fatalf("degraded = %v, want [redis]", res.Degraded)
	}
	if len(res.Failed()) != 0 {
		t.Fatalf("可选依赖不应出现在 Failed: %v", res.Failed())
	}
}

func TestRegistry_CheckPanicBecomesFailure(t *testing.T) {
	r := New(WithCacheTTL(0))
	r.Register("broken", func(context.Context) error {
		panic("nil map")
	})

	res := r.Ready(context.Background())
	if res.Ready {
		t.Fatal("检查 panic 必须判定失败")
	}
	if status := res.Checks["broken"]; status == "" || status == "ok" {
		t.Fatalf("panic 应记录失败原因，实际 %q", status)
	}
}

// TestRegistry_TimeoutMarksUnfinishedChecks 慢检查不能拖住整个探测。
//
// 依赖已经变慢时，探针本身必须按预算返回；否则 K8s probe 超时同样会把 Pod
// 判为不健康，而且会积压探测 goroutine。
func TestRegistry_TimeoutMarksUnfinishedChecks(t *testing.T) {
	r := New(WithTimeout(80*time.Millisecond), WithCacheTTL(0))
	r.Register("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	r.Register("fast", func(context.Context) error { return nil })

	start := time.Now()
	res := r.Ready(context.Background())
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("探测耗时 %v，超时预算未生效", elapsed)
	}
	if res.Ready {
		t.Fatal("超时的关键检查必须判定未就绪")
	}
	if res.Checks["fast"] != "ok" {
		t.Fatalf("fast status = %q, want ok", res.Checks["fast"])
	}
}

func TestRegistry_OptionalTimeoutOnlyDegrades(t *testing.T) {
	r := New(WithTimeout(50*time.Millisecond), WithCacheTTL(0))
	r.RegisterOptional("redis", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	res := r.Ready(context.Background())
	if !res.Ready {
		t.Fatal("可选检查超时不应阻止就绪")
	}
	if res.Checks["redis"] != "degraded" {
		t.Fatalf("redis status = %q, want degraded", res.Checks["redis"])
	}
}

// TestRegistry_ConcurrentProbesShareOneRun 并发探针必须合并成一次回源。
//
// 10 个副本同时被探测、每个探针又打一次数据库，依赖恢复瞬间会被探针自己打垮。
func TestRegistry_ConcurrentProbesShareOneRun(t *testing.T) {
	r := New(WithCacheTTL(0))
	var calls int32
	release := make(chan struct{})
	r.Register("postgres", func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		<-release
		return nil
	})

	const probes = 8
	var wg sync.WaitGroup
	results := make([]Result, probes)
	for i := 0; i < probes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = r.Ready(context.Background())
		}(i)
	}

	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("检查执行 %d 次, want 1（并发应合并）", got)
	}
	for i, res := range results {
		if !res.Ready {
			t.Fatalf("probe %d 未就绪", i)
		}
	}
}

func TestRegistry_CacheReusesResultWithinTTL(t *testing.T) {
	r := New(WithCacheTTL(100 * time.Millisecond))
	var calls int32
	r.Register("postgres", func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	})

	first := r.Ready(context.Background())
	if first.Cached {
		t.Fatal("首次探测不应标记为缓存")
	}
	second := r.Ready(context.Background())
	if !second.Cached {
		t.Fatal("TTL 内应命中缓存")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("检查执行 %d 次, want 1", got)
	}

	// 缓存返回的 map 必须是副本：调用方修改不能污染下一次探测。
	second.Checks["postgres"] = "mutated"
	third := r.Ready(context.Background())
	if third.Checks["postgres"] != "ok" {
		t.Fatalf("缓存结果被调用方修改: %v", third.Checks)
	}
}

func TestRegistry_FailureIsCachedToo(t *testing.T) {
	r := New(WithCacheTTL(100 * time.Millisecond))
	var calls int32
	r.Register("postgres", func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		return errors.New("down")
	})

	r.Ready(context.Background())
	res := r.Ready(context.Background())
	if !res.Cached || res.Ready {
		t.Fatalf("失败结果也应缓存并保持未就绪: ready=%v cached=%v", res.Ready, res.Cached)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("失败结果未缓存，检查执行 %d 次", got)
	}
}

func TestRegistry_RegisterOverridesAndUnregister(t *testing.T) {
	r := New(WithCacheTTL(0))
	r.Register("db", func(context.Context) error { return errors.New("down") })
	r.Register("db", func(context.Context) error { return nil })
	r.RegisterOptional("cache", func(context.Context) error { return nil })

	if names := r.Names(); len(names) != 2 || names[0] != "cache" || names[1] != "db" {
		t.Fatalf("names = %v, want [cache db]", names)
	}
	if res := r.Ready(context.Background()); !res.Ready {
		t.Fatalf("同名注册应覆盖旧实现: %v", res.Checks)
	}

	if !r.Unregister("db") {
		t.Fatal("unregister 已存在的检查应返回 true")
	}
	if r.Unregister("db") {
		t.Fatal("重复 unregister 应返回 false")
	}
}

func TestRegistry_NamesAreSorted(t *testing.T) {
	r := New()
	r.Register("z", func(context.Context) error { return nil })
	r.Register("a", func(context.Context) error { return nil })
	names := r.Names()
	if len(names) != 2 || names[0] != "a" || names[1] != "z" {
		t.Fatalf("names = %v, want sorted", names)
	}
}
