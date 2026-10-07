package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ChatType int

const (
	ChatTypePrivate ChatType = iota + 1
	ChatTypeGroup
	ChatTypeBroadcast
	ChatTypeEnd
)

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

type MessageType int

const (
	MessageTypeText MessageType = iota + 1
	MessageTypeImage
	MessageTypeFile
	MessageTypeVoice
	MessageTypeVideo
	MessageTypeLocation
	MessageTypeSystem
	MessageTypeEnd
)

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
	case MessageTypeVideo:
		return "video"
	case MessageTypeLocation:
		return "location"
	case MessageTypeSystem:
		return "system"
	default:
		return "unknown"
	}
}

type MessageStatus int

const (
	MessageStatusSent MessageStatus = iota + 1
	MessageStatusDelivered
	MessageStatusRead
	MessageStatusWithdrawn
	MessageStatusEdited
	MessageStatusEnd
)

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

type Metadata map[string]string

func (m Metadata) Value() (driver.Value, error) {
	if m == nil {
		return json.Marshal(map[string]string{})
	}
	return json.Marshal(m)
}

func (m *Metadata) Scan(value interface{}) error {
	if value == nil {
		*m = make(Metadata)
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*m = make(Metadata)
		return nil
	}
	return json.Unmarshal(b, m)
}

type StringArray []string

func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return json.Marshal([]string{})
	}
	return json.Marshal(a)
}

func (a *StringArray) Scan(value interface{}) error {
	if value == nil {
		*a = make(StringArray, 0)
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*a = make(StringArray, 0)
		return nil
	}
	return json.Unmarshal(b, a)
}

type JSONRaw json.RawMessage

func (j JSONRaw) Value() (driver.Value, error) {
	if len(j) == 0 {
		return json.Marshal(map[string]interface{}{})
	}
	return []byte(j), nil
}

func (j *JSONRaw) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	*j = JSONRaw(b)
	return nil
}

type ImageContent struct {
	URL       string `json:"url"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Size      int64  `json:"size,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
}

type FileContent struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type VoiceContent struct {
	URL      string  `json:"url"`
	Duration float64 `json:"duration,omitempty"`
	Size     int64   `json:"size,omitempty"`
	MimeType string  `json:"mime_type,omitempty"`
}

type VideoContent struct {
	URL       string  `json:"url"`
	Thumbnail string  `json:"thumbnail,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	Size      int64   `json:"size,omitempty"`
	MimeType  string  `json:"mime_type,omitempty"`
}

type LocationContent struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Title     string  `json:"title,omitempty"`
	Address   string  `json:"address,omitempty"`
}

type Message struct {
	ID string `gorm:"type:varchar(64);primaryKey" json:"id"`
	// Seq 会话内单调递增序号，用于稳定排序与增量/游标拉取（仅追加字段，前端可忽略）
	Seq            int64          `gorm:"index;default:0" json:"seq"`
	RequestID      string         `gorm:"type:varchar(64);index" json:"request_id"`
	ConversationID string         `gorm:"type:varchar(100);index" json:"conversation_id"`
	ChatID         string         `gorm:"type:varchar(100);index" json:"chat_id"`
	ChatType       int            `gorm:"type:integer;index" json:"chat_type"`
	SenderID       string         `gorm:"type:varchar(64);index;not null" json:"sender_id"`
	SenderName     string         `gorm:"type:varchar(100)" json:"sender_name"`
	SenderAvatar   string         `gorm:"type:varchar(500)" json:"sender_avatar"`
	MessageType    int            `gorm:"type:integer" json:"message_type"`
	Content        string         `gorm:"type:text" json:"content"`
	MediaURL       string         `gorm:"type:varchar(500)" json:"media_url"`
	MediaMeta      JSONRaw        `gorm:"type:jsonb" json:"media_meta"`
	Metadata       Metadata       `gorm:"type:jsonb" json:"metadata"`
	MentionUserIDs StringArray    `gorm:"type:jsonb" json:"mention_user_ids"`
	ReplyToMessage string         `gorm:"type:varchar(100)" json:"reply_to_message"`
	Status         string         `gorm:"type:varchar(50);default:'sent'" json:"status"`
	IsRead         bool           `gorm:"default:false" json:"is_read"`
	Channel        string         `gorm:"type:varchar(50);default:'web'" json:"channel"`
	Role           string         `gorm:"type:varchar(50);default:'user'" json:"role"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Message) TableName() string {
	return "messages"
}

