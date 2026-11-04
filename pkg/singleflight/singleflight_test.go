package singleflight

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroup_MergesConcurrentFills(t *testing.T) {
	g := NewGroup[int](time.Second)

	var calls int32
	release := make(chan struct{})
	fill := func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return 42, nil
	}

	const waiters = 20
	var wg sync.WaitGroup
	results := make([]int, waiters)
	shared := make([]bool, waiters)
	wg.Add(waiters)
	for i := 0; i < waiters; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], shared[i], _ = g.Do(context.Background(), "same-key", fill)
		}(i)
	}

	// 等所有等待者都挂到同一个 call 上，再放行回源。
	deadline := time.Now().Add(2 * time.Second)
	for g.Waiters("same-key") < waiters-1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("同一 key 的并发回源应只执行 1 次，实际 %d 次", got)
	}
	for i, v := range results {
		if v != 42 {
			t.Fatalf("第 %d 个等待者拿到 %d, want 42", i, v)
		}
	}
	sharedCount := 0
	for _, s := range shared {
		if s {
			sharedCount++
		}
	}
	if sharedCount != waiters-1 {
		t.Fatalf("应有 %d 个等待者被标记为 shared，实际 %d", waiters-1, sharedCount)
	}
}

func TestGroup_DifferentKeysDoNotBlock(t *testing.T) {
	g := NewGroup[string](time.Second)
	var calls int32
	fill := func(ctx context.Context) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "ok", nil
	}
	var wg sync.WaitGroup
	for _, k := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			if _, _, err := g.Do(context.Background(), k, fill); err != nil {
				t.Errorf("key %s 回源失败: %v", k, err)
			}
		}(k)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("不同 key 不应被合并，实际回源 %d 次", got)
	}
}

func TestGroup_WaiterHonorsItsOwnContext(t *testing.T) {
	g := NewGroup[int](time.Second)
	release := make(chan struct{})
	defer close(release)

	go func() {
		_, _, _ = g.Do(context.Background(), "k", func(ctx context.Context) (int, error) {
			<-release
			return 1, nil
		})
	}()
	deadline := time.Now().Add(time.Second)
	for g.Waiters("k") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, shared, err := g.Do(ctx, "k", func(ctx context.Context) (int, error) {
		t.Error("等待者不应执行回源")
		return 0, nil
	})
	if !shared {
		t.Fatal("第二个调用应走合并路径")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("等待者应随自己的 ctx 立刻返回，实际耗时 %v", time.Since(start))
	}
}

func TestGroup_RecoversPanic(t *testing.T) {
	g := NewGroup[int](time.Second)
	_, _, err := g.Do(context.Background(), "k", func(ctx context.Context) (int, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("回源 panic 应转成错误返回")
	}
	// 失败后 key 必须被清理，否则后续请求会永远卡在已关闭的 call 上。
	if _, _, err := g.Do(context.Background(), "k", func(ctx context.Context) (int, error) {
		return 7, nil
	}); err != nil {
		t.Fatalf("panic 后同一 key 应可重试, err = %v", err)
	}
}

func TestGroup_FillTimeout(t *testing.T) {
	g := NewGroup[int](30 * time.Millisecond)
	_, _, err := g.Do(context.Background(), "k", func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
