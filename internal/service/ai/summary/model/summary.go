package model

import (
	"time"

	"gorm.io/gorm"
)

type SummaryRecord struct {
	ID           string         `gorm:"primaryKey;size:64;comment:记录ID"`
	ChatID       string         `gorm:"size:64;index;not null;comment:聊天ID"`
	ChatType     string         `gorm:"size:32;not null;comment:聊天类型"`
	Summary      string         `gorm:"type:text;not null;comment:摘要内容"`
	KeyPoints    string         `gorm:"type:text;comment:要点JSON数组"`
	Participants string         `gorm:"type:text;comment:参与者JSON数组"`
	Todos        string         `gorm:"type:text;comment:待办JSON数组"`
	MessageIDs   string         `gorm:"type:text;comment:消息ID列表JSON"`
	CreatedAt    time.Time      `gorm:"autoCreateTime;comment:创建时间"`
	DeletedAt    gorm.DeletedAt `gorm:"index;comment:删除时间"`
}

func (SummaryRecord) TableName() string {
	return "summary_records"
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&SummaryRecord{})
}
