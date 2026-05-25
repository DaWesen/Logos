package dao

import (
	"context"
	"crypto/rand"
	"math/big"
	"strconv"
	"time"

	"Logos/internal/service/messaging/contact/model"
	"Logos/pkg/logger"

	"gorm.io/gorm"
)

func toInt64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

type ContactRepository interface {
	CreateFriendRequest(ctx context.Context, request *model.FriendRequest) error
	GetFriendRequestByID(ctx context.Context, id string) (*model.FriendRequest, error)
	GetPendingFriendRequests(ctx context.Context, toUserID string, page, pageSize int) ([]*model.FriendRequest, int64, error)
	UpdateFriendRequestStatus(ctx context.Context, id string, status model.FriendRequestStatus) error

	CreateFriendship(ctx context.Context, friendship *model.Friendship) error
	GetFriendship(ctx context.Context, userID, friendID string) (*model.Friendship, error)
	GetFriends(ctx context.Context, userID string, groupID string, page, pageSize int) ([]*model.Friendship, int64, error)
	UpdateFriendRemark(ctx context.Context, userID, friendID, remark string) error
	DeleteFriendship(ctx context.Context, userID, friendID string) error
	BlockUser(ctx context.Context, userID, blockUserID string) error
	UnblockUser(ctx context.Context, userID, unblockUserID string) error
	GetBlacklist(ctx context.Context, userID string, page, pageSize int) ([]*model.Friendship, int64, error)

	CreateFriendGroup(ctx context.Context, group *model.FriendGroup) error
	GetFriendGroups(ctx context.Context, userID string) ([]*model.FriendGroup, error)
	GetFriendGroupByID(ctx context.Context, userID, groupID string) (*model.FriendGroup, error)
	UpdateFriendGroup(ctx context.Context, userID, groupID, name string, sort int) error
	DeleteFriendGroup(ctx context.Context, userID, groupID string) error

	MoveFriendToGroup(ctx context.Context, userID, friendID, groupID string) error
}

type contactRepositoryImpl struct {
	db *gorm.DB
}

func NewContactRepository(db *gorm.DB) ContactRepository {
	return &contactRepositoryImpl{db: db}
}

func (r *contactRepositoryImpl) CreateFriendRequest(ctx context.Context, request *model.FriendRequest) error {
	return r.db.WithContext(ctx).Create(request).Error
}

func (r *contactRepositoryImpl) GetFriendRequestByID(ctx context.Context, id string) (*model.FriendRequest, error) {
	var req model.FriendRequest
	err := r.db.WithContext(ctx).First(&req, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &req, nil
}

func (r *contactRepositoryImpl) GetPendingFriendRequests(ctx context.Context, toUserID string, page, pageSize int) ([]*model.FriendRequest, int64, error) {
	var requests []*model.FriendRequest
	var total int64
	query := r.db.WithContext(ctx).Model(&model.FriendRequest{}).
		Where("to_user_id = ? AND status = ?", toInt64(toUserID), model.FriendRequestStatusPending)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := query.Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&requests).Error
	return requests, total, err
}

func (r *contactRepositoryImpl) UpdateFriendRequestStatus(ctx context.Context, id string, status model.FriendRequestStatus) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.FriendRequest{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       status,
			"processed_at": now,
			"updated_at":   now,
		}).Error
}

func (r *contactRepositoryImpl) CreateFriendship(ctx context.Context, friendship *model.Friendship) error {
	return r.db.WithContext(ctx).Create(friendship).Error
}

func (r *contactRepositoryImpl) GetFriendship(ctx context.Context, userID, friendID string) (*model.Friendship, error) {
	var friendship model.Friendship
	err := r.db.WithContext(ctx).First(&friendship,
		"user_id = ? AND friend_id = ?",
		toInt64(userID), toInt64(friendID)).Error
	if err != nil {
		return nil, err
	}
	return &friendship, nil
}

