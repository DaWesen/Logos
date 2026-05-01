package client

import (
	"context"
	"fmt"

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
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
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
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
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

	resp, err := c.client.Vectorize(ctx, req)
	if err != nil {
		logger.Error("RPC调用Vectorize失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
		return nil, fmt.Errorf("%s", resp.BaseResp.StatusMessage)
	}

	vectorID := ""
	if resp.Vector != nil {
		vectorID = resp.Vector.Id
	}
	return &IDWrapper{ID: vectorID}, nil
}
