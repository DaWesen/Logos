package mq

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// TestRetryBackoff_Sequence 验证「指数增长 + 上限截断」的确定性部分。
// 抖动传入 0（关闭），这样断言与随机源无关，不会 flaky。
func TestRetryBackoff_Sequence(t *testing.T) {
	base := 200 * time.Millisecond
	max := 2 * time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 200 * time.Millisecond},  // attempt < 1 按 1 处理
		{-3, 200 * time.Millisecond}, // 负数不放大
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1600 * time.Millisecond},
		{5, 2 * time.Second}, // 触顶
		{6, 2 * time.Second},
		{32, 2 * time.Second},
		{1000, 2 * time.Second},
	}
	for _, tc := range cases {
		if got := RetryBackoff(tc.attempt, base, max, 0); got != tc.want {
			t.Errorf("RetryBackoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// TestRetryBackoff_NoMax 未配置上限时仍按指数增长（调用方可显式关闭截断）。
func TestRetryBackoff_NoMax(t *testing.T) {
	base := 10 * time.Millisecond
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 10 * time.Millisecond},
		{2, 20 * time.Millisecond},
		{3, 40 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := RetryBackoff(tc.attempt, base, 0, 0); got != tc.want {
			t.Errorf("RetryBackoff(%d, max=0) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

// TestRetryBackoff_ZeroBase 显式关闭退避：base <= 0 返回 0，调用方可以「立即重试」。
func TestRetryBackoff_ZeroBase(t *testing.T) {
	if got := RetryBackoff(3, 0, time.Second, 0.5); got != 0 {
		t.Fatalf("base=0 应返回 0，实际 %v", got)
	}
	if got := RetryBackoff(3, -time.Second, time.Second, 0.5); got != 0 {
		t.Fatalf("base<0 应返回 0，实际 %v", got)
	}
}

// TestRetryBackoff_JitterBounds 等量抖动必须落在 [d/2, d]：
// 下限保留一半固定等待，避免抖动把退避压成「立即重试」；上限不超过基础退避。
func TestRetryBackoff_JitterBounds(t *testing.T) {
	base := 100 * time.Millisecond
	max := 3 * time.Second
	for attempt := 1; attempt <= 8; attempt++ {
		d := RetryBackoff(attempt, base, max, 0)
		for _, jitter := range []float64{0.01, 0.5, 0.99, 1} {
			got := RetryBackoff(attempt, base, max, jitter)
			if got < d/2 || got > d {
				t.Fatalf("attempt=%d jitter=%v: 抖动结果 %v 超出 [%v, %v]", attempt, jitter, got, d/2, d)
			}
		}
	}
}

// TestRetryBackoff_JitterClamped 非法抖动（>1 或 <0）被收敛到合法区间，
// 不会产生超过基础退避或负数的等待。
func TestRetryBackoff_JitterClamped(t *testing.T) {
	d := RetryBackoff(2, 100*time.Millisecond, time.Second, 0)
	if got := RetryBackoff(2, 100*time.Millisecond, time.Second, 2.5); got != d {
		t.Fatalf("jitter>1 应收敛为 d，实际 %v want %v", got, d)
	}
	if got := RetryBackoff(2, 100*time.Millisecond, time.Second, -1); got != d {
		t.Fatalf("jitter<0 应视为无抖动，实际 %v want %v", got, d)
	}
}

// TestProcessWithRetry_UsesBackoffPolicy 验证 processWithRetry 真的接入了
// RetryBackoff：失败后退避逐轮放大、触顶后保持上限，且每次成功前都记录一次 retry。
// 通过注入 retryWait 与 retryJitter，测试不 sleep、结果完全确定。
func TestProcessWithRetry_UsesBackoffPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := newFakeSource(kafka.Message{Topic: "chat_messages", Offset: 1})
	var mu sync.Mutex
	var delays []time.Duration
	attempts := 0

	handler := func(*Message) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 4 {
			return errors.New("downstream unavailable")
		}
		return nil
	}

	c := &Consumer{
		reader:          src,
		retryBackoff:    50 * time.Millisecond,
		retryBackoffMax: 150 * time.Millisecond,
		retryJitter:     func() float64 { return 0 }, // 关闭抖动，断言基础退避
		retryWait: func(_ context.Context, d time.Duration) bool {
			mu.Lock()
			delays = append(delays, d)
			mu.Unlock()
			return true
		},
	}

	if !c.processWithRetry(ctx, &Message{Topic: "chat_messages", Offset: 1}, handler) {
		t.Fatal("处理成功后 processWithRetry 应返回 true")
	}

	mu.Lock()
	got := append([]time.Duration{}, delays...)
	mu.Unlock()

	want := []time.Duration{
		50 * time.Millisecond,
		100 * time.Millisecond,
		150 * time.Millisecond, // 上限截断（100*2=200 > 150）
	}
	if len(got) != len(want) {
		t.Fatalf("退避轮数 = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 轮退避 = %v, want %v（完整序列 %v）", i+1, got[i], want[i], got)
		}
	}

	if offsets := src.committedOffsets(); len(offsets) != 1 || offsets[0] != 1 {
		t.Fatalf("成功后应提交 offset=1，实际 %v", offsets)
	}
}

// TestProcessWithRetry_CancelDuringBackoff 取消发生在退避等待期间时，
// 应立即退出且不提交 offset：重启后从已提交位点重投，不丢消息。
func TestProcessWithRetry_CancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	src := newFakeSource(kafka.Message{Topic: "im_messages", Offset: 9})
	c := &Consumer{
		reader:       src,
		retryBackoff: time.Millisecond,
		retryWait: func(_ context.Context, _ time.Duration) bool {
			cancel() // 模拟停机信号在退避期间到达
			return false
		},
	}

	ok := c.processWithRetry(ctx, &Message{Topic: "im_messages", Offset: 9}, func(*Message) error {
		return errors.New("temporary failure")
	})
	if ok {
		t.Fatal("ctx 取消后 processWithRetry 应返回 false")
	}
	if got := src.committedOffsets(); len(got) != 0 {
		t.Fatalf("取消时不得提交 offset，实际 %v", got)
	}
}
