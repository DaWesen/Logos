package governance

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:  3,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
	}
}

func TestRetryManager_Execute_SuccessFirstTry(t *testing.T) {
	m := NewRetryManager(testRetryConfig())
	calls := 0
	err := m.Execute(context.Background(), func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetryManager_Execute_RetriesUntilSuccess(t *testing.T) {
	m := NewRetryManager(testRetryConfig())
	calls := 0
	err := m.Execute(context.Background(), func() error {
		calls++
		if calls < 3 {
			return status.Error(codes.Unavailable, "temporarily unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetryManager_Execute_ExceedsMaxAttempts(t *testing.T) {
	m := NewRetryManager(testRetryConfig())
	calls := 0
	err := m.Execute(context.Background(), func() error {
		calls++
		return status.Error(codes.Unavailable, "always failing")
	})
	if err == nil {
		t.Fatal("expected error after max attempts")
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (max attempts), got %d", calls)
	}
}

func TestRetryManager_Execute_NonRetryableFailsFast(t *testing.T) {
	m := NewRetryManager(testRetryConfig())
	calls := 0
	err := m.Execute(context.Background(), func() error {
		calls++
		return status.Error(codes.NotFound, "not found")
	})
	if err == nil {
		t.Fatal("expected error for non-retryable failure")
	}
	if calls != 1 {
		t.Fatalf("non-retryable error should not be retried, got %d calls", calls)
	}
}

func TestRetryManager_Execute_OrdinaryErrorNotRetried(t *testing.T) {
	m := NewRetryManager(testRetryConfig())
	calls := 0
	err := m.Execute(context.Background(), func() error {
		calls++
		return errors.New("plain error")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("plain error should not be retried, got %d calls", calls)
	}
}

func TestRetryManager_Execute_RetryableCodes(t *testing.T) {
	tests := []struct {
		code     codes.Code
		retrying bool
	}{
		{codes.Unavailable, true},
		{codes.ResourceExhausted, true},
		{codes.Aborted, true},
		{codes.NotFound, false},
		{codes.InvalidArgument, false},
		{codes.PermissionDenied, false},
	}

	m := NewRetryManager(testRetryConfig())
	for _, tt := range tests {
		calls := 0
		_ = m.Execute(context.Background(), func() error {
			calls++
			return status.Error(tt.code, "test")
		})
		want := 1
		if tt.retrying {
			want = 3
		}
		if calls != want {
			t.Errorf("code %v: expected %d calls, got %d", tt.code, want, calls)
		}
	}
}

func TestConfig_IsRetryableCode(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.IsRetryableCode("UNAVAILABLE") {
		t.Error("UNAVAILABLE should be retryable")
	}
	if cfg.IsRetryableCode("NOT_FOUND") {
		t.Error("NOT_FOUND should not be retryable")
	}
}
