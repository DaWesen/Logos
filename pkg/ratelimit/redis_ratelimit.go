package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"Logos/pkg/cache"
)

// toInt64 兼容不同 Redis 客户端/驱动把 Lua number 解成 int64、int 或字符串的情况。
func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case uint64:
		return int64(n), true
	case float64:
		return int64(n), true
	case string:
		parsed, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

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

// fixedWindowScript 把「自增 + 首次设置过期」合并为一次原子执行。
// 若先 INCR 再 EXPIPE 两步走，进程在两步之间退出会留下永不过期的计数键，
// 该用户会被永久限流。
const fixedWindowScript = `
local key = KEYS[1]
local n = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local current = redis.call('INCRBY', key, n)
if current == n then
  redis.call('PEXPIRE', key, window)
end
return current
`

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

	current, err := l.cache.EvalSha(ctx, fixedWindowScript, []string{cacheKey},
		n, l.window.Milliseconds())
	if err != nil {
		return false, fmt.Errorf("incr failed: %w", err)
	}

	count, ok := toInt64(current)
	if !ok {
		return false, fmt.Errorf("unexpected incr result: %v", current)
	}

	return count <= int64(l.rate), nil
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

// slidingWindowScript 用一次 Lua 执行完成「清理窗口外成员 + 写入本次请求 + 计数」。
//
// 旧实现有三个问题，都会让限流形同虚设：
//  1. member 用「毫秒 + n」拼装，同一毫秒内的请求 member 相同，ZADD 只更新分数不新增成员，
//     计数被锁死在「毫秒数」上，上限远低于配置速率；
//  2. 四步命令走 pipeline，但 pipeline 不是事务，并发请求会互相插队导致超发；
//  3. 拒绝时靠客户端再发一次 ZREM，中间已产生超发窗口。
//
// 现在每个请求生成唯一 member，并在脚本内完成回滚，计数与判定严格原子。
const slidingWindowScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local n = tonumber(ARGV[4])
local member = ARGV[5]
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
for i = 1, n do
  redis.call('ZADD', key, now, member .. ':' .. i)
end
local count = redis.call('ZCARD', key)
redis.call('PEXPIRE', key, window)
if count > limit then
  for i = 1, n do
    redis.call('ZREM', key, member .. ':' .. i)
  end
  return {0, count - n}
end
return {1, count}
`

var slidingMemberSeq uint64

// nextSlidingMember 生成窗口内唯一的成员 ID：
// 毫秒时间戳保留时间维度，进程内自增序号保证同一毫秒内不重复。
func nextSlidingMember(nowMs int64) string {
	return fmt.Sprintf("%d-%d", nowMs, atomic.AddUint64(&slidingMemberSeq, 1))
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
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)

	result, err := l.cache.EvalSha(ctx, slidingWindowScript, []string{cacheKey},
		now, l.window.Milliseconds(), l.rate, n, nextSlidingMember(now))
	if err != nil {
		return false, fmt.Errorf("eval failed: %w", err)
	}

	results, ok := result.([]interface{})
	if !ok || len(results) < 1 {
		return false, fmt.Errorf("unexpected eval result: %v", result)
	}
	allowed, ok := toInt64(results[0])
	if !ok {
		return false, fmt.Errorf("unexpected allowed flag: %v", results[0])
	}

	return allowed == 1, nil
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

	local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
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

	allowed, ok := toInt64(results[0])
	if !ok {
		return false, nil
	}
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

// globalLimiters 缓存按 (name, rate, window) 复用的限流器实例。
// 限流中间件在每个请求上都会调用 GetGlobalLimiter，普通 map 在并发写时会直接 panic
// （concurrent map writes），因此必须加读写锁。
var (
	globalLimiters   = make(map[string]RateLimiter)
	globalLimitersMu sync.RWMutex
)

func GetGlobalLimiter(name string, limiterType string, cache cache.Cache, rate int, window time.Duration) RateLimiter {
	key := fmt.Sprintf("%s:%d:%d", name, rate, int(window.Seconds()))

	globalLimitersMu.RLock()
	limiter, exists := globalLimiters[key]
	globalLimitersMu.RUnlock()
	if exists {
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

	globalLimitersMu.Lock()
	defer globalLimitersMu.Unlock()
	// 双检：并发首次创建时以先写入者为准，避免同名复用到不同实例
	if existing, ok := globalLimiters[key]; ok {
		return existing
	}
	globalLimiters[key] = newLimiter
	return newLimiter
}
