package grpcserver

import "testing"

func TestResolveMsgSize(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"未配置用默认 4MB", 0, 4 << 20},
		{"负数按未配置处理", -1, 4 << 20},
		{"合法值原样保留", 8 << 20, 8 << 20},
		{"超过 64MB 截断", 1 << 30, 64 << 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ResolveMsgSize(c.in); got != c.want {
				t.Fatalf("ResolveMsgSize(%d) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// 没有加载配置时必须退回默认值，而不是 panic。
// 这也是 grpcserver 能在单测里被直接使用的前提。
func TestMsgSizeLimits_WithoutConfigUsesDefaults(t *testing.T) {
	recv, send := MsgSizeLimits()
	if recv != defaultMaxMsgSize || send != defaultMaxMsgSize {
		t.Fatalf("MsgSizeLimits() = (%d,%d), want (%d,%d)", recv, send, defaultMaxMsgSize, defaultMaxMsgSize)
	}
}

func TestClientMsgSizeLimits_WithoutConfigUsesDefaults(t *testing.T) {
	recv, send := ClientMsgSizeLimits()
	if recv != defaultMaxMsgSize || send != defaultMaxMsgSize {
		t.Fatalf("ClientMsgSizeLimits() = (%d,%d), want (%d,%d)", recv, send, defaultMaxMsgSize, defaultMaxMsgSize)
	}
}
