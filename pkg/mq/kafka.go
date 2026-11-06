package mq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"Logos/config"
	"Logos/pkg/logger"
	"Logos/pkg/obs"

	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

// messageSource 抽象 kafka.Reader 的最小能力集，便于在不依赖真实 Kafka 的单测里
// 验证「失败重试 / 提交时机 / 死信跳过」的语义。
type messageSource interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

type Consumer struct {
	reader messageSource
	// deadLetter 死信投递入口，nil 时使用 publishDeadLetter（写 <topic>.dlq）。
	deadLetter func(msg *Message, cause error) bool
	// retryBackoff 同一条消息进程内重试的初始退避，零值时用 initialConsumerRetryBackoff。
	retryBackoff time.Duration
	// retryBackoffMax 重试退避上限，零值时用 maxConsumerRetryBackoff。
	retryBackoffMax time.Duration
	// retryJitter 抖动源，返回 [0,1) 的随机数；nil 时使用全局 math/rand。
	// 做成字段是为了单测能注入确定性抖动，精确断言每轮退避值。
	retryJitter func() float64
	// retryWait 等待下一次重试，返回 false 表示 ctx 已取消。
	// 抽成字段后单测可以只记录退避值而不真的 sleep。
	retryWait func(ctx context.Context, d time.Duration) bool
}

type Message struct {
	Topic     string
	Key       string
	Value     []byte
	Partition int
	Offset    int64
	Time      time.Time
	// Headers 是 Kafka record header。只在消费侧按需物化成 map：
	// header 值本身是 []byte，直接透出会让每个 handler 都要自己处理解码与重名 key。
	Headers map[string]string
	// Context 是消费者生命周期 ctx，随消息一起传给 handler。
	//
	// handler 只拿到 *Message，如果没有这个字段，下游写库/写 ES 就只能用
	// context.Background()；进程停机时消费循环已经退出，在途写入却还在跑。
	// 它不是单条消息的 deadline，需要更短预算时由 handler 自己派生。
	Context context.Context
}

type MessageHandler func(msg *Message) error

type KafkaManager struct {
	brokers []string
}

func NewKafkaManager(brokers []string) (*KafkaManager, error) {
	if len(brokers) == 0 {
		return nil, fmt.Errorf("brokers cannot be empty")
	}
	return &KafkaManager{brokers: brokers}, nil
}

var (
	producerMu          sync.Mutex
	producerInstance    *Producer
	producerInitErr     error
	producerLastAttempt time.Time
	// producerRetryCooldown 初始化失败后的冷却时间：期间 NewProducer 直接返回 nil，
	// 避免每个请求都去重连 broker（失败时 Dial 本身就有超时，重试风暴会拖垮调用方）。
	producerRetryCooldown = 30 * time.Second
	// producerInit / producerNow 是测试注入点：生产用真实初始化与真实时钟。
	producerInit = func() (*Producer, error) {
		kafkaConfig := config.GetConfig().Kafka
		p, err := InitProducer(kafkaConfig.Brokers)
		if err != nil {
			return nil, err
		}
		if err := CreateTopics(context.Background()); err != nil {
			logger.Warn("创建Kafka主题失败", logger.ErrorField(err))
		}
		return p, nil
	}
	producerNow = time.Now
)

// NewProducer 返回进程内唯一的 Kafka 生产者，失败时允许冷却后重试。
//
// 原实现用 sync.Once：初始化失败后 once 已经触发，producerInstance 永久为 nil，
// 之后所有 Send 都返回 "producer not initialized" —— 一次启动期 broker 抖动
// 会让整个进程直到重启都发不出消息，且没有任何自动恢复路径。
// 现在改为「成功才缓存、失败记冷却」：冷却期内快速返回 nil（避免重试风暴），
// 冷却结束后重新尝试，broker 恢复后自动补齐。
func NewProducer() *Producer {
	producerMu.Lock()
	defer producerMu.Unlock()

	if producerInstance != nil {
		return producerInstance
	}
	// 冷却期内不重试；成功过之后 producerInitErr 为 nil，不会走到这里。
	if producerInitErr != nil && producerNow().Sub(producerLastAttempt) < producerRetryCooldown {
		return nil
	}

	p, err := producerInit()
	producerLastAttempt = producerNow()
	if err != nil {
		producerInitErr = err
		logger.Warn("初始化Kafka生产者失败", logger.ErrorField(err))
		return nil
	}
	producerInstance = p
	producerInitErr = nil
	return p
}

