package governance

import (
	"context"
	"fmt"
	"time"

	"Logos/pkg/logger"
)

type LLMGovernance struct {
	timeout       time.Duration
	retryMgr      *RetryManager
	circuitBreaker *CircuitBreakerManager
}

func NewLLMGovernance(cfg *Config) *LLMGovernance {
	return &LLMGovernance{
		timeout:        cfg.Timeout.LLMDefault,
		retryMgr:       NewRetryManager(RetryConfig{
			MaxAttempts:    2,
			InitialDelay:   1 * time.Second,
			MaxDelay:       10 * time.Second,
			RetryableCodes: []string{"TIMEOUT", "UNAVAILABLE"},
		}),
		circuitBreaker: NewCircuitBreakerManager(CircuitBreakerConfig{
			MaxRequests:      1,
			Interval:         60 * time.Second,
			Timeout:          30 * time.Second,
			FailureThreshold: 3,
			SuccessThreshold: 2,
		}),
	}
}

func (g *LLMGovernance) Chat(ctx context.Context, model string, fn func(ctx context.Context) (string, error)) (string, error) {
	if g.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.timeout)
		defer cancel()
	}

	var result string

	cbErr := g.retryMgr.Execute(ctx, func() error {
		res, execErr := g.circuitBreaker.Execute(ctx, "llm:"+model, func() (interface{}, error) {
			return fn(ctx)
		})
		if execErr != nil {
			return execErr
		}
		result = res.(string)
		return nil
	})

	if cbErr != nil {
		logger.Error("LLM调用失败（已耗尽重试/熔断）",
			logger.StringField("model", model),
			logger.ErrorField(cbErr))
		return "", fmt.Errorf("LLM调用失败: %w", cbErr)
	}

	return result, nil
}

func (g *LLMGovernance) Embed(ctx context.Context, model string, fn func(ctx context.Context) ([]float32, error)) ([]float32, error) {
	if g.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.timeout)
		defer cancel()
	}

	var result []float32

	cbErr := g.retryMgr.Execute(ctx, func() error {
		res, execErr := g.circuitBreaker.Execute(ctx, "llm-embed:"+model, func() (interface{}, error) {
			return fn(ctx)
		})
		if execErr != nil {
			return execErr
		}
		if res != nil {
			result = res.([]float32)
		}
		return nil
	})

	if cbErr != nil {
		logger.Error("LLM嵌入调用失败",
			logger.StringField("model", model),
			logger.ErrorField(cbErr))
		return nil, fmt.Errorf("LLM嵌入调用失败: %w", cbErr)
	}

	return result, nil
}

var defaultLLMGovernance *LLMGovernance
var llmOnce func() = func() {}

func GetLLMGovernance() *LLMGovernance {
	if defaultLLMGovernance == nil {
		cfg := DefaultConfig()
		defaultLLMGovernance = NewLLMGovernance(cfg)
	}
	return defaultLLMGovernance
}

func InitLLMGovernance(cfg *Config) *LLMGovernance {
	defaultLLMGovernance = NewLLMGovernance(cfg)
	return defaultLLMGovernance
}
