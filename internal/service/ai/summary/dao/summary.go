package dao

import (
	"context"

	"Logos/internal/service/ai/summary/model"

	"gorm.io/gorm"
)

type SummaryRepository interface {
	CreateSummary(ctx context.Context, record *model.SummaryRecord) error
	GetSummary(ctx context.Context, id string) (*model.SummaryRecord, error)
	ListSummaries(ctx context.Context, chatID string, page, pageSize int) ([]*model.SummaryRecord, int64, error)
}

type summaryRepository struct {
	db *gorm.DB
}

func NewSummaryRepository(db *gorm.DB) SummaryRepository {
	return &summaryRepository{db: db}
}

func (r *summaryRepository) CreateSummary(ctx context.Context, record *model.SummaryRecord) error {
	return r.db.WithContext(ctx).Create(record).Error
}

func (r *summaryRepository) GetSummary(ctx context.Context, id string) (*model.SummaryRecord, error) {
	var record model.SummaryRecord
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&record).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}

func (r *summaryRepository) ListSummaries(ctx context.Context, chatID string, page, pageSize int) ([]*model.SummaryRecord, int64, error) {
	var records []*model.SummaryRecord
	var total int64
	query := r.db.WithContext(ctx).Model(&model.SummaryRecord{})
	if chatID != "" {
		query = query.Where("chat_id = ?", chatID)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&records).Error
	return records, total, err
}
