package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"Logos/pkg/lazyinit"

	"github.com/minio/minio-go/v7"
)

func useFakeMinioLoader(t *testing.T, opts lazyinit.Options, load func() (*minio.Client, error)) {
	t.Helper()
	original := minioResource
	minioResource = lazyinit.New(opts, load)
	t.Cleanup(func() { minioResource = original })
}

// 回归：sync.Once 版本在首次失败后不再执行，第二次调用返回 (nil, nil)，
// 调用方会把它当成功并解引用 nil client。
func TestInitMinio_FailureNeverReturnsNilClientWithoutError(t *testing.T) {
	wantErr := errors.New("minio unreachable")
	useFakeMinioLoader(t, lazyinit.Options{RetryInterval: time.Hour}, func() (*minio.Client, error) {
		return nil, wantErr
	})

	for i := 0; i < 3; i++ {
		client, err := InitMinio()
		if client != nil {
			t.Fatalf("第 %d 次调用返回了非 nil client: %v", i+1, client)
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("第 %d 次调用错误丢失: %v", i+1, err)
		}
	}
}

// 负缓存窗口内不重复探测，窗口过后重新尝试并允许自愈。
func TestInitMinio_RetriesAfterBackoffAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	calls := 0

	useFakeMinioLoader(t, lazyinit.Options{
		RetryInterval: minioInitRetryInterval,
		Now:           func() time.Time { return now },
	}, func() (*minio.Client, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("still starting")
		}
		return &minio.Client{}, nil
	})

	if _, err := InitMinio(); err == nil {
		t.Fatal("首次探测应失败")
	}
	if calls != 1 {
		t.Fatalf("首次探测次数 = %d, want 1", calls)
	}

	if _, err := InitMinio(); err == nil {
		t.Fatal("负缓存窗口内应继续返回错误")
	}
	if calls != 1 {
		t.Fatalf("负缓存窗口内不应重复探测, calls = %d", calls)
	}

	now = now.Add(minioInitRetryInterval + time.Second)

	client, err := InitMinio()
	if err != nil {
		t.Fatalf("窗口过后应重试并成功: %v", err)
	}
	if client == nil {
		t.Fatal("成功时不应返回 nil client")
	}
	if calls != 2 {
		t.Fatalf("窗口过后应重新探测, calls = %d", calls)
	}

	if _, err := InitMinio(); err != nil {
		t.Fatalf("成功结果应复用: %v", err)
	}
	if calls != 2 {
		t.Fatalf("成功后不应重复探测, calls = %d", calls)
	}
}

// 并发首次调用只能探测一次；失败时每个调用方都必须拿到错误，
// 不能有人拿到 (nil, nil) 然后在 nil client 上崩溃。
func TestInitMinio_ConcurrentCallsShareSingleProbe(t *testing.T) {
	const callers = 16
	var probeCount int
	var probeMu sync.Mutex
	wantErr := errors.New("probe failed")

	useFakeMinioLoader(t, lazyinit.Options{RetryInterval: time.Hour}, func() (*minio.Client, error) {
		probeMu.Lock()
		probeCount++
		probeMu.Unlock()
		time.Sleep(10 * time.Millisecond)
		return nil, wantErr
	})

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := InitMinio()
			if client != nil {
				errs <- errors.New("失败路径返回了非 nil client")
				return
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if !errors.Is(err, wantErr) {
			t.Fatalf("并发调用错误不一致: %v", err)
		}
	}
	if probeCount != 1 {
		t.Fatalf("并发首次调用应只探测一次, probeCount = %d", probeCount)
	}
}

// ctx 已取消时必须在调用后端之前返回，不能退化成无上限的 Background 调用。
func TestMinioManager_CanceledContextStopsBeforeBackend(t *testing.T) {
	m := NewMinioManager(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := m.DownloadFileContext(ctx, "b", "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DownloadFileContext err = %v, want context.Canceled", err)
	}
	if err := m.DeleteFileContext(ctx, "b", "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteFileContext err = %v, want context.Canceled", err)
	}
	if _, err := m.GetFileURLContext(ctx, "b", "k", 60); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetFileURLContext err = %v, want context.Canceled", err)
	}
	if _, err := m.FileExistsContext(ctx, "b", "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("FileExistsContext err = %v, want context.Canceled", err)
	}
}
