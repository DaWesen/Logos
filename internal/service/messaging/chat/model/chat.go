package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// ChatType 聊天类型
type ChatType int

const (
	ChatTypePrivate ChatType = iota + 1
	ChatTypeGroup
	ChatTypeBroadcast
	ChatTypeEnd // 边界标记
)

// String 实现 String 方法
func (c ChatType) String() string {
	switch c {
	case ChatTypePrivate:
		return "private"
	case ChatTypeGroup:
		return "group"
	case ChatTypeBroadcast:
		return "broadcast"
	default:
		return "unknown"
	}
}

// MessageType 消息类型
type MessageType int

const (
	MessageTypeText MessageType = iota + 1
	MessageTypeImage
	MessageTypeFile
	MessageTypeVoice
	MessageTypeSystem
	MessageTypeEnd // 边界标记
)

// String 实现 String 方法
func (m MessageType) String() string {
	switch m {
	case MessageTypeText:
		return "text"
	case MessageTypeImage:
		return "image"
	case MessageTypeFile:
		return "file"
	case MessageTypeVoice:
		return "voice"
	case MessageTypeSystem:
		return "system"
	default:
		return "unknown"
	}
}

// MessageStatus 消息状态
type MessageStatus int

const (
	MessageStatusSent MessageStatus = iota + 1
	MessageStatusDelivered
	MessageStatusRead
	MessageStatusWithdrawn
	MessageStatusEdited
	MessageStatusEnd // 边界标记
)

// String 实现 String 方法
func (s MessageStatus) String() string {
	switch s {
	case MessageStatusSent:
		return "sent"
	case MessageStatusDelivered:
		return "delivered"
	case MessageStatusRead:
		return "read"
	case MessageStatusWithdrawn:
		return "withdrawn"
	case MessageStatusEdited:
		return "edited"
	default:
		return "unknown"
	}
}

// Metadata 用于存储元数据的map类型
type Metadata map[string]string

// Value 实现 driver.Valuer 接口
func (m Metadata) Value() (driver.Value, error) {
	return json.Marshal(m)
}

// Scan 实现 sql.Scanner 接口
func (m *Metadata) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, &m)
}

// Message 消息模型
type Message struct {
	ID             string         `gorm:"primaryKey;size:100" json:"id"`
	ChatID         string         `gorm:"index;size:100" json:"chat_id"`
	ChatType       ChatType       `gorm:"index" json:"chat_type"`
	SenderID       string         `gorm:"index;size:100" json:"sender_id"`
	MessageType    MessageType    `json:"message_type"`
	Content        string         `gorm:"type:text" json:"content"`
	Metadata       Metadata       `gorm:"type:json" json:"metadata"`
	Status         MessageStatus  `gorm:"type:int;default:1" json:"status"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
	ReplyToMessage string         `gorm:"size:100" json:"reply_to_message_id"`
	MentionUserIDs string         `gorm:"type:text" json:"mention_user_ids"` // 存储 JSON 数组
}

// TableName 指定表名
func (Message) TableName() string {
	return "messages"
}

// Conversation 会话模型（用于单聊/群聊基本信息）
type Conversation struct {
	ID            string         `gorm:"primaryKey;size:100" json:"id"`
	Type          ChatType       `gorm:"index" json:"type"`
	Name          string         `gorm:"size:200" json:"name"`
	Metadata      Metadata       `gorm:"type:json" json:"metadata"`
	LastMessage   *Message       `gorm:"foreignKey:LastMessageID;references:ID" json:"last_message,omitempty"`
	LastMessageID string         `gorm:"size:100" json:"last_message_id"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 指定表名
func (Conversation) TableName() string {
	return "conversations"
}

// Group 群组模型
type Group struct {
	ID           string         `gorm:"primaryKey;size:100" json:"id"`
	Name         string         `gorm:"size:200" json:"name"`
	OwnerID      string         `gorm:"index;size:100" json:"owner_id"`
	Metadata     Metadata       `gorm:"type:json" json:"metadata"`
	Announcement string         `gorm:"type:text" json:"announcement"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 指定表名
func (Group) TableName() string {
	return "groups"
}

// GroupMemberRole 群成员角色
type GroupMemberRole int

const (
	GroupMemberRoleOwner GroupMemberRole = iota + 1
	GroupMemberRoleAdmin
	GroupMemberRoleMember
	GroupMemberRoleEnd // 边界标记
)

// String 实现 String 方法
func (r GroupMemberRole) String() string {
	switch r {
	case GroupMemberRoleOwner:
		return "owner"
	case GroupMemberRoleAdmin:
		return "admin"
	case GroupMemberRoleMember:
		return "member"
	default:
		return "unknown"
	}
}

// MuteType 禁言类型
type MuteType int

const (
	MuteTypeNone MuteType = iota + 1
	MuteTypeTemporary
	MuteTypePermanent
	MuteTypeEnd // 边界标记
)

// String 实现 String 方法
func (mt MuteType) String() string {
	switch mt {
	case MuteTypeNone:
		return "none"
	case MuteTypeTemporary:
		return "temporary"
	case MuteTypePermanent:
		return "permanent"
	default:
		return "unknown"
	}
}

// GroupMember 群成员
type GroupMember struct {
	ID        string          `gorm:"primaryKey;size:100" json:"id"`
	GroupID   string          `gorm:"index;size:100" json:"group_id"`
	UserID    string          `gorm:"index;size:100" json:"user_id"`
	Role      GroupMemberRole `gorm:"default:3" json:"role"`
	MuteType  MuteType        `gorm:"default:1" json:"mute_type"`
	MuteUntil time.Time       `json:"mute_until"`
	JoinedAt  time.Time       `json:"joined_at"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TableName 指定表名
func (GroupMember) TableName() string {
	return "group_members"
}

// Participant 会话参与者（用于单聊）
type Participant struct {
	ID             string    `gorm:"primaryKey;size:100" json:"id"`
	ConversationID string    `gorm:"index;size:100" json:"conversation_id"`
	UserID         string    `gorm:"index;size:100" json:"user_id"`
	LastReadAt     time.Time `json:"last_read_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// TableName 指定表名
func (Participant) TableName() string {
	return "participants"
}

// AutoMigrate 自动迁移
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Message{},
		&Conversation{},
		&Group{},
		&GroupMember{},
		&Participant{},
		&SyncPoint{},
		&Device{},
		&MessageState{},
	)
}
