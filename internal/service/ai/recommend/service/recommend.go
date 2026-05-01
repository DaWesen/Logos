package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/ai/recommend/dao"
	"Logos/internal/service/ai/recommend/model"
	"Logos/pkg/cache"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
)

type VectorService interface {
	SearchSimilar(ctx context.Context, text string, topK int) ([]string, error)
}

type KnowledgeService interface {
	SearchEntities(ctx context.Context, query string) ([]string, error)
}

type RecommendService interface {
	GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int, context map[string]string) ([]*model.RecommendationItem, int64, error)
	GetRelatedRecommendations(ctx context.Context, entityID string, recommendType string, limit int) ([]*model.RecommendationItem, int64, error)
	SubmitFeedback(ctx context.Context, itemID string, userID int64, action string, timestamp int64) error
	GetRecommendationHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.RecommendationHistory, int64, error)
	BatchGetRecommendations(ctx context.Context, userIDs []int64, recommendType string, limit int) (map[int64][]*model.RecommendationItem, error)
}

type recommendServiceImpl struct {
	recommendRepo    dao.RecommendRepository
	einoClient       *eino.EinoManager
	vectorService    VectorService
	knowledgeService KnowledgeService
	cache            cache.Cache
}

func NewRecommendService(
	recommendRepo dao.RecommendRepository,
	einoClient *eino.EinoManager,
	vectorService VectorService,
	knowledgeService KnowledgeService,
) RecommendService {
	return &recommendServiceImpl{
		recommendRepo:    recommendRepo,
		einoClient:       einoClient,
		vectorService:    vectorService,
		knowledgeService: knowledgeService,
		cache:            cache.NewRedisCache(),
	}
}

