package outbox

import (
	"context"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"

	"gorm.io/gorm"
)

// DeadLetterEvent 描述一条进入死信终态（重试耗尽）的 Outbox 消息，
// 供告警钩子消费（可对接告警系统 / 人工排查）。
type DeadLetterEvent struct {
	ID           string
	Topic        string
	Key          string
	RetryCount   int
	ErrorMessage string
	CreatedAt    time.Time
}

// AlertHook 死信告警钩子。实现方需保证非阻塞/快速返回，避免拖慢 relay 循环。
type AlertHook func(evt DeadLetterEvent)

type Relay struct {
	repo           OutboxRepository
	db             *gorm.DB
	producer       *mq.Producer
	interval       time.Duration
	batchSize      int
	replayInterval time.Duration
	sendTimeout    time.Duration
	alertHook      AlertHook
	ctx            context.Context
	cancel         context.CancelFunc
}

func NewRelay(db *gorm.DB, producer *mq.Producer, opts ...RelayOption) *Relay {
	r := &Relay{
		repo:           NewOutboxRepository(),
		db:             db,
		producer:       producer,
		interval:       500 * time.Millisecond,
		batchSize:      100,
		replayInterval: 10 * time.Minute,
		sendTimeout:    defaultSendTimeout,
	}

	for _, opt := range opts {
		opt(r)
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())
	return r
}

type RelayOption func(*Relay)

func WithInterval(d time.Duration) RelayOption {
	return func(r *Relay) { r.interval = d }
}

func WithBatchSize(n int) RelayOption {
	return func(r *Relay) { r.batchSize = n }
}

// WithReplayInterval 设置死信定时重放的间隔，默认 10 分钟。
// 传 <= 0 可关闭定时重放（仅保留手工 ReplayDeadLetters 入口）。
func WithReplayInterval(d time.Duration) RelayOption {
	return func(r *Relay) { r.replayInterval = d }
}

// WithAlertHook 注入死信告警钩子（可插拔，未注入时仅打印 Error 日志）
func WithAlertHook(hook AlertHook) RelayOption {
	return func(r *Relay) { r.alertHook = hook }
}

// WithSendTimeout 设置单条消息投递的超时上限，默认 defaultSendTimeout。
//
// 没有单条超时时，一条卡死的 Kafka 写入会让整轮 batch 停在原地：
// 后面的消息全部延迟，租约到期后又会被其他副本重复领取。
// 传 <= 0 表示不限制（仅建议在测试中使用）。
func WithSendTimeout(d time.Duration) RelayOption {
	return func(r *Relay) { r.sendTimeout = d }
}

const (
	// defaultSendTimeout 单条消息投递超时。
	// 与 Kafka writer 的 WriteTimeout（5s）留出一倍裕量，避免正常的长尾写入被误杀。
	defaultSendTimeout = 10 * time.Second
	// dispatchFailureAbortThreshold 同一轮投递中连续失败多少条后提前结束本轮。
	//
	// 为什么要有熔断：broker 整体不可用时，一批 100 条消息会逐条等满 sendTimeout
	// 才失败（最坏 100 × 10s），但这 100 条几乎必然全部失败。提前结束本轮可以
	// 把 CPU/连接池让给健康链路，失败消息留在 pending（退避到期后重投），
	// 不会丢失；代价只是这些消息的下一轮投递时间略微延后。
	dispatchFailureAbortThreshold = 5
)

// ShouldAbortDispatch 判断本轮投递是否应提前结束。
// 阈值 <= 0 表示关闭熔断（永远返回 false）。
func ShouldAbortDispatch(consecutiveFailures, threshold int) bool {
	if threshold <= 0 {
		return false
	}
	return consecutiveFailures >= threshold
}

// sendTimeoutOrZero 返回实际生效的单条投递超时；<=0 表示不限制。
func (r *Relay) sendTimeoutOrZero() time.Duration {
	if r.sendTimeout <= 0 {
		return 0
	}
	return r.sendTimeout
}

func (r *Relay) Start() {
	go r.run()
	logger.Info("Outbox relay 已启动",
		logger.StringField("interval", r.interval.String()),
		logger.IntField("batch_size", r.batchSize),
		logger.StringField("replay_interval", r.replayInterval.String()))
}

func (r *Relay) Stop() {
	r.cancel()
	logger.Info("Outbox relay 已停止")
}

func (r *Relay) run() {
	cleanTicker := time.NewTicker(10 * time.Minute)
	defer cleanTicker.Stop()

	// 定时重放死信：failed 终态消息按 replayInterval 周期性回到 pending 重新投递
	var replayC <-chan time.Time
	if r.replayInterval > 0 {
		replayTicker := time.NewTicker(r.replayInterval)
		defer replayTicker.Stop()
		replayC = replayTicker.C
	}

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-cleanTicker.C:
			r.clean()
		case <-replayC:
			r.autoReplay()
		default:
		}

		r.dispatch()
		select {
		case <-r.ctx.Done():
			return
		case <-time.After(r.interval):
		}
	}
}

// dispatchLease 领取批次后各行的租约时长。
// 租约短于最坏发送耗时是安全的：最坏情况只是租约到期后被另一副本重复领取，
// 而 at-least-once 本就允许重复投递（消费端幂等），绝不会丢消息。
const dispatchLease = 30 * time.Second

