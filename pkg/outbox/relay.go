package outbox

import (
	"context"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/mq"

	"gorm.io/gorm"
)

type Relay struct {
	repo     OutboxRepository
	db       *gorm.DB
	producer *mq.Producer
	interval time.Duration
	batchSize int
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewRelay(db *gorm.DB, producer *mq.Producer, opts ...RelayOption) *Relay {
	r := &Relay{
		repo:      NewOutboxRepository(),
		db:        db,
		producer:  producer,
		interval:  500 * time.Millisecond,
		batchSize: 100,
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

func (r *Relay) Start() {
	go r.run()
	logger.Info("Outbox relay 已启动",
		logger.StringField("interval", r.interval.String()),
		logger.IntField("batch_size", r.batchSize))
}

func (r *Relay) Stop() {
	r.cancel()
	logger.Info("Outbox relay 已停止")
}

func (r *Relay) run() {
	cleanTicker := time.NewTicker(10 * time.Minute)
	defer cleanTicker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-cleanTicker.C:
			r.clean()
		default:
		}

		r.dispatch()
		time.Sleep(r.interval)
	}
}

func (r *Relay) dispatch() {
	messages, err := r.repo.FetchPending(r.ctx, r.db, r.batchSize)
	if err != nil {
		logger.Warn("Outbox fetch pending failed", logger.ErrorField(err))
		return
	}

	for _, msg := range messages {
		if err := r.producer.Send(r.ctx, msg.Topic, msg.Key, []byte(msg.Value)); err != nil {
			logger.Warn("Outbox send failed, will retry",
				logger.StringField("id", msg.ID),
				logger.StringField("topic", msg.Topic),
				logger.ErrorField(err))
			_ = r.repo.MarkFailed(r.ctx, r.db, msg.ID, err.Error())
			continue
		}

		_ = r.repo.MarkSent(r.ctx, r.db, msg.ID)
	}
}

func (r *Relay) clean() {
	before := time.Now().Add(-24 * time.Hour)
	if err := r.repo.CleanSent(r.ctx, r.db, before); err != nil {
		logger.Warn("Outbox clean failed", logger.ErrorField(err))
	}
}
