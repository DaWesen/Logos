
package dao

import (
	"context"

	"gorm.io/gorm"

	"Logos/internal/service/ai/question/model"
	"Logos/pkg/logger"
)

type QARepository interface {
	CreateQARecord(ctx context.Context, record *model.QARecord) error
	GetQARecord(ctx context.Context, id string) (*model.QARecord, error)
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
	logger.Info("Getting QA record",
		logger.StringField("question_id", id))

	var record model.QARecord
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&record)
	if result.Error != nil {
		logger.Error("Failed to get QA record", logger.ErrorField(result.Error))
		return nil, result.Error
	}

	return &record, nil
}

func (r *qaRepository) ListQARecords(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error) {
	logger.Info("Getting QA history list",
		logger.Int64Field("user_id", userID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	var records []*model.QARecord
	var total int64

	offset := (page - 1) * pageSize

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
