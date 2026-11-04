package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"request_id": GetRequestID(c)})
	})
	return r
}

func TestRequestID_GeneratedWhenMissing(t *testing.T) {
	r := setupTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	// 响应头必须回写 X-Request-ID
	respID := w.Header().Get("X-Request-ID")
	if respID == "" {
		t.Fatal("response should carry X-Request-ID header")
	}
	if len(respID) < 32 { // UUID 长度
		t.Errorf("generated request id looks invalid: %q", respID)
	}
}

func TestRequestID_PassthroughFromUpstream(t *testing.T) {
	r := setupTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Request-ID", "upstream-trace-123")
	r.ServeHTTP(w, req)

	// 上游携带的 ID 应原样透传（支持链路串联）
	if got := w.Header().Get("X-Request-ID"); got != "upstream-trace-123" {
		t.Errorf("upstream request id should be passed through, got %q", got)
	}
	// gin context 中可读取
	body := w.Body.String()
	if !strings.Contains(body, "upstream-trace-123") {
		t.Errorf("handler should read request id from context, body: %s", body)
	}
}

func TestRequestID_UniquePerRequest(t *testing.T) {
	r := setupTestRouter()

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		r.ServeHTTP(w, req)
		id := w.Header().Get("X-Request-ID")
		if id == "" || seen[id] {
			t.Fatalf("request ids must be unique, got duplicate/empty: %q", id)
		}
		seen[id] = true
	}
}

func TestGetRequestID_NotSetReturnsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := GetRequestID(c); got != "" {
		t.Errorf("unset request id should be empty, got %q", got)
	}
}

// TestSanitizeRequestID 客户端头不能无限长，也不能带任意字符：
// 非法值直接丢弃并生成新 ID，不能截断后继续用（截断会碰撞）。
func TestSanitizeRequestID(t *testing.T) {
	valid := []string{
		"upstream-trace-123",
		"trace_123.abc",
		"550e8400-e29b-41d4-a716-446655440000",
	}
	for _, id := range valid {
		if got := sanitizeRequestID(id); got != id {
			t.Errorf("sanitizeRequestID(%q) = %q，应原样保留", id, got)
		}
	}

	invalid := []string{
		"has space",
		"path/segment",
		"id;drop",
		"中文",
		strings.Repeat("x", maxRequestIDLength+1),
	}
	for _, id := range invalid {
		if got := sanitizeRequestID(id); got != "" {
			t.Errorf("sanitizeRequestID(%q) = %q，应拒绝", id, got)
		}
	}
}

func TestRequestID_InvalidClientValueIsReplaced(t *testing.T) {
	r := setupTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces")
	r.ServeHTTP(w, req)

	got := w.Header().Get("X-Request-ID")
	if got == "" || got == "bad id with spaces" {
		t.Fatalf("非法 request id 应被替换，实际 %q", got)
	}
}
