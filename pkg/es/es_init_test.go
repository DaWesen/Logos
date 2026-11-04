package es

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Logos/config"
)

// 回归：Ping 没有超时时，ES 地址可路由但不应答会让初始化永久挂住。
func TestInitElasticsearchClientWith_TimesOutHungPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	start := time.Now()
	_, err := initElasticsearchClientWith(config.Elasticsearch{URL: srv.URL}, 100*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Ping 一直不应答时应返回超时错误")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("启动 Ping 没有被超时截断，耗时 %v", elapsed)
	}
}

func TestInitElasticsearchClientWith_SucceedsOnHealthyPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setESHeaders(w)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"version":{"number":"8.0.0"}}`))
	}))
	defer srv.Close()

	client, err := initElasticsearchClientWith(config.Elasticsearch{URL: srv.URL}, time.Second)
	if err != nil {
		t.Fatalf("健康 ES 不应初始化失败: %v", err)
	}
	if client == nil {
		t.Fatal("成功时应返回 client")
	}
}
