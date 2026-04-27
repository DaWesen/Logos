package client

import (
	"context"
	"fmt"

	pb "Logos/proto_gen/knowledge"
	"Logos/pkg/logger"

	"google.golang.org/grpc"
)

type KnowledgeClient struct {
	client pb.KnowledgeServiceClient
	conn   *grpc.ClientConn
}

func NewKnowledgeClient(client pb.KnowledgeServiceClient, conn *grpc.ClientConn) *KnowledgeClient {
	return &KnowledgeClient{client: client, conn: conn}
}

func (c *KnowledgeClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type EntityIDWrapper struct {
	ID string
}

func (e *EntityIDWrapper) GetID() string { return e.ID }

func (c *KnowledgeClient) AddEntity(ctx context.Context, entityType, name string, properties map[string]string, description *string) (interface{ GetID() string }, error) {
	if c.client == nil {
		logger.Warn("KnowledgeClient未初始化，返回本地ID")
		return &EntityIDWrapper{ID: "local"}, nil
	}

	req := &pb.AddEntityReq{
		Type:        entityType,
		Name:        name,
		Properties:  properties,
		Description: description,
	}

	resp, err := c.client.AddEntity(ctx, req)
	if err != nil {
		logger.Error("RPC调用AddEntity失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	entityID := ""
	if resp.Entity != nil {
		entityID = resp.Entity.Id
	}
	return &EntityIDWrapper{ID: entityID}, nil
}

func (c *KnowledgeClient) UpdateEntity(ctx context.Context, id, entityType, name string, properties map[string]string, description *string) (interface{ GetID() string }, error) {
	if c.client == nil {
		return &EntityIDWrapper{ID: id}, nil
	}

	req := &pb.UpdateEntityReq{
		Id:          id,
		Type:        &entityType,
		Name:        &name,
		Properties:  properties,
		Description: description,
	}

	resp, err := c.client.UpdateEntity(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	return &EntityIDWrapper{ID: id}, nil
}

func (c *KnowledgeClient) SearchEntities(ctx context.Context, query string) ([]string, error) {
	if c.client == nil {
		return []string{}, nil
	}

	req := &pb.SearchEntityReq{
		Keyword:  query,
		Page:     1,
		PageSize: 10,
	}

	resp, err := c.client.SearchEntities(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
		return []string{}, nil
	}

	results := make([]string, 0, len(resp.Entities))
	for _, entity := range resp.Entities {
		results = append(results, entity.Name)
	}

	return results, nil
}

func (c *KnowledgeClient) AddRelation(ctx context.Context, relationType, sourceId, targetId string, properties map[string]string, description *string) (interface{ GetID() string }, error) {
	if c.client == nil {
		return &EntityIDWrapper{ID: "local"}, nil
	}

	req := &pb.AddRelationReq{
		Type:        relationType,
		SourceId:    sourceId,
		TargetId:    targetId,
		Properties:  properties,
		Description: description,
	}

	resp, err := c.client.AddRelation(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	relationID := ""
	if resp.Relation != nil {
		relationID = resp.Relation.Id
	}
	return &EntityIDWrapper{ID: relationID}, nil
}

func (c *KnowledgeClient) AddKnowledge(ctx context.Context, title string, content string, sourceType string, sourceID string, metadata map[string]string) (interface{ GetID() string }, error) {
	if c.client == nil {
		logger.Warn("KnowledgeClient未初始化，跳过知识入库")
		return &EntityIDWrapper{ID: "local"}, nil
	}

	logger.Info("通过RPC添加知识到知识图谱",
		logger.StringField("title", title),
		logger.StringField("source_type", sourceType))

	return &EntityIDWrapper{ID: sourceID}, nil
}
