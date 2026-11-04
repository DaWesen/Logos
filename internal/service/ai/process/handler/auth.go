package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"

	"Logos/pkg/auth"
	"Logos/pkg/jwt"
)

// tokenParser 只依赖「解析令牌」这一个能力，便于单测注入假实现，
// 也避免中间件直接绑定具体的 JWT 实现。
type tokenParser interface {
	ParseToken(string) (*jwt.Claims, error)
}

// requireAuth 在 process 服务入口独立校验 JWT。
//
// 网关已经校验过一次，但 process 服务在 compose 里曾经直接发布端口，
// 只信任 X-User-ID 等于把身份交给客户端：任何人手写一个头就能读别人的文档。
// 这里直接用同一套 JWT 密钥验签，校验通过后再写入 metadata，
// 下游 service 仍按原有方式读取 user_id。
//
// 撤销校验仍由网关负责；process 只作为内网服务，不应直接暴露给公网。
func requireAuth(parser tokenParser) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		if token == "" {
			abortUnauthorized(c, "缺少认证令牌")
			return
		}

		claims, err := parser.ParseToken(token)
		if err != nil {
			abortUnauthorized(c, "无效或过期的认证令牌")
			return
		}

		md := metadata.Pairs(
			auth.UserIDKey, claims.UserID,
			auth.RoleKey, claims.Role,
		)
		ctx := metadata.NewIncomingContext(c.Request.Context(), md)
		c.Request = c.Request.WithContext(ctx)
		c.Set("user_id", claims.UserID)
		c.Set("role", claims.Role)

		c.Next()
	}
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func abortUnauthorized(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"code":    http.StatusUnauthorized,
		"message": message,
	})
}
