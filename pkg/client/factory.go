package client

import (
	"Logos/config"

	pbBilling "Logos/proto_gen/billing"
	pbKnowledge "Logos/proto_gen/knowledge"
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
