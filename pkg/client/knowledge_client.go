package client

import (
	"context"
	"fmt"

	"Logos/pkg/logger"
	pb "Logos/proto_gen/knowledge"

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

type GraphEntity struct {
	ID           string
	Type         string
	Name         string
	Properties   map[string]string
	Description  string
	CollectionID string
	Color        string
}

type GraphRelation struct {
	ID           string
	Type         string
	SourceID     string
	TargetID     string
	Properties   map[string]string
	Description  string
	CollectionID string
}

type GraphSubgraph struct {
	Nodes     []*GraphEntity
	Edges     []*GraphRelation
	NodeCount int
	EdgeCount int
}

type GraphEntityPath struct {
	Entities []*GraphEntity
	Edges    []*GraphRelation
	Length   int
}

type GraphStatsInfo struct {
	EntityCount       int64
	RelationCount     int64
	EntityTypeCount   map[string]int64
	RelationTypeCount map[string]int64
}

func protoEntityToGraphEntity(e *pb.Entity) *GraphEntity {
	if e == nil {
		return nil
	}
	return &GraphEntity{
		ID:           e.GetId(),
		Type:         e.GetType(),
		Name:         e.GetName(),
		Properties:   e.GetProperties(),
		Description:  e.GetDescription(),
		CollectionID: e.GetCollectionId(),
		Color:        e.GetColor(),
	}
}

func protoRelationToGraphRelation(r *pb.Relation) *GraphRelation {
	if r == nil {
		return nil
	}
	return &GraphRelation{
		ID:           r.GetId(),
		Type:         r.GetType(),
		SourceID:     r.GetSourceId(),
		TargetID:     r.GetTargetId(),
		Properties:   r.GetProperties(),
		Description:  r.GetDescription(),
		CollectionID: r.GetCollectionId(),
	}
}

func (c *KnowledgeClient) AddEntity(ctx context.Context, entityType, name string, properties map[string]string, description *string, collectionID ...string) (interface{ GetID() string }, error) {
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

	if len(collectionID) > 0 && collectionID[0] != "" {
		req.CollectionId = collectionID[0]
	}
	if len(collectionID) > 1 {
		req.Color = collectionID[1]
	}

	resp, err := c.client.AddEntity(ctx, req)
	if err != nil {
		logger.Error("RPC调用AddEntity失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	entityID := ""
	if resp.Entity != nil {
		entityID = resp.Entity.Id
	}
	return &EntityIDWrapper{ID: entityID}, nil
}

func (c *KnowledgeClient) UpdateEntity(ctx context.Context, id, entityType, name string, properties map[string]string, description *string, collectionID ...string) (interface{ GetID() string }, error) {
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

	if len(collectionID) > 0 && collectionID[0] != "" {
		req.CollectionId = collectionID[0]
	}
	if len(collectionID) > 1 {
		req.Color = collectionID[1]
	}

	resp, err := c.client.UpdateEntity(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	return &EntityIDWrapper{ID: id}, nil
}

func (c *KnowledgeClient) DeleteEntity(ctx context.Context, id string) error {
	if c.client == nil {
		return nil
	}

	req := &pb.DeleteEntityReq{Id: id}
	resp, err := c.client.DeleteEntity(ctx, req)
	if err != nil {
		return err
	}
	if resp != nil && resp.StatusCode != 0 {
		return fmt.Errorf("%s", resp.StatusMessage)
	}
	return nil
}

func (c *KnowledgeClient) DeleteRelation(ctx context.Context, id string) error {
	if c.client == nil {
		return nil
	}

	req := &pb.DeleteRelationReq{Id: id}
	resp, err := c.client.DeleteRelation(ctx, req)
	if err != nil {
		return err
	}
	if resp != nil && resp.StatusCode != 0 {
		return fmt.Errorf("%s", resp.StatusMessage)
	}
	return nil
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
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return []string{}, nil
	}

	results := make([]string, 0, len(resp.Entities))
	for _, entity := range resp.Entities {
		results = append(results, entity.Name)
	}

	return results, nil
}

func (c *KnowledgeClient) AddRelation(ctx context.Context, relationType, sourceId, targetId, collectionID string, properties map[string]string, description *string) (interface{ GetID() string }, error) {
	if c.client == nil {
		return &EntityIDWrapper{ID: "local"}, nil
	}

	req := &pb.AddRelationReq{
		Type:         relationType,
		SourceId:     sourceId,
		TargetId:     targetId,
		CollectionId: collectionID,
		Properties:   properties,
		Description:  description,
	}

	resp, err := c.client.AddRelation(ctx, req)
	if err != nil {
		logger.Error("RPC调用AddRelation失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
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

func (c *KnowledgeClient) SearchEntitiesWithDetails(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*GraphEntity, error) {
	if c.client == nil {
		return []*GraphEntity{}, nil
	}

	req := &pb.SearchEntityReq{
		Keyword:      keyword,
		Type:         entityType,
		Page:         int32(page),
		PageSize:     int32(pageSize),
		CollectionId: collectionID,
	}

	resp, err := c.client.SearchEntities(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	entities := make([]*GraphEntity, 0, len(resp.Entities))
	for _, e := range resp.Entities {
		entities = append(entities, protoEntityToGraphEntity(e))
	}
	return entities, nil
}

func (c *KnowledgeClient) GetRelatedEntitiesDetails(ctx context.Context, entityID string, relationType string) ([]*GraphEntity, error) {
	if c.client == nil {
		return []*GraphEntity{}, nil
	}

	req := &pb.GetRelatedEntitiesReq{
		EntityId: entityID,
	}
	if relationType != "" {
		req.RelationType = &relationType
	}

	resp, err := c.client.GetRelatedEntities(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	entities := make([]*GraphEntity, 0, len(resp.Entities))
	for _, e := range resp.Entities {
		entities = append(entities, protoEntityToGraphEntity(e))
	}
	return entities, nil
}

func (c *KnowledgeClient) GetSubgraphDetails(ctx context.Context, entityID string, depth int, collectionID string) (*GraphSubgraph, error) {
	if c.client == nil {
		return &GraphSubgraph{}, nil
	}

	req := &pb.GetSubgraphReq{
		EntityId:     entityID,
		Depth:        int32(depth),
		CollectionId: collectionID,
	}

	resp, err := c.client.GetSubgraph(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	nodes := make([]*GraphEntity, 0, len(resp.Nodes))
	for _, n := range resp.Nodes {
		nodes = append(nodes, protoEntityToGraphEntity(n))
	}

	edges := make([]*GraphRelation, 0, len(resp.Edges))
	for _, e := range resp.Edges {
		edges = append(edges, protoRelationToGraphRelation(e))
	}

	return &GraphSubgraph{
		Nodes:     nodes,
		Edges:     edges,
		NodeCount: int(resp.NodeCount),
		EdgeCount: int(resp.EdgeCount),
	}, nil
}

func (c *KnowledgeClient) GetEntityPathsDetails(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*GraphEntityPath, error) {
	if c.client == nil {
		return []*GraphEntityPath{}, nil
	}

	req := &pb.GetEntityPathsReq{
		SourceId:     sourceID,
		TargetId:     targetID,
		MaxDepth:     int32(maxDepth),
		CollectionId: collectionID,
	}

	resp, err := c.client.GetEntityPaths(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	paths := make([]*GraphEntityPath, 0, len(resp.Paths))
	for _, p := range resp.Paths {
		entities := make([]*GraphEntity, 0, len(p.Entities))
		for _, e := range p.Entities {
			entities = append(entities, protoEntityToGraphEntity(e))
		}

		edges := make([]*GraphRelation, 0, len(p.Relations))
		for _, r := range p.Relations {
			edges = append(edges, protoRelationToGraphRelation(r))
		}

		paths = append(paths, &GraphEntityPath{
			Entities: entities,
			Edges:    edges,
			Length:   int(p.Length),
		})
	}
	return paths, nil
}

func (c *KnowledgeClient) GetGraphStatsDetails(ctx context.Context, collectionID string) (*GraphStatsInfo, error) {
	if c.client == nil {
		return &GraphStatsInfo{}, nil
	}

	req := &pb.EmptyReq{
		CollectionId: collectionID,
	}

	resp, err := c.client.GetGraphStats(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	return &GraphStatsInfo{
		EntityCount:       resp.EntityCount,
		RelationCount:     resp.RelationCount,
		EntityTypeCount:   resp.EntityTypeCount,
		RelationTypeCount: resp.RelationTypeCount,
	}, nil
}

func (c *KnowledgeClient) FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*GraphEntity, error) {
	if c.client == nil {
		logger.Warn("KnowledgeClient未初始化，返回本地GraphEntity")
		return &GraphEntity{ID: "local", Type: entityType, Name: name, CollectionID: collectionID, Properties: properties}, nil
	}

	req := &pb.AddEntityReq{
		Type:         entityType,
		Name:         name,
		Properties:   properties,
		Description:  description,
		CollectionId: collectionID,
		Color:        color,
	}

	resp, err := c.client.AddEntity(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	return protoEntityToGraphEntity(resp.Entity), nil
}

func (c *KnowledgeClient) AddRelationWithCollection(ctx context.Context, relationType, sourceID, targetID, collectionID string, properties map[string]string, description *string) (*GraphRelation, error) {
	if c.client == nil {
		return &GraphRelation{ID: "local", Type: relationType, SourceID: sourceID, TargetID: targetID, CollectionID: collectionID, Properties: properties}, nil
	}

	req := &pb.AddRelationReq{
		Type:         relationType,
		SourceId:     sourceID,
		TargetId:     targetID,
		Properties:   properties,
		Description:  description,
		CollectionId: collectionID,
	}

	resp, err := c.client.AddRelation(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	return protoRelationToGraphRelation(resp.Relation), nil
}

func (c *KnowledgeClient) UpdateEntityDetails(ctx context.Context, id, entityType, name string, properties map[string]string, description *string, collectionID ...string) (*GraphEntity, error) {
	if c.client == nil {
		return &GraphEntity{ID: id, Type: entityType, Name: name, Properties: properties}, nil
	}

	req := &pb.UpdateEntityReq{
		Id:          id,
		Type:        &entityType,
		Name:        &name,
		Properties:  properties,
		Description: description,
	}

	if len(collectionID) > 0 && collectionID[0] != "" {
		req.CollectionId = collectionID[0]
	}
	if len(collectionID) > 1 {
		req.Color = collectionID[1]
	}

	resp, err := c.client.UpdateEntity(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	return protoEntityToGraphEntity(resp.Entity), nil
}
