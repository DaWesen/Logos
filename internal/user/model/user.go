package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type User struct {
	ID          int64          `gorm:"primaryKey;autoIncrement;comment:用户ID" json:"id"`
	Username    string         `gorm:"uniqueIndex;size:50;not null;comment:用户名" json:"username"`
	Password    string         `gorm:"size:255;not null;comment:密码" json:"-"`
	Email       *string        `gorm:"size:100;comment:邮箱" json:"email,omitempty"`
	Phone       *string        `gorm:"size:20;comment:手机号" json:"phone,omitempty"`
	Avatar      *string        `gorm:"size:255;comment:头像" json:"avatar,omitempty"`
	Preferences JSONMap        `gorm:"type:jsonb;comment:偏好设置" json:"preferences,omitempty"`
	Interests   StringSlice    `gorm:"type:jsonb;comment:兴趣标签" json:"interests,omitempty"`
	CreatedAt   time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
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
