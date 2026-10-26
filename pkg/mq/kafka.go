package mq

import (
	"context"
	"encoding/json"
	"fmt"
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

type Consumer struct {
	reader *kafka.Reader
}

type Message struct {
	Topic     string
	Key       string
	Value     []byte
	Partition int
	Offset    int64
	Time      time.Time
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
	producerInstance *Producer
	producerOnce     sync.Once
)

func NewProducer() *Producer {
	producerOnce.Do(func() {
		kafkaConfig := config.GetConfig().Kafka
		var err error
		producerInstance, err = InitProducer(kafkaConfig.Brokers)
		if err != nil {
			logger.Warn("初始化Kafka生产者失败", logger.ErrorField(err))
		}
		ctx := context.Background()
		if err := CreateTopics(ctx); err != nil {
			logger.Warn("创建Kafka主题失败", logger.ErrorField(err))
		}
	})
	return producerInstance
}

func InitProducer(brokers []string) (*Producer, error) {
	producer := &Producer{
		writer: &kafka.Writer{
			Addr:         kafka.TCP(brokers...),
			Balancer:     &kafka.Hash{}, // 按 Key（chatID/userID）哈希分区，保证同一会话落同一分区，消息有序
			WriteTimeout: 5 * time.Second,
			ReadTimeout:  5 * time.Second,
			RequiredAcks: kafka.RequireOne, // 只需要leader确认就返回，更快
			BatchSize:    1,                // 每条消息立即发送，不批量
			BatchTimeout: 1 * time.Millisecond,
		},
	}
	return producer, nil
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
	if p == nil || p.writer == nil {
		return fmt.Errorf("producer not initialized")
	}

	msg := kafka.Message{
		Topic: topic,
		Key:   []byte(key),
		Value: value,
	}

	err := p.writer.WriteMessages(ctx, msg)
	if err != nil {
		return fmt.Errorf("发送消息失败: %w", err)
	}

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

func (m *KafkaManager) CreateTopic(topic string) error {
	if m == nil {
		return fmt.Errorf("kafka manager is nil")
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		conn, err := kafka.Dial("tcp", m.brokers[0])
		if err != nil {
			lastErr = fmt.Errorf("连接Kafka失败: %w", err)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}

		err = conn.CreateTopics(kafka.TopicConfig{
			Topic:             topic,
			NumPartitions:     3,
			ReplicationFactor: 1,
		})
		conn.Close()

		if err != nil {
			lastErr = err
			logger.Warn("创建主题失败",
				logger.StringField("topic", topic),
				logger.IntField("attempt", attempt),
				logger.ErrorField(err))
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}

		logger.Info("创建Kafka主题成功", logger.StringField("topic", topic))
		return nil
	}

	return lastErr
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
// FetchMessage 只拉取不提交，由 Subscribe 在 handler 成功后显式 CommitMessages，
// 失败则不提交、下次拉取到同一条消息重投，实现 at-least-once。
//
// 注意：调用方若直接使用 Receive 而不通过 Subscribe，需自行调用 Commit。
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
	}, nil
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
	return json.Marshal(deadLetterEnvelope{
		SourceTopic: msg.Topic,
		Partition:   msg.Partition,
		Offset:      msg.Offset,
		Key:         msg.Key,
		Error:       errMsg,
		Value:       value,
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

// failureTracker 跟踪"同一条消息（topic + partition + offset）"的连续处理失败次数，
// 用于判定毒丸消息：不同消息到达即重置计数。
// record 返回该消息此前的连续失败次数（首次为 0）。
type failureTracker struct {
	topic     string
	partition int
	offset    int64
	count     int
}

func (f *failureTracker) record(msg *Message) int {
	if msg.Topic == f.topic && msg.Partition == f.partition && msg.Offset == f.offset {
		f.count++
	} else {
		f.topic, f.partition, f.offset = msg.Topic, msg.Partition, msg.Offset
		f.count = 0
	}
	return f.count
}

// reset 清零计数但保留消息定位：
// 毒丸消息提交 offset 后再次被拉取到时，仍从第 1 次失败重新计数。
func (f *failureTracker) reset() {
	f.count = 0
}

func (c *Consumer) Subscribe(ctx context.Context, handler MessageHandler) error {
	if c == nil || c.reader == nil {
		return fmt.Errorf("consumer not initialized")
	}

	// maxConsumerFailures 同一条消息连续处理失败的上限：
	// 超过后强制提交 offset 跳过该消息，避免毒丸消息永久卡死整个分区。
	const maxConsumerFailures = 5

	go func() {
		defer func() {
			// 确保退出时关闭读取器
			_ = c.Close()
		}()
		var failures failureTracker

		for {
			select {
			case <-ctx.Done():
				// 上下文已取消，优雅退出
				return
			default:
				msg, err := c.Receive(ctx)
				if err != nil {
					// 检查是否是上下文取消或连接关闭错误
					select {
					case <-ctx.Done():
						return
					default:
					}
					// 如果是 EOF 或连接关闭错误，就不记录警告日志
					if err.Error() != "读取消息失败: fetching message: EOF" &&
						err.Error() != "读取消息失败: read tcp: read: connection reset by peer" {
						logger.Warn("接收消息失败", logger.ErrorField(err))
					}
					// 如果是严重错误，稍等一下再重试，避免死循环
					time.Sleep(100 * time.Millisecond)
					continue
				}
				// 统计同一条消息（topic + partition + offset）的连续失败次数
				failCount := failures.record(msg)

				// handler 失败不提交 offset，下次循环会重新拉取到同一条消息（at-least-once 重投）
				handlerStart := time.Now()
				if err := handler(msg); err != nil {
					obs.RecordKafkaConsume(ctx, msg.Topic, "failed", time.Since(handlerStart).Seconds())
					logger.Warn("处理消息失败，不提交 offset 等待重投",
						logger.ErrorField(err),
						logger.StringField("topic", msg.Topic),
						logger.Int64Field("offset", msg.Offset),
						logger.IntField("fail_count", failCount+1))
					// 连续失败达上限：判定为毒丸。先转存死信队列再提交 offset，
					// 避免“直接跳过”把消息永久丢弃；转存失败时落原始负载日志，保证可人工恢复。
					if failCount+1 >= maxConsumerFailures {
						logger.Error("消息连续处理失败达上限，转存死信队列后跳过",
							logger.StringField("topic", msg.Topic),
							logger.Int64Field("offset", msg.Offset),
							logger.IntField("fail_count", failCount+1))
						obs.RecordKafkaPoison(ctx, msg.Topic)
						if !c.publishDeadLetter(msg, err) {
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
						failures.reset()
					}
					continue
				}
				obs.RecordKafkaConsume(ctx, msg.Topic, "ok", time.Since(handlerStart).Seconds())
				// handler 成功后才提交 offset
				if err := c.Commit(ctx, msg); err != nil {
					logger.Warn("提交 offset 失败，下次可能重复消费",
						logger.ErrorField(err),
						logger.StringField("topic", msg.Topic),
						logger.Int64Field("offset", msg.Offset))
				}
			}
		}
	}()

	return nil
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

	topicConfigs := []kafka.TopicConfig{
		{
			Topic:             "user_events",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             "knowledge_events",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             "question_events",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             "im_messages",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             "chat_messages",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             "chat_outgoing",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             "notifications",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             TopicDocumentProcessed,
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             "bot_events",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
	}

	for _, tc := range topicConfigs {
		logger.Info("尝试创建主题", logger.StringField("topic", tc.Topic))

		var lastErr error
		for attempt := 1; attempt <= 3; attempt++ {
			// 每个 topic 独立建立连接，避免连接复用导致断开
			conn, err := kafka.Dial("tcp", kafkaConfig.Brokers[0])
			if err != nil {
				lastErr = err
				logger.Warn("连接Kafka失败",
					logger.IntField("attempt", attempt),
					logger.ErrorField(err))
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
				continue
			}

			err = conn.CreateTopics(tc)
			conn.Close()

			if err != nil {
				lastErr = err
				logger.Warn("创建主题失败",
					logger.StringField("topic", tc.Topic),
					logger.IntField("attempt", attempt),
					logger.ErrorField(err))
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
				continue
			}

			logger.Info("主题创建成功", logger.StringField("topic", tc.Topic))
			lastErr = nil
			break
		}

		if lastErr != nil {
			logger.Warn("主题最终创建失败，Kafka可能已自动创建",
				logger.StringField("topic", tc.Topic),
				logger.ErrorField(lastErr))
		}
	}

	return nil
}
