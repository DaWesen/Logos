package gateway

import (
	"sync"
	"sync/atomic"
	"time"

	"Logos/pkg/logger"
)

// 上行频率限制参数。
//
// 网关是唯一对公网暴露的长连接入口，单个客户端可以在一条连接上无限灌上行帧：
// 每帧都会触发 json.Unmarshal、可能触发一次 Kafka 生产、还可能触达 Chat 服务。
// 一个脚本化的账号用几千条连接就能把网关 CPU 与 Kafka 写放大打满，而这类流量
// 在业务上毫无意义（正常人打不出 30 条/秒的消息）。
//
// 取值依据：正常聊天 + 输入状态 + 已读回执的高峰在 5~10 条/秒，30 条/秒留 3 倍裕量；
// 突发 60 用于吸收前端 short-buffer（如批量标记已读）的一次性脉冲，避免误伤正常用户。
const (
	// InboundRatePerSecond 每连接每秒补充的令牌数（长期平均速率）。
	InboundRatePerSecond = 30
	// InboundBurst 每连接允许的瞬时突发帧数（桶容量）。
	InboundBurst = 60
	// rateLimitLogSample 限流日志采样率：超限时每 50 次才打一条 Warn，
	// 否则恶意客户端本身就会把日志刷成二次故障。
	rateLimitLogSample = 50
)

// TokenBucket 单连接令牌桶。
//
// 选令牌桶而不是固定窗口计数：固定窗口在窗口边界会出现「上一秒最后 60 条 +
// 下一秒前 60 条」的 2 倍瞬时尖峰，且实现上要维护窗口起点；令牌桶用连续补充，
// 天然平滑，也不需要为每个连接存一个滑动窗口的计数数组。
//
// 时钟可注入（now），因此单测能精确断言「突发 -> 耗尽 -> 按速率恢复」，
// 不依赖 sleep，避免慢测试与 flaky。
type TokenBucket struct {
	mu       sync.Mutex
	capacity float64
	tokens   float64
	rate     float64
	last     time.Time
	now      func() time.Time
}

// NewTokenBucket 用真实时钟创建令牌桶。
func NewTokenBucket(ratePerSecond, burst int) *TokenBucket {
	return NewTokenBucketWithClock(ratePerSecond, burst, time.Now)
}

// NewTokenBucketWithClock 用注入时钟创建令牌桶，便于确定性测试。
func NewTokenBucketWithClock(ratePerSecond, burst int, now func() time.Time) *TokenBucket {
	if ratePerSecond <= 0 {
		ratePerSecond = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if now == nil {
		now = time.Now
	}
	return &TokenBucket{
		capacity: float64(burst),
		tokens:   float64(burst), // 初始满桶：新建连接首帧不应被拒
		rate:     float64(ratePerSecond),
		last:     now(),
		now:      now,
	}
}

// NewInboundTokenBucket 创建网关默认的上行限流器（30 条/秒、突发 60）。
func NewInboundTokenBucket() *TokenBucket {
	return NewTokenBucket(InboundRatePerSecond, InboundBurst)
}

// Allow 尝试取一个令牌；返回 false 表示本次上行应被拒绝。
//
// 时钟回拨（NTP 校时、虚拟机迁移）时只把基准时间拨回、不补令牌：
// 如果按负时长计算补充量，时间倒流会让 tokens 变成负数，反而把用户锁死；
// 直接重置基准最坏只是少补一小段时间的令牌，不会误伤。
func (b *TokenBucket) Allow() bool {
	if b == nil {
		return true // 未配置限流时不拦截
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	} else if elapsed < 0 {
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Tokens 返回当前剩余令牌（四舍五入到整数），仅用于观测与测试。
func (b *TokenBucket) Tokens() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.tokens)
}

// inboundRateLimitRejections 全局上行限流拒绝次数（可观测，测试亦依赖）。
var inboundRateLimitRejections int64

// InboundRateLimitRejections 返回累计被限流拒绝的上行帧数。
func InboundRateLimitRejections() int64 {
	return atomic.LoadInt64(&inboundRateLimitRejections)
}

// NoteInboundRateLimit 记录一次上行限流命中：原子计数 + 采样日志。
//
// 计数与日志分开：计数用于告警（突增说明有人在刷），日志只做采样。
// protocol 取 websocket/tcp，便于定位是哪种接入方式被刷。
func NoteInboundRateLimit(protocol, sessionID, userID string) {
	n := atomic.AddInt64(&inboundRateLimitRejections, 1)
	if n == 1 || n%rateLimitLogSample == 0 {
		logger.Warn("上行消息超过频率限制",
			logger.StringField("protocol", protocol),
			logger.StringField("session_id", sessionID),
			logger.StringField("user_id", userID),
			logger.Int64Field("rejected_total", n))
	}
}
