package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/bot"

	"google.golang.org/grpc"
)

type BotClient struct {
	client pb.BotServiceClient
	conn   *grpc.ClientConn
}

func NewBotClient(client pb.BotServiceClient, conn *grpc.ClientConn) *BotClient {
	return &BotClient{client: client, conn: conn}
}

func NewBotClientFromConfig(cfg *config.Config) (*BotClient, error) {
	conn, err := newConn(cfg, "logos.bot")
	if err != nil {
		return nil, err
	}
	client := pb.NewBotServiceClient(conn)
	return NewBotClient(client, conn), nil
}

func (c *BotClient) SendBotMessage(ctx context.Context, botID, content, userID, chatID string, stream bool) (*BotMessageResult, error) {
	resp, err := c.client.SendBotMessage(ctx, &pb.SendBotMessageRequest{
		BotId:   botID,
		Content: content,
		UserId:  userID,
		ChatId:  chatID,
		Stream:  stream,
	})
	if err != nil {
		return nil, err
	}

	result := &BotMessageResult{
		Content: resp.GetContent(),
		Done:    resp.GetDone(),
	}
	return result, nil
}

func (c *BotClient) ListBots(ctx context.Context, page, pageSize int32) ([]*BotInfo, error) {
	resp, err := c.client.ListBots(ctx, &pb.ListBotsRequest{
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		return nil, err
	}

	var bots []*BotInfo
	for _, b := range resp.GetBots() {
		bots = append(bots, &BotInfo{
			ID:          b.GetId(),
			Name:        b.GetName(),
			Description: b.GetDescription(),
		})
	}
	return bots, nil
}

func (c *BotClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type BotMessageResult struct {
	Content string
	Done    bool
}

type BotInfo struct {
	ID          string
	Name        string
	Description string
}
