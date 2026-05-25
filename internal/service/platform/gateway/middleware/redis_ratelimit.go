package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/cache"
	"Logos/pkg/logger"
	"Logos/pkg/ratelimit"

	"github.com/gin-gonic/gin"
)

func RedisRateLimit(cache cache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cache == nil {
			c.Next()
			return
		}
		ip := c.ClientIP()
		path := c.Request.URL.Path
		method := c.Request.Method
		userID := getUserID(c)

		if !checkGlobalRateLimit(c, ip, path, method, userID, cache) {
			return
		}

		if !checkUserRateLimit(c, userID, path, cache) {
			return
		}

		c.Next()
	}
}

func checkGlobalRateLimit(c *gin.Context, ip, path, method string, _ string, cache cache.Cache) bool {
	globalKey := fmt.Sprintf("global:%s:%s", method, path)

	limiter := ratelimit.GetGlobalLimiter(
		"global_api",
		"sliding",
		cache,
		120,
		time.Minute,
	)

	allowed, err := limiter.Allow(context.Background(), globalKey)
	if err != nil {
		logger.Warn("global rate limit check failed",
			logger.ErrorField(err))
		return true
	}

	if !allowed {
		logger.Warn("API rate limit",
			logger.StringField("ip", ip),
			logger.StringField("path", path),
			logger.StringField("method", method))

		c.JSON(http.StatusTooManyRequests, model.Error(429, "operation"))
		c.Abort()
		return false
	}

	return true
}

func checkUserRateLimit(c *gin.Context, userID, path string, cache cache.Cache) bool {
	if userID == "" {
		return true
	}

	var rate int
	var window time.Duration

	switch {
	case contains(path, "/auth/login"), contains(path, "/auth/register"):
		rate = 5
		window = time.Minute

	case contains(path, "/question/ask"):
		rate = 30
		window = time.Minute

	case contains(path, "/extraction/"):
		rate = 20
		window = time.Minute

	case contains(path, "/collection/tasks/") && (c.Request.Method == "POST" || c.Request.Method == "PUT"):
		rate = 10
		window = time.Minute

	case contains(path, "/message/send"):
		rate = 50
		window = time.Minute

	case contains(path, "/recommend"):
		rate = 60
		window = time.Minute

	default:
		rate = 100
		window = time.Minute
	}

	userKey := fmt.Sprintf("user:%s:%s", userID, path)

	limiter := ratelimit.GetGlobalLimiter(
		fmt.Sprintf("user_%s", sanitizePath(path)),
		"fixed",
		cache,
		rate,
		window,
	)

	allowed, err := limiter.Allow(context.Background(), userKey)
	if err != nil {
		logger.Warn("operation",
			logger.StringField("user_id", userID),
			logger.ErrorField(err))
		return true
	}

	if !allowed {
		remaining, _ := limiter.GetRemaining(context.Background(), userKey)

		logger.Warn("operation",
			logger.StringField("user_id", userID),
			logger.StringField("path", path),
			logger.Int64Field("remaining", remaining))

		c.Header("X-RateLimit-Limited", "true")
		c.Header("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
		c.Header("Retry-After", strconv.FormatInt(int64(window.Seconds()), 10))

		c.JSON(http.StatusTooManyRequests, model.Error(429, "operation"))
		c.Abort()
		return false
	}

	remaining, _ := limiter.GetRemaining(context.Background(), userKey)
	c.Header("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))

	return true
}

func IPBasedRateLimit(cache cache.Cache, requests int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cache == nil {
			c.Next()
			return
		}
		ip := c.ClientIP()

		key := fmt.Sprintf("ip_limit:%s", ip)

		limiter := ratelimit.NewFixedWindowLimiter(cache, requests, window)

		allowed, err := limiter.Allow(context.Background(), key)
		if err != nil {
			logger.Warn("IP rate limit",
				logger.StringField("ip", ip),
				logger.ErrorField(err))
			c.Next()
			return
		}

		if !allowed {
			logger.Warn("IP rate limit",
				logger.StringField("ip", ip),
				logger.IntField("requests", requests))

			c.JSON(http.StatusTooManyRequests, model.Error(429, "operation"))
			c.Abort()
			return
		}

		c.Next()
	}
}

func ConcurrentRequestLimit(cache cache.Cache, maxConcurrent int, timeout time.Duration) gin.HandlerFunc {
	semaphore := make(chan struct{}, maxConcurrent)

	return func(c *gin.Context) {
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
			c.Next()
		default:
			logger.Warn("operation")

			c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
			c.Abort()
		}
	}
}

func BurstProtection(cache cache.Cache, threshold int, cooldown time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cache == nil {
			c.Next()
			return
		}
		ip := c.ClientIP()

		burstKey := fmt.Sprintf("burst:%s", ip)

		current, err := cache.IncrBy(context.Background(), burstKey, 1)
		if err != nil {
			c.Next()
			return
		}

		if current == 1 {
			cache.Expire(context.Background(), burstKey, cooldown)
		}

		if current > int64(threshold) {
			logger.Warn("operation",
				logger.StringField("ip", ip),
				logger.Int64Field("count", current))

			blockedKey := fmt.Sprintf("blocked:%s", ip)
			cache.Set(context.Background(), blockedKey, "1", cooldown*2)

			c.JSON(http.StatusForbidden, model.Error(403, "operation"))
			c.Abort()
			return
		}

		c.Next()
	}
}

func getUserID(c *gin.Context) string {
	userID, exists := c.Get("user_id")
	if !exists {
		return ""
	}

	switch v := userID.(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func sanitizePath(path string) string {
	result := make([]byte, 0, len(path))
	for _, c := range path {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			result = append(result, byte(c))
		} else if c == '/' {
			result = append(result, '_')
		}
	}
	return string(result)
}