func (s *recommendServiceImpl) GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int, context map[string]string) ([]*model.RecommendationItem, int64, error) {
	logger.Info("获取推荐请求",
		logger.Int64Field("user_id", userID),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if limit < 1 || limit > 100 {
		limit = 10
	}

	if s.cache != nil {
		cacheKey := fmt.Sprintf("recommend:%d:%s:limit:%d", userID, recommendType, limit)
		cachedData, err := s.cache.Get(ctx, cacheKey)
		if err == nil && cachedData != "" {
			var cachedResult struct {
				Items []*model.RecommendationItem `json:"items"`
				Total int64                       `json:"total"`
			}
			if json.Unmarshal([]byte(cachedData), &cachedResult) == nil {
				logger.Info("从Redis缓存中获取推荐数据成功",
					logger.Int64Field("user_id", userID))
				return cachedResult.Items, cachedResult.Total, nil
			}
		}
	}

	items, total, err := s.recommendRepo.GetRecommendations(ctx, userID, recommendType, limit)
	if err != nil {
		logger.Error("从数据库获取推荐失败", logger.ErrorField(err))
		return nil, 0, err
	}

	if len(items) == 0 {
		logger.Info("数据库没有推荐数据，返回默认推荐")
		items = s.generateDefaultRecommendations(recommendType, limit)
		total = int64(len(items))
	}

	s.cacheRecommendations(ctx, userID, recommendType, items, total)

	return items, total, nil
}

func (s *recommendServiceImpl) cacheRecommendations(ctx context.Context, userID int64, recommendType string, items []*model.RecommendationItem, total int64) {
	if s.cache == nil {
		return
	}

	cacheKey := fmt.Sprintf("recommend:%d:%s:limit:%d", userID, recommendType, len(items))
	cacheData := map[string]any{
		"items": items,
		"total": total,
	}

	dataJSON, _ := json.Marshal(cacheData)

	err := s.cache.Set(ctx, cacheKey, string(dataJSON), 30*time.Minute)
	if err != nil {
		logger.Warn("缓存推荐数据到Redis失败",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
	} else {
		logger.Info("已缓存推荐数据到Redis",
			logger.Int64Field("user_id", userID),
			logger.StringField("cache_key", cacheKey),
			logger.IntField("item_count", len(items)))
	}
}

func (s *recommendServiceImpl) GetRelatedRecommendations(ctx context.Context, entityID string, recommendType string, limit int) ([]*model.RecommendationItem, int64, error) {
	logger.Info("获取相关推荐请求",
		logger.StringField("entity_id", entityID),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if entityID == "" {
		return nil, 0, errors.New("实体ID不能为空")
	}

	if limit < 1 || limit > 100 {
		limit = 10
	}

	if s.vectorService != nil {
		similarResults, err := s.vectorService.SearchSimilar(ctx, entityID, limit)
		if err != nil {
			logger.Warn("向量搜索失败", logger.ErrorField(err))
		} else if len(similarResults) > 0 {
			logger.Info("从向量搜索获取相关推荐",
				logger.IntField("count", len(similarResults)))
		}
	}

	if s.knowledgeService != nil {
		entities, err := s.knowledgeService.SearchEntities(ctx, entityID)
		if err != nil {
			logger.Warn("知识图谱搜索失败", logger.ErrorField(err))
		} else if len(entities) > 0 {
			logger.Info("从知识图谱获取相关实体",
				logger.IntField("count", len(entities)))
		}
	}

	items, total, err := s.recommendRepo.GetRelatedRecommendations(ctx, entityID, recommendType, limit)
	if err != nil {
		logger.Error("从数据库获取相关推荐失败", logger.ErrorField(err))
		return nil, 0, err
	}

	if len(items) == 0 {
		logger.Info("数据库没有相关推荐数据，返回默认相关推荐")
		items = s.generateDefaultRecommendations(recommendType, limit)
		total = int64(len(items))
	}

	return items, total, nil
}

func (s *recommendServiceImpl) SubmitFeedback(ctx context.Context, itemID string, userID int64, action string, timestamp int64) error {
	logger.Info("提交推荐反馈请求",
		logger.StringField("item_id", itemID),
		logger.Int64Field("user_id", userID),
		logger.StringField("action", action))

	if itemID == "" {
		return errors.New("推荐项ID不能为空")
	}
	if action == "" {
		return errors.New("操作类型不能为空")
	}

	if timestamp == 0 {
		timestamp = time.Now().Unix()
	}

	history := &model.RecommendationHistory{
		ID:        uuid.New().String(),
		ItemID:    itemID,
		ItemType:  "default",
		Title:     "推荐项",
		Action:    action,
		UserID:    userID,
		Timestamp: timestamp,
	}

	err := s.recommendRepo.SaveHistory(ctx, history)
	if err != nil {
		logger.Error("保存推荐历史失败", logger.ErrorField(err))
		return err
	}

	return nil
}

func (s *recommendServiceImpl) GetRecommendationHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.RecommendationHistory, int64, error) {
	logger.Info("获取推荐历史请求",
		logger.Int64Field("user_id", userID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	histories, total, err := s.recommendRepo.GetHistory(ctx, userID, page, pageSize)
	if err != nil {
		logger.Error("获取推荐历史失败", logger.ErrorField(err))
		return nil, 0, err
	}

	return histories, total, nil
}

func (s *recommendServiceImpl) BatchGetRecommendations(ctx context.Context, userIDs []int64, recommendType string, limit int) (map[int64][]*model.RecommendationItem, error) {
	logger.Info("批量获取推荐请求",
		logger.IntField("user_count", len(userIDs)),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if len(userIDs) == 0 {
		return nil, errors.New("用户ID列表不能为空")
	}

	if limit < 1 || limit > 100 {
		limit = 10
	}

	result := make(map[int64][]*model.RecommendationItem)

	for _, userID := range userIDs {
		items, _, err := s.GetRecommendations(ctx, userID, recommendType, limit, nil)
		if err != nil {
			logger.Warn("获取用户推荐失败",
				logger.Int64Field("user_id", userID),
				logger.ErrorField(err))
			result[userID] = []*model.RecommendationItem{}
		} else {
			result[userID] = items
		}
	}

	return result, nil
}

func (s *recommendServiceImpl) generateDefaultRecommendations(_ string, limit int) []*model.RecommendationItem {
	now := time.Now().Unix()
	items := []*model.RecommendationItem{
		{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       "知识图谱初探",
			Description: "了解知识图谱的基本概念与应用",
			Score:       0.9,
			EntityID:    "kg_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       "向量搜索入门",
			Description: "探索向量搜索在AI推荐中的应用",
			Score:       0.85,
			EntityID:    "vector_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "relation",
			Title:       "实体关系发现",
			Description: "识别知识图谱中实体之间的隐藏关系",
			Score:       0.8,
			EntityID:    "relation_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       "AI推荐算法",
			Description: "了解个性化推荐算法的原理与实现",
			Score:       0.75,
			EntityID:    "ai_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "document",
			Title:       "系统使用指南",
			Description: "Noah平台的详细使用指南",
			Score:       0.7,
			EntityID:    "doc_001",
			CreatedAt:   now,
		},
	}

	if limit > len(items) {
		limit = len(items)
	}

	return items[:limit]
}
