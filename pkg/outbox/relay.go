package outbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"

	"github.com/google/uuid"
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
	owner          string
	wg             sync.WaitGroup
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
		owner:          newLeaseOwner(),
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
	r.wg.Add(1)
	go r.run()
	logger.Info("Outbox relay 已启动",
		logger.StringField("interval", r.interval.String()),
		logger.IntField("batch_size", r.batchSize),
		logger.StringField("replay_interval", r.replayInterval.String()))
}

// stopWaitTimeout 停机时等待在途轮次的上限。
//
// 不能无限等待：一块消息最坏要 dispatchChunkSize × sendTimeout，
// 无界等待会让进程超过 K8s 的 terminationGracePeriodSeconds 被 SIGKILL，
// 反而丢掉正在收尾的 MarkSent。超时后返回，未完成的消息靠租约到期自愈。
const stopWaitTimeout = 10 * time.Second

// Stop 取消主循环并有限等待在途轮次结束。
//
// 只 cancel 不等待会让进程在 dispatch 中途退出：此时消息已发送、MarkSent 未落库，
// 租约到期后会被重复投递。有界等待兼顾「尽量收尾」与「不被强杀」。
func (r *Relay) Stop() {
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("Outbox relay 已停止")
	case <-time.After(stopWaitTimeout):
		logger.Warn("Outbox relay 停机等待超时，未完成的消息将由租约到期后重投",
			logger.StringField("timeout", stopWaitTimeout.String()))
	}
}

