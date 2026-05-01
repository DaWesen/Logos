package mq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"Logos/config"
	"Logos/pkg/logger"

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
			Balancer:     &kafka.LeastBytes{},
			WriteTimeout: 10 * time.Second,
			ReadTimeout:  10 * time.Second,
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
			MinBytes:        10e3,
			MaxBytes:        10e6,
			MaxWait:         10 * time.Second,
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
	TopicNotification        = "notifications"
	TopicDocumentProcessed   = "document_processed"
)

func (m *KafkaManager) CreateTopic(topic string) error {
	if m == nil {
		return fmt.Errorf("kafka manager is nil")
	}
	conn, err := kafka.Dial("tcp", m.brokers[0])
	if err != nil {
		return fmt.Errorf("连接Kafka失败: %w", err)
	}
	defer conn.Close()

	err = conn.CreateTopics(kafka.TopicConfig{
		Topic:             topic,
		NumPartitions:     3,
		ReplicationFactor: 1,
	})
	if err != nil {
		logger.Warn("创建主题失败",
			logger.StringField("topic", topic),
			logger.ErrorField(err))
		return err
	}

	logger.Info("创建Kafka主题成功",
		logger.StringField("topic", topic))

	return nil
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

func (c *Consumer) Receive(ctx context.Context) (*Message, error) {
	if c == nil || c.reader == nil {
		return nil, fmt.Errorf("consumer not initialized")
	}

	msg, err := c.reader.ReadMessage(ctx)
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

func (c *Consumer) Subscribe(ctx context.Context, handler MessageHandler) error {
	if c == nil || c.reader == nil {
		return fmt.Errorf("consumer not initialized")
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, err := c.Receive(ctx)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					logger.Warn("接收消息失败", logger.ErrorField(err))
					continue
				}
				if err := handler(msg); err != nil {
					logger.Warn("处理消息失败", logger.ErrorField(err))
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

	conn, err := kafka.Dial("tcp", kafkaConfig.Brokers[0])
	if err != nil {
		logger.Warn("连接Kafka失败", logger.ErrorField(err))
		return err
	}
	defer conn.Close()

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
			Topic:             "notifications",
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
		{
			Topic:             TopicDocumentProcessed,
			NumPartitions:     3,
			ReplicationFactor: 1,
		},
	}

	for _, tc := range topicConfigs {
		logger.Info("尝试创建主题", logger.StringField("topic", tc.Topic))
		err := conn.CreateTopics(tc)
		if err != nil {
			logger.Warn("创建主题失败",
				logger.StringField("topic", tc.Topic),
				logger.ErrorField(err))
			continue
		}
		logger.Info("主题创建成功", logger.StringField("topic", tc.Topic))
	}

	return nil
}
