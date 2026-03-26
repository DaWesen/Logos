package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID          int64          `gorm:"primaryKey;autoIncrement" json:"id"`
	Username    string         `gorm:"uniqueIndex;size:50;not null" json:"username"`
	Password    string         `gorm:"size:255;not null" json:"-"`
	Email       *string        `gorm:"size:100" json:"email,omitempty"`
	Phone       *string        `gorm:"size:20" json:"phone,omitempty"`
	Avatar      *string        `gorm:"size:255" json:"avatar,omitempty"`
	Preferences JSONMap        `gorm:"type:jsonb" json:"preferences,omitempty"`
	Interests   StringSlice    `gorm:"type:jsonb" json:"interests,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

type UserStats struct {
	UserID              int64 `json:"userId"`
	QuestionCount       int64 `json:"questionCount"`
	AnswerCount         int64 `json:"answerCount"`
	RecommendationCount int64 `json:"recommendationCount"`
}

type JSONMap map[string]string

func (j JSONMap) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

type StringSlice []string

func (s StringSlice) Value() (driver.Value, error) {
	return json.Marshal(s)
}

func (s *StringSlice) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, s)
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&User{})
}
