package client

import (
	"fmt"
	"os"
	"strings"
	"time"

	"Logos/config"
	"Logos/pkg/governance"
	"Logos/pkg/grpcserver"
	"Logos/pkg/logger"

	"google.golang.org/grpc"
)

func resolveServiceHost(serviceName string) string {
	shortName := strings.TrimPrefix(serviceName, "logos.")
	upperName := strings.ToUpper(shortName)
	if h := os.Getenv("LOGOS_" + upperName + "_HOST"); h != "" {
		return h
	}
	return "127.0.0.1"
}

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
	port := getServicePort(cfg, serviceName)
	if port > 0 {
		return tryDialWithFallback(cfg, serviceName, port)
	}
	return grpcserver.NewGRPCClientConnWithGovernance(cfg.Etcd.Endpoints, serviceName, buildGovCfg(cfg))
}

func getServicePort(cfg *config.Config, serviceName string) int {
	switch serviceName {
	case "logos.chat":
		return cfg.Ports.Chat
	case "logos.bot":
		return cfg.Ports.Bot
	case "logos.summary":
		return cfg.Ports.Summary
	case "logos.moderation":
		return cfg.Ports.Moderation
	case "logos.contact":
		return cfg.Ports.Contact
	case "logos.user":
		return cfg.Ports.User
	case "logos.vector":
		return cfg.Ports.Vector
	case "logos.knowledge":
		return cfg.Ports.Knowledge
	case "logos.extraction":
		return cfg.Ports.Extraction
	case "logos.search":
		return cfg.Ports.Search
	case "logos.mcp":
		return cfg.Ports.MCP
	case "logos.question":
		return cfg.Ports.Question
	case "logos.recommend":
		return cfg.Ports.Recommend
	case "logos.collection":
		return cfg.Ports.Collection
	case "logos.message":
		return cfg.Ports.Message
	case "logos.im":
		return cfg.Ports.IM
	case "logos.billing":
		return cfg.Ports.Billing
	case "logos.monitoring":
		return cfg.Ports.Monitoring
	default:
		return 0
	}
}

func tryDialWithFallback(cfg *config.Config, serviceName string, port int) (*grpc.ClientConn, error) {
	host := resolveServiceHost(serviceName)
	logger.Info("尝试连接服务",
		logger.StringField("service", serviceName),
		logger.StringField("host", host),
		logger.IntField("port", port))

	conn, err := grpcserver.NewDirectClientConn(host, port)
	if err == nil {
		logger.Info("直连服务成功",
			logger.StringField("service", serviceName),
			logger.StringField("host", host),
			logger.IntField("port", port))
		return conn, nil
	}

	logger.Warn("直连服务失败，尝试etcd服务发现",
		logger.StringField("service", serviceName),
		logger.ErrorField(err))

	conn, err = grpcserver.NewGRPCClientConnWithGovernance(cfg.Etcd.Endpoints, serviceName, buildGovCfg(cfg))
	if err != nil {
		return nil, fmt.Errorf("直连和etcd服务发现均失败: %w", err)
	}

	logger.Info("etcd服务发现连接成功",
		logger.StringField("service", serviceName))
	return conn, nil
}
