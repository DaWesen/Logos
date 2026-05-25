package model

import (
	"time"

	"gorm.io/gorm"
)

type OnlineRecord struct {
	ID        int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    int64          `gorm:"index;not null" json:"user_id"`
	DeviceID  string         `gorm:"size:100" json:"device_id"`
	SessionID string         `gorm:"uniqueIndex;size:100" json:"session_id"`
	Online    bool           `gorm:"default:false" json:"online"`
	LastSeen  int64          `gorm:"index" json:"last_seen"`
	Platform  string         `gorm:"size:50" json:"platform"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (OnlineRecord) TableName() string {
	return "online_records"
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&OnlineRecord{})
}
