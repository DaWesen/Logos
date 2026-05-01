package types

import (
	"encoding/json"
	"time"
)

type EventType string

const (
	EventTypeMessage         EventType = "message"
	EventTypeMessageRead     EventType = "message_read"
	EventTypeTyping          EventType = "typing"
	EventTypeUserOnline      EventType = "user_online"
	EventTypeUserOffline     EventType = "user_offline"
	EventTypeNotification    EventType = "notification"
	EventTypeMessageWithdraw EventType = "message_withdraw"
	EventTypeMessageEdit     EventType = "message_edit"
	EventTypeGroupInvite     EventType = "group_invite"
	EventTypeGroupKick       EventType = "group_kick"
)

type MessageEvent struct {
	ID             string                 `json:"id"`
	ChatID         string                 `json:"chat_id"`
	ChatType       ChatType               `json:"chat_type"`
	SenderID       string                 `json:"sender_id"`
	MessageType    MessageType            `json:"message_type"`
	Content        string                 `json:"content"`
	Metadata       map[string]string      `json:"metadata,omitempty"`
	Timestamp      time.Time              `json:"timestamp"`
	ReplyToMessage string                 `json:"reply_to_message,omitempty"`
	MentionUserIDs []string               `json:"mention_user_ids,omitempty"`
	Extra          map[string]interface{} `json:"extra,omitempty"`
	RecipientIDs   []string               `json:"recipient_ids,omitempty"` // 接收者列表
}

type MessageReadEvent struct {
	MessageIDs   []string  `json:"message_ids"`
	ReaderID     string    `json:"reader_id"`
	ChatID       string    `json:"chat_id"`
	ChatType     ChatType  `json:"chat_type,omitempty"`
	RecipientIDs []string  `json:"recipient_ids,omitempty"` // 应该接收已读回执的用户列表
	Timestamp    time.Time `json:"timestamp"`
}

type TypingEvent struct {
	UserID       string    `json:"user_id"`
	ChatID       string    `json:"chat_id"`
	IsTyping     bool      `json:"is_typing"`
	RecipientIDs []string  `json:"recipient_ids,omitempty"` // 应该接收输入状态的用户列表
	Timestamp    time.Time `json:"timestamp"`
}

func (e *TypingEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func TypingEventFromJSON(data []byte) (*TypingEvent, error) {
	var e TypingEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

type UserPresenceEvent struct {
	UserID    string    `json:"user_id"`
	DeviceID  string    `json:"device_id,omitempty"`
	Online    bool      `json:"online"`
	Timestamp time.Time `json:"timestamp"`
}

type NotificationEvent struct {
	ID        string            `json:"id"`
	UserID    string            `json:"user_id"`
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

type MessageEditEvent struct {
	MessageID string    `json:"message_id"`
	EditorID  string    `json:"editor_id"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type MessageWithdrawEvent struct {
	MessageID  string    `json:"message_id"`
	WithdrawID string    `json:"withdraw_id"`
	ChatID     string    `json:"chat_id"`
	Timestamp  time.Time `json:"timestamp"`
}

type GroupEvent struct {
	Type       string            `json:"type"`
	GroupID    string            `json:"group_id"`
	UserID     string            `json:"user_id"`
	OperatorID string            `json:"operator_id"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
}

func NewMessageEvent(
	id, chatID string,
	chatType ChatType,
	senderID string,
	messageType MessageType,
	content string,
	metadata map[string]string,
	replyTo string,
	mentionUserIDs []string,
	recipientIDs []string,
) *MessageEvent {
	return &MessageEvent{
		ID:             id,
		ChatID:         chatID,
		ChatType:       chatType,
		SenderID:       senderID,
		MessageType:    messageType,
		Content:        content,
		Metadata:       metadata,
		Timestamp:      time.Now(),
		ReplyToMessage: replyTo,
		MentionUserIDs: mentionUserIDs,
		RecipientIDs:   recipientIDs,
	}
}

func (e *MessageEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func MessageEventFromJSON(data []byte) (*MessageEvent, error) {
	var e MessageEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (e *UserPresenceEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func UserPresenceEventFromJSON(data []byte) (*UserPresenceEvent, error) {
	var e UserPresenceEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (e *NotificationEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func NotificationEventFromJSON(data []byte) (*NotificationEvent, error) {
	var e NotificationEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (e *MessageReadEvent) ToJSON() ([]byte, error) {
	return json.Marshal(e)
}

func MessageReadEventFromJSON(data []byte) (*MessageReadEvent, error) {
	var e MessageReadEvent
	err := json.Unmarshal(data, &e)
	if err != nil {
		return nil, err
	}
	return &e, nil
}
