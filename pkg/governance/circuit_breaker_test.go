package governance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sony/gobreaker"
)

func testCBConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		MaxRequests:      1,
		Interval:        10 * time.Millisecond,
		Timeout:         10 * time.Millisecond,
		FailureThreshold: 3,
		SuccessThreshold: 1,
	}
}

func TestCircuitBreakerManager_GetReturnsSameInstance(t *testing.T) {
	m := NewCircuitBreakerManager(testCBConfig())
	cb1 := m.Get("svc-a")
	cb2 := m.Get("svc-a")
	if cb1 != cb2 {
		t.Fatal("Get should return the same breaker for the same name")
	}
	cb3 := m.Get("svc-b")
	if cb1 == cb3 {
		t.Fatal("Get should return different breakers for different names")
	}
}

func TestCircuitBreakerManager_ExecuteSuccess(t *testing.T) {
	m := NewCircuitBreakerManager(testCBConfig())
	result, err := m.Execute(context.Background(), "svc", func() (interface{}, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("unexpected result: %v", result)
	}
	if m.State("svc") != gobreaker.StateClosed {
		t.Fatalf("expected closed state, got %v", m.State("svc"))
	}
}

func TestCircuitBreakerManager_OpensAfterConsecutiveFailures(t *testing.T) {
	m := NewCircuitBreakerManager(testCBConfig())
	fail := func() (interface{}, error) { return nil, errors.New("boom") }

	// 未达阈值：错误直接透传
	for i := 0; i < 3; i++ {
		if _, err := m.Execute(context.Background(), "svc", fail); err == nil {
			t.Fatal("expected error")
		}
	}

	if m.State("svc") != gobreaker.StateOpen {
		t.Fatalf("expected open state after %d failures, got %v", 3, m.State("svc"))
	}

	// 熔断开启后：直接拒绝，不再执行业务函数
	executed := false
	_, err := m.Execute(context.Background(), "svc", func() (interface{}, error) {
		executed = true
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected rejection error when breaker is open")
	}
	if executed {
		t.Fatal("function should not execute when breaker is open")
	}
}

func TestCircuitBreakerManager_TransitionsToHalfOpen(t *testing.T) {
	m := NewCircuitBreakerManager(testCBConfig())
	fail := func() (interface{}, error) { return nil, errors.New("boom") }

	for i := 0; i < 3; i++ {
		_, _ = m.Execute(context.Background(), "svc", fail)
	}

	// 等待冷却时间过去，熔断器进入半开
	time.Sleep(15 * time.Millisecond)
	if m.State("svc") != gobreaker.StateHalfOpen {
		t.Fatalf("expected half-open state after timeout, got %v", m.State("svc"))
	}

	// 半开状态下一个成功请求应闭合熔断器
	if _, err := m.Execute(context.Background(), "svc", func() (interface{}, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("unexpected error in half-open probe: %v", err)
	}
	if m.State("svc") != gobreaker.StateClosed {
		t.Fatalf("expected closed state after successful probe, got %v", m.State("svc"))
	}
}
