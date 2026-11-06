package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"Logos/config"
	"Logos/pkg/logger"

	"github.com/segmentio/kafka-go"
)

// dlqNow 是死信时刻的测试注入点。信封里的时间戳决定能不能按故障窗口回放，
// 单测必须能造出「窗口内/窗口外/缺字段」三类样本，而不能靠 sleep 等真实时钟。
var dlqNow = time.Now

// 死信回放的默认参数。取值原则：宁可比下游慢，也不要一次性把积压毒丸倾泻回去。
const (
	// defaultReplayRatePerSecond 单次回放的目标速率。
	defaultReplayRatePerSecond = 50
	// defaultReplayMaxReplayCount 同一条消息最多被自动回放的轮次。
	defaultReplayMaxReplayCount = 3
	// defaultReplayGroupID 读死信 topic 的消费组。
	defaultReplayGroupID = "dead-letter-replay"
	// replayFetchWait 拉取失败后的等待，避免 broker 不可用时空转。
	replayFetchWait = 200 * time.Millisecond
)

// HeaderQuarantineReason 记录一条死信记录被隔离的原因。
const HeaderQuarantineReason = "x-logos-quarantine-reason"

// 回放停止原因，供 CLI 与告警区分「正常跑完」和「被截断」。
const (
	StopReasonLimit   = "limit_reached"
	StopReasonDrained = "dlq_drained"
	StopReasonCancel  = "cancelled"
)

// replaySink 是回放所需的生产端能力，抽出接口便于单测断言投回内容。
type replaySink interface {
	SendWithHeaders(ctx context.Context, topic string, key string, value []byte, headers map[string]string) error
}

// ReplayConfig 描述一次死信回放的范围与保护阈值。
//
// 零值不是「什么都不过滤」的随意默认：Limit/RatePerSecond 等字段留空时取保守默认，
// 显式传负数才表示放开该项，避免误用一次无界回放把下游打挂。
type ReplayConfig struct {
	// DLQTopic 要扫描的死信 topic，例如 chat_messages.dlq。必填。
	DLQTopic string
	// GroupID 读死信的消费组；位点决定「上次回放到哪」。空时用默认组。
	GroupID string
	// SourceTopic 只回放原始来源等于该 topic 的死信；空表示不限。
	SourceTopic string
	// Limit 本次最多实际回放多少条，达到即停止扫描。0 用默认，负数表示不限。
	Limit int
	// Since/Until 按故障窗口 [Since, Until) 筛选；零值表示该侧不设界。
	Since time.Time
	Until time.Time
	// MaxReplayCount 已回放轮次的上限，超过则不再自动回放，交人工判断。0 用默认，负数放开。
	MaxReplayCount int
	// RatePerSecond 回放速率上限。0 用默认，负数表示不限速。
	RatePerSecond int
	// DryRun 只统计会被回放的消息，不投递也不提交位点。
	DryRun bool
}

// ReplayReport 一次回放的完整账目。每个计数字段都对应一种可解释的处置，
// 出问题时能直接回答「剩下的那些去哪了」。
type ReplayReport struct {
	Scanned int `json:"scanned"`
	// Replayed 是真正投回源 topic 的条数（DryRun 恒为 0）。
	Replayed int            `json:"replayed"`
	ByTopic  map[string]int `json:"by_topic,omitempty"`
	// Eligible 是 DryRun 下判定「可以回放」的条数。
	Eligible int `json:"eligible"`
	// Skipped* 的条目一律不提交位点：过滤不等于处理完成，提交就等于永久放弃。
	SkippedSource      int `json:"skipped_source_topic"`
	SkippedWindow      int `json:"skipped_outside_window"`
	SkippedUnknownTime int `json:"skipped_unknown_dead_letter_time"`
	SkippedReplayLimit int `json:"skipped_replay_limit"`
	// Quarantined 是死信记录本身损坏、已转存隔离队列并提交位点的条数。
	Quarantined int `json:"quarantined"`
	// Failed 是投回或隔离失败的条数，对应位点未提交、下次仍会重试。
	Failed int `json:"failed"`
	// CommitErrors 是投回成功但死信位点提交失败的条数：重启后可能重复回放，
	// 由下游按原始 message_id 的主键幂等兜底，因此不视为丢失。
	CommitErrors int `json:"commit_errors"`
	// FetchErrors 是拉取死信失败但已退避重试的次数；不中断本次回放。
	FetchErrors int    `json:"fetch_errors"`
	StopReason  string `json:"stop_reason,omitempty"`
}

