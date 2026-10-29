package websocket

import (
	"net/http/httptest"
	"testing"
)

// TestWSTokenFromSubprotocol 校验从 Sec-WebSocket-Protocol 头提取令牌的边界。
func TestWSTokenFromSubprotocol(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"标准格式", "access_token, eyJhbGciOiJIUzI1NiJ9.abc.def", "eyJhbGciOiJIUzI1NiJ9.abc.def"},
		{"无空格", "access_token,eyJhbGciOiJIUzI1NiJ9.abc.def", "eyJhbGciOiJIUzI1NiJ9.abc.def"},
		{"协议不在首位", "chat, access_token, token-value", "token-value"},
		{"只有协议名没有令牌", "access_token", ""},
		{"空头", "", ""},
		{"非认证协议", "chat, token-value", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/ws", nil)
			if tc.header != "" {
				r.Header.Set("Sec-WebSocket-Protocol", tc.header)
			}
			if got := wsTokenFromSubprotocol(r); got != tc.want {
				t.Fatalf("wsTokenFromSubprotocol(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}
