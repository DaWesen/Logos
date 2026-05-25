package dao

import (
	"context"
	"strconv"
	"time"

	"Logos/internal/service/messaging/im/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type IMRepository interface {
	UpsertOnlineRecord(ctx context.Context, record *model.OnlineRecord) error
	GetOnlineRecordBySession(ctx context.Context, sessionID string) (*model.OnlineRecord, error)
	GetOnlineRecordsByUser(ctx context.Context, userID string) ([]*model.OnlineRecord, error)
	SetUserOffline(ctx context.Context, sessionID string) error
	SetAllUserOffline(ctx context.Context, userID string) error
	UpdateLastSeen(ctx context.Context, sessionID string) error
	GetBatchOnlineStatus(ctx context.Context, userIDs []string) (map[string]bool, error)
}

type imRepositoryImpl struct {
	db *gorm.DB
}

func NewIMRepository(db *gorm.DB) IMRepository {
	return &imRepositoryImpl{db: db}
}

func (r *imRepositoryImpl) UpsertOnlineRecord(ctx context.Context, record *model.OnlineRecord) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "session_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"user_id", "device_id", "online", "last_seen", "platform", "updated_at"}),
	}).Create(record).Error
}

func (r *imRepositoryImpl) GetOnlineRecordBySession(ctx context.Context, sessionID string) (*model.OnlineRecord, error) {
	var record model.OnlineRecord
	err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *imRepositoryImpl) GetOnlineRecordsByUser(ctx context.Context, userID string) ([]*model.OnlineRecord, error) {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return nil, err
	}

	var records []*model.OnlineRecord
	err = r.db.WithContext(ctx).Where("user_id = ? AND online = ?", uid, true).Find(&records).Error
	return records, err
}

func (r *imRepositoryImpl) SetUserOffline(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Where("session_id = ?", sessionID).
		Updates(map[string]interface{}{
			"online":    false,
			"last_seen": time.Now().UnixMilli(),
		}).Error
}

func (r *imRepositoryImpl) SetAllUserOffline(ctx context.Context, userID string) error {
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Where("user_id = ?", uid).
		Updates(map[string]interface{}{
			"online":    false,
			"last_seen": time.Now().UnixMilli(),
		}).Error
}

func (r *imRepositoryImpl) UpdateLastSeen(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Where("session_id = ?", sessionID).
		Update("last_seen", time.Now().UnixMilli()).Error
}

func (r *imRepositoryImpl) GetBatchOnlineStatus(ctx context.Context, userIDs []string) (map[string]bool, error) {
	intIDs := make([]int64, 0, len(userIDs))
	for _, uidStr := range userIDs {
		uid, err := strconv.ParseInt(uidStr, 10, 64)
		if err == nil {
			intIDs = append(intIDs, uid)
		}
	}

	type result struct {
		UserID int64
		Online bool
	}
	var results []result
	err := r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Select("user_id, BOOL_OR(online) as online").
		Where("user_id IN ?", intIDs).
		Group("user_id").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	status := make(map[string]bool)
	for _, res := range results {
		status[strconv.FormatInt(res.UserID, 10)] = res.Online
	}
	return status, nil
}