func (r *ReplayReport) incTopic(topic string) {
	if r.ByTopic == nil {
		r.ByTopic = make(map[string]int, 4)
	}
	r.ByTopic[topic]++
}

// DeadLetterReplayer 把死信 topic 里的消息按原样投回源 topic。
//
// 它补的是可靠性链路的最后一公里：毒丸消息转存进 <topic>.dlq 之后，
// 之前没有任何恢复入口，「有死信队列」实际上只是「有地方堆积」，
// 下游故障恢复后仍然要人工写脚本捞。回放工具要同时满足三条才算闭环：
// 不重复（沿用原始 message_id，靠下游主键幂等）、不倾泻（限速 + 条数上限）、
// 不自锁（累计回放轮次，超限交人工，而不是无限「死信→回放→死信」）。
type DeadLetterReplayer struct {
	source messageSource
	sink   replaySink
	cfg    ReplayConfig
	// now/wait 是时钟与等待的注入点，让限速与窗口判断可测。
	now  func() time.Time
	wait func(ctx context.Context, d time.Duration) bool
}

// NewDeadLetterReplayer 用全局 Kafka 配置构造回放器。
func NewDeadLetterReplayer(cfg ReplayConfig, sink replaySink) (*DeadLetterReplayer, error) {
	if strings.TrimSpace(cfg.DLQTopic) == "" {
		return nil, errors.New("死信回放必须指定 DLQTopic")
	}
	if sink == nil {
		return nil, errors.New("死信回放必须提供 producer")
	}

	groupID := cfg.GroupID
	if strings.TrimSpace(groupID) == "" {
		groupID = defaultReplayGroupID
	}

	brokers := config.GetConfig().Kafka.Brokers
	if len(brokers) == 0 {
		return nil, errors.New("死信回放缺少 Kafka brokers 配置")
	}

	source := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    cfg.DLQTopic,
		GroupID:  groupID,
		MinBytes: 1,
		MaxBytes: 10e6,
		MaxWait:  replayFetchWait,
		// 死信 topic 通常很小，但历史可能很长；只从最早开始扫才不会漏掉旧毒丸。
		// 后续轮次靠消费组位点续扫，不会每次从头读。
		StartOffset: kafka.FirstOffset,
	})
	return newDeadLetterReplayer(source, sink, cfg), nil
}

// newDeadLetterReplayer 允许注入 source，供单测直接喂死信记录。
func newDeadLetterReplayer(source messageSource, sink replaySink, cfg ReplayConfig) *DeadLetterReplayer {
	return &DeadLetterReplayer{
		source: source,
		sink:   sink,
		cfg:    cfg,
		now:    time.Now,
		wait:   waitReplayInterval,
	}
}

// Close 释放死信 reader。
func (r *DeadLetterReplayer) Close() error {
	if r == nil || r.source == nil {
		return nil
	}
	return r.source.Close()
}

