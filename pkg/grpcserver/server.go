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
	"Logos/pkg/health"
	"Logos/pkg/obs"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/naming/resolver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpcHealth "google.golang.org/grpc/health"
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
	// ReadyCheck 是可选的依赖就绪检查。为空时只按进程生命周期上报健康状态
	// （启动 SERVING、停机 NOT_SERVING），适合没有外部强依赖的服务。
	//
	// 非空时会启动后台探测：依赖不可用就上报 NOT_SERVING，让负载均衡/编排系统
	// 摘除本实例，而不是让请求继续打到一条注定超时的连接上。
	ReadyCheck *health.Registry
	// ReadyInterval 是就绪探测周期，默认 10s（与 K8s probe 的常见周期一致）。
	ReadyInterval time.Duration
}

type EtcdConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
}

// defaultShutdownGrace 优雅停机宽限期：小于 K8s 默认的
// terminationGracePeriodSeconds(30s)，保证容器被强杀前完成排空。
// 可用环境变量 GRPC_SHUTDOWN_GRACE (秒) 覆盖。
const defaultShutdownGrace = 25 * time.Second

// defaultReadyInterval 与 K8s readinessProbe 的常见 periodSeconds 对齐：
// 探得太快没有意义（结果缓存本身也是秒级），探得太慢则故障后迟迟不摘流量。
const defaultReadyInterval = 10 * time.Second

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

	// 收发上限从配置读取（默认 4MB）：不配置时与 gRPC 默认一致，
	// 需要传大 payload 的服务可以单独调大，而不是被默认值卡住。
	recvLimit, sendLimit := MsgSizeLimits()
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(recvLimit),
		grpc.MaxSendMsgSize(sendLimit),
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

	healthServer := grpcHealth.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)

	// 就绪探测随进程生命周期结束：StartServer 返回（正常停机或 Serve 失败）时
	// 必须停掉后台 ticker，否则进程内的探针 goroutine 会一直泄漏。
	readyCtx, stopReady := context.WithCancel(context.Background())
	defer stopReady()
	if cfg.ReadyCheck != nil {
		// 有依赖探针时不能先报 SERVING：依赖还没探过就接流量，
		// 滚动发布期间会把请求打到尚未就绪的新副本上。初始状态保持 NOT_SERVING，
		// 由第一次探测决定是否放行。
		go watchReadiness(readyCtx, cfg, healthServer)
	} else {
		setServing(healthServer, cfg.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	}

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
		setServing(healthServer, cfg.ServiceName, grpc_health_v1.HealthCheckResponse_NOT_SERVING)

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

// setServing 同时设置具名服务与「整个进程」（空 service 名）的状态。
//
// gRPC 健康协议允许客户端用空 service 名查询进程级健康；只设置具名服务时，
// 这类探针会拿到默认的 NOT_SERVING，被误判为不健康。
func setServing(h *grpcHealth.Server, service string, status grpc_health_v1.HealthCheckResponse_ServingStatus) {
	h.SetServingStatus(service, status)
	h.SetServingStatus("", status)
}

// watchReadiness 周期性探测依赖，并把聚合结果映射到 gRPC 健康状态。
//
// 只在状态发生变化时写日志与指标：探测每 10s 一次，若每次都打日志，
// 正常运行时日志会被健康检查刷满，真正的问题反而被淹没。
func watchReadiness(ctx context.Context, cfg ServerConfig, healthServer *grpcHealth.Server) {
	interval := cfg.ReadyInterval
	if interval <= 0 {
		interval = defaultReadyInterval
	}

	var lastReady *bool
	apply := func(res health.Result) {
		status := grpc_health_v1.HealthCheckResponse_NOT_SERVING
		result := "not_ready"
		if res.Ready {
			status = grpc_health_v1.HealthCheckResponse_SERVING
			result = "ready"
		}
		setServing(healthServer, cfg.ServiceName, status)
		obs.RecordReadiness(ctx, cfg.ServiceName, result)

		changed := lastReady == nil || *lastReady != res.Ready
		lastReady = &res.Ready
		if !changed {
			return
		}
		if res.Ready {
			log.Printf("Service %s is ready (checks=%v)", cfg.ServiceName, res.Checks)
			return
		}
		log.Printf("Service %s is NOT ready: failed=%v checks=%v", cfg.ServiceName, res.Failed(), res.Checks)
	}

	// 启动即探一次：等第一个 tick 才摘流量，意味着依赖已经挂了还要继续接 10s 请求。
	apply(cfg.ReadyCheck.Ready(ctx))

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			apply(cfg.ReadyCheck.Ready(ctx))
		}
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
	return NewDirectClientConnWithGovernance(host, port, nil, opts...)
}

