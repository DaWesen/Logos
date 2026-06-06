package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/ai/question/dao"
	"Logos/internal/service/ai/question/model"
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
	logger.Info("Processing question",
		logger.StringField("content", content),
		logger.Int64Field("user_id", userID))

	if content == "" {
		return "", 0, nil, "", 0, errors.New("question content cannot be empty")
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
			logger.Warn("Failed to search knowledge graph", logger.ErrorField(err))
		} else if len(entities) > 0 {
			logger.Info("Found entities from knowledge graph",
				logger.IntField("count", len(entities)))
			backgroundInfo = append(backgroundInfo, entities...)
			sources = append(sources, "knowledge_graph")
		}
	}

	if s.searchService != nil {
		searchResults, err := s.searchService.Search(ctx, content, "document")
		if err != nil {
			logger.Warn("Failed to search documents", logger.ErrorField(err))
		} else if len(searchResults) > 0 {
			logger.Info("Found documents from search",
				logger.IntField("count", len(searchResults)))
			backgroundInfo = append(backgroundInfo, searchResults...)
			sources = append(sources, "search")
		}
	}

	if s.vectorService != nil {
		vectorResults, err := s.vectorService.SearchSimilar(ctx, content, 5)
		if err != nil {
			logger.Warn("Failed to search vectors", logger.ErrorField(err))
		} else if len(vectorResults) > 0 {
			logger.Info("Found similar from vector search",
				logger.IntField("count", len(vectorResults)))
			backgroundInfo = append(backgroundInfo, vectorResults...)
			sources = append(sources, "vector")
		}
	}

	if s.einoClient != nil && s.einoClient.HasChatModel() {
		prompt := "You are a helpful AI assistant. Please answer the user's question based on the following information.\n\n"
		if len(backgroundInfo) > 0 {
			prompt += "Reference information:\n"
			for _, info := range backgroundInfo {
				prompt += "- " + info + "\n"
			}
			prompt += "\n"
		}
		prompt += "User question: " + content

		messages := []string{
			"You are a helpful AI assistant. Please answer the user's question based on the provided reference information.",
			prompt,
		}

		var err error
		answer, err = s.einoClient.Chat(ctx, messages)
		if err != nil {
			logger.Warn("Failed to answer using Eino, using default answer", logger.ErrorField(err))
			answer = "Thank you for your question. We are currently working on answering it. Please try again later."
			confidence = 0.5
		} else {
			confidence = 0.9
		}
	} else {
		logger.Warn("Eino client not initialized, using default answer")
		answer = "Thank you for your question. Our AI assistant is being prepared. Please try again later."
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
		logger.Error("Failed to create QA record", logger.ErrorField(err))
		return answer, confidence, sources, questionID, timestamp, fmt.Errorf("answered but failed to record: %w", err)
	}

	return answer, confidence, sources, questionID, timestamp, nil
}

func (s *qaServiceImpl) BatchAskQuestions(ctx context.Context, questions []string, userID int64) (map[string]string, error) {
	logger.Info("Processing batch questions",
		logger.IntField("count", len(questions)),
		logger.Int64Field("user_id", userID))

	answers := make(map[string]string)

	for _, q := range questions {
		answer, _, _, _, _, err := s.AskQuestion(ctx, q, userID, nil)
		if err != nil {
			logger.Warn("Failed to process question",
				logger.StringField("question", q),
				logger.ErrorField(err))
			answers[q] = "Failed to answer, please try again later"
		} else {
			answers[q] = answer
		}
	}

	return answers, nil
}

func (s *qaServiceImpl) GetHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error) {
	logger.Info("Getting QA history",
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

	if s.einoClient != nil && s.einoClient.HasChatModel() {
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

		result, err := s.einoClient.Chat(ctx, []string{prompt})
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