func waitReplayInterval(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Replay 扫描死信 topic 并回放符合条件的消息。
//
// 返回的 report 在出错时也尽量完整：运维需要知道「已经放回去多少条」才能决定
// 是否继续，因此这里返回 (report, err) 而不是在失败时丢掉统计。
func (r *DeadLetterReplayer) Replay(ctx context.Context) (*ReplayReport, error) {
	if r == nil || r.source == nil {
		return nil, errors.New("死信回放器未初始化")
	}
	if r.sink == nil {
		return nil, errors.New("死信回放缺少 producer")
	}

	report := &ReplayReport{}
	interval := r.paceInterval()
	maxReplays := r.maxReplayCount()
	limit := r.replayLimit()
	var nextAllowed time.Time

	for {
		if err := ctx.Err(); err != nil {
			report.StopReason = StopReasonCancel
			return report, err
		}
		if limit > 0 && report.Replayed >= limit {
			report.StopReason = StopReasonLimit
			return report, nil
		}

		km, err := r.source.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				report.StopReason = StopReasonCancel
				return report, ctx.Err()
			}
			// EOF 表示 reader 已关闭，或测试桩里的死信已读完。
			// 真实 broker 上没有新消息时不会返回 EOF，收尾靠 limit 与调用方 ctx 超时。
			if errors.Is(err, io.EOF) {
				report.StopReason = StopReasonDrained
				return report, nil
			}
			report.FetchErrors++
			logger.Warn("拉取死信记录失败，稍后重试",
				logger.StringField("dlq_topic", r.cfg.DLQTopic),
				logger.ErrorField(err))
			if !r.wait(ctx, replayFetchWait) {
				report.StopReason = StopReasonCancel
				return report, context.Cause(ctx)
			}
			continue
		}
		report.Scanned++

		env, err := parseDeadLetterEnvelope(km.Value)
		if err != nil {
			// 死信体本身不可解析：既不能猜测内容，也不能只跳过位点（下次运行仍卡在这条）。
			// 先原样转存隔离队列、再提交位点，让「放弃这一条」和「留下完整证据」同时成立。
			if qErr := r.quarantine(ctx, km, err); qErr != nil {
				report.Failed++
				return report, fmt.Errorf("隔离损坏的死信记录失败，位点未提交: %w", qErr)
			}
			if cErr := r.commit(ctx, km); cErr != nil {
				report.CommitErrors++
			}
			report.Quarantined++
			logger.Error("死信记录损坏，已转入隔离队列",
				logger.StringField("dlq_topic", km.Topic),
				logger.IntField("partition", km.Partition),
				logger.Int64Field("offset", km.Offset),
				logger.ErrorField(err))
			continue
		}

		if r.cfg.SourceTopic != "" && env.SourceTopic != r.cfg.SourceTopic {
			report.SkippedSource++
			continue
		}
		if !r.inWindow(env, report) {
			continue
		}
		if env.ReplayCount >= maxReplays {
			report.SkippedReplayLimit++
			logger.Warn("死信消息回放轮次已达上限，保留在死信队列等待人工处理",
				logger.StringField("source_topic", env.SourceTopic),
				logger.StringField("key", env.Key),
				logger.IntField("replay_count", env.ReplayCount),
				logger.IntField("max_replay_count", maxReplays))
			continue
		}

		if r.cfg.DryRun {
			report.Eligible++
			report.incTopic(env.SourceTopic)
			continue
		}

		if interval > 0 {
			if !nextAllowed.IsZero() {
				if wait := nextAllowed.Sub(r.now()); wait > 0 {
					if !r.wait(ctx, wait) {
						report.StopReason = StopReasonCancel
						return report, context.Cause(ctx)
					}
				}
			}
			// 基准取「现在」而不是「上次理论时刻 + 间隔」：长时间拉取停顿后不追赶欠下的
			// 额度，避免把限速变成一次突发。
			nextAllowed = r.now().Add(interval)
		}

		headers := map[string]string{
			HeaderReplayedFrom: km.Topic,
			HeaderReplayCount:  strconv.Itoa(env.ReplayCount + 1),
		}
		if err := r.sink.SendWithHeaders(ctx, env.SourceTopic, env.Key, env.Value, headers); err != nil {
			report.Failed++
			logger.Error("死信回放投回源 topic 失败，位点未提交",
				logger.StringField("source_topic", env.SourceTopic),
				logger.StringField("key", env.Key),
				logger.ErrorField(err))
			continue
		}

		report.Replayed++
		report.incTopic(env.SourceTopic)
		if err := r.commit(ctx, km); err != nil {
			report.CommitErrors++
			logger.Warn("死信已回放但位点提交失败，重启后可能重复回放",
				logger.StringField("dlq_topic", km.Topic),
				logger.Int64Field("offset", km.Offset),
				logger.ErrorField(err))
		}
	}
}

