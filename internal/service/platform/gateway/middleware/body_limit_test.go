package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newBodyLimitEngine(limit int64, path string, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(limit))
	r.POST(path, handler)
	return r
}

func TestBodyLimit_RejectsOversizedContentLength(t *testing.T) {
	called := false
	r := newBodyLimitEngine(16, "/echo", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(strings.Repeat("a", 64)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	if called {
		t.Fatal("超限请求不应进入业务 handler")
	}
}

func TestBodyLimit_AllowsNormalRequest(t *testing.T) {
	r := newBodyLimitEngine(1024, "/echo", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Errorf("读取 body 失败: %v", err)
		}
		c.Data(http.StatusOK, "text/plain", body)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(`{"ok":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK || w.Body.String() != `{"ok":true}` {
		t.Fatalf("正常请求被破坏: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestBodyLimit_AllowsKnownMultipartUpload(t *testing.T) {
	called := false
	r := newBodyLimitEngine(16, "/api/v1/users/avatar", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/avatar", bytes.NewReader(make([]byte, 1024)))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !called || w.Code != http.StatusOK {
		t.Fatalf("multipart 上传不应被 JSON 上限拦截: called=%v status=%d", called, w.Code)
	}
}

// TestBodyLimit_MultipartHeaderCannotBypassJSONRoute 关键回归：
// 普通 JSON 路由收到伪造的 multipart 头，仍然必须受 2MB 限制。
func TestBodyLimit_MultipartHeaderCannotBypassJSONRoute(t *testing.T) {
	called := false
	r := newBodyLimitEngine(16, "/api/v1/auth/login", func(c *gin.Context) {
		called = true
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(make([]byte, 1024)))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d，伪造 multipart 头也必须被 413 拦截", w.Code)
	}
	if called {
		t.Fatal("超限请求不应进入业务 handler")
	}
}

func TestBodyLimit_ChunkedBodyStopsAtLimit(t *testing.T) {
	r := newBodyLimitEngine(16, "/echo", func(c *gin.Context) {
		// 分块传输没有 Content-Length，MaxBytesReader 只能在读取时兜底。
		if _, err := io.ReadAll(c.Request.Body); err == nil {
			t.Error("超过上限的 chunked body 读取应返回错误")
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(strings.Repeat("a", 64)))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（由 handler 处理读取错误）", w.Code)
	}
}
