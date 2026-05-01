package dao

import (
	"context"
	"time"

	"Logos/internal/service/ai/moderation/model"

	"gorm.io/gorm"
)

type ModerationRepository interface {
	CreateModerationRecord(ctx context.Context, record *model.ModerationRecord) error
	ListModerationRecords(ctx context.Context, result string, startTime, endTime *time.Time, page, pageSize int) ([]*model.ModerationRecord, int64, error)
	CreateTranslationRecord(ctx context.Context, record *model.TranslationRecord) error
}

type moderationRepository struct {
	db *gorm.DB
}

func NewModerationRepository(db *gorm.DB) ModerationRepository {
	return &moderationRepository{db: db}
}

func (r *moderationRepository) CreateModerationRecord(ctx context.Context, record *model.ModerationRecord) error {
	return r.db.WithContext(ctx).Create(record).Error
}

func (r *moderationRepository) ListModerationRecords(ctx context.Context, result string, startTime, endTime *time.Time, page, pageSize int) ([]*model.ModerationRecord, int64, error) {
	var records []*model.ModerationRecord
	var total int64
	query := r.db.WithContext(ctx).Model(&model.ModerationRecord{})
	if result != "" {
		query = query.Where("result = ?", result)
	}
	if startTime != nil {
		query = query.Where("created_at >= ?", *startTime)
	}
	if endTime != nil {
		query = query.Where("created_at <= ?", *endTime)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&records).Error
	return records, total, err
}

func (r *moderationRepository) CreateTranslationRecord(ctx context.Context, record *model.TranslationRecord) error {
	return r.db.WithContext(ctx).Create(record).Error
}