// inWindow 判断死信是否落在配置的故障窗口内，并顺带记录被排除的原因。
//
// 设了窗口但死信没有时刻字段时计入 SkippedUnknownTime：这类旧数据无法证明属于
// 本次故障，宁可留在死信里等人工确认，也不能猜一个 topic 投回去。
func (r *DeadLetterReplayer) inWindow(env *deadLetterEnvelope, report *ReplayReport) bool {
	if r.cfg.Since.IsZero() && r.cfg.Until.IsZero() {
		return true
	}
	if env.DeadLetteredAt == nil {
		report.SkippedUnknownTime++
		return false
	}
	at := env.DeadLetteredAt.UTC()
	if !r.cfg.Since.IsZero() && at.Before(r.cfg.Since.UTC()) {
		report.SkippedWindow++
		return false
	}
	if !r.cfg.Until.IsZero() && !at.Before(r.cfg.Until.UTC()) {
		report.SkippedWindow++
		return false
	}
	return true
}

// quarantine 把无法解析的死信记录原样转存到 <dlq>.quarantine。
// 负载与位点信息都要保留，否则隔离出来的东西无法还原现场。
func (r *DeadLetterReplayer) quarantine(ctx context.Context, km kafka.Message, cause error) error {
	headers := map[string]string{
		HeaderQuarantineReason:     cause.Error(),
		"x-logos-source-partition": strconv.Itoa(km.Partition),
		"x-logos-source-offset":    strconv.FormatInt(km.Offset, 10),
	}
	return r.sink.SendWithHeaders(ctx, km.Topic+".quarantine", string(km.Key), km.Value, headers)
}

// commit 提交死信 topic 的位点。只投回成功后才调用。
func (r *DeadLetterReplayer) commit(ctx context.Context, km kafka.Message) error {
	if err := r.source.CommitMessages(ctx, km); err != nil {
		return fmt.Errorf("提交死信位点失败: %w", err)
	}
	return nil
}

func (r *DeadLetterReplayer) replayLimit() int {
	if r.cfg.Limit < 0 {
		return 0
	}
	return r.cfg.Limit
}

func (r *DeadLetterReplayer) paceInterval() time.Duration {
	if r.cfg.RatePerSecond < 0 {
		return 0
	}
	rate := r.cfg.RatePerSecond
	if rate == 0 {
		rate = defaultReplayRatePerSecond
	}
	return time.Second / time.Duration(rate)
}

// maxReplayCount 返回自动回放轮次上限。负数表示放开限制，此时返回极大值，
// 让调用方显式承担「同一条消息可能反复进出死信」的后果。
func (r *DeadLetterReplayer) maxReplayCount() int {
	if r.cfg.MaxReplayCount < 0 {
		return int(^uint(0) >> 1)
	}
	if r.cfg.MaxReplayCount == 0 {
		return defaultReplayMaxReplayCount
	}
	return r.cfg.MaxReplayCount
}

// parseDeadLetterEnvelope 解析并校验死信记录。
//
// source_topic 缺失时必须报错：回放的唯一正确目标是原始 topic，
// 猜一个「看起来像」的 topic 投回去，比这条消息继续留在死信里严重得多。
func parseDeadLetterEnvelope(value []byte) (*deadLetterEnvelope, error) {
	if len(value) == 0 {
		return nil, errors.New("空的死信记录")
	}

	var env deadLetterEnvelope
	if err := json.Unmarshal(value, &env); err != nil {
		return nil, fmt.Errorf("死信记录不是合法 JSON: %w", err)
	}
	if strings.TrimSpace(env.SourceTopic) == "" {
		return nil, errors.New("死信记录缺少 source_topic")
	}
	if len(env.Value) == 0 {
		return nil, fmt.Errorf("死信记录缺少原始负载: source_topic=%s", env.SourceTopic)
	}
	return &env, nil
}

// replayCountFromMessage 从消息 header 读取已回放轮次。
// 缺失、非数字、负数都按 0 处理：计数只用于限制自动回放，
// 不能因为上游 header 写坏就拒绝回放一条正常消息。
func replayCountFromMessage(msg *Message) int {
	if msg == nil || len(msg.Headers) == 0 {
		return 0
	}
	raw, ok := msg.Headers[HeaderReplayCount]
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
