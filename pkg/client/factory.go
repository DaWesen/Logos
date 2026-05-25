package client

import (
	"Logos/config"

	pbBilling "Logos/proto_gen/billing"
	pbExtraction "Logos/proto_gen/extraction"
	pbKnowledge "Logos/proto_gen/knowledge"
	pbMCP "Logos/proto_gen/mcp"
	pbMonitoring "Logos/proto_gen/monitoring"
	pbSearch "Logos/proto_gen/search"
	pbVector "Logos/proto_gen/vector"
)

func NewKnowledgeClientFromConfig(cfg *config.Config) (*KnowledgeClient, error) {
	conn, err := newConn(cfg, "logos.knowledge")
	if err != nil {
		return nil, err
	}
	client := pbKnowledge.NewKnowledgeServiceClient(conn)
	return NewKnowledgeClient(client, conn), nil
}

func NewSearchClientFromConfig(cfg *config.Config) (*SearchClient, error) {
	conn, err := newConn(cfg, "logos.search")
	if err != nil {
		return nil, err
	}
	client := pbSearch.NewSearchServiceClient(conn)
	return NewSearchClient(client, conn), nil
}

func NewVectorClientFromConfig(cfg *config.Config) (*VectorClient, error) {
	conn, err := newConn(cfg, "logos.vector")
	if err != nil {
		return nil, err
	}
	client := pbVector.NewVectorServiceClient(conn)
	return NewVectorClient(client, conn), nil
}

func NewBillingClientFromConfig(cfg *config.Config) (*BillingClient, error) {
	conn, err := newConn(cfg, "logos.billing")
	if err != nil {
		return nil, err
	}
	client := pbBilling.NewBillingServiceClient(conn)
	return NewBillingClient(client, conn), nil
}

func TryDialVectorWithFallback(cfg *config.Config) (*VectorClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.vector", cfg.Ports.Vector)
	if err != nil {
		return nil, err
	}
	client := pbVector.NewVectorServiceClient(conn)
	return NewVectorClient(client, conn), nil
}

func TryDialKnowledgeWithFallback(cfg *config.Config) (*KnowledgeClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.knowledge", cfg.Ports.Knowledge)
	if err != nil {
		return nil, err
	}
	client := pbKnowledge.NewKnowledgeServiceClient(conn)
	return NewKnowledgeClient(client, conn), nil
}

func TryDialExtractionWithFallback(cfg *config.Config) (*ExtractionClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.extraction", cfg.Ports.Extraction)
	if err != nil {
		return nil, err
	}
	client := pbExtraction.NewKnowledgeExtractionServiceClient(conn)
	return NewExtractionClient(client, conn), nil
}

func TryDialSearchWithFallback(cfg *config.Config) (*SearchClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.search", cfg.Ports.Search)
	if err != nil {
		return nil, err
	}
	client := pbSearch.NewSearchServiceClient(conn)
	return NewSearchClient(client, conn), nil
}

func TryDialMCPWithFallback(cfg *config.Config) (*MCPClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.mcp", cfg.Ports.MCP)
	if err != nil {
		return nil, err
	}
	client := pbMCP.NewMCPServiceClient(conn)
	return NewMCPClient(client, conn), nil
}

func TryDialMonitoringWithFallback(cfg *config.Config) (*MonitoringClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.monitoring", cfg.Ports.Monitoring)
	if err != nil {
		return nil, err
	}
	client := pbMonitoring.NewMonitoringServiceClient(conn)
	return NewMonitoringClient(client, conn), nil
}
