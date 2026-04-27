package model

import (
	"time"

	"gorm.io/gorm"
)

type RecommendationItem struct {
	ID          string    `gorm:"primaryKey;comment:推荐项ID"`
	Type        string    `gorm:"size:32;not null;comment:推荐项类型"`
	Title       string    `gorm:"size:255;not null;comment:标题"`
	Description string    `gorm:"type:text;comment:描述"`
	Score       float64   `gorm:"type:decimal(10,4);not null;comment:推荐分数"`
	EntityID    string    `gorm:"size:64;index;comment:实体ID"`
	ImageURL    *string   `gorm:"size:512;comment:图片URL"`
	CreatedAt   int64     `gorm:"not null;comment:创建时间戳"`
	DBCreatedAt time.Time `gorm:"autoCreateTime;comment:数据库创建时间"`
}

func (RecommendationItem) TableName() string {
	return "recommendation_items"
}

type RecommendationHistory struct {
	ID        string    `gorm:"primaryKey;comment:历史记录ID"`
	ItemID    string    `gorm:"size:64;not null;index;comment:推荐项ID"`
	ItemType  string    `gorm:"size:32;not null;comment:推荐项类型"`
	Title     string    `gorm:"size:255;comment:标题"`
	Action    string    `gorm:"size:32;not null;comment:用户动作"`
	UserID    int64     `gorm:"index;not null;comment:用户ID"`
	Timestamp int64     `gorm:"not null;comment:时间戳"`
	CreatedAt time.Time `gorm:"autoCreateTime;comment:创建时间"`
}

func (RecommendationHistory) TableName() string {
	return "recommendation_histories"
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&RecommendationItem{}, &RecommendationHistory{})
}