func InitProducer(brokers []string) (*Producer, error) {
	producer := &Producer{
		writer: &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Balancer:     &kafka.Hash{}, // 按 Key（chatID/userID）哈希分区，保证同一会话落同一分区，消息有序
			WriteTimeout: 5 * time.Second,
			ReadTimeout:  5 * time.Second,
			RequiredAcks: resolveRequiredAcks(config.GetConfig().Kafka),
			BatchSize:    1, // 每条消息立即发送，不批量
			BatchTimeout: 1 * time.Millisecond,
		},
	}
	return producer, nil
}

// resolveRequiredAcks 把配置字符串映射为 kafka-go 的确认级别。
// 默认 all：leader 落盘即返回（RequireOne）在 leader 宕机时仍可能丢消息，
// 只有等 ISR 全部确认，配合 ReplicationFactor >= 2 才具备 broker 级容灾。
func resolveRequiredAcks(cfg config.Kafka) kafka.RequiredAcks {
	switch strings.ToLower(strings.TrimSpace(cfg.RequiredAcks)) {
	case "none":
		return kafka.RequireNone
	case "leader", "one":
		return kafka.RequireOne
	default:
		return kafka.RequireAll
	}
}

// kafkaPartitions 新建 topic 的分区数，未配置时默认 6。
// 分区数是出站并行度的上限：topic 建好后无法减少分区，扩容只能新增。
func kafkaPartitions(cfg config.Kafka) int {
	if cfg.Partitions > 0 {
		return cfg.Partitions
	}
	return 6
}

// kafkaReplicationFactor 副本数，未配置时默认 1（开发单 broker）。
// 生产环境应配置 >= 2，否则任一 broker 宕机都会让对应分区不可读不可写。
func kafkaReplicationFactor(cfg config.Kafka) int {
	if cfg.ReplicationFactor > 0 {
		return cfg.ReplicationFactor
	}
	return 1
}

func NewConsumer(topic string, groupID string) *Consumer {
	kafkaConfig := config.GetConfig().Kafka
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:         kafkaConfig.Brokers,
			Topic:           topic,
			GroupID:         groupID,
			MinBytes:        1, // 只要有1字节就返回
			MaxBytes:        10e6,
			MaxWait:         100 * time.Millisecond, // 最多等100ms
			ReadLagInterval: time.Second,
		}),
	}
}

func (p *Producer) Send(ctx context.Context, topic string, key string, value []byte) error {
	return p.SendWithHeaders(ctx, topic, key, value, nil)
}

// SendWithHeaders 发送带 Kafka header 的消息。
//
// 链路元数据（例如死信回放次数、来源死信 topic）走 header 而不是塞进 value：
// 下游消费者的 JSON schema 无需为此变更，不认识这些 header 的旧版本消费者
// 会直接忽略，回滚时不用协调两侧发布顺序。
func (p *Producer) SendWithHeaders(ctx context.Context, topic string, key string, value []byte, headers map[string]string) error {
	if p == nil || p.writer == nil {
		obs.RecordKafkaProduce(ctx, topic, "failed", 0)
		return fmt.Errorf("producer not initialized")
	}

	msg := kafka.Message{
		Topic:   topic,
		Key:     []byte(key),
		Value:   value,
		Headers: headersToSlice(headers),
	}

	start := time.Now()
	err := p.writer.WriteMessages(ctx, msg)
	elapsed := time.Since(start).Seconds()
	if err != nil {
		// 生产失败打点走 failed；消费侧对这条消息不会有任何记录，
		// 这是写链路唯一能暴露 broker 不可用的位置。
		obs.RecordKafkaProduce(ctx, topic, "failed", elapsed)
		return fmt.Errorf("发送消息失败: %w", err)
	}

	obs.RecordKafkaProduce(ctx, topic, "ok", elapsed)
	return nil
}

