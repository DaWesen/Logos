package mq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// fakeReplaySink 记录每一次投回，用于断言 topic、key、负载与 header。
type fakeReplaySink struct {
	mu   sync.Mutex
	sent []replaySend
	// failFor 让指定 topic 的投递失败，模拟下游仍不可用。
	failFor map[string]error
}

type replaySend struct {
	Topic   string
	Key     string
	Value   []byte
	Headers map[string]string
}

func newFakeReplaySink() *fakeReplaySink {
	return &fakeReplaySink{failFor: make(map[string]error)}
}

func (f *fakeReplaySink) SendWithHeaders(_ context.Context, topic string, key string, value []byte, headers map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err, ok := f.failFor[topic]; ok && err != nil {
		return err
	}
	f.sent = append(f.sent, replaySend{Topic: topic, Key: key, Value: value, Headers: headers})
	return nil
}

func (f *fakeReplaySink) sentFor(topic string) []replaySend {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []replaySend
	for _, s := range f.sent {
		if s.Topic == topic {
			out = append(out, s)
		}
	}
	return out
}

// flakyReplaySource 在 fakeSource 之上增加「先失败若干次，再正常返回」的拉取序列。
type flakyReplaySource struct {
	mu   sync.Mutex
	msgs []kafka.Message
	// errs 是待消费的故障队列：非空时本次 fetch 返回队首错误并出队。
	errs    []error
	fetched int
	commits []kafka.Message
}

func (s *flakyReplaySource) FetchMessage(context.Context) (kafka.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return kafka.Message{}, err
		}
	}
	if s.fetched >= len(s.msgs) {
		return kafka.Message{}, io.EOF
	}
	msg := s.msgs[s.fetched]
	s.fetched++
	return msg, nil
}

func (s *flakyReplaySource) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits = append(s.commits, msgs...)
	return nil
}

func (s *flakyReplaySource) Close() error { return nil }

// dlqRecord 构造一条死信 topic 上的记录。
func dlqRecord(t *testing.T, offset int64, env deadLetterEnvelope) kafka.Message {
	t.Helper()

	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("序列化死信信封失败: %v", err)
	}
	return kafka.Message{
		Topic:     "chat_messages.dlq",
		Partition: 0,
		Offset:    offset,
		Key:       []byte(env.Key),
		Value:     payload,
	}
}

func validEnvelope(sourceTopic, key, content string, deadAt *time.Time, replayCount int) deadLetterEnvelope {
	return deadLetterEnvelope{
		SourceTopic:    sourceTopic,
		Partition:      1,
		Offset:         42,
		Key:            key,
		Error:          "db timeout",
		Value:          json.RawMessage(`{"id":"` + content + `"}`),
		DeadLetteredAt: deadAt,
		ReplayCount:    replayCount,
	}
}

// newTestReplayer 构造不依赖真实 Kafka 的回放器，时钟固定、等待只记录时长。
func newTestReplayer(source messageSource, sink replaySink, cfg ReplayConfig) (*DeadLetterReplayer, *[]time.Duration) {
	var waits []time.Duration
	r := newDeadLetterReplayer(source, sink, cfg)
	r.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	r.wait = func(ctx context.Context, d time.Duration) bool {
		waits = append(waits, d)
		return ctx.Err() == nil
	}
	return r, &waits
}

func TestReplay_ReplaysOriginalPayloadAndCommitsOnlyAfterSuccess(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "chat-1", "msg-1", nil, 0)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "chat-2", "msg-2", nil, 0)),
	)
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.Replayed != 2 || report.Scanned != 2 || report.StopReason != StopReasonDrained {
		t.Fatalf("账目不符: %+v", report)
	}
	if report.ByTopic["chat_messages"] != 2 {
		t.Fatalf("按 topic 统计缺失: %+v", report.ByTopic)
	}

	sent := sink.sentFor("chat_messages")
	if len(sent) != 2 {
		t.Fatalf("应投回 2 条，got %d", len(sent))
	}
	// key 必须沿用原始分区键：换 key 会让同一会话的消息落到不同分区，回放顺序失去意义。
	if sent[0].Key != "chat-1" || sent[1].Key != "chat-2" {
		t.Fatalf("分区键未保留: %q, %q", sent[0].Key, sent[1].Key)
	}
	if string(sent[0].Value) != `{"id":"msg-1"}` {
		t.Fatalf("原始负载被改写: %s", sent[0].Value)
	}
	if got := sent[0].Headers[HeaderReplayCount]; got != "1" {
		t.Fatalf("%s = %q, want 1", HeaderReplayCount, got)
	}
	if got := sent[0].Headers[HeaderReplayedFrom]; got != "chat_messages.dlq" {
		t.Fatalf("%s = %q, want chat_messages.dlq", HeaderReplayedFrom, got)
	}

	if len(source.commits) != 2 {
		t.Fatalf("回放成功应提交死信位点，got %d 次", len(source.commits))
	}
}

