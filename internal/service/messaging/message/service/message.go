package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/messaging/message/dao"
	"Logos/internal/service/messaging/message/model"
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
	logger.Info("SendMessage",
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
		return nil, fmt.Errorf("������Ϣʧ��: %w", err)
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
		logger.Warn("Kafka������δ��ʼ��������Kafka����")
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
		logger.Error("���л�Kafka��Ϣʧ��",
			logger.ErrorField(err))
		return
	}

	topicName := msg.Topic
	if topicName == "" || topicName == "unknown" {
		topicName = mq.TopicSystemEvent
	}

	if err := s.kafkaProducer.Send(ctx, topicName, msg.ID, eventData); err != nil {
		logger.Error("������Ϣ��Kafkaʧ��",
			logger.StringField("kafka_topic", topicName),
			logger.ErrorField(err))
		return
	}

	logger.Info("��Ϣ�ѷ��͵�Kafka",
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
			logger.Warn("����������Ϣ�е���ʧ��",
				logger.ErrorField(err))
			continue
		}
		results = append(results, msg)
	}

	return results, nil
}

func (s *messageServiceImpl) Subscribe(ctx context.Context, topic int32, consumerGroup string, config map[string]string) error {
	logger.Info("��������",
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

	logger.Info("������Ϣ",
		logger.StringField("consumer_group", consumerGroup),
		logger.StringField("topic", topicStr),
		logger.IntField("max_messages", int(maxMessages)))

	limit := int(maxMessages)
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	var msgs []*model.Message
	var err error

	if topicStr != "" && topicStr != "UNKNOWN(0)" {
		msgs, err = s.repo.ListPendingMessagesByTopic(ctx, topicStr, limit)
	} else {
		msgs, err = s.repo.ListPendingMessages(ctx, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("查询待处理消息失败: %w", err)
	}

	for _, msg := range msgs {
		s.repo.UpdateMessageStatus(ctx, msg.ID, "PROCESSED")
	}

	return msgs, nil
}

func (s *messageServiceImpl) AcknowledgeMessage(ctx context.Context, consumerGroup string, messageID string, topic int32) error {
	logger.Info("ȷ����Ϣ",
		logger.StringField("message_id", messageID))

	return s.repo.UpdateMessageStatus(ctx, messageID, "ACKED")
}

func (s *messageServiceImpl) BatchAcknowledgeMessages(ctx context.Context, consumerGroup string, messageIDs []string, topic int32) error {
	logger.Info("����ȷ����Ϣ",
		logger.IntField("count", len(messageIDs)))

	for _, id := range messageIDs {
		if err := s.repo.UpdateMessageStatus(ctx, id, "ACKED"); err != nil {
			logger.Warn("ȷ����Ϣʧ��",
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
		return nil, fmt.Errorf("��ȡȫ��ͳ��ʧ��: %w", err)
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
	logger.Info("������Ϣ����",
		logger.StringField("topic", dao.TopicString(int(topic))))

	if s.kafkaProducer != nil {
		ctx := context.Background()
		topicName := dao.TopicString(int(topic))
		if err := s.kafkaProducer.Send(ctx, topicName, "system-topic-create", []byte(fmt.Sprintf(`{"action":"create_topic","topic":"%s","timestamp":"%s"}`, topicName, time.Now().Format(time.RFC3339)))); err != nil {
			logger.Warn("�������ⴴ���¼���Kafkaʧ��",
				logger.ErrorField(err))
		} else {
			logger.Info("�ѷ������ⴴ���¼���Kafka",
				logger.StringField("topic", topicName))
		}
	}

	return nil
}

func (s *messageServiceImpl) DeleteTopic(topic int32) error {
	topicStr := dao.TopicString(int(topic))
	logger.Info("删除消息主题",
		logger.StringField("topic", topicStr))

	ctx := context.Background()

	if err := s.repo.DeleteMessagesByTopic(ctx, topicStr); err != nil {
		logger.Error("删除主题消息失败", logger.ErrorField(err))
		return fmt.Errorf("删除主题消息失败: %w", err)
	}

	if err := s.repo.DeleteSubscriptionsByTopic(ctx, topicStr); err != nil {
		logger.Warn("删除主题订阅失败", logger.ErrorField(err))
	}

	if s.kafkaProducer != nil {
		notificationData := []byte(fmt.Sprintf(`{"action":"delete_topic","topic":"%s","timestamp":"%s"}`, topicStr, time.Now().Format(time.RFC3339)))
		if err := s.kafkaProducer.Send(ctx, mq.TopicSystemEvent, "system-topic-delete", notificationData); err != nil {
			logger.Warn("发送主题删除事件到Kafka失败", logger.ErrorField(err))
		}
	}

	logger.Info("主题已删除", logger.StringField("topic", topicStr))
	return nil
}

func (s *messageServiceImpl) ClearMessages(topic int32) error {
	topicStr := dao.TopicString(int(topic))
	logger.Info("清除消息",
		logger.StringField("topic", topicStr))

	ctx := context.Background()

	if err := s.repo.ClearMessagesByTopic(ctx, topicStr); err != nil {
		logger.Error("清除主题消息失败", logger.ErrorField(err))
		return fmt.Errorf("清除主题消息失败: %w", err)
	}

	logger.Info("主题消息已清除", logger.StringField("topic", topicStr))
	return nil
}

func (s *messageServiceImpl) StartKafkaConsumer(ctx context.Context) error {
	logger.Info("����Message Service��Kafka������")

	topics := []string{mq.TopicQARequest, mq.TopicRecommendation, mq.TopicSystemEvent}

	for _, topic := range topics {
		consumer := mq.NewConsumer(topic, "message-service-group")

		go func(t string, c *mq.Consumer) {
			if err := c.Subscribe(ctx, s.handleGenericMessage); err != nil {
				logger.Error("������Ϣʧ��",
					logger.StringField("topic", t),
					logger.ErrorField(err))
			}
		}(topic, consumer)

		logger.Info("������������",
			logger.StringField("topic", topic))
	}

	return nil
}

func (s *messageServiceImpl) handleGenericMessage(msg *mq.Message) error {
	logger.Info("�յ�Kafka��Ϣ",
		logger.StringField("topic", msg.Topic),
		logger.StringField("key", msg.Key))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("����Kafka��Ϣʧ��",
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
		logger.Info("�����Ƽ�������Ϣ")
	default:
		logger.Info("����ϵͳ�¼���Ϣ")
	}

	if messageID != "" {
		ctx := context.Background()
		s.repo.UpdateMessageStatus(ctx, messageID, "PROCESSED_FROM_KAFKA")
	}

	return nil
}
