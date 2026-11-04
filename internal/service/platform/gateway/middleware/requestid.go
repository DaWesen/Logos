package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxRequestIDLength = 64

// RequestID 为每个请求生成/透传请求 ID：
//   - 客户端携带 X-Request-ID 则透传（支持上游链路串联）；
//   - 否则生成 UUID；
//   - 响应头回写 X-Request-ID，便于客户端排障时报障定位；
//   - 写入 gin context，供 Logger 等中间件取用。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := sanitizeRequestID(c.GetHeader("X-Request-ID"))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		c.Set("request_id", requestID)
		c.Writer.Header().Set("X-Request-ID", requestID)
		c.Next()
	}
}

// sanitizeRequestID 只接受长度受限的 ASCII 标识符。
//
// 完全信任客户端头会有两个问题：攻击者可以把任意长内容写进日志/响应头，
// 也可以伪造别人的 trace id 把链路串错。非法值直接丢弃并生成新 UUID，
// 不做「截断后继续用」——截断会产生碰撞。
func sanitizeRequestID(id string) string {
	if id == "" || len(id) > maxRequestIDLength {
		return ""
	}
	for i := 0; i < len(id); i++ {
		ch := id[i]
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '-', ch == '_', ch == '.':
		default:
			return ""
		}
	}
	return id
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
