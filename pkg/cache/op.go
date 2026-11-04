package cache

import (
	"context"
	"errors"
	"time"

	"Logos/pkg/obs"

	"github.com/redis/go-redis/v9"
)

// 缓存操作的独立时间预算。
//
// 为什么不能直接用请求 ctx：gRPC 服务端超时是 30s，Redis 一旦抖动，
// 一次缓存读会把整个请求拖到 30s——缓存本该是「加速器」，
// 却变成了「故障放大器」。给缓存操作单独设上限，超时后调用方
// 按「未命中/写失败」降级回源，主流程仍然可控。
const (
	cacheReadTimeout  = 200 * time.Millisecond
	cacheWriteTimeout = 500 * time.Millisecond
)

// runOp 在独立超时内执行一次 Redis 操作并打点。
func runOp[T any](ctx context.Context, op string, timeout time.Duration, fn func(context.Context) (T, error)) (T, error) {
	opCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	val, err := fn(opCtx)
	obs.RecordCacheOp(ctx, op, cacheOpResult(err), time.Since(start).Seconds())
	return val, err
}

// runErr 是只关心 error 的版本，避免每个写操作都写一遍 struct{}。
func runErr(ctx context.Context, op string, timeout time.Duration, fn func(context.Context) error) error {
	_, err := runOp(ctx, op, timeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// cacheOpResult 把错误分类成可聚合的标签。
// miss 与 error 必须分开：缓存未命中是正常业务，错误率的分母不应包含它。
func cacheOpResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, redis.Nil):
		return "miss"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}
