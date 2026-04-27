package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/messaging/message/dao"
	"Logos/internal/messaging/message/model"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
)

type QuestionService interface {
	AskQuestion(ctx context.Context, question string, userID *string) (interface{ GetID() string }, error)
}

type MessageService interface {
	SendMessage(ctx context.Context, topic int32, content string, priority *int32, headers map[string]string, correlationID *string) (*model.Message, error)
	BatchSendMessage(ctx context.Context, messages []struct {
		Topic         int32
		Content       string
		Priority      *int32
		Headers       map[string]string
		CorrelationID *string
	}) ([]*model.Message, error)

	Subscribe(ctx context.Context, topic int32, consumerGroup string, config map[string]string) error
	ConsumeMessages(ctx context.Context, consumerGroup string, topic int32, maxMessages int32, timeoutMs int32) ([]*model.Message, error)
	AcknowledgeMessage(ctx context.Context, consumerGroup string, messageID string, topic int32) error
	BatchAcknowledgeMessages(ctx context.Context, consumerGroup string, messageIDs []string, topic int32) error

	GetMessageStats() ([]map[string]interface{}, error)
	CreateTopic(topic int32) error
	DeleteTopic(topic int32) error
	ClearMessages(topic int32) error
	StartKafkaConsumer(ctx context.Context) error
}

type messageServiceImpl struct {
	repo            dao.MessageRepository
	questionService QuestionService
	kafkaProducer   *mq.Producer
}

func NewMessageService(repo dao.MessageRepository, questionService QuestionService) MessageService {
	return &messageServiceImpl{
		repo:            repo,
		questionService: questionService,
		kafkaProducer:   mq.NewProducer(),
	}
}

func (s *messageServiceImpl) SendMessage(ctx context.Context, topic int32, content string, priority *int32, headers map[string]string, correlationID *string) (*model.Message, error) {
	logger.Info("发送消息",
		logger.IntField("topic", int(topic)),
		logger.IntField("content_len", len(content)))

	priorityVal := int32(2)
	if priority != nil {
		priorityVal = *priority
	}

	msg := &model.Message{
		ID:            uuid.New().String(),
		Topic:         dao.TopicString(int(topic)),
		Content:       content,
		Priority:      int(priorityVal),
		Headers:       dao.MarshalHeaders(headers),
		CorrelationID: correlationID,
		Timestamp:     time.Now().UnixMilli(),
		Status:        "PENDING",
		CreatedAt:     time.Now(),
	}

	if err := s.repo.SaveMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("保存消息失败: %w", err)
	}

	s.sendToKafka(ctx, msg)

	if topic == 4 && s.questionService != nil {
		go func() {
			s.questionService.AskQuestion(context.Background(), content, nil)
		}()
	}

	return msg, nil
}

func (s *messageServiceImpl) sendToKafka(ctx context.Context, msg *model.Message) {
	if s.kafkaProducer == nil {
		logger.Warn("Kafka生产者未初始化，跳过Kafka发送")
		return
	}

	event := map[string]interface{}{
		"message_id":     msg.ID,
		"topic":          msg.Topic,
		"content":        msg.Content,
		"priority":       msg.Priority,
		"headers":        msg.Headers,
		"correlation_id": msg.CorrelationID,
		"timestamp":      msg.Timestamp,
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		logger.Error("序列化Kafka消息失败",
			logger.ErrorField(err))
		return
	}

	topicName := msg.Topic
	if topicName == "" || topicName == "unknown" {
		topicName = mq.TopicSystemEvent
	}

	if err := s.kafkaProducer.Send(ctx, topicName, msg.ID, eventData); err != nil {
		logger.Error("发送消息到Kafka失败",
			logger.StringField("kafka_topic", topicName),
			logger.ErrorField(err))
		return
	}

	logger.Info("消息已发送到Kafka",
		logger.StringField("kafka_topic", topicName),
		logger.StringField("message_id", msg.ID))
}

func (s *messageServiceImpl) BatchSendMessage(ctx context.Context, msgs []struct {
	Topic         int32
	Content       string
	Priority      *int32
	Headers       map[string]string
	CorrelationID *string
}) ([]*model.Message, error) {
	var results []*model.Message

	for _, m := range msgs {
		msg, err := s.SendMessage(ctx, m.Topic, m.Content, m.Priority, m.Headers, m.CorrelationID)
		if err != nil {
			logger.Warn("批量发送消息中单条失败",
				logger.ErrorField(err))
			continue
		}
		results = append(results, msg)
	}

	return results, nil
}

