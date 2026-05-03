package client

import (
	"context"
	"time"

	"Logos/config"
	pb "Logos/proto_gen/chat"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ChatClient struct {
	client pb.ChatServiceClient
	conn   *grpc.ClientConn
}

func NewChatClient(client pb.ChatServiceClient, conn *grpc.ClientConn) *ChatClient {
	return &ChatClient{client: client, conn: conn}
}

func NewChatClientFromConfig(cfg *config.Config) (*ChatClient, error) {
	conn, err := newConn(cfg, "logos.chat")
	if err != nil {
		return nil, err
	}
	client := pb.NewChatServiceClient(conn)
	return NewChatClient(client, conn), nil
}

func (c *ChatClient) GetMessageHistory(ctx context.Context, chatID string, limit int, beforeTime *time.Time) ([]*ChatMessage, error) {
	req := &pb.GetMessageHistoryRequest{
		ChatId:   chatID,
		Limit:    int32(limit),
		ChatType: pb.ChatType_CHAT_TYPE_PRIVATE,
	}
	if beforeTime != nil {
		req.BeforeTime = timestamppb.New(*beforeTime)
	}

	resp, err := c.client.GetMessageHistory(ctx, req)
	if err != nil {
		return nil, err
	}

	var messages []*ChatMessage
	for _, m := range resp.GetMessages() {
		msg := &ChatMessage{
			ID:       m.GetId(),
			ChatID:   m.GetChatId(),
			SenderID: m.GetSenderId(),
			Content:  m.GetContent(),
		}
		if ts := m.GetCreatedAt(); ts != nil {
			msg.CreatedAt = ts.AsTime()
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func (c *ChatClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type ChatMessage struct {
	ID        string
	ChatID    string
	SenderID  string
	Content   string
	CreatedAt time.Time
}
