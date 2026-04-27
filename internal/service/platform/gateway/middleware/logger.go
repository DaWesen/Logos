package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()

		log.Printf("[GIN] %d | %13v | %15s | %-7s %s%s",
			statusCode,
			latency,
			c.ClientIP(),
			c.Request.Method,
			path,
			query,
		)
	}
}
