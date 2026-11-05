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
	"Logos/pkg/obs"
	"Logos/pkg/ratelimit"

	"github.com/gin-gonic/gin"
)

// localFallback 是 Redis 不可用时的单机兜底。
// 全局一份即可：它的状态本来就只代表本进程，不需要按接口再拆。
var localFallback = ratelimit.NewLocalLimiter()

// 限流阈值：各层职责不同，阈值必须分层设置。
// 若所有层使用同一个巨大阈值，实际生效的只有最宽松的那一层，其余形同虚设。
const (
	// globalRatePerMinute 单个接口（method + path）的全站上限，避免任一接口被整体打爆
	globalRatePerMinute = 600
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
		globalRatePerMinute,
		time.Minute,
	)

	allowed, err := limiter.Allow(c.Request.Context(), globalKey)
	if err != nil {
		obs.RecordRateLimitDegraded(c.Request.Context(), "global")
		logger.Warn("global rate limit check failed",
			logger.ErrorField(err))
		// Redis 不可用时不能直接放行：退化为单机固定窗口，
		// 仍按原来的全站配额拦截，避免依赖故障变成无限流状态。
		allowed = localFallback.Allow(globalKey, globalRatePerMinute, time.Minute)
		if allowed {
			return true
		}
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

// 按接口危险程度分档：越贵/越敏感的操作阈值越低。
// 这里的分档必须与全局、IP、突发三层错开，否则低档位永远不会先触发。
func userRateLimit(path, method string) (int, time.Duration) {
	switch {
	case contains(path, "/auth/login"), contains(path, "/auth/register"):
		return 5, time.Minute // 防撞库

	case contains(path, "/question/ask"):
		return 30, time.Minute // 触发 LLM 调用

	case contains(path, "/extraction/"):
		return 60, time.Minute // 文档解析/抽取，重计算

	case contains(path, "/collection/tasks/") && (method == "POST" || method == "PUT"):
		return 30, time.Minute // 创建/重跑后台任务

	case contains(path, "/message/send"):
		return 50, time.Minute // 写扩散到多个接收者

	case contains(path, "/recommend"):
		return 120, time.Minute // 推荐查询

	default:
		return 600, time.Minute // 常规读接口
	}
}

func checkUserRateLimit(c *gin.Context, userID, path string, cache cache.Cache) bool {
	if userID == "" {
		return true
	}

	rate, window := userRateLimit(path, c.Request.Method)

	userKey := fmt.Sprintf("user:%s:%s", userID, path)

	limiter := ratelimit.GetGlobalLimiter(
		fmt.Sprintf("user_%s", sanitizePath(path)),
		"fixed",
		cache,
		rate,
		window,
	)

	allowed, err := limiter.Allow(c.Request.Context(), userKey)
	if err != nil {
		obs.RecordRateLimitDegraded(c.Request.Context(), "user")
		logger.Warn("operation",
			logger.StringField("user_id", userID),
			logger.ErrorField(err))
		if localFallback.Allow(userKey, rate, window) {
			return true
		}
		// 本地兜底也超限：沿用 429 语义，让客户端退避。
		c.Header("Retry-After", strconv.FormatInt(int64(window.Seconds()), 10))
		c.JSON(http.StatusTooManyRequests, model.Error(429, "operation"))
		c.Abort()
		return false
	}

	if !allowed {
		remaining, _ := limiter.GetRemaining(c.Request.Context(), userKey)

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

	remaining, _ := limiter.GetRemaining(c.Request.Context(), userKey)
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

		allowed, err := limiter.Allow(c.Request.Context(), key)
		if err != nil {
			obs.RecordRateLimitDegraded(c.Request.Context(), "ip")
			logger.Warn("IP rate limit",
				logger.StringField("ip", ip),
				logger.ErrorField(err))
			if localFallback.Allow(key, requests, window) {
				c.Next()
				return
			}
			allowed = false
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

// burstCache 是突发保护真正用到的最小 Redis 能力集。
// 收窄接口是为了能用一个轻量 fake 覆盖「封禁键是否真的被读取」这类回归。
type burstCache interface {
	Exists(ctx context.Context, key string) (bool, error)
	IncrBy(ctx context.Context, key string, value int64) (int64, error)
	Expire(ctx context.Context, key string, expiration time.Duration) error
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Delete(ctx context.Context, key string) error
}

type burstGuard struct {
	cache     burstCache
	threshold int
	cooldown  time.Duration
	local     *ratelimit.LocalLimiter
}

func newBurstGuard(c burstCache, threshold int, cooldown time.Duration) *burstGuard {
	if threshold < 1 {
		threshold = 1
	}
	if cooldown <= 0 {
		cooldown = time.Minute
	}
	return &burstGuard{
		cache:     c,
		threshold: threshold,
		cooldown:  cooldown,
		local:     localFallback,
	}
}

// allow 判断本次请求是否放行，degraded 表示 Redis 不可用、已退化为单机窗口。
//
// 这里必须先读 blocked:<ip>：旧实现只写不读，所谓「超限后短期封禁」从未生效，
// 攻击者只要等到 burst 计数窗口过期就能继续打。
func (g *burstGuard) allow(ctx context.Context, ip string) (allowed, degraded bool) {
	burstKey := fmt.Sprintf("burst:%s", ip)
	blockedKey := fmt.Sprintf("blocked:%s", ip)

	blocked, err := g.cache.Exists(ctx, blockedKey)
	if err != nil {
		// 无法确认封禁状态时继续走计数（Redis 大概率整体不可用，会落入单机兜底），
		// 但不能把查询失败静默当成「没有被封」。
		logger.Warn("burst protection blocked-state lookup failed",
			logger.StringField("ip", ip), logger.ErrorField(err))
	} else if blocked {
		return false, false
	}

	current, err := g.cache.IncrBy(ctx, burstKey, 1)
	if err != nil {
		// Redis 不可用时退化为单机固定窗口：窗口内超限同样拒绝，
		// 不能因为依赖故障就变成无限流。
		return g.local.Allow(burstKey, g.threshold, g.cooldown), true
	}

	if current == 1 {
		// 封禁状态必须独立于请求生命周期：请求取消后仍要落盘，
		// 否则攻击者可以用「发起即断开」绕过封禁。
		//
		// Expire 失败会让计数键永远没有 TTL：计数一旦越过阈值，这个 IP
		// 就会被永久拒绝，只能人工清 key。此时退化为删键重开窗口——
		// 宁可少挡一轮，也不制造永久封禁。
		ctxBg := context.Background()
		if err := g.cache.Expire(ctxBg, burstKey, g.cooldown); err != nil {
			logger.Error("burst counter expire failed, reset window",
				logger.StringField("ip", ip), logger.ErrorField(err))
			if delErr := g.cache.Delete(ctxBg, burstKey); delErr != nil {
				logger.Error("burst counter cleanup failed",
					logger.StringField("ip", ip), logger.ErrorField(delErr))
			}
		}
	}

	if current > int64(g.threshold) {
		// 封禁时长为窗口的两倍：让攻击者在计数窗口过期后仍被挡一段时间。
		// 写失败时当前请求仍然拒绝，后续请求也会因计数超阈值继续被拒，
		// 但封禁会提前失效，必须记录而不是静默。
		if err := g.cache.Set(context.Background(), blockedKey, "1", g.cooldown*2); err != nil {
			logger.Error("burst protection block write failed",
				logger.StringField("ip", ip), logger.ErrorField(err))
		}
		return false, false
	}
	return true, false
}

func BurstProtection(cache cache.Cache, threshold int, cooldown time.Duration) gin.HandlerFunc {
	if cache == nil {
		return func(c *gin.Context) { c.Next() }
	}
	guard := newBurstGuard(cache, threshold, cooldown)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		allowed, degraded := guard.allow(c.Request.Context(), ip)
		if degraded {
			obs.RecordRateLimitDegraded(c.Request.Context(), "burst")
			logger.Warn("burst protection degraded to local limiter",
				logger.StringField("ip", ip))
		}
		if !allowed {
			logger.Warn("burst protection blocked",
				logger.StringField("ip", ip))
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
