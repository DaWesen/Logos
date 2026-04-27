package model

import "gorm.io/gorm"

// Friendship 好友关系模型
type Friendship struct {
	gorm.Model
	UserID   int64  `gorm:"index;not null"`
	FriendID int64  `gorm:"index;not null"`
	Status   string `gorm:"size:20;default:pending"` // pending, accepted, blocked
	Remark   string `gorm:"size:100"`
}

// FriendRequest 好友申请模型
type FriendRequest struct {
	gorm.Model
	FromUserID int64  `gorm:"index;not null"`
	ToUserID   int64  `gorm:"index;not null"`
	Message    string `gorm:"size:200"`
	Status     string `gorm:"size:20;default:pending"` // pending, accepted, rejected
}

// AutoMigrate 自动迁移
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Friendship{},
		&FriendRequest{},
	)
}
