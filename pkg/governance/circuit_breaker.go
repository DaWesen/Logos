package governance

import (
	"context"
	"fmt"
	"sync"

	"Logos/pkg/logger"

	"github.com/sony/gobreaker"
)

type CircuitBreakerManager struct {
	mu       sync.RWMutex
	breakers map[string]*gobreaker.CircuitBreaker
	config   CircuitBreakerConfig
}

func NewCircuitBreakerManager(cfg CircuitBreakerConfig) *CircuitBreakerManager {
	return &CircuitBreakerManager{
		breakers: make(map[string]*gobreaker.CircuitBreaker),
		config:   cfg,
	}
}

func (m *CircuitBreakerManager) Get(name string) *gobreaker.CircuitBreaker {
	m.mu.RLock()
	cb, ok := m.breakers[name]
	m.mu.RUnlock()

	if ok {
		return cb
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	cb, ok = m.breakers[name]
	if ok {
		return cb
	}

	cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: uint32(m.config.MaxRequests),
		Interval:    m.config.Interval,
		Timeout:     m.config.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= uint32(m.config.FailureThreshold)
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Warn("熔断器状态变更",
				logger.StringField("name", name),
				logger.StringField("from", from.String()),
				logger.StringField("to", to.String()))
		},
	})

	m.breakers[name] = cb
	return cb
}

func (m *CircuitBreakerManager) Execute(ctx context.Context, name string, fn func() (interface{}, error)) (interface{}, error) {
	cb := m.Get(name)

	result, err := cb.Execute(fn)
	if err != nil {
		if err == gobreaker.ErrOpenState {
			logger.Warn("熔断器开启，请求被拒绝",
				logger.StringField("name", name))
			return nil, fmt.Errorf("服务%s暂时不可用（熔断器开启）", name)
		}
		if err == gobreaker.ErrTooManyRequests {
			logger.Warn("熔断器半开状态，请求过多",
				logger.StringField("name", name))
			return nil, fmt.Errorf("服务%s过载（熔断器半开）", name)
		}
		return nil, err
	}

	return result, nil
}

func (m *CircuitBreakerManager) State(name string) gobreaker.State {
	cb := m.Get(name)
	return cb.State()
}
