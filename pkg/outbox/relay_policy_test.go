package outbox

import (
	"testing"
	"time"
)

// TestShouldAbortDispatch 连败熔断的边界：
// 阈值 <= 0 关闭熔断；达到阈值才提前结束本轮（少一次都继续尝试下一条）。
func TestShouldAbortDispatch(t *testing.T) {
	cases := []struct {
		failures  int
		threshold int
		want      bool
	}{
		{0, 5, false},
		{4, 5, false},
		{5, 5, true},
		{6, 5, true},
		{100, 5, true},
		{5, 0, false},  // 关闭熔断
		{5, -1, false}, // 非法阈值按关闭处理
	}
	for _, tc := range cases {
		if got := ShouldAbortDispatch(tc.failures, tc.threshold); got != tc.want {
			t.Errorf("ShouldAbortDispatch(%d, %d) = %v, want %v",
				tc.failures, tc.threshold, got, tc.want)
		}
	}
}

// TestRelayDefaults 默认参数必须合理：有单条超时、批量上限、死信重放间隔。
func TestRelayDefaults(t *testing.T) {
	r := NewRelay(nil, nil)
	if r.sendTimeout != defaultSendTimeout {
		t.Fatalf("默认单条超时 = %v, want %v", r.sendTimeout, defaultSendTimeout)
	}
	if r.batchSize <= 0 {
		t.Fatalf("默认批量 = %d，必须为正", r.batchSize)
	}
	if r.interval <= 0 {
		t.Fatalf("默认轮询间隔 = %v，必须为正", r.interval)
	}
	if r.replayInterval <= 0 {
		t.Fatalf("默认死信重放间隔 = %v，必须为正", r.replayInterval)
	}
}

// TestWithSendTimeout 选项可覆盖默认值；<=0 表示不限制（返回 0）。
func TestWithSendTimeout(t *testing.T) {
	r := NewRelay(nil, nil, WithSendTimeout(3*time.Second))
	if got := r.sendTimeoutOrZero(); got != 3*time.Second {
		t.Fatalf("sendTimeoutOrZero = %v, want 3s", got)
	}

	r = NewRelay(nil, nil, WithSendTimeout(0))
	if got := r.sendTimeoutOrZero(); got != 0 {
		t.Fatalf("超时为 0 时应表示不限制，实际 %v", got)
	}

	r = NewRelay(nil, nil, WithSendTimeout(-time.Second))
	if got := r.sendTimeoutOrZero(); got != 0 {
		t.Fatalf("负超时应表示不限制，实际 %v", got)
	}
}

// TestBatchSizeOption 批量上限必须是单轮投递的硬约束：
// 没有上限时一次领取全表会在 broker 抖动时把整轮耗时放大到不可控。
func TestBatchSizeOption(t *testing.T) {
	r := NewRelay(nil, nil, WithBatchSize(10))
	if r.batchSize != 10 {
		t.Fatalf("batchSize = %d, want 10", r.batchSize)
	}
}
