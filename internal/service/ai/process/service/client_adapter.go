package service

import (
	"context"
	"fmt"

	knowledgeModel "Logos/internal/service/ai/knowledge/model"
	"Logos/pkg/client"
)

type ExtractionClientAdapter struct {
	*client.ExtractionClient
}

func NewExtractionClientAdapter(c *client.ExtractionClient) *ExtractionClientAdapter {
	return &ExtractionClientAdapter{ExtractionClient: c}
}

func (a *ExtractionClientAdapter) ExtractFromText(ctx context.Context, text string, taskType int32, parameters map[string]string) (entities []map[string]interface{}, relations []map[string]interface{}, triples []map[string]interface{}, summary *string, keyphrases []string, err error) {
	result, err := a.ExtractionClient.ExtractFromText(ctx, text, taskType, parameters)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	return result.Entities, result.Relations, result.Triples, &result.Summary, nil, nil
}

type VectorClientAdapter struct {
	*client.VectorClient
}

func NewVectorClientAdapter(c *client.VectorClient) *VectorClientAdapter {
	return &VectorClientAdapter{VectorClient: c}
}

func (a *VectorClientAdapter) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (VectorModel, error) {
	result, err := a.VectorClient.Vectorize(ctx, text, collectionID, metadata)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *VectorClientAdapter) BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]VectorModel, error) {
	results := make([]VectorModel, 0, len(texts))
	for i, text := range texts {
		var meta map[string]string
		if i < len(metadataList) {
			meta = metadataList[i]
		}
		result, err := a.VectorClient.Vectorize(ctx, text, collectionID, meta)
		if err != nil {
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

func (a *VectorClientAdapter) GetCollectionInfo(ctx context.Context, id string) (*CollectionInfo, error) {
	info, err := a.VectorClient.GetCollection(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("获取集合配置失败: %w", err)
	}
	return &CollectionInfo{
		ID:         info.ID,
		VLMModel:   info.VLMModel,
		VLMBaseURL: info.VLMBaseURL,
		VLMApiKey:  info.VLMApiKey,
		LLMModel:   info.LLMModel,
		LLMBaseURL: info.LLMBaseURL,
		LLMApiKey:  info.LLMApiKey,
	}, nil
}

type KnowledgeClientAdapter struct {
	*client.KnowledgeClient
}

func NewKnowledgeClientAdapter(c *client.KnowledgeClient) *KnowledgeClientAdapter {
	return &KnowledgeClientAdapter{KnowledgeClient: c}
}

func (a *KnowledgeClientAdapter) AddEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*knowledgeModel.Entity, error) {
	result, err := a.KnowledgeClient.AddEntity(ctx, entityType, name, properties, description, collectionID, color)
	if err != nil {
		return nil, err
	}
	return &knowledgeModel.Entity{ID: result.GetID(), Type: entityType, Name: name, CollectionID: collectionID, Properties: knowledgeModel.JSONMap(properties), Description: description}, nil
}

func (a *KnowledgeClientAdapter) FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*knowledgeModel.Entity, error) {
	result, err := a.KnowledgeClient.FindOrCreateEntity(ctx, entityType, name, collectionID, properties, description, color)
	if err != nil {
		return nil, err
	}
	var desc *string
	if result.Description != "" {
		desc = &result.Description
	}
	return &knowledgeModel.Entity{ID: result.ID, Type: result.Type, Name: result.Name, CollectionID: result.CollectionID, Properties: knowledgeModel.JSONMap(result.Properties), Description: desc}, nil
}

func (a *KnowledgeClientAdapter) UpdateEntity(ctx context.Context, id, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*knowledgeModel.Entity, error) {
	_, err := a.KnowledgeClient.UpdateEntity(ctx, id, entityType, name, properties, description, color)
	if err != nil {
		return nil, err
	}
	return &knowledgeModel.Entity{ID: id, Type: entityType, Name: name, CollectionID: collectionID, Properties: knowledgeModel.JSONMap(properties), Description: description}, nil
}

func (a *KnowledgeClientAdapter) DeleteEntity(ctx context.Context, id string) error {
	return fmt.Errorf("DeleteEntity not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetEntity(ctx context.Context, id string) (*knowledgeModel.Entity, error) {
	return nil, fmt.Errorf("GetEntity not supported by client adapter")
}

func (a *KnowledgeClientAdapter) QueryEntities(ctx context.Context, entityType, name, collectionID string, properties map[string]string, page, pageSize int) ([]*knowledgeModel.Entity, int64, error) {
	return nil, 0, fmt.Errorf("QueryEntities not supported by client adapter")
}

func (a *KnowledgeClientAdapter) SearchEntities(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*knowledgeModel.Entity, int64, error) {
	return nil, 0, fmt.Errorf("SearchEntities not supported by client adapter")
}

func (a *KnowledgeClientAdapter) AddRelation(ctx context.Context, relationType, sourceId, targetId, collectionID string, properties map[string]string, description *string) (*knowledgeModel.Relation, error) {
	result, err := a.KnowledgeClient.AddRelation(ctx, relationType, sourceId, targetId, collectionID, properties, description)
	if err != nil {
		return nil, err
	}
	return &knowledgeModel.Relation{ID: result.GetID(), Type: relationType, SourceID: sourceId, TargetID: targetId, CollectionID: collectionID, Properties: knowledgeModel.JSONMap(properties), Description: description}, nil
}

func (a *KnowledgeClientAdapter) UpdateRelation(ctx context.Context, id, relationType, sourceId, targetId string, properties map[string]string, description *string) (*knowledgeModel.Relation, error) {
	return nil, fmt.Errorf("UpdateRelation not supported by client adapter")
}

func (a *KnowledgeClientAdapter) DeleteRelation(ctx context.Context, id string) error {
	return fmt.Errorf("DeleteRelation not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetRelation(ctx context.Context, id string) (*knowledgeModel.Relation, error) {
	return nil, fmt.Errorf("GetRelation not supported by client adapter")
}

func (a *KnowledgeClientAdapter) QueryRelations(ctx context.Context, relationType, sourceId, targetId, collectionID string, page, pageSize int) ([]*knowledgeModel.Relation, int64, error) {
	return nil, 0, fmt.Errorf("QueryRelations not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetGraphStats(ctx context.Context, collectionID string) (*knowledgeModel.GraphStats, error) {
	return nil, fmt.Errorf("GetGraphStats not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*knowledgeModel.Entity, error) {
	return nil, fmt.Errorf("GetRelatedEntities not supported by client adapter")
}

func (a *KnowledgeClientAdapter) GetSubgraph(ctx context.Context, entityID string, depth int, collectionID string) (*knowledgeModel.Subgraph, error) {
	return &knowledgeModel.Subgraph{}, nil
}

func (a *KnowledgeClientAdapter) GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*knowledgeModel.EntityPath, error) {
	return []*knowledgeModel.EntityPath{}, nil
}

func (a *KnowledgeClientAdapter) ImportData(ctx context.Context, dataType string, data []string) error {
	return fmt.Errorf("ImportData not supported by client adapter")
}

func (a *KnowledgeClientAdapter) WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error {
	return fn(a)
}
