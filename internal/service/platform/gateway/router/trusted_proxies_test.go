package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newClientIPRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := applyTrustedProxies(r); err != nil {
		t.Fatalf("applyTrustedProxies: %v", err)
	}
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	return r
}

func clientIPFor(r *gin.Engine, remoteAddr, forwardedFor string) string {
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func TestTrustedProxies_DefaultIgnoresForwardedFor(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")
	r := newClientIPRouter(t)

	if got := clientIPFor(r, "203.0.113.9:1234", "1.1.1.1"); got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q，默认配置不应采信 X-Forwarded-For", got)
	}
}

func TestTrustedProxies_ConfiguredProxyIsHonored(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1")
	r := newClientIPRouter(t)

	if got := clientIPFor(r, "127.0.0.1:1234", "1.1.1.1"); got != "1.1.1.1" {
		t.Fatalf("ClientIP = %q，可信代理应透传真实客户端 IP", got)
	}
}

func TestTrustedProxies_InvalidConfigReturnsError(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "not-a-cidr")
	gin.SetMode(gin.TestMode)
	if err := applyTrustedProxies(gin.New()); err != nil {
		return
	}
	t.Fatal("非法 TRUSTED_PROXIES 应返回错误")
}