const (
	TopicDataCollection      = "data_collection"
	TopicKnowledgeExtraction = "knowledge_extraction"
	TopicVectorProcessing    = "vector_processing"
	TopicQARequest           = "qa_request"
	TopicRecommendation      = "recommendation"
	TopicUserActivity        = "user_activity"
	TopicSystemEvent         = "system_event"
	TopicIM                  = "im_messages"
	TopicChat                = "chat_messages"
	TopicChatOutgoing        = "chat_outgoing"
	TopicNotification        = "notifications"
	TopicDocumentProcessed   = "document_processed"
)

const topicCreateMaxAttempts = 3

// errKafkaBrokerUnreachable 标记「连不上 broker」，与「broker 拒绝了 topic 配置」区分：
// 前者对同一个地址重试剩余 topic 只会重复同一个失败，后者其他 topic 仍可能成功。
var errKafkaBrokerUnreachable = errors.New("kafka broker unreachable")

// topicAdmin 是创建主题所需的最小连接能力，*kafka.Conn 天然满足。
type topicAdmin interface {
	CreateTopics(...kafka.TopicConfig) error
	Close() error
}

type topicAdminDialer func(addr string) (topicAdmin, error)

func dialTopicAdmin(addr string) (topicAdmin, error) {
	return kafka.Dial("tcp", addr)
}

// topicRetryWait 是建主题重试的等待实现，返回 false 表示 ctx 已取消。
// 与 Consumer.retryWait 同一思路：独立成可替换实现，单测不必真的 sleep。
var topicRetryWait = func(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (m *KafkaManager) CreateTopic(topic string) error {
	if m == nil {
		return fmt.Errorf("kafka manager is nil")
	}
	if strings.TrimSpace(topic) == "" {
		return fmt.Errorf("topic 不能为空")
	}
	if len(m.brokers) == 0 {
		// 零值 KafkaManager 曾在这里直接 m.brokers[0] panic。
		return fmt.Errorf("kafka brokers 为空，无法创建主题 %s", topic)
	}

	return createTopicWithRetry(
		context.Background(), dialTopicAdmin, topicRetryWait,
		m.brokers[0], config.GetConfig().Kafka, topic,
	)
}

func createTopicWithRetry(
	ctx context.Context,
	dial topicAdminDialer,
	wait func(context.Context, time.Duration) bool,
	addr string,
	cfg config.Kafka,
	topic string,
) error {
	if dial == nil {
		dial = dialTopicAdmin
	}
	if wait == nil {
		wait = topicRetryWait
	}

	var lastErr error
	for attempt := 1; attempt <= topicCreateMaxAttempts; attempt++ {
		conn, err := dial(addr)
		if err != nil {
			lastErr = fmt.Errorf("%w: %w", errKafkaBrokerUnreachable, err)
			logger.Warn("连接Kafka失败",
				logger.StringField("topic", topic),
				logger.IntField("attempt", attempt),
				logger.ErrorField(err))
		} else {
			err = conn.CreateTopics(kafka.TopicConfig{
				Topic:             topic,
				NumPartitions:     kafkaPartitions(cfg),
				ReplicationFactor: kafkaReplicationFactor(cfg),
			})
			_ = conn.Close()
			if err == nil {
				logger.Info("创建Kafka主题成功", logger.StringField("topic", topic))
				return nil
			}
			lastErr = err
			logger.Warn("创建主题失败",
				logger.StringField("topic", topic),
				logger.IntField("attempt", attempt),
				logger.ErrorField(err))
		}

		// 最后一次失败后不再白等一个退避周期：启动路径上的 2s/4s 会直接变成
		// 首个请求的延迟。ctx 取消时立即返回，不再消耗剩余尝试。
		if attempt == topicCreateMaxAttempts {
			break
		}
		if !wait(ctx, time.Duration(attempt)*2*time.Second) {
			return errors.Join(
				fmt.Errorf("创建主题 %s 时被取消", topic),
				lastErr,
				ctx.Err(),
			)
		}
	}

	return fmt.Errorf("创建主题 %s 失败（已尝试 %d 次）: %w", topic, topicCreateMaxAttempts, lastErr)
}

func (p *Producer) SendUserEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["user_events"]
	if topic == "" {
		topic = "user_events"
	}
	return p.Send(ctx, topic, key, value)
}