func (r *Relay) run() {
	defer r.wg.Done()
	cleanTicker := time.NewTicker(10 * time.Minute)
	defer cleanTicker.Stop()

	// 积压采样独立于投递循环：500ms 一轮投递，但 COUNT 不需要这么频繁。
	statsTicker := time.NewTicker(backlogReportInterval)
	defer statsTicker.Stop()
	r.reportBacklog()

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
		case <-statsTicker.C:
			r.reportBacklog()
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

// dispatchLease 默认租约窗口。
//
// 租约必须覆盖「一块消息的最坏投递耗时」，否则后面的消息会在轮次结束前过期，
// 被其他副本重复领取。leaseWindow 会按实际块大小与单条超时推导出真正的窗口，
// 这个常量只是下限。
const dispatchLease = 60 * time.Second

// leaseRenewMargin 续租/领取时在「最坏耗时」之上多留的余量，
// 覆盖数据库往返与时钟漂移，避免刚好卡在到期边界。
const leaseRenewMargin = 5 * time.Second

// maxDispatchChunkSize 单块条数上限：块太大时，一条慢消息会拖长整块的续租间隔。
const maxDispatchChunkSize = 20

// dispatchChunkSizeFor 按单条超时推导单块条数，使一块的最坏耗时落在 dispatchLease 内。
// 单条超时 <= 0 表示不限制，此时只能逐条处理并逐条续租。
func dispatchChunkSizeFor(sendTimeout time.Duration) int {
	if sendTimeout <= 0 {
		return 1
	}
	budget := dispatchLease - leaseRenewMargin
	if budget <= 0 {
		return 1
	}
	n := int(budget / sendTimeout)
	if n < 1 {
		n = 1
	}
	if n > maxDispatchChunkSize {
		n = maxDispatchChunkSize
	}
	return n
}

// leaseWindow 返回能覆盖这一块最坏耗时的租约时长，且不小于 dispatchLease。
//
// 这是「续租一定来得及」的构造性保证：不假设配置落在安全区间，
// 而是按 size × sendTimeout 直接算出需要的窗口。
func leaseWindow(size int, sendTimeout time.Duration) time.Duration {
	window := dispatchLease
	if sendTimeout > 0 && size > 0 {
		need := time.Duration(size)*sendTimeout + leaseRenewMargin
		if need > window {
			window = need
		}
	}
	return window
}

// newLeaseOwner 生成本副本的 fencing token。
// 不用主机名/端口：同一台机器重启后端口可能复用，旧进程的迟到写入会误伤新进程。
func newLeaseOwner() string {
	return uuid.NewString()
}

// backlogReportInterval 积压采样间隔。
// 不用每轮投递都 COUNT：500ms 一次会给数据库增加无意义负载，
// 而积压告警的时间尺度本来就是分钟级。
const backlogReportInterval = 30 * time.Second

// reportBacklog 采样 pending 数量与最老消息年龄。
// 两个指标必须成对看：只有数量没有年龄，分不清「刚堆积」还是「卡了很久」。
func (r *Relay) reportBacklog() {
	if r.db == nil || r.repo == nil {
		return
	}
	pending, oldest, err := r.repo.BacklogStats(r.ctx, r.db)
	if err != nil {
		logger.Warn("Outbox backlog stats failed", logger.ErrorField(err))
		return
	}
	var ageSeconds float64
	if oldest != nil {
		ageSeconds = time.Since(*oldest).Seconds()
		if ageSeconds < 0 {
			ageSeconds = 0
		}
	}
	obs.RecordOutboxBacklog(r.ctx, pending, ageSeconds)
}

// dispatch 拆成三段，保证 Kafka 网络往返期间不持有数据库事务与行锁：
//  1. 短事务领取：FetchPending + 写租约，提交即释放行锁
//  2. 事务外逐条发送：网络抖动不再拖住连接池
//  3. 单语句标记 sent/failed（各自隐式短事务）
//
// 若发送成功而标记失败，租约到期后消息回到 pending 被再次投递，
// 这是 at-least-once 语义的一部分，消费端需保证幂等。
func (r *Relay) dispatch() {
	remaining := r.batchSize
	if remaining <= 0 {
		remaining = 1
	}

	consecutiveFailures := 0
	for remaining > 0 {
		if r.ctx.Err() != nil {
			return
		}
		size := dispatchChunkSizeFor(r.sendTimeoutOrZero())
		if remaining < size {
			size = remaining
		}

		var messages []*OutboxMessage
		err := r.db.WithContext(r.ctx).Transaction(func(tx *gorm.DB) error {
			var claimErr error
			window := leaseWindow(size, r.sendTimeoutOrZero())
			messages, claimErr = r.repo.ClaimPending(r.ctx, tx, size, time.Now().Add(window), r.owner)
			return claimErr
		})
		if err != nil {
			logger.Warn("Outbox dispatch failed", logger.ErrorField(err))
			return
		}
		if len(messages) == 0 {
			return
		}
		remaining -= len(messages)

		if r.dispatchChunk(messages, &consecutiveFailures) {
			return
		}
	}
}

// dispatchChunk 投递一个小块，返回是否应提前结束本轮（连败熔断或租约丢失）。
//
// 每块开始前续租一次，把「租约覆盖投递耗时」变成可证明的不变式：
// 单块最多 dispatchChunkSize 条，最坏耗时 dispatchChunkSize × sendTimeout，
// 远小于 dispatchLease。
func (r *Relay) dispatchChunk(messages []*OutboxMessage, consecutiveFailures *int) bool {
	ids := make([]string, 0, len(messages))
	for _, msg := range messages {
		ids = append(ids, msg.ID)
	}
	if err := r.renewLease(ids); err != nil {
		// 续租失败说明这块消息已被其他副本接管：继续投递只会产生重复消息。
		logger.Warn("Outbox lease renewal failed, aborting chunk", logger.ErrorField(err))
		return true
	}

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
			*consecutiveFailures++
			obs.RecordOutboxDispatch(r.ctx, msg.Topic, "failed", elapsed)
			logger.Warn("Outbox send failed, will retry with backoff",
				logger.StringField("id", msg.ID),
				logger.StringField("topic", msg.Topic),
				logger.IntField("retry_count", msg.RetryCount+1),
				logger.IntField("consecutive_failures", *consecutiveFailures),
				logger.ErrorField(sendErr))
			if markErr := r.repo.MarkFailed(r.ctx, r.db, msg, r.owner, sendErr.Error()); markErr != nil {
				if errors.Is(markErr, ErrLeaseLost) {
					logger.Warn("Outbox lease lost, another relay owns this message",
						logger.StringField("id", msg.ID),
						logger.StringField("topic", msg.Topic))
					continue
				}
				logger.Warn("Outbox mark failed error", logger.ErrorField(markErr))
			}
			if msg.RetryCount+1 >= MaxRetryCount {
				obs.RecordOutboxDeadLetter(r.ctx, msg.Topic)
				logger.Error("Outbox message exhausted retries, moved to dead letter",
					logger.StringField("id", msg.ID),
					logger.StringField("topic", msg.Topic))
				r.onDeadLetter(msg, sendErr)
			}
			if ShouldAbortDispatch(*consecutiveFailures, dispatchFailureAbortThreshold) {
				// 连败熔断：剩余消息保持 pending，租约到期后自动回到可领取状态。
				obs.RecordOutboxDispatchAbort(r.ctx, msg.Topic)
				logger.Error("Outbox 连续投递失败，提前结束本轮",
					logger.IntField("consecutive_failures", *consecutiveFailures),
					logger.IntField("batch_size", len(messages)),
					logger.StringField("last_topic", msg.Topic))
				return true
			}
			continue
		}

		*consecutiveFailures = 0
		obs.RecordOutboxDispatch(r.ctx, msg.Topic, "sent", elapsed)
		if markErr := r.repo.MarkSent(r.ctx, r.db, msg.ID, r.owner); markErr != nil {
			if errors.Is(markErr, ErrLeaseLost) {
				// 发送成功但租约已被接管：消息可能被重复投递一次。
				// at-least-once 语义下由消费端幂等兜底，这里只记录并继续。
				obs.RecordOutboxDispatch(r.ctx, msg.Topic, "duplicate", elapsed)
				logger.Warn("Outbox lease lost after send, message may be delivered twice",
					logger.StringField("id", msg.ID),
					logger.StringField("topic", msg.Topic))
				continue
			}
			logger.Warn("Outbox mark sent error", logger.ErrorField(markErr))
		}
	}
	return false
}

// renewLease 续租本块消息。返回 ErrLeaseLost 表示部分消息已不属于本副本。
func (r *Relay) renewLease(ids []string) error {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	window := leaseWindow(len(ids), r.sendTimeoutOrZero())
	renewed, err := r.repo.RenewLease(ctx, r.db, ids, r.owner, time.Now().Add(window))
	if err != nil {
		return err
	}
	if renewed < int64(len(ids)) {
		return fmt.Errorf("%w: renewed %d/%d", ErrLeaseLost, renewed, len(ids))
	}
	return nil
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
