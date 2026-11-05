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

// maxBatchSize 限制单次批量发送的消息数。
// 整批在一个事务里写入，必须给事务一个确定上界，避免调用方传入超大切片
// 造成长事务、锁表与内存放大。
const maxBatchSize = 100

// claimLease 是消费租约时长。客户端拉取后必须在租约内 ACK，
// 否则消息会被其他消费者重新领取（至少一次投递）。
const claimLease = 60 * time.Second

// 长轮询参数：客户端最多挂起 30s，空结果时每 200ms 重新领取一次。
// timeout_ms 是调用方的等待预算，不是数据库查询超时；不设上限会让
// 一个 HTTP/gRPC 连接被恶意占住几分钟，拖满服务端 goroutine。
const (
	maxConsumeTimeout   = 30 * time.Second
	consumePollInterval = 200 * time.Millisecond
)

// ErrMessageNotClaimed 由 dao 透出，供 handler 映射成 404/409 而不是一律 500。
var ErrMessageNotClaimed = dao.ErrMessageNotClaimed

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

	GetMessageStats(ctx context.Context) ([]map[string]interface{}, error)
	CreateTopic(ctx context.Context, topic int32) error
	DeleteTopic(ctx context.Context, topic int32) error
	ClearMessages(ctx context.Context, topic int32) error
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

	msg, err := newMessage(topic, content, priority, headers, correlationID)
	if err != nil {
		return nil, err
	}

	if err := s.repo.WithTransaction(ctx, func(txRepo dao.MessageRepository) error {
		if err := txRepo.SaveMessage(ctx, msg); err != nil {
			return fmt.Errorf("保存消息失败: %w", err)
		}

		return s.saveMessageEventToOutbox(ctx, txRepo.DB(), msg)
	}); err != nil {
		return nil, err
	}

	return msg, nil
}

