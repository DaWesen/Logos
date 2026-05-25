package service

import (
	"context"
	"fmt"

	"Logos/internal/service/ai/knowledge/model"
	"Logos/pkg/client"
)

type KnowledgeClientAdapter struct {
	*client.KnowledgeClient
}

func NewKnowledgeClientAdapter(c *client.KnowledgeClient) *KnowledgeClientAdapter {
	return &KnowledgeClientAdapter{KnowledgeClient: c}
}

func (a *KnowledgeClientAdapter) AddEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error) {
	result, err := a.KnowledgeClient.AddEntity(ctx, entityType, name, properties, description, collectionID, color)
	if err != nil {
		return nil, err
	}
	return &model.Entity{ID: result.GetID(), Type: entityType, Name: name, CollectionID: collectionID, Properties: model.JSONMap(properties), Description: description}, nil
}

func (a *KnowledgeClientAdapter) FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error) {
	result, err := a.KnowledgeClient.FindOrCreateEntity(ctx, entityType, name, collectionID, properties, description, color)
	if err != nil {
		return nil, err
	}
	var desc *string
	if result.Description != "" {
		desc = &result.Description
	}
	return &model.Entity{ID: result.ID, Type: result.Type, Name: result.Name, CollectionID: result.CollectionID, Properties: model.JSONMap(result.Properties), Description: desc}, nil
}

func (a *KnowledgeClientAdapter) UpdateEntity(ctx context.Context, id, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*model.Entity, error) {
	_, err := a.KnowledgeClient.UpdateEntity(ctx, id, entityType, name, properties, description, color)
	if err != nil {
		return nil, err
	}
	return &model.Entity{ID: id, Type: entityType, Name: name, CollectionID: collectionID, Properties: model.JSONMap(properties), Description: description}, nil
}

func (a *KnowledgeClientAdapter) DeleteEntity(ctx context.Context, id string) error {
	return fmt.Errorf("DeleteEntity not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetEntity(ctx context.Context, id string) (*model.Entity, error) {
	return nil, fmt.Errorf("GetEntity not supported by client adapter")
}

func (a *KnowledgeClientAdapter) QueryEntities(ctx context.Context, entityType, name, collectionID string, properties map[string]string, page, pageSize int) ([]*model.Entity, int64, error) {
	return nil, 0, fmt.Errorf("QueryEntities not supported by client adapter")
}

func (a *KnowledgeClientAdapter) SearchEntities(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*model.Entity, int64, error) {
	return nil, 0, fmt.Errorf("SearchEntities not supported by client adapter")
}

func (a *KnowledgeClientAdapter) AddRelation(ctx context.Context, relationType, sourceId, targetId, collectionID string, properties map[string]string, description *string) (*model.Relation, error) {
	result, err := a.KnowledgeClient.AddRelation(ctx, relationType, sourceId, targetId, collectionID, properties, description)
	if err != nil {
		return nil, err
	}
	return &model.Relation{ID: result.GetID(), Type: relationType, SourceID: sourceId, TargetID: targetId, CollectionID: collectionID, Properties: model.JSONMap(properties), Description: description}, nil
}

func (a *KnowledgeClientAdapter) UpdateRelation(ctx context.Context, id, relationType, sourceId, targetId string, properties map[string]string, description *string) (*model.Relation, error) {
	return nil, fmt.Errorf("UpdateRelation not supported by client adapter")
}

func (a *KnowledgeClientAdapter) DeleteRelation(ctx context.Context, id string) error {
	return fmt.Errorf("DeleteRelation not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetRelation(ctx context.Context, id string) (*model.Relation, error) {
	return nil, fmt.Errorf("GetRelation not supported by client adapter")
}

func (a *KnowledgeClientAdapter) QueryRelations(ctx context.Context, relationType, sourceId, targetId, collectionID string, page, pageSize int) ([]*model.Relation, int64, error) {
	return nil, 0, fmt.Errorf("QueryRelations not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetGraphStats(ctx context.Context, collectionID string) (*model.GraphStats, error) {
	return nil, fmt.Errorf("GetGraphStats not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*model.Entity, error) {
	return nil, fmt.Errorf("GetRelatedEntities not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetSubgraph(ctx context.Context, entityID string, depth int, collectionID string) (*model.Subgraph, error) {
	return &model.Subgraph{}, nil
}

func (a *KnowledgeClientAdapter) GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*model.EntityPath, error) {
	return []*model.EntityPath{}, nil
}

func (a *KnowledgeClientAdapter) ImportData(ctx context.Context, dataType string, data []string) error {
	return fmt.Errorf("ImportData not supported by client adapter")
}

func (a *KnowledgeClientAdapter) WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error {
	return fn(a)
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