func (m *Message) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if m.RequestID == "" {
		m.RequestID = m.ID
	}
	if m.Metadata == nil {
		m.Metadata = make(Metadata)
	}
	if m.MentionUserIDs == nil {
		m.MentionUserIDs = make(StringArray, 0)
	}
	return nil
}

type Conversation struct {
	ID        string         `gorm:"type:varchar(100);primaryKey" json:"id"`
	ChatID    string         `gorm:"type:varchar(100);index;not null" json:"chat_id"`
	ChatType  int            `gorm:"type:integer;index;not null" json:"chat_type"`
	Name      string         `gorm:"type:varchar(255)" json:"name"`
	Avatar    string         `gorm:"type:varchar(255)" json:"avatar"`
	BotID     string         `gorm:"type:varchar(36);default:''" json:"bot_id"`
	UserID    string         `gorm:"type:varchar(36);default:''" json:"user_id"`
	Title     string         `gorm:"type:text" json:"title"`
	Status    string         `gorm:"type:varchar(20);default:'active'" json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Conversation) TableName() string {
	return "conversations"
}

type Group struct {
	ID           string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	Name         string         `gorm:"type:varchar(255);not null" json:"name"`
	OwnerID      string         `gorm:"type:varchar(36);index;not null" json:"owner_id"`
	Avatar       string         `gorm:"type:varchar(255)" json:"avatar"`
	Description  string         `gorm:"type:text" json:"description"`
	Announcement string         `gorm:"type:text" json:"announcement"`
	MaxMembers   int            `gorm:"default:500" json:"max_members"`
	MemberCount  int            `gorm:"default:0" json:"member_count"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Group) TableName() string {
	return "groups"
}

type GroupMemberRole int

const (
	GroupMemberRoleOwner GroupMemberRole = iota + 1
	GroupMemberRoleAdmin
	GroupMemberRoleMember
	GroupMemberRoleBot
	GroupMemberRoleEnd
)

func (r GroupMemberRole) String() string {
	switch r {
	case GroupMemberRoleOwner:
		return "owner"
	case GroupMemberRoleAdmin:
		return "admin"
	case GroupMemberRoleMember:
		return "member"
	case GroupMemberRoleBot:
		return "bot"
	default:
		return "unknown"
	}
}

type MuteType int

const (
	MuteTypeNone MuteType = iota + 1
	MuteTypeTemporary
	MuteTypePermanent
	MuteTypeEnd
)

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

type GroupMember struct {
	ID        int64           `gorm:"primaryKey;autoIncrement" json:"id"`
	GroupID   string          `gorm:"type:varchar(36);uniqueIndex:idx_group_user;index;not null" json:"group_id"`
	UserID    string          `gorm:"type:varchar(64);uniqueIndex:idx_group_user;index;not null" json:"user_id"`
	Nickname  string          `gorm:"type:varchar(50)" json:"nickname"`
	Role      GroupMemberRole `gorm:"default:3" json:"role"`
	MuteType  MuteType        `gorm:"default:1" json:"mute_type"`
	MuteUntil *time.Time      `json:"mute_until"`
	JoinedAt  time.Time       `json:"joined_at"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func (GroupMember) TableName() string {
	return "group_members"
}

type ConversationParticipant struct {
	ID             int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ConversationID string    `gorm:"index;type:varchar(100);not null" json:"conversation_id"`
	UserID         string    `gorm:"type:varchar(64);index;not null" json:"user_id"`
	LastReadAt     time.Time `json:"last_read_at"`
	// LastReadSeq 会话已读位点：<= 该 seq 的消息视为已读，未读数由 seq 位点比对得出，
	// 避免逐条 UPDATE messages.is_read 的全表写放大。
	LastReadSeq int64     `gorm:"default:0" json:"last_read_seq"`
	JoinedAt    time.Time `json:"joined_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (ConversationParticipant) TableName() string {
	return "conversation_participants"
}

