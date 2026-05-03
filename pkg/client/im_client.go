package client

import (
	"context"

	"Logos/config"
	pbChat "Logos/proto_gen/chat"
	pb "Logos/proto_gen/im"

	"google.golang.org/grpc"
)

type IMClient struct {
	client pb.IMServiceClient
	conn   *grpc.ClientConn
}

func NewIMClient(client pb.IMServiceClient, conn *grpc.ClientConn) *IMClient {
	return &IMClient{client: client, conn: conn}
}

func NewIMClientFromConfig(cfg *config.Config) (*IMClient, error) {
	conn, err := newConn(cfg, "logos.im")
	if err != nil {
		return nil, err
	}
	client := pb.NewIMServiceClient(conn)
	return NewIMClient(client, conn), nil
}

func (c *IMClient) GetOnlineStatus(ctx context.Context, userIDs []string) (map[string]bool, error) {
	resp, err := c.client.GetOnlineStatus(ctx, &pb.GetOnlineStatusRequest{UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]bool)
	for uid, status := range resp.GetStatuses() {
		statuses[uid] = status == pb.OnlineStatus_ONLINE_STATUS_ONLINE
	}
	return statuses, nil
}

func (c *IMClient) BroadcastMessage(ctx context.Context, content string, messageType int32, metadata map[string]string) error {
	_, err := c.client.BroadcastMessage(ctx, &pb.BroadcastMessageRequest{
		Content:     content,
		MessageType: pbChat.MessageType(messageType),
		Metadata:    metadata,
	})
	return err
}

func (c *IMClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
