
package dao

import (
	"context"

	"gorm.io/gorm"

	"Logos/internal/service/ai/recommend/model"
	"Logos/pkg/logger"
)

type RecommendRepository interface {
	SaveRecommendation(ctx context.Context, item *model.RecommendationItem) error
	GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int) ([]*model.RecommendationItem, int64, error)
	GetRelatedRecommendations(ctx context.Context, entityID string, recommendType string, limit int) ([]*model.RecommendationItem, int64, error)
	SaveHistory(ctx context.Context, history *model.RecommendationHistory) error
	GetHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.RecommendationHistory, int64, error)
}

type recommendRepository struct {
	db *gorm.DB
}

func NewRecommendRepository(db *gorm.DB) RecommendRepository {
	return &recommendRepository{
		db: db,
	}
}

func (r *recommendRepository) SaveRecommendation(ctx context.Context, item *model.RecommendationItem) error {
	logger.Info("Saving recommendation",
		logger.StringField("item_id", item.ID),
		logger.StringField("type", item.Type),
		logger.Float64Field("score", item.Score))

	result := r.db.WithContext(ctx).Create(item)
	if result.Error != nil {
		logger.Error("Failed to save recommendation", logger.ErrorField(result.Error))
		return result.Error
	}

	return nil
}

func (r *recommendRepository) GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int) ([]*model.RecommendationItem, int64, error) {
	var items []*model.RecommendationItem
	var total int64

	var seenEntityIDs []string
	r.db.WithContext(ctx).Model(&model.RecommendationHistory{}).
		Where("user_id = ?", userID).
		Pluck("item_id", &seenEntityIDs)

	query := r.db.WithContext(ctx).Model(&model.RecommendationItem{})
	if recommendType != "" {
		query = query.Where("type = ?", recommendType)
	}
	if len(seenEntityIDs) > 0 {
		query = query.Where("id NOT IN ?", seenEntityIDs)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := query.Order("score DESC, created_at DESC").
		Limit(limit).
		Find(&items).Error; err != nil {
		return nil, 0, err
	}

	return items, total, nil
}

func (r *recommendRepository) GetRelatedRecommendations(ctx context.Context, entityID string, recommendType string, limit int) ([]*model.RecommendationItem, int64, error) {
	logger.Info("Getting related recommendations",
		logger.StringField("entity_id", entityID),
		logger.StringField("type", recommendType),
		logger.IntField("limit", limit))

	var items []*model.RecommendationItem
	var total int64

	query := r.db.WithContext(ctx).Model(&model.RecommendationItem{}).
		Where("entity_id = ?", entityID)
	if recommendType != "" {
		query = query.Where("type = ?", recommendType)
	}

	result := query.Count(&total)
	if result.Error != nil {
		logger.Error("Failed to get related recommendation count", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	result = query.Order("score DESC, created_at DESC").
		Limit(limit).
		Find(&items)
	if result.Error != nil {
		logger.Error("Failed to get related recommendation list", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	return items, total, nil
}

func (r *recommendRepository) SaveHistory(ctx context.Context, history *model.RecommendationHistory) error {
	logger.Info("Saving recommendation history",
		logger.StringField("history_id", history.ID),
		logger.Int64Field("user_id", history.UserID),
		logger.StringField("action", history.Action))

	result := r.db.WithContext(ctx).Create(history)
	if result.Error != nil {
		logger.Error("Failed to save recommendation history", logger.ErrorField(result.Error))
		return result.Error
	}

	return nil
}

func (r *recommendRepository) GetHistory(ctx context.Context, userID int64, page, pageSize int) ([]*model.RecommendationHistory, int64, error) {
	logger.Info("Getting recommendation history",
		logger.Int64Field("user_id", userID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	var histories []*model.RecommendationHistory
	var total int64

	offset := (page - 1) * pageSize

	result := r.db.WithContext(ctx).Model(&model.RecommendationHistory{}).
		Where("user_id = ?", userID).
		Count(&total)
	if result.Error != nil {
		logger.Error("Failed to get recommendation history count", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	result = r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("timestamp DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&histories)
	if result.Error != nil {
		logger.Error("Failed to get recommendation history list", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	return histories, total, nil
}
