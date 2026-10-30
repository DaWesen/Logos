package downlink

import (
	"Logos/internal/service/messaging/types"
)

// ReadReceiptData 已读回执载荷。
// MessageIDs 是批量已读的 id 列表（旧客户端按单条处理也能兼容）。
type ReadReceiptData struct {
	ReaderID   string   `json:"reader_id"`
	ChatID     string   `json:"chat_id"`
	MessageIDs []string `json:"message_ids"`
	Timestamp  int64    `json:"timestamp"`
}

// TypingData 输入状态载荷。不含事件时间：输入状态是易失信号，客户端只关心当前是否在输入。
type TypingData struct {
	UserID string `json:"user_id"`
	ChatID string `json:"chat_id"`
	Typing bool   `json:"typing"`
}

// WithdrawData 撤回载荷。Version 用于让接收端只升不降地应用（迟到的编辑事件不能把撤回态改回正文）。
type WithdrawData struct {
	MessageID string         `json:"message_id"`
	ChatID    string         `json:"chat_id"`
	ChatType  types.ChatType `json:"chat_type"`
	SenderID  string         `json:"sender_id"`
	Version   int64          `json:"version"`
	Timestamp int64          `json:"timestamp"`
}

// DeleteData 删除载荷。只推送给删除者本人的设备，故不携带 owner_id。
type DeleteData struct {
	MessageID string         `json:"message_id"`
	ChatID    string         `json:"chat_id"`
	ChatType  types.ChatType `json:"chat_type"`
	Timestamp int64          `json:"timestamp"`
}

// EditData 编辑载荷。接收端按 version 幂等应用，乱序/重复投递不会把新内容改回旧内容。
type EditData struct {
	MessageID string         `json:"message_id"`
	ChatID    string         `json:"chat_id"`
	ChatType  types.ChatType `json:"chat_type"`
	SenderID  string         `json:"sender_id"`
	Content   string         `json:"content"`
	Version   int64          `json:"version"`
	Timestamp int64          `json:"timestamp"`
}

// Message 聊天消息。data 直接是完整 MessageEvent：发送者、类型、媒体地址等字段已经齐全，
// WS 与 TCP 都在此之前调用 gateway.EnrichSender 补全昵称/头像。
func Message(e *types.MessageEvent) Envelope {
	return New(TypeMessage, e)
}

func ReadReceipt(e *types.MessageReadEvent) Envelope {
	return New(TypeReadReceipt, ReadReceiptData{
		ReaderID:   e.ReaderID,
		ChatID:     e.ChatID,
		MessageIDs: e.MessageIDs,
		Timestamp:  e.Timestamp.UnixMilli(),
	})
}

func Typing(e *types.TypingEvent) Envelope {
	return New(TypeTyping, TypingData{
		UserID: e.UserID,
		ChatID: e.ChatID,
		Typing: e.IsTyping,
	})
}

func Withdraw(e *types.MessageWithdrawEvent) Envelope {
	return New(TypeWithdraw, WithdrawData{
		MessageID: e.MessageID,
		ChatID:    e.ChatID,
		ChatType:  e.ChatType,
		SenderID:  e.SenderID,
		Version:   e.Version,
		Timestamp: e.Timestamp.UnixMilli(),
	})
}

func Delete(e *types.MessageDeleteEvent) Envelope {
	return New(TypeDelete, DeleteData{
		MessageID: e.MessageID,
		ChatID:    e.ChatID,
		ChatType:  e.ChatType,
		Timestamp: e.Timestamp.UnixMilli(),
	})
}

func Edit(e *types.MessageEditEvent) Envelope {
	return New(TypeEdit, EditData{
		MessageID: e.MessageID,
		ChatID:    e.ChatID,
		ChatType:  e.ChatType,
		SenderID:  e.SenderID,
		Content:   e.Content,
		Version:   e.Version,
		Timestamp: e.Timestamp.UnixMilli(),
	})
}

// Presence 用户在线状态。IM topic 上是公开在线状态，广播给所有连接即可。
func Presence(e *types.UserPresenceEvent) Envelope {
	return New(TypeOnlineStatus, e)
}
