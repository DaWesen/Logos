package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"Logos/pkg/auth"
	pkgJwt "Logos/pkg/jwt"
)

func Auth() gin.HandlerFunc {
	jwtManager := pkgJwt.NewJWTManager()

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/v1/auth") ||
			strings.HasPrefix(path, "/health") ||
			strings.HasPrefix(path, "/api/v1/search/public") ||
			strings.HasPrefix(path, "/ws") {
			c.Next()
			return
		}

		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			tokenString = c.Query("token")
		}
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

		c.Set("user_id", claims.UserID)
		c.Set("role", claims.Role)

		ctx := auth.AttachUserToContext(c.Request.Context(), claims.UserID, claims.Role)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
