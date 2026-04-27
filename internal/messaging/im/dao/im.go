package dao

import "gorm.io/gorm"

// IMRepository IM 数据访问层
type IMRepository struct {
	db *gorm.DB
}

// NewIMRepository 创建 IM 仓库
func NewIMRepository(db *gorm.DB) *IMRepository {
	return &IMRepository{db: db}
}
