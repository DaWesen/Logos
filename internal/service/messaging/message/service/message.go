package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/messaging/message/dao"
	"Logos/internal/service/messaging/message/model"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/obs"
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

// messageConsumerGroup 是消息服务自身消费 Kafka 的消费组。
// 幂等认领把它写进 claimed_by，这样「谁在处理」在库里可查，也不会和
// 外部客户端拉取消息时使用的消费组混在一起。
const messageConsumerGroup = "message-service-group"

// qaProcessingLease 必须大于 qaAnswerTimeout：问答还没结束就被别人接管，
// 会变成两条并发请求同时调模型——既重复计费，也可能写出两份答案。
const qaProcessingLease = 5 * time.Minute

// claimWriteTimeout 限制「归还认领 / 写完成态」的耗时。
// 两者都走独立超时，避免停机时 ctx 已取消导致收尾语句根本没发出去。
const claimWriteTimeout = 5 * time.Second

// claimWriteAttempts 覆盖数据库瞬时抖动。次数不宜多：收尾失败会拖长
// 分区阻塞时间，而更长时间的故障应该交给指标告警，而不是无限重试。
const claimWriteAttempts = 3

// ErrMessageNotClaimed 由 dao 透出，供 handler 映射成 404/409 而不是一律 500。
var ErrMessageNotClaimed = dao.ErrMessageNotClaimed

// newMessageConsumer 是 mq.NewConsumer 的包级工厂，测试里可替换为返回
// 零值 Consumer 的桩，从而在不连 Kafka 的前提下验证订阅失败的返回路径。
var newMessageConsumer = mq.NewConsumer

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

	GetMessageStats(ctx context.Context) ([]dao.TopicStats, error)
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

