package client

import (
	"time"

	"Logos/config"
	"Logos/pkg/governance"
	"Logos/pkg/grpcserver"

	"google.golang.org/grpc"
)

func buildGovCfg(cfg *config.Config) *governance.Config {
	clientTimeout, err := cfg.GetGRPCClientTimeout()
	if err != nil {
		clientTimeout = 30 * time.Second
	}
	govCfg := governance.DefaultConfig()
	govCfg.Timeout.ClientDefault = clientTimeout
	return govCfg
}

func newConn(cfg *config.Config, serviceName string) (*grpc.ClientConn, error) {
	return grpcserver.NewGRPCClientConnWithGovernance(cfg.Etcd.Endpoints, serviceName, buildGovCfg(cfg))
}
