package outbox

import (
	"testing"
	"time"
)

func TestRetryBackoff_Sequence(t *testing.T) {
	tests := []struct {
		retryCount int
		want       time.Duration
	}{
		{1, 30 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second},
		{4, 240 * time.Second},
		{10, 32 * time.Minute}, // 上限
		{100, 32 * time.Minute},
	}
	for _, tt := range tests {
		if got := RetryBackoff(tt.retryCount); got != tt.want {
			t.Errorf("RetryBackoff(%d) = %v, want %v", tt.retryCount, got, tt.want)
		}
	}
}

func TestRetryBackoff_Floor(t *testing.T) {
	// retryCount <= 0 时按 1 处理
	if got := RetryBackoff(0); got != 30*time.Second {
		t.Errorf("RetryBackoff(0) = %v, want 30s", got)
	}
	if got := RetryBackoff(-5); got != 30*time.Second {
		t.Errorf("RetryBackoff(-5) = %v, want 30s", got)
	}
}

func TestJSONRaw_ValueAndScan(t *testing.T) {
	// Value: 空值返回 nil
	var empty JSONRaw
	v, err := empty.Value()
	if err != nil || v != nil {
		t.Fatalf("empty Value() = %v, %v; want nil, nil", v, err)
	}

	// Value: 正常返回字节
	j := JSONRaw(`{"a":1}`)
	v, err = j.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	bytes, ok := v.([]byte)
	if !ok || string(bytes) != `{"a":1}` {
		t.Fatalf("Value() = %v, want []byte({\"a\":1})", v)
	}

	// Scan: 字节
	var dst JSONRaw
	if err := dst.Scan([]byte(`{"b":2}`)); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if string(dst) != `{"b":2}` {
		t.Fatalf("Scan() dst = %s, want {\"b\":2}", string(dst))
	}

	// Scan: nil
	if err := dst.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) error: %v", err)
	}
	if len(dst) != 0 {
		t.Fatalf("Scan(nil) should clear dst, got %s", string(dst))
	}

	// Scan: 非 []byte 类型静默忽略（与既有实现一致）
	if err := dst.Scan(12345); err != nil {
		t.Fatalf("Scan(int) should not error, got %v", err)
	}
}
