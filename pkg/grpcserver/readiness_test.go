package grpcserver

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"Logos/pkg/health"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpcHealth "google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// TestWatchReadiness_Transitions 验证依赖状态真的驱动 gRPC 健康状态。
//
// 从「健康」开始而不是从「故障」开始：gRPC 健康服务对未注册服务的默认值
// 就是 NOT_SERVING，先等 NOT_SERVING 会立刻返回，测试根本没等到第一次探测。
func TestWatchReadiness_Transitions(t *testing.T) {
	var failing atomic.Bool
	reg := health.New(health.WithCacheTTL(0), health.WithTimeout(500*time.Millisecond))
	reg.Register("db", func(context.Context) error {
		if failing.Load() {
			return errors.New("db down")
		}
		return nil
	})

	hs := grpcHealth.NewServer()
	client := startHealthServer(t, hs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchReadiness(ctx, ServerConfig{
		ServiceName:   "logos.test",
		ReadyCheck:    reg,
		ReadyInterval: 30 * time.Millisecond,
	}, hs, nil)

	waitForStatus(t, client, "logos.test", grpc_health_v1.HealthCheckResponse_SERVING)

	failing.Store(true)
	waitForStatus(t, client, "logos.test", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	failing.Store(false)
	waitForStatus(t, client, "logos.test", grpc_health_v1.HealthCheckResponse_SERVING)
}

// TestWatchReadiness_EmptyServiceNameMirrors 进程级（空 service 名）查询必须
// 与具名服务状态一致。grpc_health_probe 默认查空 service 名，只设置具名服务时
// 它会一直拿到 NOT_SERVING，Pod 永远无法就绪。
func TestWatchReadiness_EmptyServiceNameMirrors(t *testing.T) {
	reg := health.New(health.WithCacheTTL(0), health.WithTimeout(500*time.Millisecond))
	reg.Register("db", func(context.Context) error { return nil })

	hs := grpcHealth.NewServer()
	client := startHealthServer(t, hs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchReadiness(ctx, ServerConfig{
		ServiceName:   "logos.test",
		ReadyCheck:    reg,
		ReadyInterval: 30 * time.Millisecond,
	}, hs, nil)

	waitForStatus(t, client, "", grpc_health_v1.HealthCheckResponse_SERVING)
}

// TestWatchReadiness_StopsWithContext 后台探针必须随 ctx 退出，不能泄漏 goroutine。
func TestWatchReadiness_StopsWithContext(t *testing.T) {
	reg := health.New(health.WithCacheTTL(0), health.WithTimeout(500*time.Millisecond))
	reg.Register("db", func(context.Context) error { return nil })

	hs := grpcHealth.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watchReadiness(ctx, ServerConfig{
			ServiceName:   "logos.test",
			ReadyCheck:    reg,
			ReadyInterval: 30 * time.Millisecond,
		}, hs, nil)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 watchReadiness 未退出")
	}
}

func startHealthServer(t *testing.T, hs *grpcHealth.Server) grpc_health_v1.HealthClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("无法分配测试端口: %v", err)
	}
	server := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(server, hs)
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() {
		server.Stop()
		_ = ln.Close()
	})

	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return grpc_health_v1.NewHealthClient(conn)
}

func waitForStatus(t *testing.T, client grpc_health_v1.HealthClient, service string, want grpc_health_v1.HealthCheckResponse_ServingStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last grpc_health_v1.HealthCheckResponse_ServingStatus
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: service})
		cancel()
		if err == nil {
			last = resp.GetStatus()
			if last == want {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("service %q 健康查询失败: %v", service, lastErr)
	}
	t.Fatalf("service %q status = %v, want %v", service, last, want)
}
