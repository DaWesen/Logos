package client

import (
	"context"

	"Logos/pkg/logger"
	pb "Logos/proto_gen/search"

	"google.golang.org/grpc"
)

type SearchClient struct {
	client pb.SearchServiceClient
	conn   *grpc.ClientConn
}

func NewSearchClient(client pb.SearchServiceClient, conn *grpc.ClientConn) *SearchClient {
	return &SearchClient{client: client, conn: conn}
}

func (c *SearchClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *SearchClient) Search(ctx context.Context, query string, docType string) ([]string, error) {
	if c.client == nil {
		logger.Warn("SearchClient未初始化，返回空结果")
		return []string{}, nil
	}

	req := &pb.SearchReq{
		Query: query,
	}

	resp, err := c.client.Search(ctx, req)
	if err != nil {
		logger.Error("RPC调用Search失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return []string{}, nil
	}

	results := make([]string, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, item.Content)
	}

	return results, nil
}

type SearchResultItem struct {
	ID      string
	Title   string
	Content string
	Score   float64
}

func (c *SearchClient) SearchWithResults(ctx context.Context, query string, topK int) ([]*SearchResultItem, error) {
	if c.client == nil {
		logger.Warn("SearchClient未初始化，返回空结果")
		return []*SearchResultItem{}, nil
	}

	req := &pb.SearchReq{
		Query:    query,
		PageSize: int32(topK),
	}

	resp, err := c.client.Search(ctx, req)
	if err != nil {
		logger.Error("RPC调用Search失败", logger.ErrorField(err))
		return nil, err
	}
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 0 {
		return []*SearchResultItem{}, nil
	}

	results := make([]*SearchResultItem, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, &SearchResultItem{
			ID:      item.Id,
			Title:   item.Title,
			Content: item.Content,
			Score:   item.Score,
		})
	}

	return results, nil
}
