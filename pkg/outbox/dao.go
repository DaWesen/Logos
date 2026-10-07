package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type OutboxRepository interface {
	Save(ctx context.Context, db *gorm.DB, topic, key string, value interface{}) error
	SaveWithTx(ctx context.Context, tx *gorm.DB, topic, key string, value interface{}) error
	// FetchPending 在事务中锁定一批待投递消息（FOR UPDATE SKIP LOCKED，
	// 多副本部署时各实例不会抢到同一批消息），调用方必须在事务内调用。
	FetchPending(ctx context.Context, tx *gorm.DB, limit int) ([]*OutboxMessage, error)
	MarkSent(ctx context.Context, tx *gorm.DB, id string) error
	// MarkFailed 记录一次投递失败：未达到 MaxRetryCount 时消息回到 pending
	// 并按指数退避设置 NextRetryAt；达到上限后进入 failed 终态（死信）。
	MarkFailed(ctx context.Context, tx *gorm.DB, msg *OutboxMessage, errMsg string) error
	CleanSent(ctx context.Context, db *gorm.DB, before time.Time) error
	// ReplayFailed 把一批 failed 终态消息重置回 pending（清零重试计数与退避，
	// replay_count +1），供人工/定时重放入口调用；返回被重置的行数。
	// 仅重放 replay_count < MaxReplayCount 的消息，避免毒消息被无限重投。
	ReplayFailed(ctx context.Context, db *gorm.DB, limit int) (int64, error)
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

func (r *outboxRepositoryImpl) FetchPending(ctx context.Context, tx *gorm.DB, limit int) ([]*OutboxMessage, error) {
	var messages []*OutboxMessage
	err := tx.WithContext(ctx).
		Where("status = ? AND retry_count < ? AND (next_retry_at IS NULL OR next_retry_at <= ?)",
			StatusPending, MaxRetryCount, time.Now()).
		Order("created_at ASC").
		Limit(limit).
		// 多副本部署时跳过已被其他实例锁定的行，避免重复投递
		Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
		Find(&messages).Error
	return messages, err
}

// RetryBackoff 返回第 retryCount 次失败后的退避时长：30s * 2^n，上限 32 分钟
func RetryBackoff(retryCount int) time.Duration {
	if retryCount < 1 {
		retryCount = 1
	}
	// 限制移位数防止大指数导致整数溢出（30s<<7=64min 已超上限）
	shift := retryCount - 1
	if shift > 7 {
		shift = 7
	}
	backoff := 30 * time.Second * (1 << uint(shift))
	if backoff > 32*time.Minute {
		backoff = 32 * time.Minute
	}
	return backoff
}

func (r *outboxRepositoryImpl) MarkSent(ctx context.Context, tx *gorm.DB, id string) error {
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":        StatusSent,
			"sent_at":       now,
			"next_retry_at": nil,
		}).Error
}

func (r *outboxRepositoryImpl) MarkFailed(ctx context.Context, tx *gorm.DB, msg *OutboxMessage, errMsg string) error {
	newCount := msg.RetryCount + 1
	updates := map[string]interface{}{
		"retry_count":   newCount,
		"error_message": errMsg,
	}
	if newCount >= MaxRetryCount {
		// 重试耗尽，进入死信终态，保留记录供排查
		updates["status"] = StatusFailed
		updates["next_retry_at"] = nil
	} else {
		// 回到 pending，按指数退避延后重投
		updates["status"] = StatusPending
		updates["next_retry_at"] = time.Now().Add(RetryBackoff(newCount))
	}
	return tx.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id = ?", msg.ID).
		Updates(updates).Error
}

func (r *outboxRepositoryImpl) CleanSent(ctx context.Context, db *gorm.DB, before time.Time) error {
	return db.WithContext(ctx).
		Where("status = ? AND sent_at < ?", StatusSent, before).
		Delete(&OutboxMessage{}).Error
}

// ReplayFailed 将 failed 终态消息重置回 pending，交由 relay 重新投递。
// 在事务内用 FOR UPDATE SKIP LOCKED 锁定一批（多副本定时重放时不会重复处理同一行，
// 与 FetchPending 语义一致），并把 replay_count +1；达到上限的消息不再参与重放。
func (r *outboxRepositoryImpl) ReplayFailed(ctx context.Context, db *gorm.DB, limit int) (int64, error) {
	var replayed int64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var msgs []*OutboxMessage
		if err := tx.Model(&OutboxMessage{}).
			Select("id").
			Where("status = ? AND replay_count < ?", StatusFailed, MaxReplayCount).
			Order("created_at ASC").
			Limit(limit).
			// 多副本部署时跳过已被其他实例锁定的行，避免重复重放
			Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Find(&msgs).Error; err != nil {
			return err
		}
		if len(msgs) == 0 {
			return nil
		}

		ids := make([]string, 0, len(msgs))
		for _, m := range msgs {
			ids = append(ids, m.ID)
		}

		res := tx.Model(&OutboxMessage{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"status":        StatusPending,
				"retry_count":   0,
				"replay_count":  gorm.Expr("replay_count + ?", 1),
				"error_message": "",
				"next_retry_at": nil,
			})
		if res.Error != nil {
			return res.Error
		}
		replayed = res.RowsAffected
		return nil
	})
	return replayed, err
}