func (p *Producer) SendKnowledgeEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["knowledge_events"]
	if topic == "" {
		topic = "knowledge_events"
	}
	return p.Send(ctx, topic, key, value)
}

func (p *Producer) SendQuestionEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["question_events"]
	if topic == "" {
		topic = "question_events"
	}
	return p.Send(ctx, topic, key, value)
}

func (p *Producer) SendIMEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["im_messages"]
	if topic == "" {
		topic = "im_messages"
	}
	return p.Send(ctx, topic, key, value)
}

func (p *Producer) SendChatEvent(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["chat_messages"]
	if topic == "" {
		topic = "chat_messages"
	}
	return p.Send(ctx, topic, key, value)
}

func (p *Producer) SendChatOutgoingEvent(ctx context.Context, key string, value []byte) error {
	return p.Send(ctx, TopicChatOutgoing, key, value)
}

func (p *Producer) SendNotification(ctx context.Context, key string, value []byte) error {
	cfg := config.GetConfig()
	topic := cfg.Kafka.Topics["notifications"]
	if topic == "" {
		topic = "notifications"
	}
	return p.Send(ctx, topic, key, value)
}

func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}

// Receive 拉取下一条消息，但不提交 offset。
//
// 使用 FetchMessage 而非 ReadMessage：ReadMessage 会在返回前自动提交 offset，
// 导致 handler 还没执行消息就被标记为已消费（at-most-once）。
// FetchMessage 只拉取不提交，提交时机由 Subscribe 控制。
//
// 注意：FetchMessage 会推进 reader 的内存游标，未提交的消息**不会**在本进程内被
// 再次拉取到；因此失败必须由 Subscribe 就地重试（见 processWithRetry），
// 否则后续消息的提交会把失败消息的 offset 一并越过，直接丢失。
//
// 调用方若直接使用 Receive 而不通过 Subscribe，需自行调用 Commit。
func (c *Consumer) Receive(ctx context.Context) (*Message, error) {
	if c == nil || c.reader == nil {
		return nil, fmt.Errorf("consumer not initialized")
	}

	msg, err := c.reader.FetchMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取消息失败: %w", err)
	}

	return &Message{
		Topic:     msg.Topic,
		Key:       string(msg.Key),
		Value:     msg.Value,
		Partition: msg.Partition,
		Offset:    msg.Offset,
		Time:      msg.Time,
		Headers:   headersToMap(msg.Headers),
	}, nil
}

// 死信与回放链路的 Kafka header 约定。
const (
	// HeaderReplayCount 记录这条消息已经被回放的次数。回放后再次失败进死信时，
	// publishDeadLetter 会把该 header 写回信封，形成跨轮次的累计计数。
	HeaderReplayCount = "x-logos-replay-count"
	// HeaderReplayedFrom 记录这条消息由哪个死信 topic 回放而来，供排查与审计定位。
	HeaderReplayedFrom = "x-logos-replayed-from"
)

// headersToMap 把 kafka header 列表压成 map。同名 key 保留最后一个值，
// 与 kafka-go 生产端「后写覆盖」的直觉一致。空列表返回 nil，避免每条消息都分配 map。
func headersToMap(hdrs []kafka.Header) map[string]string {
	if len(hdrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(hdrs))
	for _, h := range hdrs {
		out[h.Key] = string(h.Value)
	}
	return out
}

// headersToSlice 按 key 升序生成 header 列表。
// 排序是为了让同一组 header 的编码结果稳定：单测可以直接断言字节内容，
// 排查时同一条消息重复回放也能在日志里对齐比较。
func headersToSlice(headers map[string]string) []kafka.Header {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]kafka.Header, 0, len(keys))
	for _, k := range keys {
		out = append(out, kafka.Header{Key: k, Value: []byte(headers[k])})
	}
	return out
}

