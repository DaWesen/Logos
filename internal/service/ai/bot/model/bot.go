package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Bot Bot 数据模型
type Bot struct {
	ID             string         `gorm:"primaryKey;size:36;comment:BotID" json:"id"`
	UserID         string         `gorm:"index;size:36;comment:用户ID" json:"userId"`
	Name           string         `gorm:"index;size:255;not null;comment:Bot名称" json:"name"`
	Description    string         `gorm:"type:text;comment:Bot描述" json:"description"`
	Avatar         string         `gorm:"type:text;comment:Bot头像" json:"avatar"`
	Type           string         `gorm:"size:50;comment:Bot类型" json:"type"`
	Provider       string         `gorm:"size:50;comment:模型提供商" json:"provider"`
	Model          string         `gorm:"size:255;comment:模型名称" json:"model"`
	APIKey         string         `gorm:"type:text;comment:API密钥" json:"apiKey,omitempty"`
	BaseURL        string         `gorm:"type:text;comment:API基础地址" json:"baseUrl,omitempty"`
	EmbeddingModel string         `gorm:"size:255;comment:Embedding模型名称" json:"embeddingModel,omitempty"`
	SystemPrompt   string         `gorm:"type:text;comment:系统提示词" json:"systemPrompt"`
	Config         JSONMap        `gorm:"type:jsonb;comment:Bot配置" json:"config"`
	Status         string         `gorm:"size:20;default:active;comment:状态" json:"status"`
	CreatedAt      time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt      gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

// Conversation 对话数据模型
type Conversation struct {
	ID        string         `gorm:"primaryKey;size:36;comment:对话ID" json:"id"`
	BotID     string         `gorm:"index;size:36;not null;comment:BotID" json:"botId"`
	UserID    string         `gorm:"index;size:36;not null;comment:用户ID" json:"userId"`
	Title     string         `gorm:"type:text;comment:对话标题" json:"title"`
	Status    string         `gorm:"size:20;default:active;comment:状态" json:"status"`
	Messages  []Message      `gorm:"foreignKey:ConversationID;comment:消息列表" json:"messages"`
	CreatedAt time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

// Message 消息数据模型
type Message struct {
	ID             string    `gorm:"primaryKey;size:36;comment:消息ID" json:"id"`
	BotID          string    `gorm:"index;size:36;comment:BotID" json:"botId"`
	ConversationID string    `gorm:"index;size:36;not null;comment:对话ID" json:"conversationId"`
	Role           string    `gorm:"size:50;not null;comment:角色" json:"role"`
	Content        string    `gorm:"type:text;not null;comment:消息内容" json:"content"`
	Metadata       JSONMap   `gorm:"type:jsonb;comment:元数据" json:"metadata"`
	CreatedAt      time.Time `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
}

// Prompt 提示词模型
type Prompt struct {
	ID          string         `gorm:"primaryKey;size:36;comment:提示词ID" json:"id"`
	UserID      string         `gorm:"index;size:36;comment:用户ID" json:"userId"`
	Name        string         `gorm:"index;size:255;not null;comment:提示词名称" json:"name"`
	Description string         `gorm:"type:text;comment:提示词描述" json:"description"`
	Content     string         `gorm:"type:text;not null;comment:提示词内容" json:"content"`
	Type        string         `gorm:"size:50;comment:提示词类型" json:"type"` // system/user/assistant
	IsPreset    bool           `gorm:"default:false;comment:是否预设" json:"isPreset"`
	IsPublic    bool           `gorm:"default:false;comment:是否公开" json:"isPublic"`
	Config      JSONMap        `gorm:"type:jsonb;comment:配置" json:"config"`
	CreatedAt   time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

// UserMemory 用户记忆模型
type UserMemory struct {
	ID        string         `gorm:"primaryKey;size:36;comment:记忆ID" json:"id"`
	UserID    string         `gorm:"index;size:36;not null;comment:用户ID" json:"userId"`
	BotID     string         `gorm:"index;size:36;not null;comment:BotID" json:"botId"`
	Key       string         `gorm:"size:255;not null;comment:记忆键" json:"key"`
	Value     string         `gorm:"type:text;not null;comment:记忆值" json:"value"`
	CreatedAt time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

// JSONMap JSON映射类型
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

// AutoMigrate 自动迁移数据库表
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Bot{}, &Conversation{}, &Message{}, &Prompt{}, &UserMemory{})
}
