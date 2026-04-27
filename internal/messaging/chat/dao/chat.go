package dao

import "gorm.io/gorm"

// ChatRepository Chat 数据访问层
type ChatRepository struct {
	db *gorm.DB
}

// NewChatRepository 创建 Chat 仓库
func NewChatRepository(db *gorm.DB) *ChatRepository {
	return &ChatRepository{db: db}
}
