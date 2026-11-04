package vector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

func stubMilvusClient(t *testing.T, fn func(context.Context, *milvusclient.ClientConfig) (*milvusclient.Client, error)) {
	t.Helper()
	original := newMilvusClient
	newMilvusClient = fn
	t.Cleanup(func() { newMilvusClient = original })
}

// 回归：原实现固定 60 次 × (10s 超时 + 3s 间隔)，最坏挂 13 分钟。
// 现在必须在总预算内结束，并保留重试。
func TestNewMilvusManagerWithBudget_StopsWithinBudget(t *testing.T) {
	var attempts int32
	stubMilvusClient(t, func(context.Context, *milvusclient.ClientConfig) (*milvusclient.Client, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, errors.New("connection refused")
	})

	start := time.Now()
	_, err := newMilvusManagerWithBudget(
		"127.0.0.1", 19530,
		500*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond,
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("连接持续失败时应返回错误")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("启动预算未被遵守，耗时 %v", elapsed)
	}
	if got := atomic.LoadInt32(&attempts); got < 2 {
		t.Fatalf("预算内应至少重试一次，attempts = %d", got)
	}
}

// 单次尝试的超时必须被剩余预算截断，不能出现「预算 60ms、单次超时 10s」。
func TestNewMilvusManagerWithBudget_AttemptTimeoutCappedByBudget(t *testing.T) {
	stubMilvusClient(t, func(ctx context.Context, _ *milvusclient.ClientConfig) (*milvusclient.Client, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})

	start := time.Now()
	_, err := newMilvusManagerWithBudget(
		"127.0.0.1", 19530,
		200*time.Millisecond, 20*time.Millisecond, 10*time.Second,
	)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("应返回超时错误")
	}
	if elapsed > time.Second {
		t.Fatalf("单次尝试没有被预算截断，耗时 %v", elapsed)
	}
}

func TestNewMilvusManagerWithBudget_ReturnsOnFirstSuccess(t *testing.T) {
	var attempts int32
	stubMilvusClient(t, func(context.Context, *milvusclient.ClientConfig) (*milvusclient.Client, error) {
		atomic.AddInt32(&attempts, 1)
		return &milvusclient.Client{}, nil
	})

	mgr, err := newMilvusManagerWithBudget(
		"127.0.0.1", 19530,
		time.Second, 20*time.Millisecond, 100*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("首次成功不应报错: %v", err)
	}
	if mgr == nil {
		t.Fatal("成功时应返回 manager")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("成功后不应继续重试，attempts = %d", got)
	}
}
