package tcp

import (
	"testing"
	"time"
)

// TestNextAcceptBackoff 验证 Accept 失败后的退避从 50ms 起步、逐次翻倍、封顶 1s，
// 且成功接受连接后传 0 能重置回起点。
func TestNextAcceptBackoff(t *testing.T) {
	cases := []struct {
		name string
		cur  time.Duration
		want time.Duration
	}{
		{"首次失败", 0, 50 * time.Millisecond},
		{"负值视为首次", -time.Second, 50 * time.Millisecond},
		{"50ms 翻倍", 50 * time.Millisecond, 100 * time.Millisecond},
		{"100ms 翻倍", 100 * time.Millisecond, 200 * time.Millisecond},
		{"接近上限", 500 * time.Millisecond, time.Second},
		{"已到上限", time.Second, time.Second},
		{"超过上限", 2 * time.Second, time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextAcceptBackoff(tc.cur); got != tc.want {
				t.Fatalf("NextAcceptBackoff(%s) = %s, want %s", tc.cur, got, tc.want)
			}
		})
	}
}