func (s *messageServiceImpl) Subscribe(ctx context.Context, topic int32, consumerGroup string, config map[string]string) error {
	logger.Info("订阅主题",
		logger.StringField("topic", dao.TopicString(int(topic))),
		logger.StringField("consumer_group", consumerGroup))

	sub := &model.MessageSubscription{
		ID:            uuid.New().String(),
		Topic:         dao.TopicString(int(topic)),
		ConsumerGroup: consumerGroup,
		Config:        dao.MarshalHeaders(config),
		Status:        "ACTIVE",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	return s.repo.CreateSubscription(ctx, sub)
}

func (s *messageServiceImpl) ConsumeMessages(ctx context.Context, consumerGroup string, topic int32, maxMessages int32, timeoutMs int32) ([]*model.Message, error) {
	topicStr := dao.TopicString(int(topic))

	logger.Info("消费消息",
		logger.StringField("consumer_group", consumerGroup),
		logger.StringField("topic", topicStr),
		logger.IntField("max_messages", int(maxMessages)))

	limit := int(maxMessages)
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	msgs, err := s.repo.ListPendingMessages(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("查询待消费消息失败: %w", err)
	}

	for _, msg := range msgs {
		s.repo.UpdateMessageStatus(ctx, msg.ID, "PROCESSED")
	}

	return msgs, nil
}

func (s *messageServiceImpl) AcknowledgeMessage(ctx context.Context, consumerGroup string, messageID string, topic int32) error {
	logger.Info("确认消息",
		logger.StringField("message_id", messageID))

	return s.repo.UpdateMessageStatus(ctx, messageID, "ACKED")
}

func (s *messageServiceImpl) BatchAcknowledgeMessages(ctx context.Context, consumerGroup string, messageIDs []string, topic int32) error {
	logger.Info("批量确认消息",
		logger.IntField("count", len(messageIDs)))

	for _, id := range messageIDs {
		if err := s.repo.UpdateMessageStatus(ctx, id, "ACKED"); err != nil {
			logger.Warn("确认消息失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
			continue
		}
	}
	return nil
}

func (s *messageServiceImpl) GetMessageStats() ([]map[string]interface{}, error) {
	total, pending, processed, failed, err := s.repo.GetMessageStats(context.Background(), "")
	if err != nil {
		return nil, fmt.Errorf("获取全局统计失败: %w", err)
	}

	topics := []int32{1, 2, 3, 4, 5, 6, 7}
	var stats []map[string]interface{}

	globalStat := map[string]interface{}{
		"topic":              "ALL",
		"total_messages":     total,
		"pending_messages":   pending,
		"processed_messages": processed,
		"error_messages":     failed,
	}
	stats = append(stats, globalStat)

	for _, t := range topics {
		tTotal, tPending, tProcessed, tFailed, tErr := s.repo.GetMessageStats(context.Background(), dao.TopicString(int(t)))
		if tErr != nil {
			continue
		}
		stat := map[string]interface{}{
			"topic":              dao.TopicString(int(t)),
			"total_messages":     tTotal,
			"pending_messages":   tPending,
			"processed_messages": tProcessed,
			"error_messages":     tFailed,
		}
		stats = append(stats, stat)
	}

	return stats, nil
}

func (s *messageServiceImpl) CreateTopic(topic int32) error {
	logger.Info("创建消息主题",
		logger.StringField("topic", dao.TopicString(int(topic))))

	if s.kafkaProducer != nil {
		ctx := context.Background()
		topicName := dao.TopicString(int(topic))
		if err := s.kafkaProducer.Send(ctx, topicName, "system-topic-create", []byte(fmt.Sprintf(`{"action":"create_topic","topic":"%s","timestamp":"%s"}`, topicName, time.Now().Format(time.RFC3339)))); err != nil {
			logger.Warn("发送主题创建事件到Kafka失败",
				logger.ErrorField(err))
		} else {
			logger.Info("已发送主题创建事件到Kafka",
				logger.StringField("topic", topicName))
		}
	}

	return nil
}

func (s *messageServiceImpl) DeleteTopic(topic int32) error {
	topicStr := dao.TopicString(int(topic))
	logger.Info("删除消息主题",
		logger.StringField("topic", topicStr))

	return nil
}

func (s *messageServiceImpl) ClearMessages(topic int32) error {
	topicStr := dao.TopicString(int(topic))
	logger.Info("清空消息",
		logger.StringField("topic", topicStr))

	return nil
}

func (s *messageServiceImpl) StartKafkaConsumer(ctx context.Context) error {
	logger.Info("启动Message Service的Kafka消费者")

	topics := []string{mq.TopicQARequest, mq.TopicRecommendation, mq.TopicSystemEvent}

	for _, topic := range topics {
		consumer := mq.NewConsumer(topic, "message-service-group")

		go func(t string, c *mq.Consumer) {
			if err := c.Subscribe(ctx, s.handleGenericMessage); err != nil {
				logger.Error("订阅消息失败",
					logger.StringField("topic", t),
					logger.ErrorField(err))
			}
		}(topic, consumer)

		logger.Info("已启动消费者",
			logger.StringField("topic", topic))
	}

	return nil
}

func (s *messageServiceImpl) handleGenericMessage(msg *mq.Message) error {
	logger.Info("收到Kafka消息",
		logger.StringField("topic", msg.Topic),
		logger.StringField("key", msg.Key))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("解析Kafka消息失败",
			logger.ErrorField(err))
		return err
	}

	messageID, _ := event["message_id"].(string)
	content, _ := event["content"].(string)

	switch msg.Topic {
	case mq.TopicQARequest:
		if content != "" && s.questionService != nil {
			go func() {
				s.questionService.AskQuestion(context.Background(), content, nil)
			}()
		}
	case mq.TopicRecommendation:
		logger.Info("处理推荐请求消息")
	default:
		logger.Info("处理系统事件消息")
	}

	if messageID != "" {
		ctx := context.Background()
		s.repo.UpdateMessageStatus(ctx, messageID, "PROCESSED_FROM_KAFKA")
	}

	return nil
}
