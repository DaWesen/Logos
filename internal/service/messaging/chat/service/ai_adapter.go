package service

import (
	"context"

	"Logos/pkg/client"
)

type BotClientAdapter struct {
	*client.BotClient
}

func NewBotClientAdapter(c *client.BotClient) *BotClientAdapter {
	return &BotClientAdapter{BotClient: c}
}

func (a *BotClientAdapter) SendBotMessage(ctx context.Context, botID, content, userID, chatID string) (string, error) {
	result, err := a.BotClient.SendBotMessage(ctx, botID, content, userID, chatID, false)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

type ModerationClientAdapter struct {
	*client.ModerationClient
}

func NewModerationClientAdapter(c *client.ModerationClient) *ModerationClientAdapter {
	return &ModerationClientAdapter{ModerationClient: c}
}

func (a *ModerationClientAdapter) ModerateContent(ctx context.Context, content, contentID, contentType string) (bool, error) {
	result, err := a.ModerationClient.ModerateContent(ctx, content, contentID, contentType)
	if err != nil {
		return false, err
	}

	const moderationResultRejected = 3
	return result.Result == moderationResultRejected, nil
}