// deadLetterEnvelope 死信队列消息体：保留原始消息与失败原因，
// 便于人工排查并原样回放到源 topic。
type deadLetterEnvelope struct {
	SourceTopic string          `json:"source_topic"`
	Partition   int             `json:"partition"`
	Offset      int64           `json:"offset"`
	Key         string          `json:"key"`
	Error       string          `json:"error"`
	Value       json.RawMessage `json:"value"`
	// DeadLetteredAt 记录消息进入死信的时刻。没有它就只能全量重放，
	// 把已经恢复的下游再打一遍；有了它才能按故障窗口筛选。
	// 用指针区分「字段缺失」（旧数据）与「零值」，缺失时回放工具会显式拒绝按窗口过滤。
	DeadLetteredAt *time.Time `json:"dead_lettered_at,omitempty"`
	// ReplayCount 是已累计的回放轮次，用于阻断「回放→再失败→再死信→再回放」的循环。
	ReplayCount int `json:"replay_count,omitempty"`
}

// buildDeadLetterPayload 构造死信消息体。
// 原始负载是合法 JSON 时原样保留，便于排查后直接回放；
// 非法 JSON 包装成字符串，保证死信体本身始终是合法 JSON。
func buildDeadLetterPayload(msg *Message, cause error) ([]byte, error) {
	value := json.RawMessage(msg.Value)
	if !json.Valid(value) {
		encoded, err := json.Marshal(string(msg.Value))
		if err != nil {
			return nil, err
		}
		value = encoded
	}

	errMsg := ""
	if cause != nil {
		errMsg = cause.Error()
	}
	at := dlqNow().UTC()
	return json.Marshal(deadLetterEnvelope{
		SourceTopic:    msg.Topic,
		Partition:      msg.Partition,
		Offset:         msg.Offset,
		Key:            msg.Key,
		Error:          errMsg,
		Value:          value,
		DeadLetteredAt: &at,
		// 回放过的消息再次进死信时，轮次继续沿用上游 header 里的计数，
		// 否则每一轮都会被当成「第一次回放」，循环保护永远不会触发。
		ReplayCount: replayCountFromMessage(msg),
	})
}

// publishDeadLetter 把毒丸消息原样转存到 <topic>.dlq。
// 只有转存成功后才允许跳过该 offset，否则消息会随 offset 提交永久丢失。
// 返回 false 表示三次尝试均失败，调用方需落原始消息日志以便人工恢复。
func (c *Consumer) publishDeadLetter(msg *Message, cause error) bool {
	producer := NewProducer()
	if producer == nil {
		return false
	}

	payload, err := buildDeadLetterPayload(msg, cause)
	if err != nil {
		logger.Error("构造死信消息体失败", logger.ErrorField(err))
		return false
	}

	dlqTopic := msg.Topic + ".dlq"
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := producer.Send(ctx, dlqTopic, msg.Key, payload)
		cancel()
		if err == nil {
			logger.Info("毒丸消息已转存死信队列",
				logger.StringField("topic", msg.Topic),
				logger.StringField("dlq_topic", dlqTopic),
				logger.Int64Field("offset", msg.Offset))
			return true
		}
		logger.Warn("毒丸消息转存死信队列失败",
			logger.StringField("topic", msg.Topic),
			logger.StringField("dlq_topic", dlqTopic),
			logger.Int64Field("offset", msg.Offset),
			logger.IntField("attempt", attempt),
			logger.ErrorField(err))
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	return false
}

// Commit 提交指定消息的 offset。
// 在 at-least-once 语义下，应由 Subscribe 在 handler 成功后调用。
// 失败时不调用，下次 FetchMessage 会拉取到同一条消息重投。
func (c *Consumer) Commit(ctx context.Context, msg *Message) error {
	if c == nil || c.reader == nil {
		return fmt.Errorf("consumer not initialized")
	}
	return c.reader.CommitMessages(ctx, kafka.Message{
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
		Key:       []byte(msg.Key),
		Value:     msg.Value,
	})
}

// 消费失败后的重试参数。
const (
	// maxConsumerFailures 同一条消息在进程内的最大处理次数：
	// 超过后转存死信并提交 offset，避免毒丸消息永久卡住整个分区。
	maxConsumerFailures = 5
	// initialConsumerRetryBackoff 首次重试前的退避，之后逐次翻倍。
	initialConsumerRetryBackoff = 200 * time.Millisecond
	// maxConsumerRetryBackoff 单次重试退避上限，避免一条消息阻塞分区过久。
	maxConsumerRetryBackoff = 2 * time.Second
)

