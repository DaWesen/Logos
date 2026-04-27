package dao

import "gorm.io/gorm"

// ContactRepository Contact 数据访问层
type ContactRepository struct {
	db *gorm.DB
}

// NewContactRepository 创建 Contact 仓库
func NewContactRepository(db *gorm.DB) *ContactRepository {
	return &ContactRepository{db: db}
}
