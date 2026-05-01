package dao

import (
	"context"
	"time"

	"Logos/internal/service/messaging/im/model"

	"gorm.io/gorm"
)

type IMRepository interface {
	CreateOnlineRecord(ctx context.Context, record *model.OnlineRecord) error
	UpdateOnlineRecord(ctx context.Context, record *model.OnlineRecord) error
	GetOnlineRecordBySession(ctx context.Context, sessionID string) (*model.OnlineRecord, error)
	GetOnlineRecordsByUser(ctx context.Context, userID string) ([]*model.OnlineRecord, error)
	GetOnlineUsers(ctx context.Context) ([]string, error)
	SetUserOffline(ctx context.Context, sessionID string) error
	SetAllUserOffline(ctx context.Context, userID string) error
	IsUserOnline(ctx context.Context, userID string) (bool, error)
	UpdateLastSeen(ctx context.Context, sessionID string) error
	GetBatchOnlineStatus(ctx context.Context, userIDs []string) (map[string]bool, error)
}

type imRepositoryImpl struct {
	db *gorm.DB
}

func NewIMRepository(db *gorm.DB) IMRepository {
	return &imRepositoryImpl{db: db}
}

func (r *imRepositoryImpl) CreateOnlineRecord(ctx context.Context, record *model.OnlineRecord) error {
	return r.db.WithContext(ctx).Create(record).Error
}

func (r *imRepositoryImpl) UpdateOnlineRecord(ctx context.Context, record *model.OnlineRecord) error {
	return r.db.WithContext(ctx).Save(record).Error
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
	var records []*model.OnlineRecord
	err := r.db.WithContext(ctx).Where("user_id = ? AND online = ?", userID, true).Find(&records).Error
	return records, err
}

func (r *imRepositoryImpl) GetOnlineUsers(ctx context.Context) ([]string, error) {
	var userIDs []string
	err := r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Select("DISTINCT user_id").
		Where("online = ?", true).
		Pluck("user_id", &userIDs).Error
	return userIDs, err
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
	return r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Where("user_id = ?", userID).
		Updates(map[string]interface{}{
			"online":    false,
			"last_seen": time.Now().UnixMilli(),
		}).Error
}

func (r *imRepositoryImpl) IsUserOnline(ctx context.Context, userID string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Where("user_id = ? AND online = ?", userID, true).
		Count(&count).Error
	return count > 0, err
}

func (r *imRepositoryImpl) UpdateLastSeen(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Where("session_id = ?", sessionID).
		Update("last_seen", time.Now().UnixMilli()).Error
}

func (r *imRepositoryImpl) GetBatchOnlineStatus(ctx context.Context, userIDs []string) (map[string]bool, error) {
	type result struct {
		UserID string
		Online bool
	}
	var results []result
	err := r.db.WithContext(ctx).Model(&model.OnlineRecord{}).
		Select("user_id, MAX(online) as online").
		Where("user_id IN ?", userIDs).
		Group("user_id").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}
	status := make(map[string]bool)
	for _, r := range results {
		status[r.UserID] = r.Online
	}
	return status, nil
}
