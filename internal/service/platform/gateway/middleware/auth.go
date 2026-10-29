package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"Logos/pkg/auth"
	pkgJwt "Logos/pkg/jwt"
)

func Auth(revoker *auth.Revoker) gin.HandlerFunc {
	jwtManager := pkgJwt.NewJWTManager()

	return func(c *gin.Context) {
		if isPublicPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		// 令牌只认 Authorization 头。URL 里的令牌会进 access log、Referer 与浏览器历史，
		// 一旦泄漏可被直接复用；WebSocket 握手改走 Sec-WebSocket-Protocol 子协议。
		tokenString := c.GetHeader("Authorization")
		tokenString = strings.TrimPrefix(tokenString, "Bearer ")

		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, map[string]interface{}{
				"code":    401,
				"message": "缺少认证令牌",
			})
			return
		}

		claims, err := jwtManager.ParseToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, map[string]interface{}{
				"code":    401,
				"message": "无效或过期的认证令牌",
			})
			return
		}

		// 撤销校验：登出/改密后的旧令牌仍能通过签名与过期校验，必须再查一次撤销状态
		if revoker.IsRevoked(c.Request.Context(), claims) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, map[string]interface{}{
				"code":    401,
				"message": "令牌已失效，请重新登录",
			})
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("role", claims.Role)

		ctx := auth.AttachUserToContext(c.Request.Context(), claims.UserID, claims.Role)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// publicExactPaths 免认证路径按精确匹配，不能用前缀：
// 原先用 strings.HasPrefix(path, "/api/v1/auth") 会把 /api/v1/authXxx 也放行，
// 新增的 /api/v1/auth/logout 这类需要认证的接口一旦挂上就被白名单吞掉。
var publicExactPaths = map[string]struct{}{
	"/health":                     {},
	"/metrics":                    {},
	"/test":                       {},
	"/ws":                         {},
	"/api/v1/auth/login":          {},
	"/api/v1/auth/register":       {},
	"/api/v1/auth/check-username": {},
}

// publicPrefixes 仅用于确实按前缀开放的资源（如公开搜索）
var publicPrefixes = []string{
	"/api/v1/search/public",
}

func isPublicPath(path string) bool {
	if _, ok := publicExactPaths[path]; ok {
		return true
	}
	for _, prefix := range publicPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
