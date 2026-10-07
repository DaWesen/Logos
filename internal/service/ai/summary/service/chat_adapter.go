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
	// 总结场景只关心最近一段历史，不需要增量补拉游标，afterSeq 固定传 0。
	msgs, err := a.ChatClient.GetMessageHistory(ctx, chatID, limit, beforeTime, 0)
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