func TestReplay_ProduceFailureKeepsOffsetUncommitted(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "chat-1", "msg-1", nil, 0)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "chat-2", "msg-2", nil, 0)),
	)
	sink := newFakeReplaySink()
	sink.failFor["chat_messages"] = errors.New("broker down")
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("投回失败不应中断整轮回放: %v", err)
	}
	if report.Failed != 2 || report.Replayed != 0 {
		t.Fatalf("账目不符: %+v", report)
	}
	// 一条都没回放成功，位点绝不能推进，否则死信里的消息就永久消失了。
	if len(source.commits) != 0 {
		t.Fatalf("投回失败不得提交位点，got %d 次", len(source.commits))
	}
}

func TestReplay_FilterSkipsNeverCommit(t *testing.T) {
	inside := time.Unix(1_700_000_100, 0).UTC()
	outside := time.Unix(1_600_000_000, 0).UTC()

	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("other_topic", "k", "a", &inside, 0)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "k", "b", &outside, 0)),
		dlqRecord(t, 3, validEnvelope("chat_messages", "k", "c", nil, 0)),
		dlqRecord(t, 4, validEnvelope("chat_messages", "k", "d", &inside, 0)),
	)
	sink := newFakeReplaySink()
	cfg := ReplayConfig{
		DLQTopic:      "chat_messages.dlq",
		SourceTopic:   "chat_messages",
		Since:         time.Unix(1_699_999_000, 0).UTC(),
		RatePerSecond: -1,
	}
	r, _ := newTestReplayer(source, sink, cfg)

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.SkippedSource != 1 || report.SkippedWindow != 1 || report.SkippedUnknownTime != 1 {
		t.Fatalf("过滤计数不符: %+v", report)
	}
	if report.Replayed != 1 {
		t.Fatalf("窗口内应只回放 1 条，got %d", report.Replayed)
	}
	// 被过滤的三条绝不能提交位点：过滤不等于处理完成，提交等于永久放弃。
	if len(source.commits) != 1 {
		t.Fatalf("只应提交已回放的那条，got %d 次", len(source.commits))
	}
	if source.commits[0].Offset != 4 {
		t.Fatalf("提交的位点应为 offset 4，got %d", source.commits[0].Offset)
	}
}

func TestReplay_ReplayLimitBlocksAutoReplay(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "k", "fresh", nil, defaultReplayMaxReplayCount-1)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "k", "stale", nil, defaultReplayMaxReplayCount)),
		dlqRecord(t, 3, validEnvelope("chat_messages", "k", "beyond", nil, defaultReplayMaxReplayCount+5)),
	)
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.Replayed != 1 || report.SkippedReplayLimit != 2 {
		t.Fatalf("超限消息不得自动回放: %+v", report)
	}
	if len(sink.sentFor("chat_messages")) != 1 {
		t.Fatalf("只应投回 1 条")
	}
}

func TestReplay_MaxReplayCountNegativeDisablesGuard(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "k", "a", nil, 9999)),
	)
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{
		DLQTopic:       "chat_messages.dlq",
		MaxReplayCount: -1,
		RatePerSecond:  -1,
	})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.Replayed != 1 {
		t.Fatalf("显式放开上限时应回放: %+v", report)
	}
	if got := sink.sentFor("chat_messages")[0].Headers[HeaderReplayCount]; got != "10000" {
		t.Fatalf("回放轮次应继续累加，got %q", got)
	}
}

func TestReplay_DryRunProducesAndCommitsNothing(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "k", "a", nil, 0)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "k", "b", nil, 0)),
	)
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", DryRun: true, RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.Eligible != 2 || report.Replayed != 0 {
		t.Fatalf("DryRun 只统计不回放: %+v", report)
	}
	if len(sink.sent) != 0 {
		t.Fatalf("DryRun 不得投递任何消息，got %d", len(sink.sent))
	}
	if len(source.commits) != 0 {
		t.Fatalf("DryRun 不得推进位点")
	}
}

