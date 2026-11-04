package grpcserver

import (
	"context"
	"net"
	"testing"
	"time"

	"Logos/pkg/governance"

	"google.golang.org/protobuf/types/known/emptypb"
)

// TestDirectClientConn_AppliesGovernanceTimeout
//
// 直连一个没有服务监听的端口：WaitForReady(true) 会一直等连接可用。
// 没有治理拦截器时，这个调用会挂到测试超时；有了 100ms 的客户端超时，
// 必须快速返回 DeadlineExceeded。这是「单机部署不会被下游拖死」的回归测试。
func TestDirectClientConn_AppliesGovernanceTimeout(t *testing.T) {
	// 先占一个端口再释放，得到一个几乎确定无人监听的地址。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("无法分配测试端口: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()

	cfg := governance.DefaultConfig()
	cfg.Timeout.ClientDefault = 100 * time.Millisecond
	cfg.Retry.MaxAttempts = 1
	cfg.Retry.RetryableCodes = nil

	conn, err := NewDirectClientConnWithGovernance("127.0.0.1", addr.Port, cfg)
	if err != nil {
		t.Fatalf("创建连接失败: %v", err)
	}
	defer conn.Close()

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		// 故意不设调用方 deadline：验证治理层会兜底。
		// 若调用方自己带了 deadline，则以其为准（更短的预算不应被放大）。
		done <- conn.Invoke(context.Background(), "/logos.test/NoSuchMethod", &emptypb.Empty{}, &emptypb.Empty{})
	}()

	var invokeErr error
	select {
	case invokeErr = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("治理超时未生效：无 deadline 的调用在 3s 后仍未返回")
	}
	elapsed := time.Since(start)

	if invokeErr == nil {
		t.Fatal("调用不存在的服务应返回错误")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("治理超时未生效，调用耗时 %v", elapsed)
	}
}
