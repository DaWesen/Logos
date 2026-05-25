package client

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"Logos/config"
	pb "Logos/proto_gen/bot"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type BotClient struct {
	client   pb.BotServiceClient
	conn     *grpc.ClientConn
	botCache map[string]string
	cacheMu  sync.RWMutex
}

func NewBotClient(client pb.BotServiceClient, conn *grpc.ClientConn) *BotClient {
	return &BotClient{
		client:   client,
		conn:     conn,
		botCache: make(map[string]string),
	}
}

func NewBotClientFromConfig(cfg *config.Config) (*BotClient, error) {
	conn, err := newConn(cfg, "logos.bot")
	if err != nil {
		return nil, err
	}
	client := pb.NewBotServiceClient(conn)
	return NewBotClient(client, conn), nil
}

func (c *BotClient) SendBotMessage(ctx context.Context, botID, content, userID, chatID string, stream bool, modelConfig map[string]string) (*BotMessageResult, error) {
	var header metadata.MD
	req := &pb.SendBotMessageRequest{
		BotId:   botID,
		Content: content,
		UserId:  userID,
		ChatId:  chatID,
		Stream:  stream,
	}
	if len(modelConfig) > 0 {
		req.Metadata = modelConfig
	}
	resp, err := c.client.SendBotMessage(ctx, req, grpc.Header(&header))
	if err != nil {
		return nil, err
	}

	result := &BotMessageResult{
		Content: resp.GetContent(),
		Done:    resp.GetDone(),
	}

	if costVals := header.Get("x-cost"); len(costVals) > 0 {
		if cost, parseErr := strconv.ParseFloat(costVals[0], 64); parseErr == nil {
			result.Cost = cost
		}
	}
	if tokenVals := header.Get("x-tokens"); len(tokenVals) > 0 {
		if tokens, parseErr := strconv.ParseInt(tokenVals[0], 10, 64); parseErr == nil {
			result.Tokens = int(tokens)
		}
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

func (c *BotClient) ResolveBotID(ctx context.Context, botName string) (string, error) {
	c.cacheMu.RLock()
	if id, ok := c.botCache[botName]; ok {
		c.cacheMu.RUnlock()
		return id, nil
	}
	c.cacheMu.RUnlock()

	resp, err := c.client.ListBots(ctx, &pb.ListBotsRequest{PageSize: 100})
	if err != nil {
		return "", err
	}

	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	for _, bot := range resp.GetBots() {
		c.botCache[bot.GetName()] = bot.GetId()
		if bot.GetName() == botName {
			return bot.GetId(), nil
		}
	}
	return "", fmt.Errorf("bot not found: %s", botName)
}

type BotMessageResult struct {
	Content string
	Done    bool
	Cost    float64
	Tokens  int
}

type BotInfo struct {
	ID          string
	Name        string
	Description string
}
