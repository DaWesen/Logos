package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"Logos/config"
)

func JWTAuth() gin.HandlerFunc {
	cfg := config.GetConfig()
	secret := cfg.JWT.Secret

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/v1/auth") ||
			strings.HasPrefix(path, "/health") ||
			strings.HasPrefix(path, "/api/v1/search/public") {
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

		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, map[string]interface{}{
				"code":    401,
				"message": "无效或过期的认证令牌",
			})
			return
		}

		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			userID, _ := claims["sub"].(string)
			username, _ := claims["username"].(string)
			c.Set("user_id", userID)
			c.Set("username", username)
		}

		c.Next()
	}
}
