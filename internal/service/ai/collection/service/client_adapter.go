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

func (a *KnowledgeClientAdapter) AddKnowledge(ctx context.Context, title, content, sourceType, sourceID string, metadata map[string]string) (interface{ GetID() string }, error) {
	return a.KnowledgeClient.AddKnowledge(ctx, title, content, sourceType, sourceID, metadata)
}

type ExtractionClientAdapter struct {
	*client.ExtractionClient
}

func NewExtractionClientAdapter(c *client.ExtractionClient) *ExtractionClientAdapter {
	return &ExtractionClientAdapter{ExtractionClient: c}
}

func (a *ExtractionClientAdapter) CreateTask(ctx context.Context, taskType int32, dataID, dataType string, parameters map[string]string, scheduledTime *string) (interface{ GetID() string }, error) {
	id, err := a.ExtractionClient.CreateTask(ctx, taskType, dataID, dataType, parameters, scheduledTime)
	if err != nil {
		return nil, err
	}
	return &idWrapper{id: id}, nil
}

type idWrapper struct {
	id string
}

func (w *idWrapper) GetID() string { return w.id }
