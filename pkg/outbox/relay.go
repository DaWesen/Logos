package outbox

import (
	"context"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/mq"

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

// dispatch 在单个数据库事务内完成「锁定 -> 投递 -> 标记」，
// FOR UPDATE SKIP LOCKED 保证多副本时各实例不会重复投递同一批消息。
// 注意：若 Kafka 发送成功但事务提交失败，消息会回到 pending 被再次投递，
// 这是 at-least-once 语义的一部分，消费端需保证幂等。
func (r *Relay) dispatch() {
	err := r.db.WithContext(r.ctx).Transaction(func(tx *gorm.DB) error {
		messages, err := r.repo.FetchPending(r.ctx, tx, r.batchSize)
		if err != nil {
			return err
		}

		for _, msg := range messages {
			if err := r.producer.Send(r.ctx, msg.Topic, msg.Key, []byte(msg.Value)); err != nil {
				logger.Warn("Outbox send failed, will retry with backoff",
					logger.StringField("id", msg.ID),
					logger.StringField("topic", msg.Topic),
					logger.IntField("retry_count", msg.RetryCount+1),
					logger.ErrorField(err))
				if markErr := r.repo.MarkFailed(r.ctx, tx, msg, err.Error()); markErr != nil {
					logger.Warn("Outbox mark failed error", logger.ErrorField(markErr))
				}
				if msg.RetryCount+1 >= MaxRetryCount {
					logger.Error("Outbox message exhausted retries, moved to dead letter",
						logger.StringField("id", msg.ID),
						logger.StringField("topic", msg.Topic))
					r.onDeadLetter(msg, err)
				}
				continue
			}

			if markErr := r.repo.MarkSent(r.ctx, tx, msg.ID); markErr != nil {
				logger.Warn("Outbox mark sent error", logger.ErrorField(markErr))
			}
		}
		return nil
	})
	if err != nil {
		logger.Warn("Outbox dispatch failed", logger.ErrorField(err))
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