// ConversationSeq 维护每个会话的 seq 分配器。
// 通过 INSERT ... ON CONFLICT DO NOTHING 保证行存在，再 FOR UPDATE 行锁原子自增，
// 从而在多副本并发写入时为同一会话分配严格单调递增且不重复的 seq。
type ConversationSeq struct {
	ChatID    string    `gorm:"type:varchar(100);primaryKey" json:"chat_id"`
	LastSeq   int64     `gorm:"not null;default:0" json:"last_seq"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (ConversationSeq) TableName() string {
	return "conversation_seqs"
}

type ConversationItem struct {
	ChatID      string    `json:"chat_id"`
	ChatType    int       `json:"chat_type"`
	Name        string    `json:"name"`
	Avatar      string    `json:"avatar"`
	LastMessage *Message  `json:"last_message,omitempty"`
	UnreadCount int64     `json:"unread_count"`
	UpdatedAt   time.Time `json:"updated_at"`
	IsPinned    bool      `json:"is_pinned"`
	IsMuted     bool      `json:"is_muted"`
	IsFriend    bool      `json:"is_friend"`
	IsBlocked   bool      `json:"is_blocked"`
}

func AutoMigrate(db *gorm.DB) error {
	// 先清理无效的 JSON 数据
	cleanInvalidJSON(db)

	if err := db.AutoMigrate(
		&Message{},
		&Conversation{},
		&Group{},
		&GroupMember{},
		&ConversationParticipant{},
		&ConversationSeq{},
	); err != nil {
		return err
	}

	// 为历史消息回填 seq（仅 PostgreSQL），保证按 seq 排序/游标拉取对旧数据同样生效
	backfillMessageSeq(db)
	// 为历史参与者回填已读位点，避免升级后把全部历史消息误判为未读
	backfillReadSeq(db)
	return nil
}

// backfillReadSeq 依据既有 last_read_at 推导会话已读位点：
// 取该会话中 created_at <= last_read_at 的最大 seq（排除自己发送的消息）。
// 仅回填 last_read_seq = 0 的行，保证已推进过的位点不被回退。
func backfillReadSeq(db *gorm.DB) {
	if db.Dialector.Name() != "postgres" {
		return
	}
	db.Exec(`UPDATE conversation_participants p
		SET last_read_seq = COALESCE((
			SELECT MAX(m.seq) FROM messages m
			WHERE m.chat_id = p.conversation_id
			  AND m.created_at <= p.last_read_at
			  AND m.sender_id <> p.user_id
		), 0)
		WHERE p.last_read_seq = 0`)
}

// backfillMessageSeq 为 seq=0 的历史消息按 created_at 顺序分配会话内 seq，
// 并把 conversation_seqs 的分配器推进到各会话当前最大 seq，避免新增消息 seq 冲突。
func backfillMessageSeq(db *gorm.DB) {
	if db.Dialector.Name() != "postgres" {
		return
	}
	// 幂等：仅处理 seq=0 的行；已回填过的行 seq>0 会被跳过
	db.Exec(`UPDATE messages m SET seq = sub.rn
		FROM (
			SELECT id, ROW_NUMBER() OVER (PARTITION BY chat_id ORDER BY created_at ASC, id ASC) AS rn
			FROM messages WHERE seq = 0
		) sub
		WHERE m.id = sub.id AND m.seq = 0`)

	// 将分配器同步到现有最大 seq（取较大值，不回退）
	db.Exec(`INSERT INTO conversation_seqs (chat_id, last_seq, updated_at)
		SELECT chat_id, MAX(seq), NOW() FROM messages GROUP BY chat_id
		ON CONFLICT (chat_id) DO UPDATE
		SET last_seq = GREATEST(conversation_seqs.last_seq, EXCLUDED.last_seq),
		    updated_at = NOW()`)
}

func cleanInvalidJSON(db *gorm.DB) {
	// 检查 messages 表是否存在
	var tableExists bool
	db.Raw("SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_name = 'messages')").Scan(&tableExists)
	if !tableExists {
		return
	}

	// 检查 media_meta 列是否存在
	var columnExists bool
	db.Raw("SELECT EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'messages' AND column_name = 'media_meta')").Scan(&columnExists)
	if !columnExists {
		return
	}

	// 清理无效的 JSON 数据，设置为 NULL 或空对象
	db.Exec("UPDATE messages SET media_meta = NULL WHERE media_meta IS NOT NULL AND media_meta::text = ''")
	db.Exec("UPDATE messages SET media_meta = '{}' WHERE media_meta IS NULL")

	// 尝试转换为 jsonb 类型（更宽松的方式）
	db.Exec(`DO $$
	BEGIN
		IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'messages' AND column_name = 'media_meta' AND data_type <> 'jsonb') THEN
			ALTER TABLE messages ALTER COLUMN media_meta TYPE jsonb USING CASE 
				WHEN media_meta IS NULL OR media_meta = '' THEN '{}'::jsonb 
				WHEN media_meta::text LIKE '{%' OR media_meta::text LIKE '[%' THEN media_meta::jsonb 
				ELSE '{}'::jsonb 
			END;
		END IF;
	END $$;`)
}
