package service

import (
	"context"
	"time"

	"Logos/pkg/client"
)

type ChatClientAdapter struct {
	*client.ChatClient
}

func NewChatClientAdapter(c *client.ChatClient) *ChatClientAdapter {
	return &ChatClientAdapter{ChatClient: c}
}

func (a *ChatClientAdapter) GetMessageHistory(ctx context.Context, chatID string, limit int, beforeTime *time.Time) ([]*ChatMessage, error) {
	msgs, err := a.ChatClient.GetMessageHistory(ctx, chatID, limit, beforeTime)
	if err != nil {
		return nil, err
	}

	var result []*ChatMessage
	for _, m := range msgs {
		result = append(result, &ChatMessage{
			ID:        m.ID,
			ChatID:    m.ChatID,
			SenderID:  m.SenderID,
			Content:   m.Content,
			CreatedAt: m.CreatedAt,
		})
	}
	return result, nil
}