func (s *messageServiceImpl) GetMessageStats(ctx context.Context) ([]dao.TopicStats, error) {
	rows, err := s.repo.GetMessageStatsByTopic(ctx)
	if err != nil {
		return nil, fmt.Errorf("统计消息失败: %w", err)
	}

	byTopic := make(map[string]dao.TopicStats, len(rows))
	for _, row := range rows {
		byTopic[row.Topic] = row
	}

	// 即使某个 topic 一条消息都没有也返回零值，客户端表格不会缺行。
	stats := make([]dao.TopicStats, 0, 7)
	for t := 0; t <= 6; t++ {
		name := dao.TopicString(t)
		row := byTopic[name]
		// 用结构体而不是 map[string]interface{} 跨层传统计值：
		// 旧写法到 handler 侧要靠 5 次裸类型断言取值，字段名拼错或类型
		// 变化时断言失败会静默变成 0，监控面板直接显示成「没有消息」。
		row.Topic = name
		stats = append(stats, row)
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
		consumer := newMessageConsumer(topic, messageConsumerGroup)
		// 订阅失败必须同步上抛：Subscribe 内部自己起消费循环，这里再包一层
		// goroutine 只会把初始化错误留在日志里，cmd 层的 fail-fast 永远看不到。
		if err := consumer.Subscribe(ctx, s.handleGenericMessage); err != nil {
			logger.Error("operation",
				logger.StringField("topic", topic),
				logger.ErrorField(err))
			return fmt.Errorf("subscribe topic %s: %w", topic, err)
		}

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

	// 字段类型必须显式校验：`value, _ := event["x"].(string)` 会把
	// 类型错误吞成空字符串，表现为问答「没有内容」却成功提交 offset，
	// 或者状态更新被静默跳过（消息永远停在 PENDING）。
	messageID, _, err := eventStringField(event, "message_id")
	if err != nil {
		logger.Error("kafka operation",
			logger.ErrorField(err),
			logger.StringField("topic", msg.Topic),
			logger.StringField("key", msg.Key))
		return err
	}

	ctx := messageContext(msg)
	claimed := false

	switch msg.Topic {
	case mq.TopicQARequest:
		content, hasContent, err := eventStringField(event, "content")
		if err != nil {
			logger.Error("kafka operation",
				logger.ErrorField(err),
				logger.StringField("topic", msg.Topic),
				logger.StringField("key", msg.Key))
			return err
		}
		if !hasContent || content == "" {
			// 没有问题的问答请求重试多少次都不会产生答案，按不可恢复消息
			// 跳过：既不会静默标成已处理，也不会白占分区重试 5 次。
			logger.Warn("kafka operation",
				logger.StringField("topic", msg.Topic),
				logger.StringField("key", msg.Key))
			return fmt.Errorf("%w: 问答请求缺少 content", mq.ErrSkipMessage)
		}
		if s.questionService == nil {
			// 装配缺失属于服务端故障，不能当成处理成功把消息标成 PROCESSED。
			return fmt.Errorf("问答服务未初始化，无法处理问答请求")
		}

		// 幂等闸门必须在调用模型之前：DLQ 回放、offset 提交失败后的重复投递
		// 都会带着同一个 message_id 再来一次，而 AskQuestion 会真实调用模型并计费。
		// 先认领再执行，保证同一条消息最多只有一个处理者。
		if messageID != "" {
			if s.repo == nil {
				// 没有仓储就无法记录「已处理」，幂等承诺不成立；宁可报错重试，
				// 也不要在没有闸门的情况下重复调用模型。
				return fmt.Errorf("消息仓库未初始化，无法保证问答幂等")
			}

			acquired, claimErr := s.repo.TryClaimMessageForProcessing(
				ctx, messageID, messageConsumerGroup, time.Now().Add(qaProcessingLease))
			if claimErr != nil {
				return fmt.Errorf("认领问答消息失败: %w", claimErr)
			}
			if !acquired {
				stored, getErr := s.repo.GetMessage(ctx, messageID)
				if getErr != nil {
					// 连状态都查不到时无法判断是「已处理」还是「无记录」，
					// 交给消费端重试比猜一个更安全。
					return fmt.Errorf("查询问答消息状态失败: %w", getErr)
				}
				if stored != nil {
					reason := duplicateReason(stored.Status)
					obs.RecordKafkaDuplicate(ctx, msg.Topic, reason)
					if reason == "not_claimable" {
						// 状态既不是终态也不是「处理中」：可能是认领失败后对方
						// 刚好释放回 PENDING，也可能是 FAILED 等需要人工看的记录。
						// 这时不能提交 offset——PENDING 重试一次就能重新认领，
						// 其余状态交给重试耗尽后的死信，至少消息还在。
						return fmt.Errorf("消息状态 %s 不允许认领，交给重试处理: %s",
							stored.Status, messageID)
					}
					// 已处理或正在被其他实例处理：这一次不执行任何业务逻辑，
					// 直接返回 nil 提交 offset，重复消息到此为止。
					logger.Warn("重复的问答消息已跳过",
						logger.StringField("message_id", messageID),
						logger.StringField("status", stored.Status),
						logger.StringField("reason", reason))
					return nil
				}
				// 认领失败但记录不存在（外部生产者直投、历史数据已清理）：
				// 没有可冲突的状态，按原有行为继续处理，避免整类消息被静默丢弃。
				logger.Warn("问答消息记录不存在，跳过幂等认领",
					logger.StringField("message_id", messageID))
			}
			claimed = acquired
		}

		// 必须同步等待结果：handler 返回 nil 后 offset 就会被提交，
		// 裸 goroutine 失败时无法重试，进程崩溃时这条问答请求直接丢失。
		// 串行消费下会牺牲吞吐，但换来 at-least-once；要提升吞吐应把
		// 任务先持久化再异步处理，而不是吞掉错误。
		answerCtx, cancel := context.WithTimeout(ctx, qaAnswerTimeout)
		_, err = s.questionService.AskQuestion(answerCtx, content, nil)
		cancel()
		if err != nil {
			if claimed {
				// 失败必须把认领还回去，否则 Kafka 的就地重试会被幂等闸门挡下，
				// 一次可恢复的失败就变成了永久跳过。
				s.releaseClaim(ctx, messageID)
			}
			return fmt.Errorf("问答处理失败: %w", err)
		}
		if claimed {
			// 完成态要同时清空租约，否则租约过期后这条已成功的消息
			// 会再次满足接管条件，回放时又调一次模型。
			if err := s.finalizeClaim(ctx, messageID); err != nil {
				// 模型已经成功、答案也已落库，此时退回认领会造成重复计费，
				// 因此提交 offset，只把「状态没写进去」作为不一致暴露出去。
				obs.RecordMessageFinalizeFailure(ctx, msg.Topic)
				logger.Error("问答已完成但消息终态写入失败，消息将停留在 PROCESSING",
					logger.StringField("message_id", messageID),
					logger.ErrorField(err))
				return nil
			}
			return nil
		}
	case mq.TopicRecommendation:
		logger.Info("operation")
	default:
		logger.Info("operation")
	}

	if messageID != "" {
		// 状态必须落在统计认识的集合里：历史上写 PROCESSED_FROM_KAFKA，
		// 而统计只累加 PROCESSED/ACKED，Kafka 消费量永远显示为 0。
		if err := s.repo.UpdateMessageStatus(ctx, messageID, model.StatusProcessed); err != nil {
			return fmt.Errorf("更新消息状态失败: %w", err)
		}
	}

	return nil
}

// releaseClaim 把认领退回 PENDING，并做少量重试。
//
// 用 WithoutCancel：停机/超时会取消业务 ctx，但「把消息放回队列」不应该
// 跟着被取消，否则消息会停在 PROCESSING，直到租约过期才能被接管。
// 重试只覆盖瞬时数据库抖动——归还失败而消费端又立刻重试时，幂等闸门会把
// 这次重试当成「别人正在处理」直接跳过，那才是真正会丢消息的窗口。
func (s *messageServiceImpl) releaseClaim(ctx context.Context, messageID string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimWriteTimeout)
	defer cancel()

	var err error
	for attempt := 1; attempt <= claimWriteAttempts; attempt++ {
		err = s.repo.ReleaseMessageClaim(releaseCtx, messageID, messageConsumerGroup)
		if err == nil {
			return
		}
		if attempt == claimWriteAttempts || !waitClaimRetry(releaseCtx, attempt) {
			break
		}
	}
	logger.Error("归还问答消息认领失败，消息将在租约过期后才能被接管",
		logger.StringField("message_id", messageID),
		logger.ErrorField(err))
}

