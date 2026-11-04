package governance

import (
	"context"
	"time"

	"Logos/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func UnaryClientTimeout(timeout time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if timeout <= 0 {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func UnaryClientRetry(rm *RetryManager) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// 按方法名判断是否允许重试：SendMessage/Deposit 这类非幂等写
		// 即使遇到 Unavailable 也只能执行一次，否则可能重复发消息/重复扣费。
		return rm.ExecuteMethod(ctx, method, func() error {
			return invoker(ctx, method, req, reply, cc, opts...)
		})
	}
}

func UnaryClientCircuitBreaker(manager *CircuitBreakerManager) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		cbName := "client:" + method
		_, err := manager.Execute(ctx, cbName, func() (interface{}, error) {
			err := invoker(ctx, method, req, reply, cc, opts...)
			return nil, err
		})
		return err
	}
}

func UnaryClientFallback(fallbackFn func(ctx context.Context, method string, req interface{}, err error) error) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			st, ok := status.FromError(err)
			if ok && (st.Code() == codes.Unavailable || st.Code() == codes.DeadlineExceeded) {
				logger.Warn("服务不可用，尝试降级",
					logger.StringField("method", method),
					logger.StringField("error", err.Error()))
				if fallbackFn != nil {
					return fallbackFn(ctx, method, req, err)
				}
			}
		}
		return err
	}
}

func StreamClientTimeout(timeout time.Duration) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if timeout <= 0 {
			return streamer(ctx, desc, cc, method, opts...)
		}

		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		return streamer(ctx, desc, cc, method, opts...)
	}
}

func DefaultClientInterceptors(cfg *Config) []grpc.DialOption {
	return DefaultClientInterceptorsWithPolicy(cfg, RetryPolicy{})
}

// DefaultClientInterceptorsWithPolicy 允许调用方追加可重试方法。
// 默认策略是白名单：只有只读与幂等覆盖写会重试。
func DefaultClientInterceptorsWithPolicy(cfg *Config, policy RetryPolicy) []grpc.DialOption {
	retryMgr := NewRetryManager(cfg.Retry)
	retryMgr.policy = policy
	cbMgr := NewCircuitBreakerManager(cfg.CircuitBreaker)

	return []grpc.DialOption{
		grpc.WithChainUnaryInterceptor(
			UnaryClientTimeout(cfg.Timeout.ClientDefault),
			UnaryClientCircuitBreaker(cbMgr),
			UnaryClientRetry(retryMgr),
		),
		grpc.WithChainStreamInterceptor(
			StreamClientTimeout(cfg.Timeout.ClientDefault),
		),
	}
}

func DefaultServerInterceptors(cfg *Config) []grpc.ServerOption {
	cbMgr := NewCircuitBreakerManager(cfg.CircuitBreaker)

	return []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			UnaryServerRecovery(),
			UnaryServerTimeout(cfg.Timeout.ServerDefault),
			UnaryServerRateLimit(cfg.RateLimit.MaxRequestsPerSecond, cfg.RateLimit.MaxConcurrent),
			UnaryServerCircuitBreaker(cbMgr),
		),
	}
}
