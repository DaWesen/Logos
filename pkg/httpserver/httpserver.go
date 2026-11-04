// Package httpserver 统一 HTTP 入口的超时与优雅停机。
//
// net/http 的零值 Server 没有 ReadHeaderTimeout，慢连接可以只发半个请求头
// 就长期占住 goroutine；r.Run() 也不处理 SIGTERM，进程被重启时在途请求会被直接切断。
package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
	DefaultShutdownTimeout   = 10 * time.Second
	DefaultMaxHeaderBytes    = 1 << 20
)

// Options 只暴露需要按服务调整的参数；零值使用安全默认值。
type Options struct {
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxHeaderBytes    int
}

// New 构造一个带安全默认值的 Server。
// 不设置 WriteTimeout：上传/下载类接口可能需要较长写时间，应由业务层 ctx 控制。
func New(addr string, handler http.Handler, opts Options) *http.Server {
	if opts.ReadHeaderTimeout <= 0 {
		opts.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = DefaultShutdownTimeout
	}
	if opts.MaxHeaderBytes <= 0 {
		opts.MaxHeaderBytes = DefaultMaxHeaderBytes
	}

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: opts.ReadHeaderTimeout,
		IdleTimeout:       opts.IdleTimeout,
		MaxHeaderBytes:    opts.MaxHeaderBytes,
	}
}

// Serve 在 listener 上提供服务，ctx 取消后停止接收新连接并等待在途请求。
// 返回 nil 表示正常停机；Shutdown 超时会返回对应错误，由调用方决定是否强制退出。
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration) error {
	if shutdownTimeout <= 0 {
		shutdownTimeout = DefaultShutdownTimeout
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			// Shutdown 超时不会强制断开连接：如果调用方决定不退出进程，
			// 在途连接与 goroutine 会一直挂着。超时后显式 Close 兜底。
			_ = srv.Close()
			return err
		}
		return nil
	}
}