// RetryBackoff 计算第 attempt 次失败后、下一次重试前的等待时长（attempt 从 1 开始）。
//
// 计算分两步：
//  1. 指数增长：base * 2^(attempt-1)，并截断到 max；
//  2. 等量抖动（equal jitter）：最终值落在 [d/2, d] 区间，jitter 取 [0,1)。
//
// 为什么需要抖动：同一条消息的失败往往来自下游整体故障，而不是这条消息本身。
// 若退避是固定的，多个消费者/多个分区会在同一毫秒集中重试，把刚恢复的下游
// 再次打垮（重试风暴）。抖动把重试时间摊开，同时保留一半固定等待，
// 避免随机数把退避压到接近 0 —— 那等于没有退避。
//
// 纯函数 + 显式入参（含 jitter）让策略可断言：单测不需要 sleep 就能验证
// 序列、上限与抖动区间。base <= 0 返回 0（调用方显式关闭退避）；
// jitter <= 0 返回基础值（确定性，便于测试与排障）。
func RetryBackoff(attempt int, base, max time.Duration, jitter float64) time.Duration {
	if base <= 0 {
		return 0
	}
	if attempt < 1 {
		attempt = 1
	}

	d := base
	for i := 1; i < attempt; i++ {
		if max > 0 && d >= max {
			d = max
			break
		}
		// 溢出保护：base 极大且 attempt 很高时 d*2 会变成负数，
		// 此时直接取上限（或退回当前值），避免负数退避变成「立即重试」。
		if d > (1<<62)/2 {
			if max > 0 {
				d = max
			}
			break
		}
		d *= 2
	}
	if max > 0 && d > max {
		d = max
	}

	if jitter <= 0 {
		return d
	}
	if jitter > 1 {
		jitter = 1
	}
	half := d / 2
	return half + time.Duration(float64(half)*jitter)
}

// retryJitter 返回 [0,1) 的抖动系数；字段为 nil 时用全局随机源。
func (c *Consumer) retryJitterValue() float64 {
	if c != nil && c.retryJitter != nil {
		return c.retryJitter()
	}
	return rand.Float64()
}

// waitBeforeRetry 等待下一次重试；返回 false 表示 ctx 已取消，调用方应立即退出。
func (c *Consumer) waitBeforeRetry(ctx context.Context, d time.Duration) bool {
	if c != nil && c.retryWait != nil {
		return c.retryWait(ctx, d)
	}
	if d <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (c *Consumer) Subscribe(ctx context.Context, handler MessageHandler) error {
	if c == nil || c.reader == nil {
		return fmt.Errorf("consumer not initialized")
	}

	go c.consumeLoop(ctx, handler)
	return nil
}

// consumeLoop 串行消费：拉取 → 就地重试处理 → 提交，同一时刻只持有一条消息。
func (c *Consumer) consumeLoop(ctx context.Context, handler MessageHandler) {
	defer func() {
		// 确保退出时关闭读取器
		_ = c.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := c.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// io.EOF 表示 reader 已关闭（正常退出），不打 Warn；其余错误降噪后重试。
			if !isExpectedFetchError(err) {
				logger.Warn("接收消息失败", logger.ErrorField(err))
			}
			// 稍等一下再重试，避免拉取失败时死循环
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		if !c.processWithRetry(ctx, msg, handler) {
			return
		}
	}
}

// isExpectedFetchError 判断拉取错误是否属于「正常关闭」类。
// 用 errors.Is 而不是比字符串：kafka-go 升级后错误文案随时可能变，字符串匹配会静默失效。
func isExpectedFetchError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// ErrHandlerPanic 标记消息处理器 panic。
//
// 处理器 panic 若不拦截，会直接击穿 consumeLoop 的 goroutine：该分区的消费
// 永久停止（进程还活着、位点不再推进、也没有任何指标），只能靠重启恢复。
// 把 panic 归一成「本条消息处理失败」，才能复用既有的「就地重试 → 死信」路径。
var ErrHandlerPanic = errors.New("message handler panicked")

// ErrSkipMessage 表示消息本身无法处理，重试也不会成功（例如缺必填字段、
// 不支持的枚举值）。消费者会直接提交 offset 跳过，不进入重试与死信队列。
//
// 用它而不是返回普通 error：普通 error 会触发 5 次指数退避，把「数据问题」
// 当成「下游抖动」，既拖慢分区消费，也会把无效消息塞进死信队列。
var ErrSkipMessage = errors.New("message skipped by handler")

// invokeHandler 调用处理器并兜住 panic，返回错误而不是让 goroutine 崩溃。
// 用独立函数而不是在循环里 defer：defer 只能兜住一次调用，
// 这里显式把「调用 + 恢复」放在一起，语义更清楚，也便于单测直接验证。
func invokeHandler(handler MessageHandler, msg *Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrHandlerPanic, r)
		}
	}()
	return handler(msg)
}

