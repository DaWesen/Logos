package model

import (
	"time"
)

// User 用户模型
type User struct {
	ID          int64             `gorm:"primaryKey;autoIncrement;comment:用户ID" json:"id"`
	Username    string            `gorm:"size:32;uniqueIndex;not null;comment:用户名" json:"username"`
	Password    string            `gorm:"size:128;not null;comment:密码" json:"-"`
	Email       string            `gorm:"size:100;uniqueIndex;comment:邮箱" json:"email"`
	Phone       string            `gorm:"size:20;uniqueIndex;comment:电话" json:"phone"`
	Avatar      string            `gorm:"size:255;default:'';comment:头像" json:"avatar"`
	Preferences map[string]string `gorm:"type:jsonb;comment:偏好设置" json:"preferences"`
	Interests   []string          `gorm:"type:jsonb;comment:兴趣爱好" json:"interests"`
	CreatedAt   time.Time         `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt   time.Time         `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}

// ToCommonUser 转换为 common.User
func (u *User) ToCommonUser() map[string]interface{} {
	return map[string]interface{}{
		"id":          u.ID,
		"username":    u.Username,
		"email":       u.Email,
		"phone":       u.Phone,
		"avatar":      u.Avatar,
		"preferences": u.Preferences,
		"interests":   u.Interests,
		"createdAt":   u.CreatedAt.Unix(),
		"updatedAt":   u.UpdatedAt.Unix(),
	}
}

// UserStats 用户统计模型
type UserStats struct {
	UserID              int64 `gorm:"primaryKey;comment:用户ID" json:"userId"`
	QuestionCount       int64 `gorm:"default:0;comment:问题数量" json:"questionCount"`
	AnswerCount         int64 `gorm:"default:0;comment:回答数量" json:"answerCount"`
	RecommendationCount int64 `gorm:"default:0;comment:推荐数量" json:"recommendationCount"`
}

// TableName 指定表名
func (UserStats) TableName() string {
	return "user_stats"
}
