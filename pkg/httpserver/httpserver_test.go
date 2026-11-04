package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNew_AppliesSafeDefaults(t *testing.T) {
	srv := New(":0", http.NewServeMux(), Options{})

	if srv.ReadHeaderTimeout <= 0 {
		t.Fatal("必须设置 ReadHeaderTimeout，防止慢请求头占住连接")
	}
	if srv.IdleTimeout <= 0 {
		t.Fatal("必须设置 IdleTimeout")
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Fatal("必须限制请求头大小")
	}
}

func TestNew_KeepsExplicitValues(t *testing.T) {
	srv := New(":0", http.NewServeMux(), Options{
		ReadHeaderTimeout: 3 * time.Second,
		IdleTimeout:       7 * time.Second,
		MaxHeaderBytes:    4096,
	})

	if srv.ReadHeaderTimeout != 3*time.Second || srv.IdleTimeout != 7*time.Second || srv.MaxHeaderBytes != 4096 {
		t.Fatalf("显式配置被覆盖: %+v", srv)
	}
}

// TestServe_WaitsForInflightRequest ctx 取消后 Shutdown 必须等在途请求完成。
func TestServe_WaitsForInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := New("", mux, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(ctx, srv, ln, time.Second) }()

	respCh := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			respCh <- err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		respCh <- string(body)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("请求没有进入 handler")
	}

	cancel()
	close(release)

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve 返回错误: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve 没有在停机窗口内返回")
	}

	select {
	case body := <-respCh:
		if body != "done" {
			t.Fatalf("在途请求被截断: %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("在途请求没有完成")
	}
}

// Shutdown 超时后必须强制 Close：否则调用方若选择继续存活，
// 卡住的连接和 handler goroutine 会永久占着资源。
func TestServe_ClosesConnectionsWhenShutdownTimesOut(t *testing.T) {
	started := make(chan struct{})
	connectionClosed := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(connectionClosed)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := New("", mux, Options{})

	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(ctx, srv, ln, 50*time.Millisecond) }()

	go func() {
		_, _ = http.Get("http://" + ln.Addr().String() + "/hang")
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("请求没有进入 handler")
	}

	cancel()

	select {
	case err := <-serveErr:
		if err == nil {
			t.Fatal("Shutdown 超时应返回错误")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve 没有在停机窗口内返回")
	}

	select {
	case <-connectionClosed:
	case <-time.After(time.Second):
		t.Fatal("Shutdown 超时后连接没有被强制关闭")
	}
}
