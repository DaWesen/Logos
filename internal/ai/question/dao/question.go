package dao

import (
	"context"

	"gorm.io/gorm"

	"Logos/internal/ai/question/model"
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
	logger.Info("创建问答记录",
		logger.StringField("question_id", record.ID),
		logger.Int64Field("user_id", record.UserID))

	result := r.db.WithContext(ctx).Create(record)
	if result.Error != nil {
		logger.Error("创建问答记录失败", logger.ErrorField(result.Error))
		return result.Error
	}

	return nil
}

func (r *qaRepository) GetQARecord(ctx context.Context, id string) (*model.QARecord, error) {
	logger.Info("获取问答记录",
		logger.StringField("question_id", id))

	var record model.QARecord
	result := r.db.WithContext(ctx).Where("id = ?", id).First(&record)
	if result.Error != nil {
		logger.Error("获取问答记录失败", logger.ErrorField(result.Error))
		return nil, result.Error
	}

	return &record, nil
}

func (r *qaRepository) ListQARecords(ctx context.Context, userID int64, page, pageSize int) ([]*model.QARecord, int64, error) {
	logger.Info("获取问答历史列表",
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
		logger.Error("获取问答历史总数失败", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	result = r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("timestamp DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&records)
	if result.Error != nil {
		logger.Error("获取问答历史列表失败", logger.ErrorField(result.Error))
		return nil, 0, result.Error
	}

	return records, total, nil
}

func (r *qaRepository) UpdateFeedback(ctx context.Context, questionID string, feedback string, rating *int32) error {
	logger.Info("更新用户反馈",
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
		logger.Error("更新用户反馈失败", logger.ErrorField(result.Error))
		return result.Error
	}

	return nil
}
