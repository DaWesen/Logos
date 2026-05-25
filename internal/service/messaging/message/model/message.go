package model

import (
	"time"

	"gorm.io/gorm"
)

type Message struct {
	ID            string    `gorm:"primaryKey;size:64;comment:消息ID"`
	Topic         string    `gorm:"size:64;index;not null;comment:消息主题"`
	Content       string    `gorm:"type:text;not null;comment:消息内容"`
	Priority      int       `gorm:"not null;default:2;comment:优先级 1-低 2-普通 3-高 4-紧急"`
	Headers       string    `gorm:"type:text;comment:头部JSON"`
	CorrelationID *string   `gorm:"size:128;comment:关联ID"`
	Timestamp     int64     `gorm:"not null;comment:时间戳"`
	Status        string    `gorm:"size:32;not null;default:PENDING;comment:状态 PENDING/PROCESSED/FAILED/ACKED"`
	CreatedAt     time.Time `gorm:"autoCreateTime;comment:创建时间"`
}

func (Message) TableName() string {
	return "queue_messages"
}

func (m *Message) GetID() string             { return m.ID }
func (m *Message) GetTopic() string          { return m.Topic }
func (m *Message) GetContent() string        { return m.Content }
func (m *Message) GetPriority() int          { return m.Priority }
func (m *Message) GetHeaders() string        { return m.Headers }
func (m *Message) GetCorrelationID() *string { return m.CorrelationID }
func (m *Message) GetTimestamp() int64       { return m.Timestamp }
func (m *Message) GetStatus() string         { return m.Status }
func (m *Message) GetCreatedAt() time.Time   { return m.CreatedAt }

type MessageSubscription struct {
	ID            string    `gorm:"primaryKey;size:64;comment:订阅ID"`
	Topic         string    `gorm:"size:64;index;not null;comment:订阅主题"`
	ConsumerGroup string    `gorm:"size:128;index;not null;comment:消费者组"`
	Config        string    `gorm:"type:text;comment:配置JSON"`
	Status        string    `gorm:"size:32;not null;default:ACTIVE;comment:状态"`
	CreatedAt     time.Time `gorm:"autoCreateTime;comment:创建时间"`
	UpdatedAt     time.Time `gorm:"autoUpdateTime;comment:更新时间"`
}

func (MessageSubscription) TableName() string {
	return "message_subscriptions"
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Message{}, &MessageSubscription{})
}
