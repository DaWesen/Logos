package middleware

import (
	"fmt"
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
		requestID := GetRequestID(c)

		logger.Info("Request started",
			logger.StringField("request_id", requestID),
			logger.StringField("method", method),
			logger.StringField("path", path),
			logger.StringField("query", query),
		)

		c.Next()

		latency := time.Since(start)
		statusCode := c.Writer.Status()

		logger.Info("Request completed",
			logger.StringField("request_id", requestID),
			logger.IntField("status_code", statusCode),
			logger.StringField("latency", latency.String()),
			logger.StringField("method", method),
			logger.StringField("path", path),
		)

		// 异步上报日志到 monitoring 服务。
		//
		// 这里不再为每个请求起一个 goroutine：请求量由外部决定，监控服务一慢，
		// 每个请求都会留下一个阻塞在 RPC 上的 goroutine，最终把网关自己拖垮。
		// 改为投递到有界队列，队列满时丢日志并计数，主链路永不等待观测系统。
		level := "INFO"
		if statusCode >= 500 {
			level = "ERROR"
		} else if statusCode >= 400 {
			level = "WARN"
		}
		getLogDispatcher().enqueue(logRecord{
			serviceName: "logos.gateway",
			level:       level,
			message:     fmt.Sprintf("%s %s %d %s", method, path, statusCode, latency),
			fields: map[string]string{
				"request_id":  requestID,
				"method":      method,
				"path":        path,
				"query":       query,
				"status_code": fmt.Sprintf("%d", statusCode),
				"latency":     latency.String(),
				"client_ip":   c.ClientIP(),
			},
		})
	}
}
