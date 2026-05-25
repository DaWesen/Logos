package client

import (
	"context"

	"Logos/config"
	pb "Logos/proto_gen/user"

	"google.golang.org/grpc"
)

type UserClient struct {
	client pb.UserServiceClient
	conn   *grpc.ClientConn
}

func NewUserClient(client pb.UserServiceClient, conn *grpc.ClientConn) *UserClient {
	return &UserClient{client: client, conn: conn}
}

func NewUserClientFromConfig(cfg *config.Config) (*UserClient, error) {
	conn, err := tryDialWithFallback(cfg, "logos.user", cfg.Ports.User)
	if err != nil {
		return nil, err
	}
	client := pb.NewUserServiceClient(conn)
	return NewUserClient(client, conn), nil
}

func (c *UserClient) GetUserInfo(ctx context.Context, userID int64) (*UserInfo, error) {
	resp, err := c.client.GetUserInfo(ctx, &pb.UserInfoReq{UserId: userID})
	if err != nil {
		return nil, err
	}
	info := &UserInfo{}
	if resp.User != nil {
		info.UserID = resp.User.Id
		info.Username = resp.User.GetUsername()
		info.Avatar = resp.User.GetAvatar()
		if resp.User.Email != nil {
			info.Email = *resp.User.Email
		}
	}
	return info, nil
}

func (c *UserClient) VerifyToken(ctx context.Context, token string) (bool, error) {
	resp, err := c.client.VerifyToken(ctx, &pb.VerifyTokenReq{Token: token})
	if err != nil {
		return false, err
	}
	return resp.GetValid(), nil
}

func (c *UserClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type UserInfo struct {
	UserID   int64
	Username string
	Email    string
	Avatar   string
}