// dispatch 拆成三段，保证 Kafka 网络往返期间不持有数据库事务与行锁：
//  1. 短事务领取：FetchPending + 写租约，提交即释放行锁
//  2. 事务外逐条发送：网络抖动不再拖住连接池
//  3. 单语句标记 sent/failed（各自隐式短事务）
//
// 若发送成功而标记失败，租约到期后消息回到 pending 被再次投递，
// 这是 at-least-once 语义的一部分，消费端需保证幂等。
func (r *Relay) dispatch() {
	var messages []*OutboxMessage
	err := r.db.WithContext(r.ctx).Transaction(func(tx *gorm.DB) error {
		var claimErr error
		messages, claimErr = r.repo.ClaimPending(r.ctx, tx, r.batchSize, time.Now().Add(dispatchLease))
		return claimErr
	})
	if err != nil {
		logger.Warn("Outbox dispatch failed", logger.ErrorField(err))
		return
	}

	consecutiveFailures := 0
	for _, msg := range messages {
		sendStart := time.Now()
		// 每条消息独立超时：一条卡死的写入不能拖住整轮 batch。
		// 超时只取消本次发送，relay 的 ctx 不受影响（后续消息与下一轮照常进行）。
		sendCtx := r.ctx
		cancel := func() {}
		if timeout := r.sendTimeoutOrZero(); timeout > 0 {
			sendCtx, cancel = context.WithTimeout(r.ctx, timeout)
		}
		sendErr := r.producer.Send(sendCtx, msg.Topic, msg.Key, []byte(msg.Value))
		cancel()
		elapsed := time.Since(sendStart).Seconds()

		if sendErr != nil {
			consecutiveFailures++
			obs.RecordOutboxDispatch(r.ctx, msg.Topic, "failed", elapsed)
			logger.Warn("Outbox send failed, will retry with backoff",
				logger.StringField("id", msg.ID),
				logger.StringField("topic", msg.Topic),
				logger.IntField("retry_count", msg.RetryCount+1),
				logger.IntField("consecutive_failures", consecutiveFailures),
				logger.ErrorField(sendErr))
			if markErr := r.repo.MarkFailed(r.ctx, r.db, msg, sendErr.Error()); markErr != nil {
				logger.Warn("Outbox mark failed error", logger.ErrorField(markErr))
			}
			if msg.RetryCount+1 >= MaxRetryCount {
				obs.RecordOutboxDeadLetter(r.ctx, msg.Topic)
				logger.Error("Outbox message exhausted retries, moved to dead letter",
					logger.StringField("id", msg.ID),
					logger.StringField("topic", msg.Topic))
				r.onDeadLetter(msg, sendErr)
			}
			if ShouldAbortDispatch(consecutiveFailures, dispatchFailureAbortThreshold) {
				// 连败熔断：剩余消息保持 pending，租约到期后自动回到可领取状态。
				obs.RecordOutboxDispatchAbort(r.ctx, msg.Topic)
				logger.Error("Outbox 连续投递失败，提前结束本轮",
					logger.IntField("consecutive_failures", consecutiveFailures),
					logger.IntField("batch_size", len(messages)),
					logger.StringField("last_topic", msg.Topic))
				return
			}
			continue
		}

		consecutiveFailures = 0
		obs.RecordOutboxDispatch(r.ctx, msg.Topic, "sent", elapsed)
		if markErr := r.repo.MarkSent(r.ctx, r.db, msg.ID); markErr != nil {
			logger.Warn("Outbox mark sent error", logger.ErrorField(markErr))
		}
	}
}

func (r *Relay) clean() {
	before := time.Now().Add(-24 * time.Hour)
	if err := r.repo.CleanSent(r.ctx, r.db, before); err != nil {
		logger.Warn("Outbox clean failed", logger.ErrorField(err))
	}
}

// autoReplay 定时重放死信：把 failed 终态消息重新置回 pending，交由 dispatch 重新投递。
// 单次最多处理 batchSize 条，循环排空（每批都受 replay_count 上限约束），
// 并用超时上下文避免长时间占用 relay 主循环。
func (r *Relay) autoReplay() {
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()

	limit := r.batchSize
	if limit <= 0 {
		limit = 100
	}

	for {
		n, err := r.ReplayDeadLetters(ctx, limit)
		if err != nil {
			return
		}
		// 未取满一批说明死信已排空（或剩余均已达重放上限）
		if n < int64(limit) {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

// onDeadLetter 触发死信告警。钩子可插拔：未注入时仅保留 Error 日志，
// 注入后可对接告警系统。钩子内 panic 会被捕获，避免影响 relay 主循环。
func (r *Relay) onDeadLetter(msg *OutboxMessage, cause error) {
	if r.alertHook == nil {
		return
	}
	evt := DeadLetterEvent{
		ID:         msg.ID,
		Topic:      msg.Topic,
		Key:        msg.Key,
		RetryCount: msg.RetryCount + 1,
		CreatedAt:  msg.CreatedAt,
	}
	if cause != nil {
		evt.ErrorMessage = cause.Error()
	}
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Warn("Outbox alert hook panicked", logger.AnyField("panic", rec))
			}
		}()
		r.alertHook(evt)
	}()
}

// ReplayDeadLetters 重放入口：把 failed 终态消息重置回 pending（清零重试计数与退避，
// 并累计 replay_count），交由 relay 下一轮重新投递。
// 既可由 relay 的定时任务（autoReplay）自动调用，也可由管理接口在人工确认后调用；
// 达到 MaxReplayCount 的消息不再被重放。
// limit <= 0 时使用默认批量 100。
func (r *Relay) ReplayDeadLetters(ctx context.Context, limit int) (int64, error) {
	if limit <= 0 {
		limit = 100
	}
	n, err := r.repo.ReplayFailed(ctx, r.db, limit)
	if err != nil {
		logger.Warn("Outbox replay dead letters failed", logger.ErrorField(err))
		return 0, err
	}
	if n > 0 {
		logger.Info("Outbox dead letters replayed", logger.IntField("count", int(n)))
	}
	return n, nil
}
