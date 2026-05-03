package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/recommend"

	"google.golang.org/grpc"
)

type RecommendClient struct {
	client pb.RecommendationServiceClient
	conn   *grpc.ClientConn
}

func NewRecommendClient(client pb.RecommendationServiceClient, conn *grpc.ClientConn) *RecommendClient {
	return &RecommendClient{client: client, conn: conn}
}

func NewRecommendClientFromConfig(cfg *config.Config) (*RecommendClient, error) {
	conn, err := newConn(cfg, "logos.recommend")
	if err != nil {
		return nil, err
	}
	client := pb.NewRecommendationServiceClient(conn)
	return NewRecommendClient(client, conn), nil
}

func (c *RecommendClient) GetRecommendations(ctx context.Context, userID int64, limit int32) ([]*RecommendItem, error) {
	resp, err := c.client.GetRecommendations(ctx, &pb.RecommendationReq{
		UserId: userID,
		Limit:  &limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]*RecommendItem, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		items = append(items, &RecommendItem{
			ID:          item.GetId(),
			Title:       item.GetTitle(),
			Description: item.GetDescription(),
			Score:       item.GetScore(),
		})
	}
	return items, nil
}

func (c *RecommendClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type RecommendItem struct {
	ID          string
	Title       string
	Description string
	Score       float64
}
