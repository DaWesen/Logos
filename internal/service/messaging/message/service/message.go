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
	"Logos/pkg/outbox"

	"gorm.io/gorm"
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
	outboxRepo      outbox.OutboxRepository
}

func NewMessageService(repo dao.MessageRepository, questionService QuestionService, outboxRepo outbox.OutboxRepository) MessageService {
	return &messageServiceImpl{
		repo:            repo,
		questionService: questionService,
		outboxRepo:      outboxRepo,
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

	err := s.repo.WithTransaction(ctx, func(txRepo dao.MessageRepository) error {
		if err := txRepo.SaveMessage(ctx, msg); err != nil {
			return fmt.Errorf("failed: %w", err)
		}

		return s.saveMessageEventToOutbox(ctx, txRepo.DB(), msg)
	})
	if err != nil {
		return nil, err
	}

	if topic == 4 && s.questionService != nil {
		go func() {
			s.questionService.AskQuestion(context.Background(), content, nil)
		}()
	}

	return msg, nil
}

func (s *messageServiceImpl) saveMessageEventToOutbox(ctx context.Context, txDB *gorm.DB, msg *model.Message) error {
	event := map[string]interface{}{
		"message_id":     msg.ID,
		"topic":          msg.Topic,
		"content":        msg.Content,
		"priority":       msg.Priority,
		"headers":        msg.Headers,
		"correlation_id": msg.CorrelationID,
		"timestamp":      msg.Timestamp,
	}

	topicName := msg.Topic
	if topicName == "" || topicName == "unknown" {
		topicName = mq.TopicSystemEvent
	}

	return s.outboxRepo.SaveWithTx(ctx, txDB, topicName, msg.ID, event)
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
			logger.Warn("operation",
				logger.ErrorField(err))
			continue
		}
		results = append(results, msg)
	}

	return results, nil
}

func (s *messageServiceImpl) Subscribe(ctx context.Context, topic int32, consumerGroup string, config map[string]string) error {
	logger.Info("operation",
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

	logger.Info("operation",
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
	logger.Info("operation",
		logger.StringField("message_id", messageID))

	return s.repo.UpdateMessageStatus(ctx, messageID, "ACKED")
}

func (s *messageServiceImpl) BatchAcknowledgeMessages(ctx context.Context, consumerGroup string, messageIDs []string, topic int32) error {
	logger.Info("operation",
		logger.IntField("count", len(messageIDs)))

	for _, id := range messageIDs {
		if err := s.repo.UpdateMessageStatus(ctx, id, "ACKED"); err != nil {
			logger.Warn("operation",
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
		return nil, fmt.Errorf("failed: %w", err)
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
	logger.Info("operation",
		logger.StringField("topic", dao.TopicString(int(topic))))

	ctx := context.Background()
	topicName := dao.TopicString(int(topic))

	return s.repo.WithTransaction(ctx, func(txRepo dao.MessageRepository) error {
		event := map[string]interface{}{
			"action":    "create_topic",
			"topic":     topicName,
			"timestamp": time.Now().Format(time.RFC3339),
		}
		return s.outboxRepo.SaveWithTx(ctx, txRepo.DB(), topicName, "system-topic-create", event)
	})
}

func (s *messageServiceImpl) DeleteTopic(topic int32) error {
	topicStr := dao.TopicString(int(topic))
	logger.Info("删除消息主题",
		logger.StringField("topic", topicStr))

	ctx := context.Background()

	err := s.repo.WithTransaction(ctx, func(txRepo dao.MessageRepository) error {
		if err := txRepo.DeleteMessagesByTopic(ctx, topicStr); err != nil {
			return fmt.Errorf("删除主题消息失败: %w", err)
		}

		if err := txRepo.DeleteSubscriptionsByTopic(ctx, topicStr); err != nil {
			logger.Warn("删除主题订阅失败", logger.ErrorField(err))
		}

		event := map[string]interface{}{
			"action":    "delete_topic",
			"topic":     topicStr,
			"timestamp": time.Now().Format(time.RFC3339),
		}
		return s.outboxRepo.SaveWithTx(ctx, txRepo.DB(), mq.TopicSystemEvent, "system-topic-delete", event)
	})
	if err != nil {
		logger.Error("删除主题消息失败", logger.ErrorField(err))
		return err
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
	logger.Info("kafka operation")

	topics := []string{mq.TopicQARequest, mq.TopicRecommendation, mq.TopicSystemEvent}

	for _, topic := range topics {
		consumer := mq.NewConsumer(topic, "message-service-group")

		go func(t string, c *mq.Consumer) {
			if err := c.Subscribe(ctx, s.handleGenericMessage); err != nil {
				logger.Error("operation",
					logger.StringField("topic", t),
					logger.ErrorField(err))
			}
		}(topic, consumer)

		logger.Info("operation",
			logger.StringField("topic", topic))
	}

	return nil
}

func (s *messageServiceImpl) handleGenericMessage(msg *mq.Message) error {
	logger.Info("kafka operation",
		logger.StringField("topic", msg.Topic),
		logger.StringField("key", msg.Key))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("kafka operation",
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
		logger.Info("operation")
	default:
		logger.Info("operation")
	}

	if messageID != "" {
		ctx := context.Background()
		s.repo.UpdateMessageStatus(ctx, messageID, "PROCESSED_FROM_KAFKA")
	}

	return nil
}
