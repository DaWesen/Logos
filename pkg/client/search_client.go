package client

import (
	"context"

	pb "Logos/proto_gen/search"
	"Logos/pkg/logger"

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
	if resp.BaseResp != nil && resp.BaseResp.StatusCode != 200 {
		return []string{}, nil
	}

	results := make([]string, 0, len(resp.Results))
	for _, item := range resp.Results {
		results = append(results, item.Content)
	}

	return results, nil
}
