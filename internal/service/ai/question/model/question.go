package model

import (
	"time"

	"gorm.io/gorm"
)

type QARecord struct {
	ID         string  `gorm:"primaryKey;comment:问答记录ID"`
	Question   string  `gorm:"type:text;not null;comment:问题内容"`
	Answer     string  `gorm:"type:text;not null;comment:回答内容"`
	Confidence float64 `gorm:"type:decimal(5,4);default:0;comment:置信度"`
	UserID     int64   `gorm:"index;not null;comment:用户ID"`
	Timestamp  int64   `gorm:"not null;comment:时间戳"`
	Feedback   *string `gorm:"type:text;comment:用户反馈"`
	Rating     *int32  `gorm:"comment:用户评分"`
	// Degraded 标记这条回答只是兜底文案（模型未配置、调用失败或超时）。
	// 它不能和正常回答一样被幂等键当成终态：否则一次模型故障会被
	// 永久固化成这个 request_id 的答案，后续重放也只会拿回占位文本。
	Degraded  bool      `gorm:"not null;default:false;comment:是否为降级回答"`
	CreatedAt time.Time `gorm:"autoCreateTime;comment:创建时间"`
	UpdatedAt time.Time `gorm:"autoUpdateTime;comment:更新时间"`
}

func (QARecord) TableName() string {
	return "qa_records"
}

type QuestionContext struct {
	Context map[string]string
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&QARecord{})
}
