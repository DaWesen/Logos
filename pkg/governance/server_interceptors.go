package governance

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"Logos/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func UnaryServerTimeout(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if timeout <= 0 {
			return handler(ctx, req)
		}

		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		resp, err := handler(ctx, req)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, status.Error(codes.DeadlineExceeded, fmt.Sprintf("方法%s超时(%v)", info.FullMethod, timeout))
		}
		return resp, err
	}
}

func UnaryServerRecovery() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)
				logger.Error("gRPC服务端panic恢复",
					logger.StringField("method", info.FullMethod),
					logger.StringField("panic", fmt.Sprintf("%v", r)),
					logger.StringField("stack", string(buf[:n])))
				err = status.Error(codes.Internal, "服务内部错误")
			}
		}()
		return handler(ctx, req)
	}
}

func UnaryServerRateLimit(maxRequestsPerSecond float64, maxConcurrent int) grpc.UnaryServerInterceptor {
	sem := make(chan struct{}, maxConcurrent)
	limiter := NewTokenBucketLimiter(maxRequestsPerSecond)

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !limiter.Allow() {
			return nil, status.Error(codes.ResourceExhausted, fmt.Sprintf("请求频率超限: %s", info.FullMethod))
		}

		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			return nil, status.Error(codes.ResourceExhausted, fmt.Sprintf("并发数超限: %s", info.FullMethod))
		}

		return handler(ctx, req)
	}
}

func UnaryServerCircuitBreaker(manager *CircuitBreakerManager) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		cbName := "server:" + info.FullMethod
		result, err := manager.Execute(ctx, cbName, func() (interface{}, error) {
			return handler(ctx, req)
		})
		return result, err
	}
}

type TokenBucketLimiter struct {
	tokens     float64
	maxTokens  float64
	rate       float64
	lastRefill time.Time
}

func NewTokenBucketLimiter(rate float64) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		tokens:     rate,
		maxTokens:  rate,
		rate:       rate,
		lastRefill: time.Now(),
	}
}

func (l *TokenBucketLimiter) Allow() bool {
	now := time.Now()
	elapsed := now.Sub(l.lastRefill).Seconds()
	l.tokens += elapsed * l.rate
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
	l.lastRefill = now

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}
