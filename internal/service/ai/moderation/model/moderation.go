package model

import (
	"time"

	"gorm.io/gorm"
)

type ModerationRecord struct {
	ID          string         `gorm:"primaryKey;size:64;comment:记录ID"`
	Content     string         `gorm:"type:text;not null;comment:内容"`
	ContentID   string         `gorm:"size:64;index;comment:内容ID"`
	ContentType string         `gorm:"size:32;comment:内容类型"`
	Result      string         `gorm:"size:32;not null;index;comment:审核结果 passed/flagged/rejected"`
	Categories  string         `gorm:"type:text;comment:分类JSON数组"`
	Scores      string         `gorm:"type:text;comment:评分JSON"`
	ActionTaken string         `gorm:"size:64;comment:采取的操作"`
	ModeratorID string         `gorm:"size:64;comment:审核者ID"`
	CreatedAt   time.Time      `gorm:"autoCreateTime;comment:创建时间"`
	DeletedAt   gorm.DeletedAt `gorm:"index;comment:删除时间"`
}

func (ModerationRecord) TableName() string {
	return "moderation_records"
}

type TranslationRecord struct {
	ID               string    `gorm:"primaryKey;size:64;comment:记录ID"`
	Content          string    `gorm:"type:text;not null;comment:原文"`
	TranslatedContent string  `gorm:"type:text;not null;comment:译文"`
	SourceLang       string    `gorm:"size:16;not null;comment:源语言"`
	TargetLang       string    `gorm:"size:16;not null;comment:目标语言"`
	ContentID        string    `gorm:"size:64;index;comment:内容ID"`
	CreatedAt        time.Time `gorm:"autoCreateTime;comment:创建时间"`
}

func (TranslationRecord) TableName() string {
	return "translation_records"
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&ModerationRecord{}, &TranslationRecord{})
}
