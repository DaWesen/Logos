package safehttp

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsForbiddenIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"100.64.0.1", true},
		{"0.0.0.0", true},
		{"224.0.0.1", true},
		{"::1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"2001:db8::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	}
	for _, tc := range cases {
		if got := IsForbiddenIP(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("IsForbiddenIP(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
	if !IsForbiddenIP(nil) {
		t.Error("nil IP 必须按禁止处理")
	}
}

func TestValidateURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{"https", "https://example.com/article", nil},
		{"http with port", "http://example.com:8080/a", nil},
		{"ftp rejected", "ftp://example.com/file", ErrInvalidURL},
		{"credentials rejected", "https://user:pass@example.com", ErrInvalidURL},
		{"empty rejected", "", ErrInvalidURL},
		{"no host rejected", "https:///path", ErrInvalidURL},
		{"invalid port rejected", "https://example.com:99999", ErrInvalidURL},
		{"localhost rejected", "http://localhost:8080", ErrForbiddenAddress},
		{"localhost suffix rejected", "http://api.localhost", ErrForbiddenAddress},
		{"internal suffix rejected", "http://db.internal", ErrForbiddenAddress},
		{"loopback rejected", "http://127.0.0.1/admin", ErrForbiddenAddress},
		{"metadata rejected", "http://169.254.169.254/latest/meta-data", ErrForbiddenAddress},
		{"ipv6 loopback rejected", "http://[::1]/", ErrForbiddenAddress},
		{"too long rejected", "https://example.com/" + strings.Repeat("a", maxURLLength), ErrInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateURL(tc.raw)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateURL(%q) 返回错误: %v", tc.raw, err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateURL(%q) = %v, want %v", tc.raw, err, tc.wantErr)
			}
		})
	}
}

// TestClientBlocksLoopback 连接层必须真的拦住回环地址：
// 只在 ValidateURL 里判断会漏掉「域名解析到 127.0.0.1」的情况。
func TestClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should not be reached"))
	}))
	defer srv.Close()

	client := NewClient(2*time.Second, 3)
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("访问 127.0.0.1 应被拒绝")
	}
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("错误应可识别为 ErrForbiddenAddress，实际 %v", err)
	}
}

func TestCheckRedirect(t *testing.T) {
	check := CheckRedirect(2)

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/next", nil)
	if err := check(req, nil); err != nil {
		t.Fatalf("合法跳转不应报错: %v", err)
	}

	reqBad, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/admin", nil)
	if err := check(reqBad, nil); !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("跳转到回环地址应拒绝，实际 %v", err)
	}

	via := make([]*http.Request, 2)
	if err := check(req, via); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("超过跳转次数应拒绝，实际 %v", err)
	}
}
