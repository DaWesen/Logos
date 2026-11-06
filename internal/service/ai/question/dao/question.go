package dao

import (
	"context"

	"gorm.io/gorm"

	"Logos/internal/service/ai/question/model"
	"Logos/pkg/logger"
	"Logos/pkg/pagination"
)

type QARepository interface {
	CreateQARecord(ctx context.Context, record *model.QARecord) error
	GetQARecord(ctx context.Context, id string) (*model.QARecord, error)
	ReplaceDegradedAnswer(ctx context.Context, id, answer string, confidence float64) (bool, error)
	ListQARecords(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error)
	UpdateFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error
}

type qaRepository struct {
	db *gorm.DB
}

func NewQARepository(db *gorm.DB) QARepository {
	return &qaRepository{
		db: db,
	}
}

func (r *qaRepository) CreateQARecord(ctx context.Context, record *model.QARecord) error {
	logger.Info("Creating QA record",
		logger.StringField("question_id", record.ID),
		logger.Int64Field("user_id", record.UserID))

	result := r.db.WithContext(ctx).Create(record)
	if result.Error != nil {
		logger.Error("Failed to create QA record", logger.ErrorField(result.Error))
		return result.Error
	}

	return nil
}

func (r *qaRepository) GetQARecord(ctx context.Context, id string) (*model.QARecord, error) {
	var record model.QARecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&record).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}

// ReplaceDegradedAnswer 用真正生成的回答覆盖同一条降级记录。
//
// 用条件更新而不是「先查再写」：两个重试实例可能同时拿到同一个降级记录，
// 只有第一个把 degraded 从 true 改成 false 的实例应该生效，后到的实例
// 通过 RowsAffected=0 得知自己没抢到，回查即可拿到先写入的答案。
// 这样既避免了覆盖别人已经生成的正常答案，也不需要额外加分布式锁。
func (r *qaRepository) ReplaceDegradedAnswer(
	ctx context.Context,
	id string,
	answer string,
	confidence float64,
) (bool, error) {
	result := r.db.WithContext(ctx).Model(&model.QARecord{}).
		Where("id = ? AND degraded = ?", id, true).
		Updates(map[string]interface{}{
			"answer":     answer,
			"confidence": confidence,
			"degraded":   false,
		})
	if result.Error != nil {
		logger.Error("Failed to replace degraded QA answer",
			logger.StringField("question_id", id),
			logger.ErrorField(result.Error))
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *qaRepository) ListQARecords(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error) {
	logger.Info("Getting QA history list",
		logger.Int64Field("user_id", userID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	var records []*model.QARecord
	var total int64

	offset, pageSize := pagination.Offset(page, pageSize)

	result := r.db.WithContext(ctx).Model(&model.QARecord{}).
		Where("user_id = ?", userID).
		Count(&total)
	if result.Error != nil {
		logger.Error("Failed to get QA history count", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	result = r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("timestamp DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&records)
	if result.Error != nil {
		logger.Error("Failed to get QA history list", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	return records, total, nil
}

func (r *qaRepository) UpdateFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error {
	logger.Info("Updating user feedback",
		logger.StringField("question_id", questionID))

	updates := map[string]interface{}{}
	if feedback != "" {
		updates["feedback"] = feedback
	}
	if rating != nil {
		updates["rating"] = *rating
	}

	result := r.db.WithContext(ctx).Model(&model.QARecord{}).
		Where("id = ?", questionID).
		Updates(updates)
	if result.Error != nil {
		logger.Error("Failed to update user feedback", logger.ErrorField(result.Error))
		return result.Error
	}

	return nil
}
