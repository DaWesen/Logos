package client

import (
	"context"
	"fmt"

	"Logos/config"
	pb "Logos/proto_gen/moderation"

	"google.golang.org/grpc"
)

type ModerationClient struct {
	client pb.ModerationServiceClient
	conn   *grpc.ClientConn
}

func NewModerationClient(client pb.ModerationServiceClient, conn *grpc.ClientConn) *ModerationClient {
	return &ModerationClient{client: client, conn: conn}
}

func NewModerationClientFromConfig(cfg *config.Config) (*ModerationClient, error) {
	conn, err := newConn(cfg, "logos.moderation")
	if err != nil {
		return nil, err
	}
	client := pb.NewModerationServiceClient(conn)
	return NewModerationClient(client, conn), nil
}

func (c *ModerationClient) ModerateContent(ctx context.Context, content, contentID, contentType string) (*ModerationResult, error) {
	resp, err := c.client.ModerateContent(ctx, &pb.ModerateContentRequest{
		Content:     content,
		ContentId:   contentID,
		ContentType: contentType,
	})
	if err != nil {
		return nil, err
	}
	// Code != 0 表示审核侧未能给出判定（模型不可用/输出无法解析），
	// 必须向上抛错而不是当作“通过”，否则审核防线会被静默绕过。
	if resp.GetCode() != 0 {
		return nil, fmt.Errorf("内容审核失败(code=%d): %s", resp.GetCode(), resp.GetMessage())
	}

	result := &ModerationResult{
		Code:    resp.GetCode(),
		Message: resp.GetMessage(),
	}
	if resp.Data != nil {
		result.ActionTaken = resp.Data.ActionTaken
		result.Result = int32(resp.Data.Result)
	}
	return result, nil
}

func (c *ModerationClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *ModerationClient) Translate(ctx context.Context, content, sourceLang, targetLang, contentID string) (string, error) {
	resp, err := c.client.Translate(ctx, &pb.TranslateRequest{
		Content:    content,
		SourceLang: sourceLang,
		TargetLang: targetLang,
		ContentId:  contentID,
	})
	if err != nil {
		return "", err
	}
	return resp.GetTranslatedContent(), nil
}

type ModerationResult struct {
	Code        int32
	Message     string
	Result      int32
	ActionTaken string
}
