package model

import (
	"time"

	"gorm.io/gorm"
)

// FriendshipStatus 好友关系状态
type FriendshipStatus string

const (
	FriendshipStatusPending  FriendshipStatus = "pending"
	FriendshipStatusAccepted FriendshipStatus = "accepted"
	FriendshipStatusBlocked  FriendshipStatus = "blocked"
)

// FriendRequestStatus 好友申请状态
type FriendRequestStatus string

const (
	FriendRequestStatusPending  FriendRequestStatus = "pending"
	FriendRequestStatusAccepted FriendRequestStatus = "accepted"
	FriendRequestStatusRejected FriendRequestStatus = "rejected"
)

// Friendship 好友关系模型
type Friendship struct {
	ID        string           `gorm:"primaryKey;size:100" json:"id"`
	UserID    int64            `gorm:"index:idx_user_friend;not null" json:"user_id"`
	FriendID  int64            `gorm:"index:idx_user_friend;not null" json:"friend_id"`
	Status    FriendshipStatus `gorm:"size:20;index;default:pending" json:"status"`
	Remark    string           `gorm:"size:100" json:"remark"`
	GroupID   string           `gorm:"size:100;index" json:"group_id"` // 分组ID
	CreatedAt time.Time        `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time        `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt   `gorm:"index" json:"-"`
}

// FriendRequest 好友申请模型
type FriendRequest struct {
	ID          string              `gorm:"primaryKey;size:100" json:"id"`
	FromUserID  int64               `gorm:"index;not null" json:"from_user_id"`
	ToUserID    int64               `gorm:"index;not null" json:"to_user_id"`
	Remark      string              `gorm:"size:100" json:"remark"`
	Message     string              `gorm:"size:200" json:"message"`
	Status      FriendRequestStatus `gorm:"size:20;index;default:pending" json:"status"`
	ProcessedAt *time.Time          `json:"processed_at"`
	CreatedAt   time.Time           `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time           `gorm:"autoUpdateTime" json:"updated_at"`
}

// FriendGroup 好友分组模型
type FriendGroup struct {
	ID        string    `gorm:"primaryKey;size:100" json:"id"`
	UserID    int64     `gorm:"index;not null" json:"user_id"`
	Name      string    `gorm:"size:50" json:"name"`
	Sort      int       `gorm:"default:0" json:"sort"` // 排序
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// FriendGroupMember 分组成员关系表
type FriendGroupMember struct {
	ID        string    `gorm:"primaryKey;size:100" json:"id"`
	UserID    int64     `gorm:"index;not null" json:"user_id"`
	FriendID  int64     `gorm:"index;not null" json:"friend_id"`
	GroupID   string    `gorm:"index;size:100" json:"group_id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}

// AutoMigrate 自动迁移
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Friendship{},
		&FriendRequest{},
		&FriendGroup{},
		&FriendGroupMember{},
	)
}