// processWithRetry 对同一条消息就地重试，直到成功或达到 maxConsumerFailures。
//
// 为什么必须就地重试，而不是「失败就不提交、等下一次拉取重投」：
// kafka-go 的 FetchMessage 会推进 reader 的内存游标，未提交的消息在本进程内不会
// 再次返回；如果继续消费下一条，后续 CommitMessages 会把 offset 推到更大值，
// 这条失败消息就被永久越过 —— 实际语义退化成 at-most-once（丢消息），
// 且「连续失败计数」永远涨不上去，死信队列形同虚设。
//
// 返回 false 表示 ctx 已取消（调用方应立即退出循环），此时该消息未提交，
// 重启后会从 last committed offset 重新投递。
func (c *Consumer) processWithRetry(ctx context.Context, msg *Message, handler MessageHandler) bool {
	// 把消费循环 ctx 交给 handler：下游写库/写 ES 才能随停机一起取消。
	// 只覆盖消息级字段，不改变 handler 签名，既有消费者无需改动。
	msg.Context = ctx

	base := c.retryBackoff
	if base <= 0 {
		base = initialConsumerRetryBackoff
	}
	maxBackoff := c.retryBackoffMax
	if maxBackoff <= 0 {
		maxBackoff = maxConsumerRetryBackoff
	}

	for attempt := 1; ; attempt++ {
		handlerStart := time.Now()
		err := invokeHandler(handler, msg)
		elapsed := time.Since(handlerStart).Seconds()

		if err == nil {
			obs.RecordKafkaConsume(ctx, msg.Topic, "ok", elapsed)
			// 处理成功后才提交 offset
			if cerr := c.Commit(ctx, msg); cerr != nil {
				logger.Warn("提交 offset 失败，重启后可能重复消费",
					logger.ErrorField(cerr),
					logger.StringField("topic", msg.Topic),
					logger.Int64Field("offset", msg.Offset))
			}
			return true
		}

		// panic 单独打标签：failed 说明「业务处理出错」，panic 说明「代码有 bug」，
		// 两者的告警等级与处置方式不同，混在一起会让真正的崩溃被普通失败淹没。
		consumeResult := "failed"
		switch {
		case errors.Is(err, ErrHandlerPanic):
			consumeResult = "panic"
		case errors.Is(err, ErrSkipMessage):
			consumeResult = "skipped"
		}
		obs.RecordKafkaConsume(ctx, msg.Topic, consumeResult, elapsed)

		// 不可恢复的消息：记录后直接提交 offset，不重试、不进死信。
		// 死信队列是给「可能恢复」的消息兜底的；确定无效的消息放进去只会污染队列。
		if errors.Is(err, ErrSkipMessage) {
			logger.Warn("消息不可恢复，跳过",
				logger.ErrorField(err),
				logger.StringField("topic", msg.Topic),
				logger.Int64Field("offset", msg.Offset))
			if cerr := c.Commit(ctx, msg); cerr != nil {
				logger.Warn("跳过不可恢复消息时提交 offset 失败",
					logger.ErrorField(cerr),
					logger.StringField("topic", msg.Topic),
					logger.Int64Field("offset", msg.Offset))
			}
			return true
		}

		logger.Warn("处理消息失败，就地重试",
			logger.ErrorField(err),
			logger.StringField("topic", msg.Topic),
			logger.Int64Field("offset", msg.Offset),
			logger.IntField("attempt", attempt))

		if attempt >= maxConsumerFailures {
			// 判定为毒丸：先转存死信队列再提交 offset，避免「直接跳过」把消息永久丢弃；
			// 转存失败时落原始负载日志，保证可人工恢复。
			logger.Error("消息连续处理失败达上限，转存死信队列后跳过",
				logger.StringField("topic", msg.Topic),
				logger.Int64Field("offset", msg.Offset),
				logger.IntField("attempt", attempt))
			obs.RecordKafkaPoison(ctx, msg.Topic)

			deadLetter := c.deadLetter
			if deadLetter == nil {
				deadLetter = c.publishDeadLetter
			}
			if !deadLetter(msg, err) {
				logger.Error("死信转存失败，原始消息已记录，需人工恢复",
					logger.StringField("topic", msg.Topic),
					logger.IntField("partition", msg.Partition),
					logger.Int64Field("offset", msg.Offset),
					logger.StringField("raw", string(msg.Value)))
			}
			if cerr := c.Commit(ctx, msg); cerr != nil {
				logger.Warn("跳过毒丸消息时提交 offset 失败",
					logger.ErrorField(cerr),
					logger.StringField("topic", msg.Topic),
					logger.Int64Field("offset", msg.Offset))
			}
			return true
		}

		// 记录一次真正会发生的重试（毒丸路径在上面已 return，不计入 retry）。
		// retry 与 poison 分开统计，才能区分「下游短暂抖动」和「持续故障」。
		obs.RecordKafkaRetry(ctx, msg.Topic)

		// 退避按「第几次失败」计算（attempt 从 1 开始），指数增长 + 等量抖动，
		// 既有上限又避免多消费者同步重试；ctx 取消时立即退出且不提交 offset。
		if !c.waitBeforeRetry(ctx, RetryBackoff(attempt, base, maxBackoff, c.retryJitterValue())) {
			return false
		}
	}
}

