package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	rateLimitMap sync.Map
)

func RateLimit(requestsPerMinute int) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		var count int
		var lastAccess time.Time

		if val, ok := rateLimitMap.Load(clientIP); ok {
			entry := val.([2]interface{})
			count = entry[0].(int)
			lastAccess = entry[1].(time.Time)
		}

		now := time.Now()

		if now.Sub(lastAccess) >= time.Minute {
			count = 1
			lastAccess = now
		} else {
			count++
		}

		rateLimitMap.Store(clientIP, [2]interface{}{count, lastAccess})

		if count > requestsPerMinute {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, map[string]interface{}{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			return
		}

		c.Next()
	}
}
