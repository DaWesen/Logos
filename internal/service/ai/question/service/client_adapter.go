package service

import (
	"context"

	"Logos/pkg/client"
)

type KnowledgeClientAdapter struct {
	*client.KnowledgeClient
}

func NewKnowledgeClientAdapter(c *client.KnowledgeClient) *KnowledgeClientAdapter {
	return &KnowledgeClientAdapter{KnowledgeClient: c}
}

func (a *KnowledgeClientAdapter) SearchEntities(ctx context.Context, query string) ([]string, error) {
	return a.KnowledgeClient.SearchEntities(ctx, query)
}

type SearchClientAdapter struct {
	*client.SearchClient
}

func NewSearchClientAdapter(c *client.SearchClient) *SearchClientAdapter {
	return &SearchClientAdapter{SearchClient: c}
}

func (a *SearchClientAdapter) Search(ctx context.Context, query string, indexType string) ([]string, error) {
	return a.SearchClient.Search(ctx, query, indexType)
}

type VectorClientAdapter struct {
	*client.VectorClient
}

func NewVectorClientAdapter(c *client.VectorClient) *VectorClientAdapter {
	return &VectorClientAdapter{VectorClient: c}
}

func (a *VectorClientAdapter) SearchSimilar(ctx context.Context, text string, topK int) ([]string, error) {
	return a.VectorClient.SearchSimilar(ctx, text, topK)
}