func TestReplay_MalformedRecordGoesToQuarantineBeforeCommit(t *testing.T) {
	cases := []struct {
		name  string
		value []byte
	}{
		{"not_json", []byte("plain text poison pill")},
		{"missing_source_topic", []byte(`{"key":"k","value":{"id":"x"}}`)},
		{"missing_payload", []byte(`{"source_topic":"chat_messages","key":"k"}`)},
		{"empty", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := newFakeSource(kafka.Message{
				Topic: "chat_messages.dlq", Partition: 2, Offset: 7, Key: []byte("k"), Value: tc.value,
			})
			sink := newFakeReplaySink()
			r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

			report, err := r.Replay(context.Background())
			if err != nil {
				t.Fatalf("Replay 返回错误: %v", err)
			}
			if report.Quarantined != 1 || report.Replayed != 0 {
				t.Fatalf("损坏记录应进隔离队列: %+v", report)
			}

			quarantined := sink.sentFor("chat_messages.dlq.quarantine")
			if len(quarantined) != 1 {
				t.Fatalf("隔离队列应收到 1 条，got %d", len(quarantined))
			}
			// 隔离必须保留原始字节与分区/位点，否则无法还原现场。
			if string(quarantined[0].Value) != string(tc.value) {
				t.Fatalf("隔离负载被改写")
			}
			if quarantined[0].Headers["x-logos-source-partition"] != "2" ||
				quarantined[0].Headers["x-logos-source-offset"] != "7" {
				t.Fatalf("隔离记录缺少现场信息: %+v", quarantined[0].Headers)
			}
			if quarantined[0].Headers[HeaderQuarantineReason] == "" {
				t.Fatalf("隔离记录缺少原因")
			}
			if len(source.commits) != 1 {
				t.Fatalf("隔离成功后才允许提交位点")
			}
		})
	}
}

func TestReplay_QuarantineFailureAbortsWithoutCommit(t *testing.T) {
	source := newFakeSource(kafka.Message{
		Topic: "chat_messages.dlq", Partition: 0, Offset: 3, Value: []byte("broken"),
	})
	sink := newFakeReplaySink()
	sink.failFor["chat_messages.dlq.quarantine"] = errors.New("producer not initialized")
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err == nil {
		t.Fatalf("隔离失败必须上抛，不能让运维以为回放已完成")
	}
	if report.Failed != 1 || report.Quarantined != 0 {
		t.Fatalf("账目不符: %+v", report)
	}
	// 隔离写失败 + 提交位点 = 消息既没进隔离队列也没了位点，属于永久丢失。
	if len(source.commits) != 0 {
		t.Fatalf("隔离失败时不得提交位点")
	}
}

func TestReplay_LimitStopsAfterReachingBudget(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "k", "a", nil, 0)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "k", "b", nil, 0)),
		dlqRecord(t, 3, validEnvelope("chat_messages", "k", "c", nil, 0)),
	)
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", Limit: 2, RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.Replayed != 2 || report.Scanned != 2 {
		t.Fatalf("达到上限后不得继续扫描: %+v", report)
	}
	if report.StopReason != StopReasonLimit {
		t.Fatalf("停止原因应为 %s，got %q", StopReasonLimit, report.StopReason)
	}
}

func TestReplay_RateLimitPacesEachReplay(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "k", "a", nil, 0)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "k", "b", nil, 0)),
		dlqRecord(t, 3, validEnvelope("chat_messages", "k", "c", nil, 0)),
	)
	sink := newFakeReplaySink()
	r, waits := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: 100})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.Replayed != 3 {
		t.Fatalf("应回放 3 条: %+v", report)
	}
	// 第一条不等；之后每条都等满 1/100 秒。基准取当前时刻而非理论时刻，
	// 因此长时间停顿后不会累积出「追赶式突发」。
	if len(*waits) != 2 {
		t.Fatalf("限速等待次数不符: %v", *waits)
	}
	for _, w := range *waits {
		if w != 10*time.Millisecond {
			t.Fatalf("限速间隔 = %v, want 10ms", w)
		}
	}
}

func TestReplay_DefaultRateIsConservative(t *testing.T) {
	r := newDeadLetterReplayer(nil, nil, ReplayConfig{DLQTopic: "chat_messages.dlq"})
	if got := r.paceInterval(); got != time.Second/time.Duration(defaultReplayRatePerSecond) {
		t.Fatalf("默认限速间隔不符: %v", got)
	}
	if got := r.maxReplayCount(); got != defaultReplayMaxReplayCount {
		t.Fatalf("默认回放轮次上限不符: %d", got)
	}
}

