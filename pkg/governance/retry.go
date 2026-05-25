package governance

import (
	"context"
	"fmt"
	"sync"

	"Logos/pkg/logger"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RetryManager struct {
	config RetryConfig
}

func NewRetryManager(cfg RetryConfig) *RetryManager {
	return &RetryManager{config: cfg}
}

func (m *RetryManager) Execute(ctx context.Context, fn func() error) error {
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

	switch st.Code() {
	case codes.Unavailable, codes.ResourceExhausted, codes.Aborted:
		return true
	default:
		return false
	}
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
