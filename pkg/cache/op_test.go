package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestCacheOpResult(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"成功", nil, "ok"},
		{"未命中", redis.Nil, "miss"},
		{"超时", context.DeadlineExceeded, "timeout"},
		{"取消", context.Canceled, "canceled"},
		{"真实错误", errors.New("connection refused"), "error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cacheOpResult(c.err); got != c.want {
				t.Fatalf("cacheOpResult(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

func TestRunOp_AppliesItsOwnTimeout(t *testing.T) {
	start := time.Now()
	_, err := runOp(context.Background(), "get", 30*time.Millisecond, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("缓存操作应在自己的预算内返回，实际耗时 %v", elapsed)
	}
}

func TestRunOp_RespectsShorterCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := runOp(ctx, "get", time.Second, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("不应延长调用方已有的更短 deadline，实际耗时 %v", elapsed)
	}
}