func TestReplay_FetchErrorBacksOffAndContinues(t *testing.T) {
	src := &flakyReplaySource{
		msgs: []kafka.Message{dlqRecord(t, 1, validEnvelope("chat_messages", "k", "a", nil, 0))},
		errs: []error{errors.New("dial tcp: connection refused")},
	}
	sink := newFakeReplaySink()
	r, waits := newTestReplayer(src, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("瞬时拉取失败不应让整轮回放报错: %v", err)
	}
	if report.FetchErrors != 1 || report.Replayed != 1 {
		t.Fatalf("拉取失败应退避后继续: %+v", report)
	}
	if len(*waits) != 1 || (*waits)[0] != replayFetchWait {
		t.Fatalf("拉取失败后应退避 %v，got %v", replayFetchWait, *waits)
	}
	if len(src.commits) != 1 {
		t.Fatalf("回放成功后应提交位点")
	}
}

func TestReplay_CanceledContextReportsProgress(t *testing.T) {
	source := newFakeSource(
		dlqRecord(t, 1, validEnvelope("chat_messages", "k", "a", nil, 0)),
		dlqRecord(t, 2, validEnvelope("chat_messages", "k", "b", nil, 0)),
	)
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	ctx, cancel := context.WithCancel(context.Background())
	r.wait = func(context.Context, time.Duration) bool {
		cancel()
		return false
	}
	// 限速打开，让第二条在等待处感知取消。
	r.cfg.RatePerSecond = 1

	report, err := r.Replay(ctx)
	if err == nil {
		t.Fatalf("ctx 取消必须返回错误，让调用方知道回放未完成")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误应可被 errors.Is 识别，got %v", err)
	}
	if report.StopReason != StopReasonCancel {
		t.Fatalf("停止原因应为 cancelled，got %q", report.StopReason)
	}
	// 出错时也要交出已完成的账目：运维据此判断已经放回多少条。
	if report.Replayed != 1 {
		t.Fatalf("取消前应已回放 1 条: %+v", report)
	}
}

func TestReplay_RejectsMissingConfiguration(t *testing.T) {
	if _, err := NewDeadLetterReplayer(ReplayConfig{}, newFakeReplaySink()); err == nil {
		t.Fatalf("未指定死信 topic 必须报错")
	}
	if _, err := NewDeadLetterReplayer(ReplayConfig{DLQTopic: "chat_messages.dlq"}, nil); err == nil {
		t.Fatalf("未提供 producer 必须报错")
	}
}

// TestReplay_BuildsEnvelopeWithDeadLetterTimeAndInheritedReplayCount 验证死信信封
// 带上了回放所需的两项元数据：进死信的时刻、从 header 继承的回放轮次。
func TestReplay_BuildsEnvelopeWithDeadLetterTimeAndInheritedReplayCount(t *testing.T) {
	fixed := time.Unix(1_700_000_500, 0).UTC()
	origDLQNow := dlqNow
	dlqNow = func() time.Time { return fixed }
	t.Cleanup(func() { dlqNow = origDLQNow })

	msg := &Message{
		Topic:     "chat_messages",
		Partition: 3,
		Offset:    99,
		Key:       "chat-1",
		Value:     []byte(`{"id":"m-1"}`),
		Headers:   map[string]string{HeaderReplayCount: "2"},
	}

	payload, err := buildDeadLetterPayload(msg, errors.New("db timeout"))
	if err != nil {
		t.Fatalf("构造死信体失败: %v", err)
	}

	env, err := parseDeadLetterEnvelope(payload)
	if err != nil {
		t.Fatalf("死信体必须能被回放器解析: %v", err)
	}
	if env.DeadLetteredAt == nil || !env.DeadLetteredAt.Equal(fixed) {
		t.Fatalf("死信时刻不符: %v", env.DeadLetteredAt)
	}
	if env.ReplayCount != 2 {
		t.Fatalf("回放轮次未从 header 继承，got %d", env.ReplayCount)
	}
	if string(env.Value) != `{"id":"m-1"}` {
		t.Fatalf("原始负载必须原样保留: %s", env.Value)
	}
}

