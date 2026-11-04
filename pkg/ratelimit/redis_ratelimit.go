package ratelimit

import (
	"context"
	"fmt"
	"math"
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
	// 0 速率会让桶永远补不满，0 容量则永远拒绝；都归一到「至少允许 1 个」，
	// 避免一个配置笔误把整条链路限死。
	if rate < 1 {
		rate = 1
	}
	if capacity < 1 {
		capacity = 1
	}
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

// tokenBucketScript 用浮点令牌做补充，避免丢弃分数时间。
//
// 旧实现是 `refill = math.floor(elapsed * rate)`，然后把 last_refill 重置为 now。
// 高频请求下每次 elapsed 都不足一个令牌周期，floor 恒为 0，而时间基准却一直在前移，
// 分数部分被永久丢弃：桶一旦打空就再也补不回来，限流从「按速率放行」退化成「永久拒绝」。
// 现在令牌与时间都不取整，每次只把「已经补进来的量」记账，剩余分数留到下次。
const tokenBucketScript = `
local key = KEYS[1]
local rate = tonumber(ARGV[1])
local capacity = tonumber(ARGV[2])
local want = tonumber(ARGV[3])
local now = tonumber(ARGV[4])
local ttl = tonumber(ARGV[5])

local bucket = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens = tonumber(bucket[1])
local last_refill = tonumber(bucket[2])
if tokens == nil then tokens = capacity end
if last_refill == nil then last_refill = now end

local elapsed = (now - last_refill) / 1000000
if elapsed < 0 then elapsed = 0 end
tokens = math.min(capacity, tokens + elapsed * rate)

local allowed = 0
if tokens >= want then
  tokens = tokens - want
  allowed = 1
end

redis.call('HMSET', key, 'tokens', tostring(tokens), 'last_refill', tostring(now))
redis.call('PEXPIRE', key, ttl)
return {allowed, tostring(tokens)}
`

// tokenBucketTTL 桶键的过期时间：至少 1 小时，且不短于「从空桶补满」所需时间。
// 过期时间太短会让空闲用户的桶提前重置为满，等于凭空多给一次突发额度。
func tokenBucketTTL(rate, capacity int) time.Duration {
	if rate < 1 {
		rate = 1
	}
	if capacity < 1 {
		capacity = 1
	}
	refill := time.Duration(capacity) * time.Second / time.Duration(rate)
	ttl := time.Hour
	if refill*2 > ttl {
		ttl = refill * 2
	}
	return ttl
}

// refillTokens 与 Lua 脚本保持同一套补充语义，供 GetRemaining 与单测使用。
// tokens 允许小数：分数令牌是「高频请求下仍能按速率恢复」的关键。
func refillTokens(tokens float64, lastRefillMicros, nowMicros int64, rate, capacity int) float64 {
	elapsed := float64(nowMicros-lastRefillMicros) / 1e6
	if elapsed < 0 {
		elapsed = 0
	}
	tokens += elapsed * float64(rate)
	if tokens > float64(capacity) {
		tokens = float64(capacity)
	}
	return tokens
}

func (l *TokenBucketRateLimiter) AllowN(ctx context.Context, key string, n int) (bool, error) {
	cacheKey := fmt.Sprintf("%s:%s", l.prefix, key)
	now := time.Now().UnixMicro()

	result, err := l.cache.EvalSha(ctx, tokenBucketScript, []string{cacheKey},
		l.rate, l.capacity, n, now, tokenBucketTTL(l.rate, l.capacity).Milliseconds())
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

	fields, err := l.cache.HGetAll(ctx, cacheKey)
	if err != nil {
		return int64(l.capacity), err
	}
	tokens, lastRefillMicros, hasTime, ok := parseTokenBucketState(fields)
	if !ok {
		return int64(l.capacity), nil
	}
	if !hasTime {
		return int64(math.Floor(tokens)), nil
	}

	tokens = refillTokens(tokens, lastRefillMicros, time.Now().UnixMicro(), l.rate, l.capacity)
	return int64(math.Floor(tokens)), nil
}

// parseTokenBucketState 解析桶状态。
//
// Redis Lua 的 tostring 用 "%.14g"，微秒时间戳可能被写成科学计数法
// （如 1.7623456789012e+15）。用 ParseInt 会直接失败并静默退化成「不补充」，
// 所以 tokens 与时间统一按浮点解析再截断。
func parseTokenBucketState(fields map[string]string) (tokens float64, lastRefillMicros int64, hasTime, ok bool) {
	if len(fields) == 0 {
		return 0, 0, false, false
	}
	tokens, err := strconv.ParseFloat(fields["tokens"], 64)
	if err != nil {
		return 0, 0, false, false
	}
	lastRefill, err := strconv.ParseFloat(fields["last_refill"], 64)
	if err != nil {
		// 时间缺失时仍可返回当前令牌数，但不能凭空按 now-0 补满整个桶。
		return tokens, 0, false, true
	}
	return tokens, int64(lastRefill), true, true
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
