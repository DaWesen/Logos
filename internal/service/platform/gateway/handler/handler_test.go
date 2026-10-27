package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"Logos/pkg/auth"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

// withUserID 必须基于请求上下文：客户端断开时下游调用要一起被取消，
// 同时保留认证中间件写入的调用方身份 metadata。
func TestWithUserID_UsesRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest("POST", "/", nil)
	req = req.WithContext(reqCtx)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	c.Set("user_id", "user-1")

	h := &Handler{}
	got := h.withUserID(c)

	md, ok := metadata.FromOutgoingContext(got)
	if !ok {
		t.Fatal("下游 ctx 缺少 outgoing metadata")
	}
	if values := md.Get(auth.UserIDKey); len(values) == 0 || values[0] != "user-1" {
		t.Fatalf("user_id metadata = %v, want user-1", values)
	}

	// 请求取消后，下游上下文必须同步取消（而不是挂在 Background 上继续执行）
	cancel()
	select {
	case <-got.Done():
	case <-time.After(time.Second):
		t.Fatal("请求取消后下游 ctx 未被取消")
	}
}