func newMessage(topic int32, content string, priority *int32, headers map[string]string, correlationID *string) (*model.Message, error) {
	if !dao.ValidTopic(int(topic)) {
		return nil, fmt.Errorf("无效的消息主题: %d", topic)
	}

	priorityVal := int32(2)
	if priority != nil {
		priorityVal = *priority
	}
	if priorityVal < 1 || priorityVal > 4 {
		return nil, fmt.Errorf("无效的消息优先级: %d", priorityVal)
	}

	msg := &model.Message{
		ID:            uuid.New().String(),
		Topic:         dao.TopicString(int(topic)),
		Content:       content,
		Priority:      int(priorityVal),
		Headers:       dao.MarshalHeaders(headers),
		CorrelationID: correlationID,
		Timestamp:     time.Now().UnixMilli(),
		Status:        model.StatusPending,
		CreatedAt:     time.Now(),
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

	topicName := kafkaTopicFor(msg.Topic)
	return s.outboxRepo.SaveWithTx(ctx, txDB, topicName, msg.ID, event)
}

// kafkaTopicFor 把内部 topic 名映射为 Kafka 实际 topic。
//
// 内部名是大写下划线（DAO、gRPC enum、监控统计都在用），Kafka 常量是小写。
// outbox relay 直接把 Topic 字段当 Kafka topic 发送：不映射时 "QA_REQUEST"
// 会被投到一个没有任何消费者订阅的 topic，消息显示发送成功却永远不会被处理。
func kafkaTopicFor(internal string) string {
	switch internal {
	case "DATA_COLLECTION":
		return mq.TopicDataCollection
	case "KNOWLEDGE_EXTRACTION":
		return mq.TopicKnowledgeExtraction
	case "VECTOR_PROCESSING":
		return mq.TopicVectorProcessing
	case "QA_REQUEST":
		return mq.TopicQARequest
	case "RECOMMENDATION":
		return mq.TopicRecommendation
	case "USER_ACTIVITY":
		return mq.TopicUserActivity
	case "SYSTEM_EVENT":
		return mq.TopicSystemEvent
	}
	if internal == "" || internal == "unknown" {
		return mq.TopicSystemEvent
	}
	return internal
}

func (s *messageServiceImpl) BatchSendMessage(ctx context.Context, msgs []struct {
	Topic         int32
	Content       string
	Priority      *int32
	Headers       map[string]string
	CorrelationID *string
}) ([]*model.Message, error) {
	if len(msgs) == 0 {
		return nil, nil
	}
	if len(msgs) > maxBatchSize {
		return nil, fmt.Errorf("批量发送消息数超过上限 %d: %d", maxBatchSize, len(msgs))
	}

	// 先整体校验再写库：任何一条非法都不应该产生副作用。
	results := make([]*model.Message, 0, len(msgs))
	for i, m := range msgs {
		msg, err := newMessage(m.Topic, m.Content, m.Priority, m.Headers, m.CorrelationID)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条消息无效: %w", i+1, err)
		}
		results = append(results, msg)
	}

	// 整批消息与对应 outbox 事件在同一事务提交：要么全部入队，要么全部回滚。
	// 逐条 SendMessage 会中途留下已提交的子集，调用方拿到错误重试还会重复入队。
	if err := s.repo.WithTransaction(ctx, func(txRepo dao.MessageRepository) error {
		if err := txRepo.SaveMessages(ctx, results); err != nil {
			return fmt.Errorf("批量保存消息失败: %w", err)
		}
		for _, msg := range results {
			if err := s.saveMessageEventToOutbox(ctx, txRepo.DB(), msg); err != nil {
				return fmt.Errorf("保存消息事件失败: %w", err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return results, nil
}

func (s *messageServiceImpl) Subscribe(ctx context.Context, topic int32, consumerGroup string, config map[string]string) error {
	if !dao.ValidTopic(int(topic)) {
		return fmt.Errorf("无效的消息主题: %d", topic)
	}
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
	if !dao.ValidTopic(int(topic)) {
		return nil, fmt.Errorf("无效的消息主题: %d", topic)
	}

	logger.Info("operation",
		logger.StringField("consumer_group", consumerGroup),
		logger.StringField("topic", topicStr),
		logger.IntField("max_messages", int(maxMessages)),
		logger.IntField("timeout_ms", int(timeoutMs)))

	limit := int(maxMessages)
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	// timeout_ms <= 0 表示只尝试一次（保持旧调用方的立即返回语义）；
	// 超过上限则收敛到 30s，避免调用方把等待预算写成天文数字。
	waitBudget := normalizeConsumeTimeout(timeoutMs)
	if waitBudget <= 0 {
		return s.claimOnce(ctx, topicStr, limit, consumerGroup)
	}

	// 长轮询：空结果不是“没有消息”，而是“暂时没有”。在预算内反复领取，
	// 有消息立即返回；预算耗尽返回空列表（正常的长轮询超时）。
	deadline := time.Now().Add(waitBudget)
	for {
		msgs, err := s.claimOnce(ctx, topicStr, limit, consumerGroup)
		if err != nil {
			return nil, err
		}
		if len(msgs) > 0 {
			return msgs, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}
		if err := sleepWithContext(ctx, minDuration(consumePollInterval, remaining)); err != nil {
			return nil, err
		}
	}
}

func (s *messageServiceImpl) claimOnce(ctx context.Context, topicStr string, limit int, consumerGroup string) ([]*model.Message, error) {
	msgs, err := s.repo.ClaimPendingMessages(ctx, topicStr, limit, consumerGroup, time.Now().Add(claimLease))
	if err != nil {
		return nil, fmt.Errorf("领取待处理消息失败: %w", err)
	}
	return msgs, nil
}

func normalizeConsumeTimeout(timeoutMs int32) time.Duration {
	if timeoutMs <= 0 {
		return 0
	}
	wait := time.Duration(timeoutMs) * time.Millisecond
	if wait > maxConsumeTimeout {
		return maxConsumeTimeout
	}
	return wait
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// sleepWithContext 等待 d 或 ctx 取消，二者先到者生效。
// 用 timer 而不是 time.Sleep，客户端断开时能立刻释放 goroutine。
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *messageServiceImpl) AcknowledgeMessage(ctx context.Context, consumerGroup string, messageID string, topic int32) error {
	logger.Info("operation",
		logger.StringField("message_id", messageID))

	if err := s.repo.AckMessage(ctx, messageID, consumerGroup); err != nil {
		return fmt.Errorf("确认消息失败: %w", err)
	}
	return nil
}

func (s *messageServiceImpl) BatchAcknowledgeMessages(ctx context.Context, consumerGroup string, messageIDs []string, topic int32) error {
	logger.Info("operation",
		logger.IntField("count", len(messageIDs)))

	ids := dedupeNonEmpty(messageIDs)
	if len(ids) == 0 {
		return nil
	}
	if err := s.repo.AckMessages(ctx, ids, consumerGroup); err != nil {
		return fmt.Errorf("批量确认消息失败: %w", err)
	}
	return nil
}

func dedupeNonEmpty(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *messageServiceImpl) GetMessageStats(ctx context.Context) ([]map[string]interface{}, error) {
	rows, err := s.repo.GetMessageStatsByTopic(ctx)
	if err != nil {
		return nil, fmt.Errorf("统计消息失败: %w", err)
	}

	byTopic := make(map[string]dao.TopicStats, len(rows))
	for _, row := range rows {
		byTopic[row.Topic] = row
	}

	// 即使某个 topic 一条消息都没有也返回零值，客户端表格不会缺行。
	stats := make([]map[string]interface{}, 0, 7)
	for t := 0; t <= 6; t++ {
		name := dao.TopicString(t)
		row := byTopic[name]
		stats = append(stats, map[string]interface{}{
			"topic":              name,
			"total_messages":     row.Total,
			"pending_messages":   row.Pending,
			"processed_messages": row.Processed,
			"error_messages":     row.Failed,
		})
	}

	return stats, nil
}

func (s *messageServiceImpl) CreateTopic(ctx context.Context, topic int32) error {
	if !dao.ValidTopic(int(topic)) {
		return fmt.Errorf("无效的消息主题: %d", topic)
	}
	topicName := dao.TopicString(int(topic))
	logger.Info("创建消息主题",
		logger.StringField("topic", topicName))

	return s.repo.WithTransaction(ctx, func(txRepo dao.MessageRepository) error {
		event := map[string]interface{}{
			"action":    "create_topic",
			"topic":     topicName,
			"timestamp": time.Now().Format(time.RFC3339),
		}
		// topic 生命周期事件统一进 system_event；直接拿内部大写名当 Kafka topic
		// 会再次触发「投递成功但无人消费」的命名漂移。
		return s.outboxRepo.SaveWithTx(ctx, txRepo.DB(), mq.TopicSystemEvent, "system-topic-create", event)
	})
}

func (s *messageServiceImpl) DeleteTopic(ctx context.Context, topic int32) error {
	if !dao.ValidTopic(int(topic)) {
		return fmt.Errorf("无效的消息主题: %d", topic)
	}
	topicStr := dao.TopicString(int(topic))
	logger.Info("删除消息主题",
		logger.StringField("topic", topicStr))

	err := s.repo.WithTransaction(ctx, func(txRepo dao.MessageRepository) error {
		if err := txRepo.DeleteMessagesByTopic(ctx, topicStr); err != nil {
			return fmt.Errorf("删除主题消息失败: %w", err)
		}
		if err := txRepo.DeleteSubscriptionsByTopic(ctx, topicStr); err != nil {
			return fmt.Errorf("删除主题订阅失败: %w", err)
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

func (s *messageServiceImpl) ClearMessages(ctx context.Context, topic int32) error {
	if !dao.ValidTopic(int(topic)) {
		return fmt.Errorf("无效的消息主题: %d", topic)
	}
	topicStr := dao.TopicString(int(topic))
	logger.Info("清除消息",
		logger.StringField("topic", topicStr))

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
			// 必须同步等待结果：handler 返回 nil 后 offset 就会被提交，
			// 裸 goroutine 失败时无法重试，进程崩溃时这条问答请求直接丢失。
			// 串行消费下会牺牲吞吐，但换来 at-least-once；要提升吞吐应把
			// 任务先持久化再异步处理，而不是吞掉错误。
			answerCtx, cancel := context.WithTimeout(messageContext(msg), qaAnswerTimeout)
			_, err := s.questionService.AskQuestion(answerCtx, content, nil)
			cancel()
			if err != nil {
				return fmt.Errorf("问答处理失败: %w", err)
			}
		}
	case mq.TopicRecommendation:
		logger.Info("operation")
	default:
		logger.Info("operation")
	}

	if messageID != "" {
		// 状态必须落在统计认识的集合里：历史上写 PROCESSED_FROM_KAFKA，
		// 而统计只累加 PROCESSED/ACKED，Kafka 消费量永远显示为 0。
		if err := s.repo.UpdateMessageStatus(messageContext(msg), messageID, model.StatusProcessed); err != nil {
			return fmt.Errorf("更新消息状态失败: %w", err)
		}
	}

	return nil
}

// qaAnswerTimeout 是单条问答的处理上限。没有上限时，一次卡住的 LLM 调用
// 会让整个分区永远停在这条消息上。
const qaAnswerTimeout = 120 * time.Second

// messageContext 返回消费循环注入的生命周期 ctx；测试或直接调用时兜底 Background。
func messageContext(msg *mq.Message) context.Context {
	if msg == nil || msg.Context == nil {
		return context.Background()
	}
	return msg.Context
}
