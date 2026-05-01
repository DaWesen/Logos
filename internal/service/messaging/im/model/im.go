package model

import "gorm.io/gorm"

// OnlineRecord 在线状态记录
type OnlineRecord struct {
	gorm.Model
	UserID    string `gorm:"index;not null;size:100"`
	DeviceID  string `gorm:"size:100"`
	SessionID string `gorm:"size:100;index"`
	Online    bool   `gorm:"default:false"`
	LastSeen  int64  `gorm:"index"`
	Platform  string `gorm:"size:50"` // 平台标识：web、ios、android、desktop
}

// AutoMigrate 自动迁移
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&OnlineRecord{},
	)
}