package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProcessHTTPClient_HasBoundedTimeout(t *testing.T) {
	if processAPIClient.Timeout <= 0 {
		t.Fatal("process 普通接口客户端必须有超时")
	}
	if processUploadClient.Timeout <= processAPIClient.Timeout {
		t.Fatal("上传客户端应使用更长的独立预算")
	}

	transport, ok := processAPIClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport 类型 = %T", processAPIClient.Transport)
	}
	if transport.MaxIdleConnsPerHost <= 0 || transport.IdleConnTimeout <= 0 {
		t.Fatal("连接池必须有上限和空闲回收时间")
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("必须限制等待下游响应头的时间，反向代理不读 Client.Timeout")
	}
}

// TestProcessHTTPClient_TimesOutSlowUpstream 零值 http.Client 会无限等待；
// 这里用 30ms 客户端 + 200ms 服务确认超时真正生效。
func TestProcessHTTPClient_TimesOutSlowUpstream(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	client := newProcessHTTPClient(30 * time.Millisecond)
	start := time.Now()
	_, err := client.Get(server.URL)
	if err == nil {
		t.Fatal("慢下游应返回超时错误")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("超时耗时 %v，客户端没有及时返回", elapsed)
	}
}

// TestProcessHTTPClient_ContextCancel 客户端断开时，下游请求必须立刻取消，
// 不能等满 30s 的客户端总超时。
func TestProcessHTTPClient_ContextCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	client := newProcessHTTPClient(5 * time.Second)
	start := time.Now()
	if _, err := client.Do(req); err == nil {
		t.Fatal("ctx 取消后请求应返回错误")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("取消耗时 %v，请求没有跟随 ctx", elapsed)
	}
}
