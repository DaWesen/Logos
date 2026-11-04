package outbox

import (
	"context"
	"encoding/json"
	"errors"
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
	// ClaimPending 在短事务内领取一批消息并写入租约（next_retry_at = leaseUntil）。
	// 事务提交后行锁即释放，不持有到网络发送结束；其他副本在租约到期前不会重复领取。
	ClaimPending(ctx context.Context, tx *gorm.DB, limit int, leaseUntil time.Time, owner string) ([]*OutboxMessage, error)
	// RenewLease 为仍在处理中的消息续租。返回实际续租的行数；
	// 小于传入 id 数说明部分消息已被其他副本接管（租约曾过期），调用方应停止投递。
	RenewLease(ctx context.Context, db *gorm.DB, ids []string, owner string, leaseUntil time.Time) (int64, error)
	MarkSent(ctx context.Context, tx *gorm.DB, id, owner string) error
	// MarkFailed 记录一次投递失败：未达到 MaxRetryCount 时消息回到 pending
	// 并按指数退避设置 NextRetryAt；达到上限后进入 failed 终态（死信）。
	MarkFailed(ctx context.Context, tx *gorm.DB, msg *OutboxMessage, owner, errMsg string) error
	CleanSent(ctx context.Context, db *gorm.DB, before time.Time) error
	// ReplayFailed 把一批 failed 终态消息重置回 pending（清零重试计数与退避，
	// replay_count +1），供人工/定时重放入口调用；返回被重置的行数。
	// 仅重放 replay_count < MaxReplayCount 的消息，避免毒消息被无限重投。
	ReplayFailed(ctx context.Context, db *gorm.DB, limit int) (int64, error)
	// BacklogStats 返回 pending 消息数与最老一条的创建时间。
	// 成功率之外必须看积压：100% 成功也可能在持续堆积。
	BacklogStats(ctx context.Context, db *gorm.DB) (int64, *time.Time, error)
}

// ErrLeaseLost 表示本次操作的 fencing token 已失效：
// 消息在租约过期后被其他副本重新领取，旧副本不能再改写它的状态。
var ErrLeaseLost = errors.New("outbox: lease lost to another relay instance")

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

func (r *outboxRepositoryImpl) ClaimPending(ctx context.Context, tx *gorm.DB, limit int, leaseUntil time.Time, owner string) ([]*OutboxMessage, error) {
	messages, err := r.FetchPending(ctx, tx, limit)
	if err != nil || len(messages) == 0 {
		return messages, err
	}

	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		ids = append(ids, m.ID)
	}
	// 用 next_retry_at 充当租约截止时间：到期前其他副本的 FetchPending 不会命中这些行，
	// 到期后自动回到可领取状态（进程崩溃时的自愈入口）
	return messages, r.claimUpdateQuery(ctx, tx, ids, leaseUntil, owner).Error
}

// claimUpdateQuery 构造领取写回语句，抽出来便于直接断言 SQL 形态。
func (r *outboxRepositoryImpl) claimUpdateQuery(ctx context.Context, tx *gorm.DB, ids []string, leaseUntil time.Time, owner string) *gorm.DB {
	return tx.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"next_retry_at": leaseUntil,
			"lease_owner":   owner,
		})
}

// RenewLease 只续租仍归本副本所有的消息。
//
// 用 lease_owner 做 fencing：仅靠时间戳相等判断不可靠（数据库精度可能截断），
// 而 owner 是本副本生成的唯一 token，其他副本领取时一定会覆盖它。
func (r *outboxRepositoryImpl) RenewLease(ctx context.Context, db *gorm.DB, ids []string, owner string, leaseUntil time.Time) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.renewLeaseQuery(ctx, db, ids, owner, leaseUntil).Update("next_retry_at", leaseUntil)
	return res.RowsAffected, res.Error
}

// renewLeaseQuery 构造续租语句，抽出来便于直接断言 SQL 形态。
func (r *outboxRepositoryImpl) renewLeaseQuery(ctx context.Context, db *gorm.DB, ids []string, owner string, leaseUntil time.Time) *gorm.DB {
	return db.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id IN ? AND lease_owner = ? AND status = ?", ids, owner, StatusPending).
		Where("next_retry_at < ?", leaseUntil)
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

func (r *outboxRepositoryImpl) MarkSent(ctx context.Context, tx *gorm.DB, id, owner string) error {
	now := time.Now()
	res := r.markSentQuery(ctx, tx, id, owner).
		Updates(map[string]interface{}{
			"status":        StatusSent,
			"sent_at":       now,
			"next_retry_at": nil,
			"lease_owner":   "",
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// markSentQuery 构造状态写回语句，抽出来便于直接断言 fencing 条件。
func (r *outboxRepositoryImpl) markSentQuery(ctx context.Context, tx *gorm.DB, id, owner string) *gorm.DB {
	return tx.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id = ? AND lease_owner = ?", id, owner)
}

func (r *outboxRepositoryImpl) MarkFailed(ctx context.Context, tx *gorm.DB, msg *OutboxMessage, owner, errMsg string) error {
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
	res := tx.WithContext(ctx).
		Model(&OutboxMessage{}).
		Where("id = ? AND lease_owner = ?", msg.ID, owner).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
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

// BacklogStats 用一条聚合查询拿到 pending 数量与最老创建时间。
// 只查状态索引覆盖的列，不加载消息体；采样频率是 30s 级别，不需要更细。
func (r *outboxRepositoryImpl) BacklogStats(ctx context.Context, db *gorm.DB) (int64, *time.Time, error) {
	var row struct {
		Count  int64
		Oldest *time.Time
	}
	if err := r.backlogStatsQuery(ctx, db).Scan(&row).Error; err != nil {
		return 0, nil, err
	}
	return row.Count, row.Oldest, nil
}

// backlogStatsQuery 抽出来是为了让测试能直接断言查询形态，
// 而不依赖 DryRun 对 Scan 解码的支持。
func (r *outboxRepositoryImpl) backlogStatsQuery(ctx context.Context, db *gorm.DB) *gorm.DB {
	return db.WithContext(ctx).
		Model(&OutboxMessage{}).
		Select("COUNT(*) AS count, MIN(created_at) AS oldest").
		Where("status = ?", StatusPending)
}
