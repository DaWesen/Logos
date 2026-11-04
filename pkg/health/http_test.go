package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestRouter(r *Registry) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	h := NewHTTPHandler(r, "logos-test")
	engine.GET("/livez", h.Live)
	engine.GET("/readyz", h.Ready)
	return engine
}

func TestLive_IgnoresDependencyFailure(t *testing.T) {
	reg := New(WithCacheTTL(0))
	reg.Register("db", func(context.Context) error { return errors.New("down") })
	engine := newTestRouter(reg)

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("livez 必须只反映进程存活，实际 %d", w.Code)
	}
}

func TestReady_HealthyReturns200(t *testing.T) {
	reg := New(WithCacheTTL(0))
	reg.Register("db", func(context.Context) error { return nil })
	engine := newTestRouter(reg)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("依赖健康时应返回 200，实际 %d: %s", w.Code, w.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["status"] != "ready" {
		t.Fatalf("status = %v, want ready", body["status"])
	}
}

func TestReady_DependencyFailureReturns503WithReason(t *testing.T) {
	reg := New(WithCacheTTL(0))
	reg.Register("db", func(context.Context) error { return errors.New("connection refused") })
	engine := newTestRouter(reg)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("依赖失败时应返回 503，实际 %d", w.Code)
	}
	var body struct {
		Ready  bool     `json:"ready"`
		Failed []string `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Ready || len(body.Failed) != 1 || body.Failed[0] != "db" {
		t.Fatalf("失败原因未暴露: %+v", body)
	}
}

func TestReady_OptionalFailureStill200(t *testing.T) {
	reg := New(WithCacheTTL(0))
	reg.Register("db", func(context.Context) error { return nil })
	reg.RegisterOptional("redis", func(context.Context) error { return errors.New("down") })
	engine := newTestRouter(reg)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("可选依赖失败不应摘流量，实际 %d", w.Code)
	}
	var body struct {
		Degraded []string `json:"degraded"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(body.Degraded) != 1 || body.Degraded[0] != "redis" {
		t.Fatalf("降级状态未暴露: %+v", body)
	}
}
