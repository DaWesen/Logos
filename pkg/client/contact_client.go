package client

import (
	"context"
	"fmt"

	"Logos/config"
	pb "Logos/proto_gen/contact"

	"google.golang.org/grpc"
)

type ContactClient struct {
	client pb.ContactServiceClient
	conn   *grpc.ClientConn
}

func NewContactClient(client pb.ContactServiceClient, conn *grpc.ClientConn) *ContactClient {
	return &ContactClient{client: client, conn: conn}
}

func NewContactClientFromConfig(cfg *config.Config) (*ContactClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.contact", cfg.Ports.Contact)
	if err != nil {
		return nil, err
	}
	client := pb.NewContactServiceClient(conn)
	return NewContactClient(client, conn), nil
}

func (c *ContactClient) AddFriend(ctx context.Context, userID, remark, message string) error {
	_, err := c.client.AddFriend(ctx, &pb.AddFriendRequest{
		UserId:  userID,
		Remark:  remark,
		Message: message,
	})
	return err
}

func (c *ContactClient) GetFriendList(ctx context.Context, groupID string, page, pageSize int32) ([]*FriendInfo, error) {
	resp, err := c.client.GetFriendList(ctx, &pb.GetFriendListRequest{
		GroupId:  groupID,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		return nil, err
	}
	friends := make([]*FriendInfo, 0, len(resp.GetFriends()))
	for _, f := range resp.GetFriends() {
		friends = append(friends, &FriendInfo{
			UserID: f.GetFriendId(),
			Remark: f.GetRemark(),
		})
	}
	return friends, nil
}

func (c *ContactClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type FriendInfo struct {
	UserID string
	Remark string
}

type FriendshipStatus struct {
	IsFriend  bool
	IsBlocked bool
}

func (c *ContactClient) IsFriend(ctx context.Context, userID, friendID string) (*FriendshipStatus, error) {
	resp, err := c.client.CheckFriendship(ctx, &pb.CheckFriendshipRequest{
		UserId:   userID,
		FriendId: friendID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, fmt.Errorf("check friendship failed: %s", resp.Message)
	}
	return &FriendshipStatus{
		IsFriend:  resp.IsFriend,
		IsBlocked: resp.IsBlocked,
	}, nil
}
