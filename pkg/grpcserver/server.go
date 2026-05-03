package grpcserver

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
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
	Etcd        EtcdConfig
	Governance  *governance.Config
}

type EtcdConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
}

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
			Time:                  30 * time.Second,
			Timeout:               10 * time.Second,
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

	if len(cfg.Etcd.Endpoints) > 0 {
		if err := registerEtcd(cfg); err != nil {
			log.Printf("Warning: failed to register with etcd: %v", err)
		} else {
			log.Printf("Service %s registered with etcd", cfg.ServiceName)
		}
	}

	log.Printf("gRPC server %s starting on :%d", cfg.ServiceName, cfg.Port)
	return server.Serve(lis)
}

func registerEtcd(cfg ServerConfig) error {
	etcdCli, err := clientv3.NewFromURLs(cfg.Etcd.Endpoints)
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}

	etcdResolver, err := resolver.NewBuilder(etcdCli)
	if err != nil {
		return fmt.Errorf("failed to create etcd resolver: %w", err)
	}

	hostname := os.Getenv("SERVICE_HOST")
	if hostname == "" {
		hostname = os.Getenv("HOSTNAME")
	}
	if hostname == "" {
		hostname = "127.0.0.1"
	}
	serviceAddr := fmt.Sprintf("%s:%d", hostname, cfg.Port)
	lease, err := etcdCli.Grant(context.Background(), 10)
	if err != nil {
		return fmt.Errorf("failed to create etcd lease: %w", err)
	}

	key := fmt.Sprintf("/grpc/%s/%s", cfg.ServiceName, serviceAddr)
	_, err = etcdCli.Put(context.Background(), key, serviceAddr, clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("failed to register service with etcd: %w", err)
	}

	log.Printf("Service %s registered with etcd at %s", cfg.ServiceName, serviceAddr)

	go func() {
		ch, kaErr := etcdCli.KeepAlive(context.Background(), lease.ID)
		if kaErr != nil {
			log.Printf("etcd keep alive error: %v", kaErr)
			return
		}
		for range ch {
		}
	}()

	_ = etcdResolver
	return nil
}

func NewGRPCClientConn(etcdEndpoints []string, serviceName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	return NewGRPCClientConnWithGovernance(etcdEndpoints, serviceName, nil, opts...)
}

func NewDirectClientConn(host string, port int, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	target := fmt.Sprintf("%s:%d", host, port)

	defaultOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	}

	defaultOpts = append(defaultOpts, opts...)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
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
