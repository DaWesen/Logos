package client

import (
	"context"
	"fmt"
	"time"

	"Logos/pkg/logger"
	pb "Logos/proto_gen/vector"

	"google.golang.org/grpc"
)

type VectorClient struct {
	client pb.VectorServiceClient
	conn   *grpc.ClientConn
}

func NewVectorClient(client pb.VectorServiceClient, conn *grpc.ClientConn) *VectorClient {
	return &VectorClient{client: client, conn: conn}
}

func (c *VectorClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type IDWrapper struct {
	ID string
}

func (i *IDWrapper) GetID() string { return i.ID }

func (c *VectorClient) SearchSimilar(ctx context.Context, text string, topK int) ([]string, error) {
	if c.client == nil {
		logger.Warn("VectorClient未初始化，返回空结果")
		return []string{}, nil
	}

	req := &pb.TextSearchReq{
		Text: text,
	}
	if topK > 0 {
		req.TopK = int32(topK)
	}

	resp, err := c.client.TextSearch(ctx, req)
	if err != nil {
		logger.Error("RPC调用TextSearch失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return []string{}, nil
	}

	results := make([]string, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, item.VectorId)
	}

	return results, nil
}

func (c *VectorClient) TextSearch(ctx context.Context, collectionID string, text string, topK int) ([]string, error) {
	if c.client == nil {
		logger.Warn("VectorClient未初始化，返回空结果")
		return []string{}, nil
	}

	req := &pb.TextSearchReq{
		Text:         text,
		CollectionId: collectionID,
	}
	if topK > 0 {
		req.TopK = int32(topK)
	}

	resp, err := c.client.TextSearch(ctx, req)
	if err != nil {
		logger.Error("RPC调用TextSearch失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return []string{}, nil
	}

	results := make([]string, 0, len(resp.Results))
	for _, item := range resp.Results {
		if content, ok := item.Metadata["content"]; ok {
			results = append(results, content)
		} else {
			results = append(results, item.VectorId)
		}
	}

	return results, nil
}

type VectorSearchResult struct {
	ID       string
	Content  string
	Score    float64
	Metadata map[string]string
}

func (c *VectorClient) SearchWithScores(ctx context.Context, collectionID string, text string, topK int) ([]*VectorSearchResult, error) {
	if c.client == nil {
		logger.Warn("VectorClient未初始化，返回空结果")
		return []*VectorSearchResult{}, nil
	}

	req := &pb.TextSearchReq{
		Text:         text,
		CollectionId: collectionID,
	}
	if topK > 0 {
		req.TopK = int32(topK)
	}

	resp, err := c.client.TextSearch(ctx, req)
	if err != nil {
		logger.Error("RPC调用TextSearch失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return []*VectorSearchResult{}, nil
	}

	results := make([]*VectorSearchResult, 0, len(resp.Results))
	for _, item := range resp.Results {
		metadata := make(map[string]string)
		for k, v := range item.Metadata {
			metadata[k] = v
		}
		content := ""
		if c, ok := metadata["content"]; ok {
			content = c
		}
		results = append(results, &VectorSearchResult{
			ID:       item.VectorId,
			Content:  content,
			Score:    float64(item.Score),
			Metadata: metadata,
		})
	}

	return results, nil
}

type VectorCollectionInfo struct {
	ID         string
	Name       string
	Size       int64
	VLMModel   string
	VLMBaseURL string
	VLMApiKey  string
	LLMModel   string
	LLMBaseURL string
	LLMApiKey  string
}

func (c *VectorClient) ListCollections(ctx context.Context) ([]*VectorCollectionInfo, error) {
	if c.client == nil {
		logger.Warn("VectorClient未初始化，返回空列表")
		return []*VectorCollectionInfo{}, nil
	}

	resp, err := c.client.ListCollections(ctx, &pb.EmptyReq{})
	if err != nil {
		logger.Error("RPC调用ListCollections失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return []*VectorCollectionInfo{}, nil
	}

	collections := make([]*VectorCollectionInfo, 0, len(resp.Collections))
	for _, coll := range resp.Collections {
		collections = append(collections, &VectorCollectionInfo{
			ID:   coll.Id,
			Name: coll.Name,
			Size: coll.Size,
		})
	}

	return collections, nil
}

func (c *VectorClient) GetCollection(ctx context.Context, id string) (*VectorCollectionInfo, error) {
	if c.client == nil {
		logger.Warn("VectorClient未初始化，返回空")
		return nil, fmt.Errorf("vector client not initialized")
	}

	resp, err := c.client.GetCollection(ctx, &pb.GetByIdReq{Id: id})
	if err != nil {
		logger.Error("RPC调用GetCollection失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("GetCollection失败: %s", resp.BaseResp.StatusMessage)
	}
	if resp.Collection == nil {
		return nil, fmt.Errorf("集合 %s 不存在", id)
	}

	return &VectorCollectionInfo{
		ID:         resp.Collection.Id,
		Name:       resp.Collection.Name,
		Size:       resp.Collection.Size,
		VLMModel:   resp.Collection.VlmModel,
		VLMBaseURL: resp.Collection.VlmBaseUrl,
		VLMApiKey:  resp.Collection.VlmApiKey,
		LLMModel:   resp.Collection.LlmModel,
		LLMBaseURL: resp.Collection.LlmBaseUrl,
		LLMApiKey:  resp.Collection.LlmApiKey,
	}, nil
}

func (c *VectorClient) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (interface{ GetID() string }, error) {
	if c.client == nil {
		logger.Warn("VectorClient未初始化，跳过向量化")
		return &IDWrapper{ID: "local"}, nil
	}

	req := &pb.VectorizeReq{
		Text:         text,
		CollectionId: collectionID,
		Metadata:     metadata,
	}

	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	resp, err := c.client.Vectorize(callCtx, req)
	if err != nil {
		logger.Error("RPC调用Vectorize失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	vectorID := ""
	if resp.Vector != nil {
		vectorID = resp.Vector.Id
	}
	return &IDWrapper{ID: vectorID}, nil
}

func (c *VectorClient) UpdateCollection(ctx context.Context, collectionID, name string, parameters map[string]string) error {
	if c.client == nil {
		logger.Warn("VectorClient未初始化，跳过更新集合")
		return nil
	}

	req := &pb.UpdateCollectionReq{
		Id:         collectionID,
		Parameters: parameters,
	}
	if name != "" {
		req.Name = &name
	}

	resp, err := c.client.UpdateCollection(ctx, req)
	if err != nil {
		logger.Error("RPC调用UpdateCollection失败", logger.ErrorField(err))
		return err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return fmt.Errorf("UpdateCollection失败: %s", resp.BaseResp.StatusMessage)
	}

	return nil
}
