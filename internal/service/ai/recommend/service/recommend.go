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

	if len(items) > 0 {
		s.cacheRecommendations(ctx, userID, recommendType, items, total)
		return items, total, nil
	}

	logger.Info("数据库没有推荐数据，基于多源检索生成推荐")
	items = s.generateSmartRecommendations(ctx, userID, recommendType, limit)
	total = int64(len(items))

	if len(items) > 0 {
		s.cacheRecommendations(ctx, userID, recommendType, items, total)
	}

	return items, total, nil
}

func (s *recommendServiceImpl) generateSmartRecommendations(ctx context.Context, userID int64, recommendType string, limit int) []*model.RecommendationItem {
	var items []*model.RecommendationItem
	now := time.Now().Unix()
	seen := make(map[string]bool)

	if s.vectorService != nil {
		query := recommendType
		if query == "" {
			query = "knowledge"
		}
		similarIDs, err := s.vectorService.SearchSimilar(ctx, query, limit)
		if err != nil {
			logger.Warn("向量搜索生成推荐失败", logger.ErrorField(err))
		} else {
			for i, id := range similarIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				score := 0.9 - float64(i)*0.05
				if score < 0.5 {
					score = 0.5
				}
				items = append(items, &model.RecommendationItem{
					ID:          uuid.New().String(),
					Type:        "vector",
					Title:       fmt.Sprintf("相关内容 #%d", i+1),
					Description: fmt.Sprintf("基于向量相似度推荐的内容（相似度: %.0f%%）", score*100),
					Score:       score,
					EntityID:    id,
					CreatedAt:   now,
				})
			}
		}
	}

	if s.knowledgeService != nil {
		query := recommendType
		if query == "" {
			query = "entity"
		}
		entityIDs, err := s.knowledgeService.SearchEntities(ctx, query)
		if err != nil {
			logger.Warn("知识图谱生成推荐失败", logger.ErrorField(err))
		} else {
			for i, id := range entityIDs {
				if seen[id] {
					continue
				}
				seen[id] = true
				score := 0.85 - float64(i)*0.05
				if score < 0.4 {
					score = 0.4
				}
				items = append(items, &model.RecommendationItem{
					ID:          uuid.New().String(),
					Type:        "entity",
					Title:       fmt.Sprintf("知识实体 #%d", i+1),
					Description: fmt.Sprintf("基于知识图谱关联推荐的实体（相关度: %.0f%%）", score*100),
					Score:       score,
					EntityID:    id,
					CreatedAt:   now,
				})
			}
		}
	}

	if s.einoClient != nil && len(items) > 0 {
		s.enrichWithEino(ctx, items)
	}

	if len(items) > limit {
		items = items[:limit]
	}

	return items
}

func (s *recommendServiceImpl) enrichWithEino(ctx context.Context, items []*model.RecommendationItem) {
	for _, item := range items {
		if item.Title != "" && !startsWith(item.Title, "相关内容") && !startsWith(item.Title, "知识实体") {
			continue
		}
		prompt := fmt.Sprintf("请为以下推荐内容生成一个简洁的中文标题（不超过20字），不要加引号：\n描述：%s\n实体ID：%s", item.Description, item.EntityID)
		result, err := s.einoClient.Chat(ctx, []string{prompt})
		if err != nil {
			logger.Warn("Eino生成标题失败", logger.ErrorField(err))
			continue
		}
		if result != "" {
			item.Title = result
		}
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
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

	var vectorIDs []string
	if s.vectorService != nil {
		similarResults, err := s.vectorService.SearchSimilar(ctx, entityID, limit)
		if err != nil {
			logger.Warn("向量搜索失败", logger.ErrorField(err))
		} else {
			vectorIDs = similarResults
			logger.Info("从向量搜索获取相关推荐",
				logger.IntField("count", len(similarResults)))
		}
	}

	var knowledgeIDs []string
	if s.knowledgeService != nil {
		entities, err := s.knowledgeService.SearchEntities(ctx, entityID)
		if err != nil {
			logger.Warn("知识图谱搜索失败", logger.ErrorField(err))
		} else {
			knowledgeIDs = entities
			logger.Info("从知识图谱获取相关实体",
				logger.IntField("count", len(entities)))
		}
	}

	items, total, err := s.recommendRepo.GetRelatedRecommendations(ctx, entityID, recommendType, limit)
	if err != nil {
		logger.Error("从数据库获取相关推荐失败", logger.ErrorField(err))
		return nil, 0, err
	}

	now := time.Now().Unix()
	seen := make(map[string]bool)
	for _, item := range items {
		if item.EntityID != "" {
			seen[item.EntityID] = true
		}
	}

	for i, id := range vectorIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		score := 0.9 - float64(i)*0.05
		if score < 0.5 {
			score = 0.5
		}
		items = append(items, &model.RecommendationItem{
			ID:          uuid.New().String(),
			Type:        "vector",
			Title:       fmt.Sprintf("向量相似内容 #%d", i+1),
			Description: fmt.Sprintf("基于向量相似度推荐（相似度: %.0f%%）", score*100),
			Score:       score,
			EntityID:    id,
			CreatedAt:   now,
		})
	}

	for i, id := range knowledgeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		score := 0.85 - float64(i)*0.05
		if score < 0.4 {
			score = 0.4
		}
		items = append(items, &model.RecommendationItem{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       fmt.Sprintf("知识图谱关联 #%d", i+1),
			Description: fmt.Sprintf("基于知识图谱关联推荐（相关度: %.0f%%）", score*100),
			Score:       score,
			EntityID:    id,
			CreatedAt:   now,
		})
	}

	if s.einoClient != nil && len(items) > 0 {
		s.enrichWithEino(ctx, items)
	}

	total = int64(len(items))
	if len(items) > limit {
		items = items[:limit]
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

	itemType := "unknown"
	title := itemID
	dbItems, _, err := s.recommendRepo.GetRecommendations(ctx, userID, "", 100)
	if err == nil {
		for _, item := range dbItems {
			if item.ID == itemID {
				itemType = item.Type
				title = item.Title
				break
			}
		}
	}

	history := &model.RecommendationHistory{
		ID:        uuid.New().String(),
		ItemID:    itemID,
		ItemType:  itemType,
		Title:     title,
		Action:    action,
		UserID:    userID,
		Timestamp: timestamp,
	}

	err = s.recommendRepo.SaveHistory(ctx, history)
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