// TestReplay_ReplayCountSurvivesRepeatedDeadLettering 打通完整闭环：
// 死信 → 回放（header 计数 +1）→ 再次失败进死信（从 header 继承计数）→ ……
// 直到达到上限后回放器拒绝再自动回放。缺任何一环，毒丸都会无限循环。
func TestReplay_ReplayCountSurvivesRepeatedDeadLettering(t *testing.T) {
	original := &Message{
		Topic:     "chat_messages",
		Key:       "chat-1",
		Value:     []byte(`{"id":"m-1"}`),
		Partition: 0,
		Offset:    1,
	}

	current := original
	for round := 0; round <= defaultReplayMaxReplayCount; round++ {
		if current.Headers == nil {
			current.Headers = map[string]string{}
		}
		if got := replayCountFromMessage(current); got != round {
			t.Fatalf("第 %d 轮回放前 header 计数应为 %d，got %d", round, round, got)
		}

		dlqPayload, err := buildDeadLetterPayload(current, errors.New("db timeout"))
		if err != nil {
			t.Fatalf("第 %d 轮构造死信体失败: %v", round, err)
		}
		env, err := parseDeadLetterEnvelope(dlqPayload)
		if err != nil {
			t.Fatalf("第 %d 轮解析死信体失败: %v", round, err)
		}
		if env.ReplayCount != round {
			t.Fatalf("第 %d 轮死信信封应记录已回放 %d 次，got %d", round, round, env.ReplayCount)
		}

		source := newFakeSource(kafka.Message{
			Topic: "chat_messages.dlq", Offset: int64(round), Value: dlqPayload,
		})
		sink := newFakeReplaySink()
		r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

		report, err := r.Replay(context.Background())
		if err != nil {
			t.Fatalf("第 %d 轮回放报错: %v", round, err)
		}

		if round < defaultReplayMaxReplayCount {
			if report.Replayed != 1 {
				t.Fatalf("第 %d 轮应自动回放: %+v", round, report)
			}
			sent := sink.sentFor("chat_messages")
			if sent[0].Headers[HeaderReplayCount] != strconv.Itoa(round+1) {
				t.Fatalf("第 %d 轮回放 header 计数应为 %d，got %q",
					round, round+1, sent[0].Headers[HeaderReplayCount])
			}
			// 下一轮：把回放出去的消息再喂回消费者，模拟它再次失败进死信。
			current = &Message{
				Topic:     sent[0].Topic,
				Key:       sent[0].Key,
				Value:     sent[0].Value,
				Partition: 0,
				Offset:    int64(round + 1),
				Headers:   sent[0].Headers,
			}
			continue
		}

		if report.SkippedReplayLimit != 1 || report.Replayed != 0 {
			t.Fatalf("达到上限后必须停止自动回放并交人工: %+v", report)
		}
		if len(source.commits) != 0 {
			t.Fatalf("超限消息必须留在死信里，位点不得推进")
		}
	}
}

