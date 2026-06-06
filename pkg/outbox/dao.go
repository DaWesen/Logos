package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type OutboxRepository interface {
	Save(ctx context.Context, db *gorm.DB, topic, key string, value interface{}) error
	SaveWithTx(ctx context.Context, tx *gorm.DB, topic, key string, value interface{}) error
	FetchPending(ctx context.Context, db *gorm.DB, limit int) ([]*OutboxMessage, error)
	MarkSent(ctx context.Context, db *gorm.DB, id string) error
	MarkFailed(ctx context.Context, db *gorm.DB, id string, errMsg string) error
	CleanSent(ctx context.Context, db *gorm.DB, before time.Time) error
}

type outboxRepositoryImpl struct{}

func NewOutboxRepository() OutboxRepository {
	return &outboxRepositoryImpl{}
}

func (r *outboxRepositoryImpl) Save(ctx context.Context, db *gorm.DB, topic, key string, value interface{}) error {
	return r.SaveWithTx(ctx, db, topic, key, value)
}

func (r *outboxRepositoryImpl) SaveWithTx(ctx context.Context, tx *gorm.DB, topic, key string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	msg := &OutboxMessage{
		ID:     uuid.NewString(),
		Topic:  topic,
		Key:    key,
		Value:  JSONRaw(data),
		Status: StatusPending,
	}

	return tx.WithContext(ctx).Create(msg).Error
}

func (r *outboxRepositoryImpl) FetchPending(ctx context.Context, db *gorm.DB, limit int) ([]*OutboxMessage, error) {
	var messages []*OutboxMessage
	err := db.WithContext(ctx).
		Where("status = ? AND retry_count < ?", StatusPending, MaxRetryCount).
		Order("created_at ASC").
		Limit(limit).
		Find(&messages).Error
	return messages, err
}

func (r *outboxRepositoryImpl) MarkSent(ctx context.Context, db *gorm.DB, id string) error {
	now := time.Now()
	return db.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":  StatusSent,
			"sent_at": now,
		}).Error
}

func (r *outboxRepositoryImpl) MarkFailed(ctx context.Context, db *gorm.DB, id string, errMsg string) error {
	return db.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       StatusFailed,
			"error_message": errMsg,
			"retry_count":  gorm.Expr("retry_count + 1"),
		}).Error
}

func (r *outboxRepositoryImpl) CleanSent(ctx context.Context, db *gorm.DB, before time.Time) error {
	return db.WithContext(ctx).
		Where("status = ? AND sent_at < ?", StatusSent, before).
		Delete(&OutboxMessage{}).Error
}