func (c *Consumer) Close() error {
	if c == nil || c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

func CreateTopics(ctx context.Context) error {
	kafkaConfig := config.GetConfig().Kafka
	logger.Info("Kafka主题配置",
		logger.AnyField("topics", kafkaConfig.Topics),
		logger.AnyField("brokers", kafkaConfig.Brokers))

	if len(kafkaConfig.Brokers) == 0 {
		logger.Warn("Kafka brokers配置为空")
		return nil
	}

	return createTopics(ctx, kafkaConfig, dialTopicAdmin, topicRetryWait)
}

// createTopics 逐个创建主题并聚合失败原因。
// 单个 topic 失败不阻断其余 topic（它们可能配置正确），但错误必须返回给调用方——
// 旧实现无论如何都返回 nil，调用方看到的「初始化成功」和真实状态无关。
func createTopics(
	ctx context.Context,
	kafkaConfig config.Kafka,
	dial topicAdminDialer,
	wait func(context.Context, time.Duration) bool,
) error {
	topicNames := []string{
		"user_events",
		"knowledge_events",
		"question_events",
		"im_messages",
		"chat_messages",
		"chat_outgoing",
		"notifications",
		TopicDocumentProcessed,
		"bot_events",
	}

	var errs []error
	for i, name := range topicNames {
		if ctxErr := ctx.Err(); ctxErr != nil {
			errs = append(errs, fmt.Errorf("创建 Kafka 主题被取消: %w", ctxErr))
			break
		}

		logger.Info("尝试创建主题", logger.StringField("topic", name))
		err := createTopicWithRetry(ctx, dial, wait, kafkaConfig.Brokers[0], kafkaConfig, name)
		if err == nil {
			continue
		}
		errs = append(errs, err)
		if errors.Is(err, errKafkaBrokerUnreachable) {
			// 同一个 broker 连不上，剩下 8 个主题只会重复同样的 2s+4s。
			// 旧实现在这里会对每个 topic 各重试 3 次，最坏把首个请求拖住近两分钟。
			logger.Error("Kafka broker 不可达，停止创建剩余主题",
				logger.StringField("topic", name),
				logger.IntField("remaining", len(topicNames)-i-1))
			break
		}
	}

	if len(errs) > 0 {
		logger.Error("Kafka 主题初始化未全部成功", logger.IntField("failed", len(errs)))
		return errors.Join(errs...)
	}

	logger.Info("Kafka 主题全部就绪", logger.IntField("topics", len(topicNames)))
	return nil
}