// finalizeClaim 把「业务已完成」写成终态，并清空认领。
//
// 用 WithoutCancel：停机/超时取消的是业务 ctx，不应该阻止最后一步收尾，
// 否则一条已经成功的消息会停在 PROCESSING，直到租约过期。
// 与 releaseClaim 一样做短重试，覆盖数据库瞬时抖动。
func (s *messageServiceImpl) finalizeClaim(ctx context.Context, messageID string) error {
	finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimWriteTimeout)
	defer cancel()

	var err error
	for attempt := 1; attempt <= claimWriteAttempts; attempt++ {
		err = s.repo.MarkMessageProcessed(finalizeCtx, messageID, messageConsumerGroup)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrMessageNotClaimed) {
			// 认领已被接管，重试也写不进去；继续重试只会推迟 offset 提交。
			return err
		}
		if attempt == claimWriteAttempts || !waitClaimRetry(finalizeCtx, attempt) {
			break
		}
	}
	return err
}

// waitClaimRetry 在收尾重试之间做线性退避；返回 false 表示 ctx 已取消。
// 退避刻意比消费重试更短：这里只覆盖「一次 UPDATE 的瞬时失败」，
// 长时间阻塞只会推迟 offset 提交、拖住整个分区。
func waitClaimRetry(ctx context.Context, attempt int) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
		return true
	}
}

// duplicateReason 把「认领失败时查到的状态」收敛成固定的指标标签。
// 不直接把 status 当标签：already_done 与 in_flight 的处置方式不同——
// 前者是幂等生效，后者是并发竞争，混在一起看不出到底挡下了什么。
func duplicateReason(status string) string {
	switch status {
	case model.StatusProcessed, model.StatusAcked, model.StatusCleared:
		return "already_done"
	case model.StatusProcessing:
		return "in_flight"
	default:
		return "not_claimable"
	}
}

// eventStringField 按事件契约读取字符串字段，返回 (值, 字段是否存在, 错误)。
//
// 字段缺失与类型错误必须区分：缺失可以按默认值处理，而类型错误说明生产者
// 和消费者对 schema 的理解已经不一致，重试不会自愈，用 mq.ErrSkipMessage
// 让消费端快速跳过并留下日志，而不是静默降级成空值。
func eventStringField(event map[string]interface{}, name string) (string, bool, error) {
	raw, exists := event[name]
	if !exists || raw == nil {
		return "", false, nil
	}

	value, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("%w: 字段 %s 期望 string，实际为 %T", mq.ErrSkipMessage, name, raw)
	}

	return value, true, nil
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
