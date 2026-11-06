package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/ai/question/dao"
	"Logos/internal/service/ai/question/model"
	"Logos/pkg/cache"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
	"Logos/pkg/pagination"
)

type KnowledgeService interface {
	SearchEntities(ctx context.Context, query string) ([]string, error)
}

type SearchService interface {
	Search(ctx context.Context, query string, indexType string) ([]string, error)
}

type VectorService interface {
	SearchSimilar(ctx context.Context, text string, topK int) ([]string, error)
}

// ChatClient 抽象出问答真正依赖的模型能力。
//
// 只依赖这两个方法而不是具体的 *eino.EinoManager，测试里才能注入
// 「第一次失败、第二次成功」的模型，验证降级记录会被后续重试覆盖。
type ChatClient interface {
	HasChatModel() bool
	Chat(ctx context.Context, messages []string) (string, error)
}

type QAService interface {
	AskQuestion(ctx context.Context, content string, userID int64, reqContext map[string]string, requestID string) (string, float64, []string, string, int64, bool, error)
	BatchAskQuestions(ctx context.Context, questions []string, userID int64) (map[string]string, error)
	GetHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error)
	SubmitFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error
	GetRecommendedQuestions(ctx context.Context, userID int64) ([]string, error)
}

type qaServiceImpl struct {
	qaRepo           dao.QARepository
	chat             ChatClient
	knowledgeService KnowledgeService
	searchService    SearchService
	vectorService    VectorService
	cache            cache.Cache

	// 三个预算字段留空时使用 retrieval.go 里的默认值；
	// 测试通过注入更短的预算来验证超时与降级路径。
	answerTimeout     time.Duration
	retrievalTimeout  time.Duration
	persistTimeout    time.Duration
	batchTimeout      time.Duration
	batchMaxQuestions int
	batchConcurrency  int
}

// ErrQARequestIDConflict 表示同一个幂等键被不同用户或不同问题复用。
// 这时不能返回已有答案：前者会把别人的问答内容泄露给当前调用方，
// 后者会让调用方拿到一个与其请求内容不符的答案。
var ErrQARequestIDConflict = errors.New("qa: request_id already used by another request")

// qaRecordIDMaxLen 与历史 uuid 主键保持同一量级，避免不同数据库
// 对 varchar 默认长度的差异把确定性 ID 截断成碰撞。
const qaRecordIDMaxLen = 64

func NewQAService(qaRepo dao.QARepository, einoClient *eino.EinoManager, knowledgeService KnowledgeService, searchService SearchService, vectorService VectorService) QAService {
	svc := &qaServiceImpl{
		qaRepo:           qaRepo,
		knowledgeService: knowledgeService,
		searchService:    searchService,
		vectorService:    vectorService,
		cache:            cache.NewRedisCache(),
	}
	// 不能把 typed nil 直接装进接口：接口本身非 nil，调用 HasChatModel
	// 会打到 nil 接收者上。初始化失败时保持 chat 为 nil，走降级分支。
	if einoClient != nil {
		svc.chat = einoClient
	}
	return svc
}

