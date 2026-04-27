package grpcserver

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/naming/resolver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// RegisterFunc is a function that registers a gRPC service implementation.
type RegisterFunc func(server *grpc.Server)

// ServerConfig holds configuration for a gRPC server.
type ServerConfig struct {
	ServiceName string
	Port        int
	Etcd        EtcdConfig
}

// EtcdConfig holds etcd connection configuration.
type EtcdConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
}

// StartServer starts a gRPC server with etcd service registration and optional OTel interceptors.
func StartServer(cfg ServerConfig, register RegisterFunc, serverOptions ...grpc.ServerOption) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", cfg.Port, err)
	}

	// Build server options
	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     15 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 10 * time.Second,
			Time:                  30 * time.Second,
			Timeout:               10 * time.Second,
		}),
	}

	opts = append(opts, serverOptions...)

	server := grpc.NewServer(opts...)
	register(server)

	// Register with etcd
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

// registerEtcd registers the service with etcd for service discovery.
func registerEtcd(cfg ServerConfig) error {
	etcdCli, err := clientv3.NewFromURLs(cfg.Etcd.Endpoints)
	if err != nil {
		return fmt.Errorf("failed to connect to etcd: %w", err)
	}

	etcdResolver, err := resolver.NewBuilder(etcdCli)
	if err != nil {
		return fmt.Errorf("failed to create etcd resolver: %w", err)
	}

	// Use grpc-etcd naming for service registration
	// The service key format: /grpc/service_name/host:port
	serviceAddr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	lease, err := etcdCli.Grant(context.Background(), 10)
	if err != nil {
		return fmt.Errorf("failed to create etcd lease: %w", err)
	}

	key := fmt.Sprintf("/grpc/%s/%s", cfg.ServiceName, serviceAddr)
	_, err = etcdCli.Put(context.Background(), key, serviceAddr, clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("failed to register service with etcd: %w", err)
	}

	// Keep the lease alive
	go func() {
		ch, kaErr := etcdCli.KeepAlive(context.Background(), lease.ID)
		if kaErr != nil {
			log.Printf("etcd keep alive error: %v", kaErr)
			return
		}
		for range ch {
			// drain the channel
		}
	}()

	_ = etcdResolver
	return nil
}

// NewGRPCClientConn creates a gRPC client connection with etcd service discovery.
func NewGRPCClientConn(etcdEndpoints []string, serviceName string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
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

	defaultOpts = append(defaultOpts, opts...)

	target := fmt.Sprintf("etcd:///grpc/%s", serviceName)
	conn, err := grpc.NewClient(target, defaultOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}

	return conn, nil
}
