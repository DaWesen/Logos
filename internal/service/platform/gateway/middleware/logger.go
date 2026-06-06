package middleware

import (
	"context"
	"fmt"
	"time"

	pb "Logos/proto_gen/monitoring"

	"Logos/pkg/logger"

	"github.com/gin-gonic/gin"
)

// MonitoringClient 用于日志上报，由 SetupRouter 注入
var MonitoringClient pb.MonitoringServiceClient

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

		// 异步上报日志到 monitoring 服务
		if MonitoringClient != nil {
			go func() {
				level := "INFO"
				if statusCode >= 500 {
					level = "ERROR"
				} else if statusCode >= 400 {
					level = "WARN"
				}
				msg := fmt.Sprintf("%s %s %d %s", method, path, statusCode, latency)
				fields := map[string]string{
					"method":      method,
					"path":        path,
					"query":       query,
					"status_code": fmt.Sprintf("%d", statusCode),
					"latency":     latency.String(),
					"client_ip":   c.ClientIP(),
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_, _ = MonitoringClient.RecordLog(ctx, &pb.RecordLogReq{
					ServiceName: "logos.gateway",
					Level:       level,
					Message:     msg,
					Fields:      fields,
				})
			}()
		}
	}
}
