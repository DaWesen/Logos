package model

import "gorm.io/gorm"

// ChatMessage 聊天消息模型
type ChatMessage struct {
	gorm.Model
	SenderID   int64  `gorm:"index;not null"`
	ReceiverID int64  `gorm:"index"`
	GroupID    int64  `gorm:"index"`
	Type       int    `gorm:"default:1"` // 1=text, 2=image, 3=file, 4=system
	Content    string `gorm:"type:text"`
	Read       bool   `gorm:"default:false"`
}

// ChatGroup 群聊模型
type ChatGroup struct {
	gorm.Model
	CreatorID int64  `gorm:"not null"`
	Name      string `gorm:"size:100;not null"`
}

// ChatGroupMember 群聊成员
type ChatGroupMember struct {
	gorm.Model
	GroupID int64 `gorm:"index;not null"`
	UserID  int64 `gorm:"index;not null"`
}

// AutoMigrate 自动迁移
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&ChatMessage{},
		&ChatGroup{},
		&ChatGroupMember{},
	)
}