func (s *qaServiceImpl) AskQuestion(
	ctx context.Context,
	content string,
	userID int64,
	reqContext map[string]string,
	requestID string,
) (string, float64, []string, string, int64, bool, error) {
	logger.Info("Processing question",
		logger.StringField("content", content),
		logger.Int64Field("user_id", userID),
		logger.StringField("request_id", requestID))

	if content == "" {
		return "", 0, nil, "", 0, false, errors.New("question content cannot be empty")
	}

	questionID := uuid.New().String()
	// 命中降级记录时不是「已完成」，而是同一个逻辑请求还没成功。此时要
	// 重新执行，并跳过限流：重试不是用户发起的新请求，不应该再吃一次配额。
	retryingDegraded := false
	if requestID != "" {
		questionID = qaRecordID(requestID)
		// 幂等查询必须放在限流之前：重放已经完成的问题不应该再消耗配额，
		// 更不能因为限流把「已有答案」变成一次失败。
		existing, err := s.qaRepo.GetQARecord(ctx, questionID)
		if err != nil {
			return "", 0, nil, "", 0, false, fmt.Errorf("查询幂等问答记录失败: %w", err)
		}
		if existing != nil {
			if existing.UserID != userID {
				obs.RecordQAIdempotency(ctx, "conflict")
				return "", 0, nil, "", 0, false, fmt.Errorf("%w: %s", ErrQARequestIDConflict, requestID)
			}
			if existing.Question != content {
				obs.RecordQAIdempotency(ctx, "conflict")
				return "", 0, nil, "", 0, false, fmt.Errorf("%w: %s", ErrQARequestIDConflict, requestID)
			}
			if existing.Degraded {
				// 降级答案只是占位文本，不能当成幂等终态返回。继续往下走，
				// 用同一个确定性主键重新生成，成功后再覆盖这条记录。
				obs.RecordQAIdempotency(ctx, "degraded_hit")
				logger.Info("命中降级问答记录，重新生成",
					logger.StringField("request_id", requestID),
					logger.StringField("question_id", existing.ID))
				retryingDegraded = true
			} else {
				obs.RecordQAIdempotency(ctx, "hit")
				logger.Info("命中幂等问答记录，跳过模型调用",
					logger.StringField("request_id", requestID),
					logger.StringField("question_id", existing.ID))
				return existing.Answer, existing.Confidence, []string{}, existing.ID, existing.Timestamp, false, nil
			}
		}
	}

	if !retryingDegraded {
		if err := s.checkQuestionRateLimit(ctx, userID); err != nil {
			return "", 0, nil, "", 0, false, err
		}
	}

	// 从这里到模型返回共用同一个端到端预算。检索、模型、落库都在这条
	// 时间线上，避免“每一步都没超时，加起来却把调用方拖死”。
	ctx, cancel := context.WithTimeout(ctx, s.answerBudget())
	defer cancel()

	timestamp := time.Now().Unix()

	var answer string
	confidence := 0.8
	degraded := false

	// 检索并行执行：总耗时从“三个依赖之和”收敛到“最慢的那个”，
	// 单个依赖超时只丢这一条证据，不影响整次回答。
	rawBackground := s.gatherBackground(ctx, content)
	backgroundInfo, sources, stats := assembleContext(rawBackground)
	obs.RecordQAContextItem(ctx, "kept", stats.kept)
	obs.RecordQAContextItem(ctx, "duplicated", stats.duplicated)
	obs.RecordQAContextItem(ctx, "truncated", stats.truncated)
	obs.RecordQAContextItem(ctx, "dropped", stats.dropped)
	if stats.duplicated > 0 || stats.truncated > 0 || stats.dropped > 0 {
		logger.Info("问答参考信息已按预算裁剪",
			logger.StringField("stats", contextStatsSummary(stats)))
	}

	if s.chat != nil && s.chat.HasChatModel() {
		messages := s.buildPrompt(content, backgroundInfo)

		var err error
		answer, err = s.chat.Chat(ctx, messages)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				logger.Warn("模型调用超出问答预算，返回降级答案",
					logger.StringField("budget", s.answerBudget().String()),
					logger.ErrorField(err))
			} else {
				logger.Warn("Failed to answer using Eino, using default answer", logger.ErrorField(err))
			}
			answer = "Thank you for your question. We are currently working on answering it. Please try again later."
			confidence = 0.5
			degraded = true
		} else {
			confidence = 0.9
		}
	} else {
		logger.Warn("Eino client not initialized, using default answer")
		answer = "Thank you for your question. Our AI assistant is being prepared. Please try again later."
		confidence = 0.5
		degraded = true
	}

	if degraded {
		obs.RecordQAAnswer(ctx, "degraded")
	} else {
		obs.RecordQAAnswer(ctx, "ok")
	}

	record := &model.QARecord{
		ID:         questionID,
		Question:   content,
		Answer:     answer,
		Confidence: confidence,
		UserID:     userID,
		Timestamp:  timestamp,
		Degraded:   degraded,
	}

	// 答案已经生成，落库不再继承请求的取消信号：否则预算边界上的成功
	// 生成会被记成一次失败，消费端只能重试并再付一次模型成本。
	persistCtx, persistCancel := s.persistContext(ctx)
	defer persistCancel()

	err := s.qaRepo.CreateQARecord(persistCtx, record)
	if err != nil {
		if requestID != "" {
			// 并发请求可能已经用同一个确定性 ID 写入成功；唯一键冲突时
			// 返回先写入的答案，而不是把一次成功问答报成失败。
			existing, getErr := s.qaRepo.GetQARecord(persistCtx, questionID)
			if getErr == nil && existing != nil {
				if existing.UserID != userID || existing.Question != content {
					obs.RecordQAIdempotency(ctx, "conflict")
					return "", 0, nil, "", 0, false, fmt.Errorf("%w: %s", ErrQARequestIDConflict, requestID)
				}
				if existing.Degraded && !degraded {
					// 本次真正生成了回答：用条件更新把降级记录升级成正常记录。
					// 条件更新保证多个重试实例里只有一个能覆盖，后到的回查即可。
					upgraded, upgradeErr := s.qaRepo.ReplaceDegradedAnswer(persistCtx, questionID, answer, confidence)
					if upgradeErr != nil {
						logger.Error("覆盖降级问答记录失败，本次回答未能落库",
							logger.StringField("question_id", questionID),
							logger.ErrorField(upgradeErr))
						// 模型已经给出了正常回答，不能因为落库失败又把占位文案返回给
						// 用户；历史记录的不一致留给日志与下一次重试收敛。
						return answer, confidence, sources, questionID, existing.Timestamp, false, nil
					}
					if upgraded {
						obs.RecordQAIdempotency(ctx, "healed")
						logger.Info("降级问答记录已被成功回答覆盖",
							logger.StringField("request_id", requestID),
							logger.StringField("question_id", questionID))
						return answer, confidence, sources, questionID, existing.Timestamp, false, nil
					}
					// 没抢到说明另一个实例已经覆盖成功，优先返回它的答案。
					latest, latestErr := s.qaRepo.GetQARecord(persistCtx, questionID)
					if latestErr == nil && latest != nil && !latest.Degraded {
						return latest.Answer, latest.Confidence, sources, latest.ID, latest.Timestamp, false, nil
					}
					// 条件更新没生效、回查也没拿到更新后的记录：本次答案依然
					// 是有效的，优先返回它，而不是退回占位文案。
					return answer, confidence, sources, questionID, existing.Timestamp, false, nil
				}
				if !existing.Degraded {
					obs.RecordQAIdempotency(ctx, "concurrent_hit")
				}
				logger.Warn("幂等问答记录已被并发写入，返回已有答案",
					logger.StringField("request_id", requestID),
					logger.StringField("question_id", existing.ID),
					logger.BoolField("degraded", existing.Degraded))
				return existing.Answer, existing.Confidence, []string{}, existing.ID, existing.Timestamp, existing.Degraded, nil
			}
		}
		logger.Error("Failed to create QA record", logger.ErrorField(err))
		return answer, confidence, sources, questionID, timestamp, degraded, fmt.Errorf("answered but failed to record: %w", err)
	}

	return answer, confidence, sources, questionID, timestamp, degraded, nil
}