func TestReplayCountFromMessage(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"nil message", nil, 0},
		{"absent", map[string]string{"other": "1"}, 0},
		{"valid", map[string]string{HeaderReplayCount: "3"}, 3},
		{"with spaces", map[string]string{HeaderReplayCount: " 2 "}, 2},
		{"not a number", map[string]string{HeaderReplayCount: "abc"}, 0},
		{"negative", map[string]string{HeaderReplayCount: "-1"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &Message{Headers: tc.headers}
			if got := replayCountFromMessage(msg); got != tc.want {
				t.Fatalf("replayCountFromMessage() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHeadersRoundTrip(t *testing.T) {
	headers := map[string]string{HeaderReplayCount: "1", HeaderReplayedFrom: "chat_messages.dlq"}

	slice := headersToSlice(headers)
	if len(slice) != 2 {
		t.Fatalf("应有 2 个 header，got %d", len(slice))
	}
	// 顺序必须稳定：否则单测无法直接比对字节，日志里同一消息的两轮也难以对齐。
	if !strings.HasPrefix(slice[0].Key, "x-logos-replay") || !strings.Contains(slice[0].Key, "count") {
		t.Fatalf("header 未按 key 升序: %q, %q", slice[0].Key, slice[1].Key)
	}
	if got := headersToMap(slice)[HeaderReplayCount]; got != "1" {
		t.Fatalf("header 往返丢失: %v", headersToMap(slice))
	}
	if headersToMap(nil) != nil {
		t.Fatalf("空 header 不应分配 map")
	}
	if headersToSlice(map[string]string{}) != nil {
		t.Fatalf("空 header 不应生成切片")
	}
}

func TestReplay_CloseReleasesSource(t *testing.T) {
	source := newFakeSource()
	r := newDeadLetterReplayer(source, newFakeReplaySink(), ReplayConfig{DLQTopic: "chat_messages.dlq"})
	if err := r.Close(); err != nil {
		t.Fatalf("Close 返回错误: %v", err)
	}
	if !source.closed {
		t.Fatalf("Close 必须释放死信 reader")
	}
}

// TestReplay_RestoresOriginalHeadersAndOverridesReplayMarkers 验证回放的元数据保真：
// 原始 header 要原样还原，回放标记要以本轮为准，不能被信封里的旧值覆盖。
// 少了任何一半，回放出去的消息都和原消息不是同一条。
func TestReplay_RestoresOriginalHeadersAndOverridesReplayMarkers(t *testing.T) {
	env := validEnvelope("chat_messages", "chat-1", "msg-1", nil, 1)
	env.Headers = map[string]string{
		"x-tenant-id":      "tenant-7",
		"x-trace-id":       "trace-abc",
		HeaderReplayCount:  "1",
		HeaderReplayedFrom: "stale.dlq",
	}

	source := newFakeSource(dlqRecord(t, 1, env))
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	if _, err := r.Replay(context.Background()); err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}

	sent := sink.sentFor("chat_messages")
	if len(sent) != 1 {
		t.Fatalf("应投回 1 条，got %d", len(sent))
	}
	if got := sent[0].Headers["x-tenant-id"]; got != "tenant-7" {
		t.Fatalf("租户 header 丢失: %q", got)
	}
	if got := sent[0].Headers["x-trace-id"]; got != "trace-abc" {
		t.Fatalf("追踪 header 丢失: %q", got)
	}
	// 旧值分别是 1 和 stale.dlq：必须以本轮为准，否则循环保护与溯源都会失真。
	if got := sent[0].Headers[HeaderReplayCount]; got != "2" {
		t.Fatalf("回放轮次应覆盖为 2，got %q", got)
	}
	if got := sent[0].Headers[HeaderReplayedFrom]; got != "chat_messages.dlq" {
		t.Fatalf("来源死信 topic 应覆盖为本次，got %q", got)
	}
}

// TestReplay_LegacyHeaderCountStillTripsLoopGuard 覆盖旧格式死信：
// replay_count 字段是后加的，升级前的信封只有 header 里的计数。
// 只读字段会让计数从 0 重新开始，循环保护形同虚设。
func TestReplay_LegacyHeaderCountStillTripsLoopGuard(t *testing.T) {
	env := validEnvelope("chat_messages", "chat-1", "msg-1", nil, 0)
	env.Headers = map[string]string{HeaderReplayCount: strconv.Itoa(defaultReplayMaxReplayCount)}

	source := newFakeSource(dlqRecord(t, 1, env))
	sink := newFakeReplaySink()
	r, _ := newTestReplayer(source, sink, ReplayConfig{DLQTopic: "chat_messages.dlq", RatePerSecond: -1})

	report, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay 返回错误: %v", err)
	}
	if report.SkippedReplayLimit != 1 || report.Replayed != 0 {
		t.Fatalf("旧信封的 header 计数必须参与上限判断: %+v", report)
	}
	if len(source.commits) != 0 {
		t.Fatalf("超限消息不得提交位点")
	}
}

// TestBuildDeadLetterPayload_PreservesOriginalHeaders 验证死信阶段就把 header 存进信封。
// 回放能还原的前提是写入时没有丢；nil header 不能生成空对象，否则旧格式字节会变。
func TestBuildDeadLetterPayload_PreservesOriginalHeaders(t *testing.T) {
	msg := &Message{
		Topic:   "chat_messages",
		Key:     "chat-1",
		Value:   []byte(`{"id":"m-1"}`),
		Headers: map[string]string{"x-tenant-id": "tenant-7"},
	}
	payload, err := buildDeadLetterPayload(msg, errors.New("db timeout"))
	if err != nil {
		t.Fatalf("构造死信体失败: %v", err)
	}
	env, err := parseDeadLetterEnvelope(payload)
	if err != nil {
		t.Fatalf("解析死信体失败: %v", err)
	}
	if got := env.Headers["x-tenant-id"]; got != "tenant-7" {
		t.Fatalf("死信信封未保留原始 header: %v", env.Headers)
	}

	msg.Headers = nil
	payload, err = buildDeadLetterPayload(msg, errors.New("db timeout"))
	if err != nil {
		t.Fatalf("构造无 header 死信体失败: %v", err)
	}
	if strings.Contains(string(payload), `"headers"`) {
		t.Fatalf("空 header 不应写进信封: %s", payload)
	}
}
