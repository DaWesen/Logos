package lazyinit

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 回归 sync.Once 的缺陷：首次失败后不能返回 (零值, nil)。
func TestResource_FailureNeverReturnsZeroWithoutError(t *testing.T) {
	wantErr := errors.New("dependency down")
	r := New(Options{RetryInterval: time.Hour}, func() (*int, error) {
		return nil, wantErr
	})

	for i := 0; i < 3; i++ {
		got, err := r.Get()
		if got != nil {
			t.Fatalf("第 %d 次调用返回非 nil 值: %v", i+1, got)
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("第 %d 次调用错误丢失: %v", i+1, err)
		}
	}
}

func TestResource_RetriesAfterCooldownAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	var calls int32

	r := New(Options{
		RetryInterval: 5 * time.Second,
		Now:           func() time.Time { return now },
	}, func() (string, error) {
		atomic.AddInt32(&calls, 1)
		if calls == 1 {
			return "", errors.New("still starting")
		}
		return "ready", nil
	})

	if _, err := r.Get(); err == nil {
		t.Fatal("首次探测应失败")
	}
	if _, err := r.Get(); err == nil {
		t.Fatal("冷却窗口内应继续返回错误")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("冷却窗口内不应重复探测, calls = %d", got)
	}

	now = now.Add(6 * time.Second)

	got, err := r.Get()
	if err != nil {
		t.Fatalf("冷却结束后应重试成功: %v", err)
	}
	if got != "ready" {
		t.Fatalf("got = %q, want ready", got)
	}

	// 成功结果永久复用。
	if _, err := r.Get(); err != nil {
		t.Fatalf("成功结果应复用: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("成功后不应重复探测, calls = %d", got)
	}
	if !r.Ready() {
		t.Fatal("成功后 Ready() 应为 true")
	}
}

func TestResource_ConcurrentFirstCallProbesOnce(t *testing.T) {
	const callers = 16
	var calls int32
	wantErr := errors.New("probe failed")

	r := New(Options{RetryInterval: time.Hour}, func() (int, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(10 * time.Millisecond)
		return 0, wantErr
	})

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Get(); !errors.Is(err, wantErr) {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("并发调用错误不一致: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("并发首次调用应只探测一次, calls = %d", got)
	}
}

// 零冷却窗口表示「失败后立即重试」，适用于廉价且需要快速自愈的初始化。
func TestResource_ZeroCooldownRetriesImmediately(t *testing.T) {
	var calls int32
	r := New(Options{}, func() (int, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return 0, errors.New("first fail")
		}
		return 7, nil
	})

	if _, err := r.Get(); err == nil {
		t.Fatal("首次应失败")
	}
	got, err := r.Get()
	if err != nil {
		t.Fatalf("零冷却窗口应立即重试: %v", err)
	}
	if got != 7 {
		t.Fatalf("got = %d, want 7", got)
	}
}

func TestNew_PanicsOnNilLoader(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil loader 应 panic")
		}
	}()
	New[int](Options{}, nil)
}
