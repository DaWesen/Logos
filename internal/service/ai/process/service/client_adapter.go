package service

import (
	"context"

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
	return result.Entities, result.Relations, nil, &result.Summary, nil, nil
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

type KnowledgeClientAdapter struct {
	*client.KnowledgeClient
}

func NewKnowledgeClientAdapter(c *client.KnowledgeClient) *KnowledgeClientAdapter {
	return &KnowledgeClientAdapter{KnowledgeClient: c}
}

func (a *KnowledgeClientAdapter) AddEntity(ctx context.Context, entityType, name string, properties map[string]string, description *string) (VectorModel, error) {
	return a.KnowledgeClient.AddEntity(ctx, entityType, name, properties, description)
}

func (a *KnowledgeClientAdapter) AddRelation(ctx context.Context, relationType, sourceId, targetId string, properties map[string]string, description *string) (VectorModel, error) {
	return a.KnowledgeClient.AddRelation(ctx, relationType, sourceId, targetId, properties, description)
}
