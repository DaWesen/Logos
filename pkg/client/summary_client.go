package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/summary"

	"google.golang.org/grpc"
)

type SummaryClient struct {
	client pb.SummaryServiceClient
	conn   *grpc.ClientConn
}

func NewSummaryClient(client pb.SummaryServiceClient, conn *grpc.ClientConn) *SummaryClient {
	return &SummaryClient{client: client, conn: conn}
}

func NewSummaryClientFromConfig(cfg *config.Config) (*SummaryClient, error) {
	conn, err := newConn(cfg, "logos.summary")
	if err != nil {
		return nil, err
	}
	client := pb.NewSummaryServiceClient(conn)
	return NewSummaryClient(client, conn), nil
}

func (c *SummaryClient) SummarizeMessages(ctx context.Context, chatID string, messageIDs []string) (string, error) {
	resp, err := c.client.SummarizeMessages(ctx, &pb.SummarizeMessagesRequest{
		ChatId:     chatID,
		MessageIds: messageIDs,
	})
	if err != nil {
		return "", err
	}
	return resp.GetSummary(), nil
}

func (c *SummaryClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
