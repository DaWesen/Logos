package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/ai/question/dao"
	"Logos/internal/ai/question/model"
	"Logos/pkg/cache"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
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

type QAService interface {
	AskQuestion(ctx context.Context, content string, userID int64, context map[string]string) (string, float64, []string, string, int64, error)
	BatchAskQuestions(ctx context.Context, questions []string, userID int64) (map[string]string, error)
	GetHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error)
	SubmitFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error
	GetRecommendedQuestions(ctx context.Context, userID int64) ([]string, error)
}

type qaServiceImpl struct {
	qaRepo           dao.QARepository
	einoClient       *eino.EinoManager
	knowledgeService KnowledgeService
	searchService    SearchService
	vectorService    VectorService
	cache            cache.Cache
}

func NewQAService(qaRepo dao.QARepository, einoClient *eino.EinoManager, knowledgeService KnowledgeService, searchService SearchService, vectorService VectorService) QAService {
	return &qaServiceImpl{
		qaRepo:           qaRepo,
		einoClient:       einoClient,
		knowledgeService: knowledgeService,
		searchService:    searchService,
		vectorService:    vectorService,
		cache:            cache.NewRedisCache(),
	}
}

func (s *qaServiceImpl) AskQuestion(ctx context.Context, content string, userID int64, context map[string]string) (string, float64, []string, string, int64, error) {
	logger.Info("处理问答请求",
		logger.StringField("content", content),
		logger.Int64Field("user_id", userID))

	if content == "" {
		return "", 0, nil, "", 0, errors.New("问题内容不能为空")
	}

	if err := s.checkQuestionRateLimit(ctx, userID); err != nil {
		return "", 0, nil, "", 0, err
	}

	questionID := uuid.New().String()
	timestamp := time.Now().Unix()

	var answer string
	confidence := 0.8
	sources := []string{}
	backgroundInfo := []string{}

	if s.knowledgeService != nil {
		entities, err := s.knowledgeService.SearchEntities(ctx, content)
		if err != nil {
			logger.Warn("搜索知识图谱失败", logger.ErrorField(err))
		} else if len(entities) > 0 {
			logger.Info("从知识图谱获取到相关实体",
				logger.IntField("count", len(entities)))
			backgroundInfo = append(backgroundInfo, entities...)
			sources = append(sources, "knowledge_graph")
		}
	}

	if s.searchService != nil {
		searchResults, err := s.searchService.Search(ctx, content, "document")
		if err != nil {
			logger.Warn("搜索文档失败", logger.ErrorField(err))
		} else if len(searchResults) > 0 {
			logger.Info("从搜索获取到相关文档",
				logger.IntField("count", len(searchResults)))
			backgroundInfo = append(backgroundInfo, searchResults...)
			sources = append(sources, "search")
		}
	}

	if s.vectorService != nil {
		vectorResults, err := s.vectorService.SearchSimilar(ctx, content, 5)
		if err != nil {
			logger.Warn("向量搜索失败", logger.ErrorField(err))
		} else if len(vectorResults) > 0 {
			logger.Info("从向量搜索获取到相似内容",
				logger.IntField("count", len(vectorResults)))
			backgroundInfo = append(backgroundInfo, vectorResults...)
			sources = append(sources, "vector")
		}
	}

	if s.einoClient != nil && s.einoClient.HasChatModel() {
		prompt := "你是一个有用的AI助手，请根据用户的问题和以下背景信息提供准确的回答。\n\n"
		if len(backgroundInfo) > 0 {
			prompt += "背景信息：\n"
			for _, info := range backgroundInfo {
				prompt += "- " + info + "\n"
			}
			prompt += "\n"
		}
		prompt += "用户问题：" + content

		messages := []string{
			"你是一个有用的AI助手，请根据用户的问题和提供的背景信息提供准确的回答。",
			prompt,
		}

		var err error
		answer, err = s.einoClient.Chat(ctx, messages)
		if err != nil {
			logger.Warn("使用Eino回答失败，使用默认回答", logger.ErrorField(err))
			answer = "感谢您的提问！我们正在处理您的请求，请稍后再试。"
			confidence = 0.5
		} else {
			confidence = 0.9
		}
	} else {
		logger.Warn("Eino客户端未初始化，使用默认回答")
		answer = "感谢您的提问！我们的AI助手正在准备中，请稍后再试。"
		confidence = 0.5
	}

	record := &model.QARecord{
		ID:         questionID,
		Question:   content,
		Answer:     answer,
		Confidence: confidence,
		UserID:     userID,
		Timestamp:  timestamp,
	}

	err := s.qaRepo.CreateQARecord(ctx, record)
	if err != nil {
		logger.Error("保存问答记录失败", logger.ErrorField(err))
		return answer, confidence, sources, questionID, timestamp, fmt.Errorf("回答已生成，但保存记录失败: %w", err)
	}

	return answer, confidence, sources, questionID, timestamp, nil
}

func (s *qaServiceImpl) BatchAskQuestions(ctx context.Context, questions []string, userID int64) (map[string]string, error) {
	logger.Info("批量处理问答请求",
		logger.IntField("count", len(questions)),
		logger.Int64Field("user_id", userID))

	answers := make(map[string]string)

	for _, q := range questions {
		answer, _, _, _, _, err := s.AskQuestion(ctx, q, userID, nil)
		if err != nil {
			logger.Warn("单个问题处理失败",
				logger.StringField("question", q),
				logger.ErrorField(err))
			answers[q] = "处理失败，请稍后重试"
		} else {
			answers[q] = answer
		}
	}

	return answers, nil
}

func (s *qaServiceImpl) GetHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error) {
	logger.Info("获取问答历史",
		logger.Int64Field("user_id", userID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	records, total, err := s.qaRepo.ListQARecords(ctx, userID, page, pageSize)
	if err != nil {
		logger.Error("获取问答历史失败", logger.ErrorField(err))
		return nil, 0, err
	}

	return records, total, nil
}

func (s *qaServiceImpl) SubmitFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error {
	logger.Info("提交用户反馈",
		logger.StringField("question_id", questionID),
		logger.StringField("feedback", feedback))

	if questionID == "" {
		return errors.New("问题ID不能为空")
	}

	if rating != nil && (*rating < 1 || *rating > 5) {
		return errors.New("评分必须在1-5之间")
	}

	err := s.qaRepo.UpdateFeedback(ctx, questionID, feedback, rating)
	if err != nil {
		logger.Error("更新用户反馈失败", logger.ErrorField(err))
		return err
	}

	return nil
}

func (s *qaServiceImpl) GetRecommendedQuestions(ctx context.Context, userID int64) ([]string, error) {
	logger.Info("获取推荐问题",
		logger.Int64Field("user_id", userID))

	recommendations := []string{
		"什么是知识图谱？",
		"如何使用向量搜索？",
		"AI助手能做什么？",
		"如何提升问答准确度？",
	}

	return recommendations, nil
}

func (s *qaServiceImpl) checkQuestionRateLimit(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}

	key := fmt.Sprintf("ratelimit:question:%d", userID)

	current, err := s.cache.IncrBy(ctx, key, 1)
	if err != nil {
		logger.Warn("问答限流计数失败，放行",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
		return nil
	}

	if current == 1 {
		s.cache.Expire(ctx, key, time.Minute)
	}

	if current > 30 {
		logger.Warn("问答频率超限",
			logger.Int64Field("user_id", userID),
			logger.Int64Field("count", current))
		return fmt.Errorf("提问过于频繁，请%d秒后再试", 60)
	}

	return nil
}
