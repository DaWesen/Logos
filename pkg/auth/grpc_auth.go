package auth

import (
	"context"
	"strings"

	"Logos/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	UserIDKey    = "user_id"
	UsernameKey  = "username"
	RoleKey      = "role"
	AuthTokenKey = "authorization"
)

// UserClaims 用户声明
type UserClaims struct {
	UserID   string
	Username string
	Role     string
}

// GetUserFromContext 从 gRPC context 获取用户信息
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
		UserID:   userID,
		Username: getFirstValue(md, UsernameKey),
		Role:     getFirstValue(md, RoleKey),
	}, nil
}

// MustGetUserFromContext 必须获取用户信息，失败则 panic
func MustGetUserFromContext(ctx context.Context) *UserClaims {
	claims, err := GetUserFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return claims
}

// GetUserID 从 context 中只获取用户 ID
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

// MustGetUserID 必须获取用户 ID
func MustGetUserID(ctx context.Context) string {
	userID, err := GetUserID(ctx)
	if err != nil {
		panic(err)
	}
	return userID
}

// getFirstValue 获取 metadata 中第一个值
func getFirstValue(md metadata.MD, key string) string {
	values := md.Get(key)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

// UnaryInterceptor gRPC 一元拦截器（用于验证）
func UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		logger.Debug("收到 gRPC 请求",
			logger.StringField("method", info.FullMethod))

		// 这里可以添加 token 验证逻辑
		// 现在我们假设 metadata 已经在调用时被设置
		return handler(ctx, req)
	}
}

// AttachUserToContext 将用户信息附加到 context metadata 中（客户端调用时使用）
func AttachUserToContext(ctx context.Context, userID, username, role string) context.Context {
	return metadata.AppendToOutgoingContext(
		ctx,
		UserIDKey, userID,
		UsernameKey, username,
		RoleKey, role,
	)
}

// ParseAuthToken 从 metadata 中解析 bearer token（可选）
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

	// 去除 Bearer 前缀
	if idx := strings.Index(strings.ToLower(token), "bearer "); idx == 0 {
		if endIdx := strings.Index(token, " "); endIdx != -1 {
			token = token[endIdx+1:]
		}
		if parts := strings.SplitN(token, " ", 2); len(parts) == 2 {
			token = parts[1]
		}
	}

	if token == "" {
		return "", status.Error(1001, "无效的认证信息")
	}

	return token, nil
}
