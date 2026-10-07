package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestID 为每个请求生成/透传请求 ID：
//   - 客户端携带 X-Request-ID 则透传（支持上游链路串联）；
//   - 否则生成 UUID；
//   - 响应头回写 X-Request-ID，便于客户端排障时报障定位；
//   - 写入 gin context，供 Logger 等中间件取用。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Set("request_id", requestID)
		c.Writer.Header().Set("X-Request-ID", requestID)
		c.Next()
	}
}

// GetRequestID 从 gin context 读取请求 ID（未设置时返回空串）
func GetRequestID(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
