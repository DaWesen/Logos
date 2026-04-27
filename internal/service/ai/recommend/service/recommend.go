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
	logger.Info("GetRecommendations",
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
				logger.Info("��Redis�����ȡ�Ƽ�����ɹ�",
					logger.Int64Field("user_id", userID))
				return cachedResult.Items, cachedResult.Total, nil
			}
		}
	}

	items, total, err := s.recommendRepo.GetRecommendations(ctx, userID, recommendType, limit)
	if err != nil {
		logger.Error("�����ݿ��ȡ�Ƽ�ʧ��", logger.ErrorField(err))
		return nil, 0, err
	}

	if len(items) == 0 {
		logger.Info("���ݿ������Ƽ�������Ĭ���Ƽ�")
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
	cacheData := map[string]interface{}{
		"items": items,
		"total": total,
	}

	dataJSON, _ := json.Marshal(cacheData)

	err := s.cache.Set(ctx, cacheKey, string(dataJSON), 30*time.Minute)
	if err != nil {
		logger.Warn("�����Ƽ������Redisʧ��",
			logger.Int64Field("user_id", userID),
			logger.ErrorField(err))
	} else {
		logger.Info("�ѻ����Ƽ������Redis",
			logger.Int64Field("user_id", userID),
			logger.StringField("cache_key", cacheKey),
			logger.IntField("item_count", len(items)))
	}
}

func (s *recommendServiceImpl) GetRelatedRecommendations(ctx context.Context, entityID string, recommendType string, limit int) ([]*model.RecommendationItem, int64, error) {
	logger.Info("��ȡ����Ƽ�",
		logger.StringField("entity_id", entityID),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if entityID == "" {
		return nil, 0, errors.New("ʵ��ID����Ϊ��")
	}

	if limit < 1 || limit > 100 {
		limit = 10
	}

	if s.vectorService != nil {
		similarResults, err := s.vectorService.SearchSimilar(ctx, entityID, limit)
		if err != nil {
			logger.Warn("��������ʧ��", logger.ErrorField(err))
		} else if len(similarResults) > 0 {
			logger.Info("������������ȡ���������",
				logger.IntField("count", len(similarResults)))
		}
	}

	if s.knowledgeService != nil {
		entities, err := s.knowledgeService.SearchEntities(ctx, entityID)
		if err != nil {
			logger.Warn("֪ʶͼ������ʧ��", logger.ErrorField(err))
		} else if len(entities) > 0 {
			logger.Info("��֪ʶͼ�׻�ȡ�����ʵ��",
				logger.IntField("count", len(entities)))
		}
	}

	items, total, err := s.recommendRepo.GetRelatedRecommendations(ctx, entityID, recommendType, limit)
	if err != nil {
		logger.Error("�����ݿ��ȡ����Ƽ�ʧ��", logger.ErrorField(err))
		return nil, 0, err
	}

	if len(items) == 0 {
		logger.Info("���ݿ���������Ƽ�������Ĭ������Ƽ�")
		items = s.generateDefaultRecommendations(recommendType, limit)
		total = int64(len(items))
	}

	return items, total, nil
}

func (s *recommendServiceImpl) SubmitFeedback(ctx context.Context, itemID string, userID int64, action string, timestamp int64) error {
	logger.Info("�ύ�Ƽ�����",
		logger.StringField("item_id", itemID),
		logger.Int64Field("user_id", userID),
		logger.StringField("action", action))

	if itemID == "" {
		return errors.New("�Ƽ���ID����Ϊ��")
	}
	if action == "" {
		return errors.New("��������Ϊ��")
	}

	if timestamp == 0 {
		timestamp = time.Now().Unix()
	}

	history := &model.RecommendationHistory{
		ID:        uuid.New().String(),
		ItemID:    itemID,
		ItemType:  "default",
		Title:     "�Ƽ���",
		Action:    action,
		UserID:    userID,
		Timestamp: timestamp,
	}

	err := s.recommendRepo.SaveHistory(ctx, history)
	if err != nil {
		logger.Error("�����Ƽ���ʷʧ��", logger.ErrorField(err))
		return err
	}

	return nil
}

func (s *recommendServiceImpl) GetRecommendationHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.RecommendationHistory, int64, error) {
	logger.Info("��ȡ�Ƽ���ʷ",
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
		logger.Error("��ȡ�Ƽ���ʷʧ��", logger.ErrorField(err))
		return nil, 0, err
	}

	return histories, total, nil
}

func (s *recommendServiceImpl) BatchGetRecommendations(ctx context.Context, userIDs []int64, recommendType string, limit int) (map[int64][]*model.RecommendationItem, error) {
	logger.Info("������ȡ�Ƽ�",
		logger.IntField("user_count", len(userIDs)),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	if len(userIDs) == 0 {
		return nil, errors.New("�û�ID�б�����Ϊ��")
	}

	if limit < 1 || limit > 100 {
		limit = 10
	}

	result := make(map[int64][]*model.RecommendationItem)

	for _, userID := range userIDs {
		items, _, err := s.GetRecommendations(ctx, userID, recommendType, limit, nil)
		if err != nil {
			logger.Warn("��ȡ�û��Ƽ�ʧ��",
				logger.Int64Field("user_id", userID),
				logger.ErrorField(err))
			result[userID] = []*model.RecommendationItem{}
		} else {
			result[userID] = items
		}
	}

	return result, nil
}

func (s *recommendServiceImpl) generateDefaultRecommendations(recommendType string, limit int) []*model.RecommendationItem {
	now := time.Now().Unix()
	items := []*model.RecommendationItem{
		{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       "֪ʶͼ������",
			Description: "�˽�֪ʶͼ�׵Ļ��������Ӧ��",
			Score:       0.9,
			EntityID:    "kg_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       "������������",
			Description: "̽������������AI�Ƽ��е�Ӧ��",
			Score:       0.85,
			EntityID:    "vector_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "relation",
			Title:       "ʵ���������",
			Description: "����֪ʶͼ����ʵ��֮��Ĺ�����ϵ",
			Score:       0.8,
			EntityID:    "relation_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "entity",
			Title:       "AI�Ƽ��㷨",
			Description: "�˽���Ի��Ƽ��㷨��ԭ����ʵ��",
			Score:       0.75,
			EntityID:    "ai_001",
			CreatedAt:   now,
		},
		{
			ID:          uuid.New().String(),
			Type:        "document",
			Title:       "ϵͳʹ��ָ��",
			Description: "Noahƽ̨����ʹ��ָ��",
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
