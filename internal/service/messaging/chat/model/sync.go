package model

import "time"

// SyncPoint 同步点，记录用户在设备上的同步状态
type SyncPoint struct {
	ID         string    `gorm:"primaryKey;size:100" json:"id"`
	UserID     string    `gorm:"index;size:100" json:"user_id"`
	DeviceID   string    `gorm:"index;size:100" json:"device_id"`
	LastSyncAt time.Time `gorm:"index" json:"last_sync_at"`
	SyncType   string    `gorm:"size:50" json:"sync_type"` // full/incremental
	Version    int64     `gorm:"default:0" json:"version"` // 递增版本号，乐观锁
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Device 用户设备信息
type Device struct {
	ID         string    `gorm:"primaryKey;size:100" json:"id"`
	UserID     string    `gorm:"index;size:100" json:"user_id"`
	DeviceType string    `gorm:"size:50" json:"device_type"` // mobile/web/desktop
	DeviceName string    `gorm:"size:200" json:"device_name"`
	LastOnline time.Time `gorm:"index" json:"last_online"`
	Status     string    `gorm:"size:50" json:"status"` // active/inactive
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// MessageState 消息状态变更记录（用于同步）
type MessageState struct {
	ID        string    `gorm:"primaryKey;size:100" json:"id"`
	MessageID string    `gorm:"index;size:100" json:"message_id"`
	UserID    string    `gorm:"index;size:100" json:"user_id"`
	State     string    `gorm:"size:50" json:"state"` // read/deleted/edited
	ChatID    string    `gorm:"index;size:100" json:"chat_id"`
	UpdatedAt time.Time `gorm:"index" json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

// SyncResponse 同步响应
type SyncResponse struct {
	Messages      []*Message      `json:"messages,omitempty"`
	MessageStates []*MessageState `json:"message_states,omitempty"`
	HasMore       bool            `json:"has_more"`
	NextSyncTime  time.Time       `json:"next_sync_time"`
	LastMessageAt time.Time       `json:"last_message_at"`
	Version       int64           `json:"version"`
}

// SyncRequest 同步请求
type SyncRequest struct {
	UserID        string    `json:"user_id"`
	DeviceID      string    `json:"device_id"`
	LastSyncAt    time.Time `json:"last_sync_at"`
	LastVersion   int64     `json:"last_version"`
	ChatIDs       []string  `json:"chat_ids,omitempty"` // 指定同步的会话
	Limit         int       `json:"limit,omitempty"`    // 单次最大同步数量
	IncludeStates bool      `json:"include_states"`     // 是否包含状态变更
}
