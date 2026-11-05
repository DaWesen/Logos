package dao

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"Logos/internal/service/ai/recommend/model"
	"Logos/pkg/logger"
	"Logos/pkg/pagination"
)

type RecommendRepository interface {
	SaveRecommendation(ctx context.Context, item *model.RecommendationItem) error
	GetRecommendations(ctx context.Context, userID int64, recommendType string, limit int) ([]*model.RecommendationItem, int64, error)
	GetRecommendationByID(ctx context.Context, itemID string) (*model.RecommendationItem, error)
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
	// 已读过滤失败只会让推荐重复，但这是用户可见的质量问题，不能完全静默。
	if err := r.db.WithContext(ctx).Model(&model.RecommendationHistory{}).
		Where("user_id = ?", userID).
		Pluck("item_id", &seenEntityIDs).Error; err != nil {
		logger.Warn("Failed to load seen recommendation ids",
			logger.Int64Field("user_id", userID), logger.ErrorField(err))
	}

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

// GetRecommendationByID 按 ID 直接取推荐项。
//
// 反馈链路不能用 GetRecommendations 来「顺手查一下」：那个方法会排除用户
// 已经产生过历史记录的条目，而用户往往正是对看过的条目点赞/点踩；它还会
// 在缓存失效时重新触发整条推荐生成链路。按主键查一次即可。
func (r *recommendRepository) GetRecommendationByID(ctx context.Context, itemID string) (*model.RecommendationItem, error) {
	var item model.RecommendationItem
	err := r.db.WithContext(ctx).Where("id = ?", itemID).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
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

	offset, pageSize := pagination.Offset(page, pageSize)

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
