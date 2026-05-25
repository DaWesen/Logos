package middleware

import (
	"time"

	"Logos/pkg/logger"

	"github.com/gin-gonic/gin"
)

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		method := c.Request.Method

		logger.Info("Request started",
			logger.StringField("method", method),
			logger.StringField("path", path),
			logger.StringField("query", query),
		)

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()

		logger.Info("Request completed",
			logger.IntField("status_code", statusCode),
			logger.StringField("latency", latency.String()),
			logger.StringField("method", method),
			logger.StringField("path", path),
		)
	}
}
