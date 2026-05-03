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

func (a *KnowledgeClientAdapter) AddEntity(ctx context.Context, entityType, name string, properties map[string]string, description *string) (interface{ GetID() string }, error) {
	return a.KnowledgeClient.AddEntity(ctx, entityType, name, properties, description)
}

func (a *KnowledgeClientAdapter) AddRelation(ctx context.Context, relationType, sourceId, targetId string, properties map[string]string, description *string) (interface{ GetID() string }, error) {
	return a.KnowledgeClient.AddRelation(ctx, relationType, sourceId, targetId, properties, description)
}

type VectorClientAdapter struct {
	*client.VectorClient
}

func NewVectorClientAdapter(c *client.VectorClient) *VectorClientAdapter {
	return &VectorClientAdapter{VectorClient: c}
}

func (a *VectorClientAdapter) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (interface{ GetID() string }, error) {
	return a.VectorClient.Vectorize(ctx, text, collectionID, metadata)
}
