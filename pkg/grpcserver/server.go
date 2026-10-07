package grpcserver

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"Logos/pkg/governance"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/naming/resolver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

type RegisterFunc func(server *grpc.Server)

type ServerConfig struct {
	ServiceName string
	Port        int
	Host        string // 注册到 etcd 的 host，默认 127.0.0.1
	Etcd        EtcdConfig
	Governance  *governance.Config
}

type EtcdConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
}

// defaultShutdownGrace 优雅停机宽限期：小于 K8s 默认的
// terminationGracePeriodSeconds(30s)，保证容器被强杀前完成排空。
// 可用环境变量 GRPC_SHUTDOWN_GRACE (秒) 覆盖。
const defaultShutdownGrace = 25 * time.Second

func shutdownGracePeriod() time.Duration {
	if v := os.Getenv("GRPC_SHUTDOWN_GRACE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
		log.Printf("invalid GRPC_SHUTDOWN_GRACE %q, using default %s", v, defaultShutdownGrace)
	}
	return defaultShutdownGrace
}

// StartServer 启动 gRPC 服务，并在收到 SIGINT/SIGTERM 时执行优雅停机：
//  1. 从 etcd 注销，新流量不再路由到本实例（否则只能等 60s lease 过期，
//     期间客户端仍会拿到死地址）；
//  2. 健康检查置 NOT_SERVING，负载均衡主动摘除；
//  3. GracefulStop 停止接收新连接、等待 in-flight 请求完成；
//  4. 宽限期耗尽则强制 Stop，避免被 K8s SIGKILL 撕断。
func StartServer(cfg ServerConfig, register RegisterFunc, serverOptions ...grpc.ServerOption) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", cfg.Port, err)
	}

	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     15 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 10 * time.Second,
			Time:                  60 * time.Second,
			Timeout:               20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             30 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	if cfg.Governance != nil {
		opts = append(opts, governance.DefaultServerInterceptors(cfg.Governance)...)
	}

	opts = append(opts, serverOptions...)

	server := grpc.NewServer(opts...)
	register(server)

	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus(cfg.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)

	var deregister func()
	if len(cfg.Etcd.Endpoints) > 0 {
		if d, err := registerEtcd(cfg); err != nil {
			log.Printf("Warning: failed to register with etcd: %v", err)
		} else {
			deregister = d
			log.Printf("Service %s registered with etcd", cfg.ServiceName)
		}
	}

	log.Printf("gRPC server %s starting on :%d", cfg.ServiceName, cfg.Port)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(lis)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		if deregister != nil {
			deregister()
		}
		return err

	case sig := <-sigCh:
		log.Printf("Service %s received %v, shutting down gracefully (grace=%s)",
			cfg.ServiceName, sig, shutdownGracePeriod())

		// 1) 先从 etcd 摘除，避免新流量继续路由到本实例
		if deregister != nil {
			deregister()
		}

		// 2) 健康检查置 NOT_SERVING
		healthServer.SetServingStatus(cfg.ServiceName, grpc_health_v1.HealthCheckResponse_NOT_SERVING)

		// 3) 等待 in-flight 请求排空；宽限期耗尽则强制停止
		done := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(done)
		}()
		select {
		case <-done:
			log.Printf("Service %s drained and stopped gracefully", cfg.ServiceName)
		case <-time.After(shutdownGracePeriod()):
			server.Stop()
			log.Printf("Service %s grace period exhausted, forced stop", cfg.ServiceName)
		}
		return nil
	}
}

// registerEtcd 注册服务到 etcd（lease 保活），返回注销函数。
// 注销函数在停机时主动删除注册 key，并关闭 etcd 连接。
func registerEtcd(cfg ServerConfig) (deregister func(), err error) {
	etcdCli, err := clientv3.NewFromURLs(cfg.Etcd.Endpoints)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	hostname := cfg.Host
	if hostname == "" {
		hostname = os.Getenv("SERVICE_HOST")
	}
	if hostname == "" {
		hostname = os.Getenv("HOSTNAME")
	}
	if hostname == "" {
		hostname = "127.0.0.1"
	}
	serviceAddr := fmt.Sprintf("%s:%d", hostname, cfg.Port)
	lease, err := etcdCli.Grant(context.Background(), 60)
	if err != nil {
		_ = etcdCli.Close()
		return nil, fmt.Errorf("failed to create etcd lease: %w", err)
	}

	key := fmt.Sprintf("/grpc/%s/%s", cfg.ServiceName, serviceAddr)
	_, err = etcdCli.Put(context.Background(), key, serviceAddr, clientv3.WithLease(lease.ID))
	if err != nil {
		_ = etcdCli.Close()
		return nil, fmt.Errorf("failed to register service with etcd: %w", err)
	}

	log.Printf("Service %s registered with etcd at %s", cfg.ServiceName, serviceAddr)

	kaDone := make(chan struct{})
	go func() {
		defer close(kaDone)
		ch, kaErr := etcdCli.KeepAlive(context.Background(), lease.ID)
		if kaErr != nil {
			log.Printf("etcd keep alive error: %v", kaErr)
			return
		}
		for range ch {
		}
	}()

	deregister = func() {
		// 主动删除注册 key：立即生效，无需等待 lease 过期
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, delErr := etcdCli.Delete(ctx, key); delErr != nil {
			// 删除失败时 lease 过期兜底（60s 内自动消失）
			log.Printf("etcd deregister failed (will expire via lease): %v", delErr)
		} else {
			log.Printf("Service %s deregistered from etcd", cfg.ServiceName)
		}
		// 撤销 lease 立即释放，并结束保活 goroutine
		_, _ = etcdCli.Revoke(context.Background(), lease.ID)
		_ = etcdCli.Close()
		<-kaDone
	}
	return deregister, nil
}

func NewGRPCClientConn(etcdEndpoints []string, serviceName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return NewGRPCClientConnWithGovernance(etcdEndpoints, serviceName, nil, opts...)
}

func NewDirectClientConn(host string, port int, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	target := fmt.Sprintf("%s:%d", host, port)

	defaultOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                60 * time.Second,
			Timeout:             20 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	}

	defaultOpts = append(defaultOpts, opts...)

	// 增加超时时间到 10 秒
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, target, defaultOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", target, err)
	}

	return conn, nil
}

func NewGRPCClientConnWithGovernance(etcdEndpoints []string, serviceName string, govCfg *governance.Config, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	etcdCli, err := clientv3.NewFromURLs(etcdEndpoints)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	etcdResolver, err := resolver.NewBuilder(etcdCli)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd resolver: %w", err)
	}

	defaultOpts := []grpc.DialOption{
		grpc.WithResolvers(etcdResolver),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                60 * time.Second,
			Timeout:             20 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
		// etcd resolver 返回多个 addr 时，gRPC 默认 pick_first 只用第一个
		// 显式启用 round_robin 让多副本真正负载均衡
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
	}

	if govCfg == nil {
		govCfg = governance.DefaultConfig()
	}
	defaultOpts = append(defaultOpts, governance.DefaultClientInterceptors(govCfg)...)
	defaultOpts = append(defaultOpts, opts...)

	target := fmt.Sprintf("etcd:///grpc/%s", serviceName)
	conn, err := grpc.NewClient(target, defaultOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	return conn, nil
}
