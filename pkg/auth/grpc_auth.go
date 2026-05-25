package auth

import (
	"context"
	"strings"

	"Logos/pkg/logger"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	UserIDKey    = "user_id"
	RoleKey      = "role"
	AuthTokenKey = "authorization"
)

type UserClaims struct {
	UserID string
	Role   string
}

func GetUserFromContext(ctx context.Context) (*UserClaims, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		logger.Warn("从context获取metadata失败")
		return nil, status.Error(1001, "未提供认证信息")
	}

	userID := getFirstValue(md, UserIDKey)
	if userID == "" {
		logger.Warn("metadata中未找到user_id")
		return nil, status.Error(1001, "未提供认证信息")
	}

	return &UserClaims{
		UserID: userID,
		Role:   getFirstValue(md, RoleKey),
	}, nil
}

func GetUserID(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(1001, "未提供认证信息")
	}

	userID := getFirstValue(md, UserIDKey)
	if userID == "" {
		return "", status.Error(1001, "未提供认证信息")
	}

	return userID, nil
}

func MustGetUserID(ctx context.Context) string {
	userID, err := GetUserID(ctx)
	if err != nil {
		panic(err)
	}
	return userID
}

func getFirstValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

func AttachUserToContext(ctx context.Context, userID, role string) context.Context {
	return metadata.AppendToOutgoingContext(
		ctx,
		UserIDKey, userID,
		RoleKey, role,
	)
}

func ParseAuthToken(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(1001, "未提供认证信息")
	}

	tokens := md.Get(AuthTokenKey)
	if len(tokens) == 0 {
		return "", status.Error(1001, "未提供认证信息")
	}

	token := tokens[0]
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.TrimPrefix(token, "bearer ")

	if token == "" {
		return "", status.Error(1001, "无效的认证信息")
	}

	return token, nil
}
