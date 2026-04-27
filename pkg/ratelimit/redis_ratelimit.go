package ratelimit

import (
	"context"
	"fmt"
	"time"

	"Logos/pkg/cache"
)

type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
	AllowN(ctx context.Context, key string, n int) (bool, error)
	Reset(ctx context.Context, key string) error
	GetRemaining(ctx context.Context, key string) (int64, error)
}

type FixedWindowRateLimiter struct {
	cache  cache.Cache
	rate   int
	window time.Duration
	prefix string
}

func NewFixedWindowLimiter(cache cache.Cache, rate int, window time.Duration) *FixedWindowRateLimiter {
	return &FixedWindowRateLimiter{
		cache:  cache,
		rate:   rate,
		window: window,
		prefix: "ratelimit:fixed",
	}
}

func (l *FixedWindowRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return l.AllowN(ctx, key, 1)
}

func (l *FixedWindowRateLimiter) AllowN(ctx context.Context, key string, n int) (bool, error) {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)

	current, err := l.cache.IncrBy(ctx, cacheKey, int64(n))
	if err != nil {
		return false, fmt.Errorf("incr failed: %w", err)
	}

	if current == int64(n) {
		l.cache.Expire(ctx, cacheKey, l.window)
	}

	return current <= int64(l.rate), nil
}

func (l *FixedWindowRateLimiter) Reset(ctx context.Context, key string) error {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)
	return l.cache.Delete(ctx, cacheKey)
}

func (l *FixedWindowRateLimiter) GetRemaining(ctx context.Context, key string) (int64, error) {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)

	val, err := l.cache.Get(ctx, cacheKey)
	if err != nil || val == "" {
		return int64(l.rate), nil
	}

	var count int64
	fmt.Sscanf(val, "%d", &count)
	remaining := int64(l.rate) - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

type SlidingWindowRateLimiter struct {
	cache  cache.Cache
	rate   int
	window time.Duration
	prefix string
}

func NewSlidingWindowLimiter(cache cache.Cache, rate int, window time.Duration) *SlidingWindowRateLimiter {
	return &SlidingWindowRateLimiter{
		cache:  cache,
		rate:   rate,
		window: window,
		prefix: "ratelimit:sliding",
	}
}

func (l *SlidingWindowRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return l.AllowN(ctx, key, 1)
}

func (l *SlidingWindowRateLimiter) AllowN(ctx context.Context, key string, n int) (bool, error) {
	now := time.Now().UnixMilli()
	windowStart := now - l.window.Milliseconds()
	member := fmt.Sprintf("%d-%d", now, n)

	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)

	pipeCmds := []struct {
		Cmd  string
		Args []interface{}
	}{
		{"ZREMRANGEBYSCORE", []interface{}{cacheKey, 0, windowStart}},
		{"ZADD", []interface{}{cacheKey, now, member}},
		{"ZRANGE", []interface{}{cacheKey, 0, -1}},
		{"PEXPIRE", []interface{}{cacheKey, l.window.Milliseconds()}},
	}

	results, err := l.cache.PipelineExec(ctx, pipeCmds)
	if err != nil {
		return false, fmt.Errorf("pipeline exec failed: %w", err)
	}

	count := 0
	if len(results) >= 3 && results[2] != nil {
		members, ok := results[2].([]string)
		if ok {
			count = len(members)
		}
	}

	if count > l.rate {
		l.cache.ZRem(ctx, cacheKey, member)
		return false, nil
	}

	return true, nil
}

func (l *SlidingWindowRateLimiter) Reset(ctx context.Context, key string) error {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)
	return l.cache.Delete(ctx, cacheKey)
}

func (l *SlidingWindowRateLimiter) GetRemaining(ctx context.Context, key string) (int64, error) {
	now := time.Now().UnixMilli()
	windowStart := now - l.window.Milliseconds()
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)

	count, err := l.cache.ZCount(ctx, cacheKey, float64(windowStart), float64(now))
	if err != nil {
		return int64(l.rate), err
	}

	remaining := int64(l.rate) - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

type TokenBucketRateLimiter struct {
	cache    cache.Cache
	rate     int
	capacity int
	prefix   string
}

func NewTokenBucketLimiter(cache cache.Cache, rate, capacity int) *TokenBucketRateLimiter {
	return &TokenBucketRateLimiter{
		cache:    cache,
		rate:     rate,
		capacity: capacity,
		prefix:   "ratelimit:token",
	}
}

func (l *TokenBucketRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return l.AllowN(ctx, key, 1)
}

func (l *TokenBucketRateLimiter) AllowN(ctx context.Context, key string, n int) (bool, error) {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)
	now := time.Now().UnixMicro()

	script := `
	local key = KEYS[1]
	local rate = tonumber(ARGV[1])
	local capacity = tonumber(ARGV[2])
	local tokens = tonumber(ARGV[3])
	local now = tonumber(ARGV[4])

	local bucket = redis.call('HMGET', key', 'tokens', 'last_refill')
	local current_tokens = tonumber(bucket[1]) or capacity
	local last_refill = tonumber(bucket[2]) or now

	local elapsed = math.max(0, (now - last_refill) / 1000000)
	local refill = math.floor(elapsed * rate)
	current_tokens = math.min(capacity, current_tokens + refill)

	if current_tokens >= tokens then
		current_tokens = current_tokens - tokens
		redis.call('HMSET', key, 'tokens', tostring(current_tokens), 'last_refill', tostring(now))
		redis.call('PEXPIRE', key, 3600000)
		return {1, tostring(current_tokens)}
	else
		redis.call('HMSET', key, 'tokens', tostring(current_tokens), 'last_refill', tostring(now))
		redis.call('PEXPIRE', key, 3600000)
		return {0, tostring(current_tokens)}
	end
	`

	result, err := l.cache.EvalSha(ctx, script, []string{cacheKey},
		l.rate, l.capacity, n, now)
	if err != nil {
		return false, fmt.Errorf("evalsha failed: %w", err)
	}

	results, ok := result.([]interface{})
	if !ok || len(results) < 1 {
		return false, nil
	}

	allowed, _ := results[0].(int64)
	return allowed == 1, nil
}

func (l *TokenBucketRateLimiter) Reset(ctx context.Context, key string) error {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)
	return l.cache.Delete(ctx, cacheKey)
}

func (l *TokenBucketRateLimiter) GetRemaining(ctx context.Context, key string) (int64, error) {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)

	val, err := l.cache.HGet(ctx, cacheKey, "tokens")
	if err != nil || val == "" {
		return int64(l.capacity), nil
	}

	var tokens int64
	fmt.Sscanf(val, "%d", &tokens)
	return tokens, nil
}

var globalLimiters = make(map[string]RateLimiter)

func GetGlobalLimiter(name string, limiterType string, cache cache.Cache, rate int, window time.Duration) RateLimiter {
	key := fmt.Sprintf("%s:%d:%d", name, rate, int(window.Seconds()))

	if limiter, exists := globalLimiters[key]; exists {
		return limiter
	}

	var newLimiter RateLimiter
	switch limiterType {
	case "fixed":
		newLimiter = NewFixedWindowLimiter(cache, rate, window)
	case "sliding":
		newLimiter = NewSlidingWindowLimiter(cache, rate, window)
	case "token":
		newLimiter = NewTokenBucketLimiter(cache, rate, rate*2)
	default:
		newLimiter = NewFixedWindowLimiter(cache, rate, window)
	}

	globalLimiters[key] = newLimiter
	return newLimiter
}