func (r *contactRepositoryImpl) GetFriends(ctx context.Context, userID string, groupID string, page, pageSize int) ([]*model.Friendship, int64, error) {
	var friendships []*model.Friendship
	var total int64
	query := r.db.WithContext(ctx).Model(&model.Friendship{}).
		Where("user_id = ? AND status = ?", toInt64(userID), model.FriendshipStatusAccepted)
	if groupID != "" {
		query = query.Where("group_id = ?", groupID)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := query.Order("updated_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&friendships).Error
	return friendships, total, err
}

func (r *contactRepositoryImpl) UpdateFriendRemark(ctx context.Context, userID, friendID, remark string) error {
	return r.db.WithContext(ctx).Model(&model.Friendship{}).
		Where("user_id = ? AND friend_id = ?", toInt64(userID), toInt64(friendID)).
		Updates(map[string]interface{}{
			"remark": remark,
		}).Error
}

func (r *contactRepositoryImpl) DeleteFriendship(ctx context.Context, userID, friendID string) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND friend_id = ?", toInt64(userID), toInt64(friendID)).
		Delete(&model.Friendship{}).Error
}

func (r *contactRepositoryImpl) BlockUser(ctx context.Context, userID, blockUserID string) error {
	uid := toInt64(userID)
	bid := toInt64(blockUserID)
	var existing model.Friendship
	err := r.db.WithContext(ctx).First(&existing, "user_id = ? AND friend_id = ?", uid, bid).Error
	if err == nil {
		return r.db.WithContext(ctx).Model(&existing).
			Updates(map[string]interface{}{
				"status": model.FriendshipStatusBlocked,
			}).Error
	}
	friendship := &model.Friendship{
		ID:       generateID(),
		UserID:   uid,
		FriendID: bid,
		Status:   model.FriendshipStatusBlocked,
	}
	return r.db.WithContext(ctx).Create(friendship).Error
}

func (r *contactRepositoryImpl) UnblockUser(ctx context.Context, userID, unblockUserID string) error {
	return r.db.WithContext(ctx).Where("user_id = ? AND friend_id = ? AND status = ?",
		toInt64(userID), toInt64(unblockUserID), model.FriendshipStatusBlocked).
		Delete(&model.Friendship{}).Error
}

func (r *contactRepositoryImpl) GetBlacklist(ctx context.Context, userID string, page, pageSize int) ([]*model.Friendship, int64, error) {
	var list []*model.Friendship
	var total int64
	offset := (page - 1) * pageSize

	db := r.db.WithContext(ctx).Model(&model.Friendship{}).Where("user_id = ? AND status = ?", toInt64(userID), model.FriendshipStatusBlocked)
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Offset(offset).Limit(pageSize).Order("updated_at DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *contactRepositoryImpl) CreateFriendGroup(ctx context.Context, group *model.FriendGroup) error {
	return r.db.WithContext(ctx).Create(group).Error
}

func (r *contactRepositoryImpl) GetFriendGroups(ctx context.Context, userID string) ([]*model.FriendGroup, error) {
	var groups []*model.FriendGroup
	err := r.db.WithContext(ctx).Where("user_id = ?", toInt64(userID)).
		Order("sort ASC, created_at ASC").
		Find(&groups).Error
	return groups, err
}

func (r *contactRepositoryImpl) GetFriendGroupByID(ctx context.Context, userID, groupID string) (*model.FriendGroup, error) {
	var group model.FriendGroup
	err := r.db.WithContext(ctx).First(&group, "id = ? AND user_id = ?", groupID, toInt64(userID)).Error
	if err != nil {
		return nil, err
	}
	return &group, nil
}

func (r *contactRepositoryImpl) UpdateFriendGroup(ctx context.Context, userID, groupID, name string, sort int) error {
	updates := map[string]interface{}{}
	if name != "" {
		updates["name"] = name
	}
	if sort >= 0 {
		updates["sort"] = sort
	}
	return r.db.WithContext(ctx).Model(&model.FriendGroup{}).
		Where("id = ? AND user_id = ?", groupID, toInt64(userID)).
		Updates(updates).Error
}

func (r *contactRepositoryImpl) DeleteFriendGroup(ctx context.Context, userID, groupID string) error {
	uid := toInt64(userID)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ? AND user_id = ?", groupID, uid).
			Delete(&model.FriendGroupMember{}).Error; err != nil {
			logger.Error("删除分组成员失败", logger.ErrorField(err))
		}
		if err := tx.Model(&model.Friendship{}).
			Where("user_id = ? AND group_id = ?", uid, groupID).
			Update("group_id", "").Error; err != nil {
			logger.Error("更新好友group_id失败", logger.ErrorField(err))
		}
		return tx.Where("id = ? AND user_id = ?", groupID, uid).
			Delete(&model.FriendGroup{}).Error
	})
}

func (r *contactRepositoryImpl) MoveFriendToGroup(ctx context.Context, userID, friendID, groupID string) error {
	uid := toInt64(userID)
	fid := toInt64(friendID)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Friendship{}).
			Where("user_id = ? AND friend_id = ?", uid, fid).
			Update("group_id", groupID).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND friend_id = ?", uid, fid).
			Delete(&model.FriendGroupMember{}).Error; err != nil {
			logger.Warn("删除旧分组成员失败", logger.ErrorField(err))
		}
		if groupID != "" {
			member := &model.FriendGroupMember{
				ID:       generateID(),
				UserID:   uid,
				FriendID: fid,
				GroupID:  groupID,
			}
			return tx.Create(member).Error
		}
		return nil
	})
}

func generateID() string {
	return "fg_" + time.Now().Format("20060102150405") + "_" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		b[i] = letters[idx.Int64()]
	}
	return string(b)
}
