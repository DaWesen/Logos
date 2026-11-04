package governance

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"Logos/pkg/logger"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RetryManager struct {
	config RetryConfig
	policy RetryPolicy
}

func NewRetryManager(cfg RetryConfig) *RetryManager {
	return &RetryManager{config: cfg}
}

// NewRetryManagerWithPolicy 允许调用方指定重试白名单。
func NewRetryManagerWithPolicy(cfg RetryConfig, policy RetryPolicy) *RetryManager {
	return &RetryManager{config: cfg, policy: policy}
}

func (m *RetryManager) Execute(ctx context.Context, fn func() error) error {
	return m.execute(ctx, "", fn)
}

// ExecuteMethod 按方法名执行，并根据策略决定是否允许重试。
// 非白名单方法即使返回 Unavailable 也只执行一次。
func (m *RetryManager) ExecuteMethod(ctx context.Context, method string, fn func() error) error {
	return m.execute(ctx, method, fn)
}

func (m *RetryManager) execute(ctx context.Context, method string, fn func() error) error {
	bo := backoff.WithContext(backoff.NewExponentialBackOff(
		backoff.WithInitialInterval(m.config.InitialDelay),
		backoff.WithMaxInterval(m.config.MaxDelay),
		backoff.WithMaxElapsedTime(0),
	), ctx)

	attempt := 0
	return backoff.Retry(func() error {
		attempt++
		err := fn()
		if err == nil {
			return nil
		}

		if !m.isRetryable(err) {
			return backoff.Permanent(err)
		}
		// 方法不在白名单时不做重试：非幂等写的副作用可能已经发生。
		if method != "" && !m.policy.ShouldRetry(method) {
			return backoff.Permanent(err)
		}

		if attempt >= m.config.MaxAttempts {
			return backoff.Permanent(fmt.Errorf("重试%d次后仍失败: %w", m.config.MaxAttempts, err))
		}

		logger.Warn("请求失败，准备重试",
			logger.IntField("attempt", attempt),
			logger.StringField("error", err.Error()))
		return err
	}, bo)
}

func (m *RetryManager) isRetryable(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}

	// 以配置为准：DefaultConfig 是 UNAVAILABLE/DEADLINE_EXCEEDED/RESOURCE_EXHAUSTED，
	// 但环境可以按自己的下游特性调整。此前这里硬编码三个码，
	// 配置里的 retryable_codes 写了却完全不生效。
	codeName := st.Code().String()
	for _, rc := range m.config.RetryableCodes {
		if sameCodeName(rc, codeName) {
			return true
		}
	}

	// 配置为空时保留一组安全默认，避免「没配置 = 完全不重试」的静默行为。
	if len(m.config.RetryableCodes) == 0 {
		switch st.Code() {
		case codes.Unavailable, codes.ResourceExhausted:
			return true
		}
	}
	return false
}

// sameCodeName 比较配置里的重试码与 gRPC 的 Code.String()。
//
// 两者写法不同：配置常用 UNAVAILABLE / RESOURCE_EXHAUSTED，
// 而 codes.Code.String() 返回 Unavailable / ResourceExhausted。
// 忽略下划线并转小写后再比较，避免「配置写了但匹配不上」。
func sameCodeName(configured, actual string) bool {
	normalize := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", ""))
	}
	return normalize(configured) == normalize(actual)
}

var defaultRetryManager *RetryManager
var retryOnce sync.Once

func GetRetryManager() *RetryManager {
	retryOnce.Do(func() {
		cfg := DefaultConfig()
		defaultRetryManager = NewRetryManager(cfg.Retry)
	})
	return defaultRetryManager
}

func InitRetryManager(cfg RetryConfig) *RetryManager {
	defaultRetryManager = NewRetryManager(cfg)
	return defaultRetryManager
}
