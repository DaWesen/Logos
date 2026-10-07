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
