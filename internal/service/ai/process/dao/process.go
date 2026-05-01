package dao

import (
	"context"
	"time"

	"gorm.io/gorm"

	"Logos/internal/service/ai/process/model"
)

type ProcessRepository interface {
	CreateDocument(ctx context.Context, doc *model.Document) error
	UpdateDocument(ctx context.Context, doc *model.Document) error
	GetDocument(ctx context.Context, id string) (*model.Document, error)
	GetDocumentByHash(ctx context.Context, hash string) (*model.Document, error)
	ListDocuments(ctx context.Context, status *int, page, pageSize int) ([]*model.Document, int64, error)
	DeleteDocument(ctx context.Context, id string) error
	CreateChunk(ctx context.Context, chunk *model.DocumentChunk) error
	GetChunksByDocumentID(ctx context.Context, docID string) ([]*model.DocumentChunk, error)
	DeleteChunksByDocumentID(ctx context.Context, docID string) error
	WithTransaction(ctx context.Context, fn func(repo ProcessRepository) error) error
}

type processRepository struct {
	db *gorm.DB
}

func NewProcessRepository(db *gorm.DB) ProcessRepository {
	return &processRepository{db: db}
}

func (r *processRepository) CreateDocument(ctx context.Context, doc *model.Document) error {
	return r.db.WithContext(ctx).Create(doc).Error
}

func (r *processRepository) UpdateDocument(ctx context.Context, doc *model.Document) error {
	doc.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Save(doc).Error
}

func (r *processRepository) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	var doc model.Document
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&doc).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &doc, nil
}

func (r *processRepository) GetDocumentByHash(ctx context.Context, hash string) (*model.Document, error) {
	var doc model.Document
	err := r.db.WithContext(ctx).Where("file_hash = ?", hash).First(&doc).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &doc, nil
}

func (r *processRepository) ListDocuments(ctx context.Context, status *int, page, pageSize int) ([]*model.Document, int64, error) {
	var docs []*model.Document
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Document{})
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&docs).Error; err != nil {
		return nil, 0, err
	}

	return docs, total, nil
}

func (r *processRepository) DeleteDocument(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&model.Document{}, "id = ?", id).Error
}

func (r *processRepository) CreateChunk(ctx context.Context, chunk *model.DocumentChunk) error {
	return r.db.WithContext(ctx).Create(chunk).Error
}

func (r *processRepository) GetChunksByDocumentID(ctx context.Context, docID string) ([]*model.DocumentChunk, error) {
	var chunks []*model.DocumentChunk
	err := r.db.WithContext(ctx).Where("document_id = ?", docID).Order("chunk_index ASC").Find(&chunks).Error
	return chunks, err
}

func (r *processRepository) DeleteChunksByDocumentID(ctx context.Context, docID string) error {
	return r.db.WithContext(ctx).Delete(&model.DocumentChunk{}, "document_id = ?", docID).Error
}

func (r *processRepository) WithTransaction(ctx context.Context, fn func(repo ProcessRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &processRepository{db: tx}
		return fn(txRepo)
	})
}