// qaRecordID 从调用方幂等键派生确定性主键。
// 短 ID 保留可读的原文，便于按 message_id 直接查库；超长时用 SHA-256
// 收敛到固定长度，避免数据库截断后不同请求撞到同一条记录。
func qaRecordID(requestID string) string {
	const prefix = "qa:"
	id := prefix + requestID
	if len(id) <= qaRecordIDMaxLen {
		return id
	}
	sum := sha256.Sum256([]byte(requestID))
	return prefix + hex.EncodeToString(sum[:])[:qaRecordIDMaxLen-len(prefix)]
}

func (s *qaServiceImpl) GetHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error) {
	logger.Info("Getting QA history",
		logger.Int64Field("user_id", userID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	page, pageSize = pagination.Normalize(page, pageSize)

	records, total, err := s.qaRepo.ListQARecords(ctx, userID, page, pageSize)
	if err != nil {
		logger.Error("Failed to get QA history", logger.ErrorField(err))
		return nil, 0, err
	}

	return records, total, nil
}

func (s *qaServiceImpl) SubmitFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error {
	logger.Info("Submitting user feedback",
		logger.StringField("question_id", questionID),
		logger.StringField("feedback", feedback))

	if questionID == "" {
		return errors.New("question ID cannot be empty")
	}

	if rating != nil && (*rating < 1 || *rating > 5) {
		return errors.New("rating must be between 1 and 5")
	}

	err := s.qaRepo.UpdateFeedback(ctx, questionID, feedback, rating)
	if err != nil {
		logger.Error("Failed to update user feedback", logger.ErrorField(err))
		return err
	}

	return nil
}

func (s *qaServiceImpl) GetRecommendedQuestions(ctx context.Context, userID int64) ([]string, error) {
	logger.Info("Getting recommended questions",
		logger.Int64Field("user_id", userID))

	var questions []string

	if s.chat != nil && s.chat.HasChatModel() {
		history, _, err := s.qaRepo.ListQARecords(ctx, userID, 1, 10)
		if err != nil {
			logger.Warn("Failed to get user history for recommendations", logger.ErrorField(err))
		}

		prompt := "请生成4个用户可能感兴趣的中文问题，每个问题一行，不要加序号和引号。"
		if len(history) > 0 {
			prompt += "\n\n用户最近提问过：\n"
			for i, h := range history {
				if i >= 5 {
					break
				}
				prompt += "- " + h.Question + "\n"
			}
			prompt += "\n请基于用户兴趣生成相关的延伸问题。"
		}

		result, err := s.chat.Chat(ctx, []string{prompt})
		if err != nil {
			logger.Warn("Eino生成推荐问题失败", logger.ErrorField(err))
		} else if result != "" {
			for _, line := range splitLines(result) {
				line = trimSpaces(line)
				if line != "" {
					questions = append(questions, line)
				}
			}
		}
	}

	if len(questions) < 4 {
		questions = append(questions, s.getDefaultQuestions(4-len(questions))...)
	}

	if len(questions) > 8 {
		questions = questions[:8]
	}

	return questions, nil
}

func (s *qaServiceImpl) getDefaultQuestions(count int) []string {
	defaults := []string{
		"知识图谱是什么？如何构建知识图谱？",
		"向量搜索的原理是什么？",
		"AI助手能帮我做什么？",
		"如何提高问答系统的准确率？",
		"如何管理我的知识库文档？",
		"RAG技术的工作流程是怎样的？",
	}
	if count > len(defaults) {
		count = len(defaults)
	}
	return defaults[:count]
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpaces(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func (s *qaServiceImpl) checkQuestionRateLimit(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}

	key := fmt.Sprintf("ratelimit:question:%d", userID)

	current, err := s.cache.IncrBy(ctx, key, 1)
	if err != nil {
		logger.Warn("Failed to rate limit question, skipping",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
		return nil
	}

	if current == 1 {
		s.cache.Expire(ctx, key, time.Minute)
	}

	if current > 30 {
		logger.Warn("Question rate limit exceeded",
			logger.Int64Field("user_id", userID),
			logger.Int64Field("count", current))
		return fmt.Errorf("too many requests, please try again in %d seconds", 60)
	}

	return nil
}