// NewDirectClientConnWithGovernance 直连模式 + 治理拦截器。
//
// 直连模式此前只设置 WaitForReady(true) 和消息大小，没有超时/熔断/重试。
// WaitForReady 的语义是「一直等到连接可用」，下游没起来时调用会无限挂住，
// 把网关 goroutine 和连接池一起占满。etcd 模式有拦截器、直连没有，
// 于是「单机部署」反而比「集群部署」更容易被拖垮。
// 这里让两条路径使用同一套治理能力。
func NewDirectClientConnWithGovernance(host string, port int, govCfg *governance.Config, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return NewDirectClientConnWithOptions(host, port, govCfg, governance.RetryPolicy{}, opts...)
}

// NewDirectClientConnWithOptions 直连 + 治理 + 可追加的重试白名单。
func NewDirectClientConnWithOptions(host string, port int, govCfg *governance.Config, policy governance.RetryPolicy, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	target := fmt.Sprintf("%s:%d", host, port)
	recvLimit, sendLimit := ClientMsgSizeLimits()

	defaultOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                60 * time.Second,
			Timeout:             20 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.WaitForReady(true),
			grpc.MaxCallRecvMsgSize(recvLimit),
			grpc.MaxCallSendMsgSize(sendLimit),
		),
	}

	if govCfg == nil {
		govCfg = governance.DefaultConfig()
	}
	defaultOpts = append(defaultOpts, governance.DefaultClientInterceptorsWithPolicy(govCfg, policy)...)
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
	return NewGRPCClientConnWithPolicy(etcdEndpoints, serviceName, govCfg, governance.RetryPolicy{}, opts...)
}

// NewGRPCClientConnWithPolicy 在 etcd 服务发现基础上追加重试白名单。
func NewGRPCClientConnWithPolicy(etcdEndpoints []string, serviceName string, govCfg *governance.Config, policy governance.RetryPolicy, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	etcdCli, err := clientv3.NewFromURLs(etcdEndpoints)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	etcdResolver, err := resolver.NewBuilder(etcdCli)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd resolver: %w", err)
	}

	recvLimit, sendLimit := ClientMsgSizeLimits()
	defaultOpts := []grpc.DialOption{
		grpc.WithResolvers(etcdResolver),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                60 * time.Second,
			Timeout:             20 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(
			grpc.WaitForReady(true),
			grpc.MaxCallRecvMsgSize(recvLimit),
			grpc.MaxCallSendMsgSize(sendLimit),
		),
		// etcd resolver 返回多个 addr 时，gRPC 默认 pick_first 只用第一个
		// 显式启用 round_robin 让多副本真正负载均衡
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
	}

	if govCfg == nil {
		govCfg = governance.DefaultConfig()
	}
	defaultOpts = append(defaultOpts, governance.DefaultClientInterceptorsWithPolicy(govCfg, policy)...)
	defaultOpts = append(defaultOpts, opts...)

	target := fmt.Sprintf("etcd:///grpc/%s", serviceName)
	conn, err := grpc.NewClient(target, defaultOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	return conn, nil
}
