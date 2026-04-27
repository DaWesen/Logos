package model

import "gorm.io/gorm"

// OnlineRecord 在线状态记录
type OnlineRecord struct {
	gorm.Model
	UserID   int64  `gorm:"index;not null"`
	DeviceID string `gorm:"size:100"`
	Online   bool   `gorm:"default:false"`
	LastSeen int64
}

// AutoMigrate 自动迁移
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&OnlineRecord{},
	)
}
