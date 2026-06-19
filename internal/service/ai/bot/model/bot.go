package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
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
	QQNumber       string         `gorm:"type:varchar(20);uniqueIndex;comment:关联QQ号" json:"qqNumber,omitempty"`
	Status         string         `gorm:"size:20;default:active;comment:状态" json:"status"`
	CreatedAt      time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt      gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

// Conversation 对话数据模型
type Conversation struct {
	ID        string         `gorm:"type:varchar(100);primaryKey;comment:对话ID" json:"id"`
	ChatID    string         `gorm:"type:varchar(100);index;comment:聊天ID" json:"chatId"`
	ChatType  int            `gorm:"type:integer;index;comment:聊天类型" json:"chatType"`
	Name      string         `gorm:"type:varchar(255);comment:名称" json:"name"`
	Avatar    string         `gorm:"type:varchar(255);comment:头像" json:"avatar"`
	BotID     string         `gorm:"type:varchar(36);index;comment:BotID" json:"botId"`
	UserID    string         `gorm:"type:varchar(36);index;comment:用户ID" json:"userId"`
	Title     string         `gorm:"type:text;comment:对话标题" json:"title"`
	Status    string         `gorm:"type:varchar(20);default:active;comment:状态" json:"status"`
	Messages  []Message      `gorm:"foreignKey:ConversationID;comment:消息列表" json:"messages"`
	CreatedAt time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

// Message 消息数据模型
type Message struct {
	ID             string         `gorm:"type:varchar(36);primaryKey;comment:消息ID" json:"id"`
	RequestID      string         `gorm:"type:varchar(36);comment:请求ID" json:"requestId"`
	ConversationID string         `gorm:"type:varchar(100);index;comment:会话ID" json:"conversationId"`
	ChatID         string         `gorm:"type:varchar(100);index;comment:聊天ID" json:"chatId"`
	ChatType       int            `gorm:"type:integer;comment:聊天类型" json:"chatType"`
	SenderID       string         `gorm:"type:varchar(36);index;not null;comment:发送者ID" json:"senderId"`
	BotID          string         `gorm:"type:varchar(36);index;comment:BotID" json:"botId"`
	MessageType    int            `gorm:"type:integer;default:1;comment:消息类型" json:"messageType"`
	Content        string         `gorm:"type:text;comment:消息内容" json:"content"`
	MediaURL       string         `gorm:"type:varchar(500);comment:媒体URL" json:"mediaUrl"`
	MediaMeta      string         `gorm:"type:text;comment:媒体元数据" json:"mediaMeta"`
	Metadata       JSONMap        `gorm:"type:jsonb;comment:元数据" json:"metadata"`
	MentionUserIDs []string       `gorm:"type:jsonb;comment:提及用户ID列表" json:"mentionUserIds"`
	ReplyToMessage string         `gorm:"type:varchar(36);comment:回复消息ID" json:"replyToMessage"`
	Status         string         `gorm:"type:varchar(50);default:'sent';comment:消息状态" json:"status"`
	IsRead         bool           `gorm:"default:false;comment:是否已读" json:"isRead"`
	Channel        string         `gorm:"type:varchar(50);default:'web';comment:消息渠道" json:"channel"`
	Role           string         `gorm:"type:varchar(50);default:'user';comment:角色" json:"role"`
	CreatedAt      time.Time      `gorm:"type:timestamptz;comment:创建时间" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"type:timestamptz;comment:更新时间" json:"updatedAt"`
	DeletedAt      gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
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
	ID         string         `gorm:"primaryKey;size:36" json:"id"`
	UserID     string         `gorm:"index;size:36;not null" json:"userId"`
	BotID      string         `gorm:"index;size:36;not null" json:"botId"`
	Key        string         `gorm:"size:255;not null" json:"key"`
	Value      string         `gorm:"type:text;not null" json:"value"`
	Category   string         `gorm:"size:50;index" json:"category"`
	Source     string         `gorm:"size:50" json:"source"`
	Confidence float64        `gorm:"type:decimal(3,2);default:0.8" json:"confidence"`
	CreatedAt  time.Time      `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt  time.Time      `gorm:"autoUpdateTime" json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// JSONMap JSON映射类型
type JSONMap map[string]string

func (j JSONMap) Value() (driver.Value, error) {
	if j == nil {
		return json.Marshal(map[string]string{})
	}
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

func (m *Message) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.RequestID == "" {
		m.RequestID = m.ID
	}
	if m.Metadata == nil {
		m.Metadata = make(JSONMap)
	}
	if m.MentionUserIDs == nil {
		m.MentionUserIDs = []string{}
	}
	if m.MediaMeta == "" {
		m.MediaMeta = "{}"
	}
	return nil
}

// AutoMigrate 自动迁移数据库表
func AutoMigrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&Bot{}, &Conversation{}, &Message{}, &Prompt{}, &UserMemory{}); err != nil {
		return err
	}

	// 创建复合索引
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_bots_user_deleted_created ON bots(user_id, deleted_at, created_at DESC)`).Error; err != nil {
		return err
	}

	return nil
}
