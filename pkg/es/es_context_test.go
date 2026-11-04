package es

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
)

// esProductHeader 是 go-elasticsearch 校验服务端身份所需的响应头；
// httptest 默认响应会被客户端判定为「不是 Elasticsearch」而直接报错。
func setESHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Elastic-Product", "Elasticsearch")
	w.Header().Set("Content-Type", "application/json")
}

// newTestESManager 起一个 httptest 服务充当 Elasticsearch，返回可观测的 manager。
func newTestESManager(t *testing.T, handler http.HandlerFunc) (*ESManager, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setESHeaders(w)
		handler(w, r)
	}))
	client, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{srv.URL}})
	if err != nil {
		srv.Close()
		t.Fatalf("创建 ES 客户端失败: %v", err)
	}
	return NewESManager(client), srv
}

// TestSearchWithContext_PropagatesCancellation 检索链路必须把调用方 ctx 透传给 ES：
// 客户端断连后若仍用 context.Background()，查询会继续占用连接和 ES 计算资源。
func TestSearchWithContext_PropagatesCancellation(t *testing.T) {
	started := make(chan struct{})

	mgr, srv := newTestESManager(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		// 最多挂 300ms 再应答：客户端在 100ms 取消时若 ctx 已透传，
		// 请求会在应答前就返回 context 错误；若仍用 Background 则会正常拿到响应。
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
			_, _ = w.Write([]byte(`{"hits":{"total":{"value":0},"hits":[]}}`))
		}
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		var result SearchResult
		errCh <- mgr.SearchWithContext(ctx, "users", SearchQuery{Size: 1}, &result)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("ES 请求未发出")
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("ctx 取消后 SearchWithContext 应返回错误，而不是静默成功")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 取消后请求没有中止：ctx 未透传给 ES 客户端")
	}
}

// TestSearchWithContext_NilContext 调用方传 nil ctx 时不能 panic，应退回 Background。
func TestSearchWithContext_NilContext(t *testing.T) {
	mgr, srv := newTestESManager(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":0},"hits":[]}}`))
	})
	defer srv.Close()

	var result SearchResult
	if err := mgr.SearchWithContext(nil, "users", SearchQuery{Size: 1}, &result); err != nil {
		t.Fatalf("nil ctx 不应报错: %v", err)
	}
}

// TestSearchWithContext_PropagatesDeadline 上游 deadline 必须传导到 ES 请求，
// 否则 gRPC 已经超时返回了，ES 还在后台跑。
func TestSearchWithContext_PropagatesDeadline(t *testing.T) {
	mgr, srv := newTestESManager(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var result SearchResult
	start := time.Now()
	err := mgr.SearchWithContext(ctx, "users", SearchQuery{Size: 1}, &result)
	if err == nil {
		t.Fatal("deadline 到期后应返回错误")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("deadline 未传导到 ES，耗时 %v", elapsed)
	}
}
