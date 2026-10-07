package outbox

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

const (
	StatusPending = "pending"
	StatusSent    = "sent"
	StatusFailed  = "failed"

	MaxRetryCount = 5
	// MaxReplayCount 单条死信被重放（failed -> pending）的次数上限，
	// 防止永久失败的消息（毒消息）被定时任务无限循环重投。
	MaxReplayCount = 3
)

type OutboxMessage struct {
	ID          string     `gorm:"primaryKey;size:36"`
	Topic       string     `gorm:"index;size:128;not null"`
	Key         string     `gorm:"size:256;not null"`
	Value       JSONRaw    `gorm:"type:jsonb;not null"`
	Status      string     `gorm:"index;size:20;not null;default:'pending'"`
	RetryCount  int        `gorm:"not null;default:0"`
	ErrorMessage string    `gorm:"type:text"`
	// NextRetryAt 控制失败消息的指数退避重试时间，NULL 表示立即可投递
	NextRetryAt *time.Time `gorm:"index"`
	// ReplayCount 该消息被重放的次数，达到 MaxReplayCount 后不再自动重放
	ReplayCount int       `gorm:"not null;default:0"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index"`
	SentAt      *time.Time
}

type JSONRaw json.RawMessage

func (j JSONRaw) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return []byte(j), nil
}

func (j *JSONRaw) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	*j = JSONRaw(bytes)
	return nil
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&OutboxMessage{})
}
