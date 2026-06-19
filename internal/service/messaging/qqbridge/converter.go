package qqbridge

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/cache"
	"Logos/pkg/logger"

	"github.com/google/uuid"
)

// InboundMessage 入站消息结构（来自 ZeroBot）
type InboundMessage struct {
	MessageType string `json:"message_type"` // private / group
	SubType     string `json:"sub_type"`
	MessageID   int64  `json:"message_id"`
	UserID      int64  `json:"user_id"` // 对方的QQ号
	GroupID     int64  `json:"group_id,omitempty"`
	SelfID      int64  `json:"self_id"` // 小号的QQ号
	RawMessage  string `json:"raw_message"`
	IsSent      bool   `json:"is_sent"` // true=小号发出的消息，false=别人发来的消息
	Sender      struct {
		UserID   int64  `json:"user_id"`
		Nickname string `json:"nickname"`
		Card     string `json:"card,omitempty"`
	} `json:"sender"`
	Message []OneBotSegment `json:"message"`
}

// OneBotSegment OneBot 消息段
type OneBotSegment struct {
	Type string `json:"type"`
	Data struct {
		Text string `json:"text,omitempty"`
		ID   int64  `json:"id,omitempty"`
		URL  string `json:"url,omitempty"`
		File string `json:"file,omitempty"`
	} `json:"data"`
}

// MessageConverter 消息格式转换器
type MessageConverter struct {
	cfg *config.Config
}

// NewMessageConverter 创建转换器
func NewMessageConverter(cfg *config.Config) *MessageConverter {
	return &MessageConverter{cfg: cfg}
}

// ToMessageEvent 将 OneBot 入站消息转换为 Logos MessageEvent
func (c *MessageConverter) ToMessageEvent(ctx context.Context, msg *InboundMessage) (*types.MessageEvent, error) {
	qqPrefix := c.getQQUserPrefix()
	groupPrefix := c.getQQGroupPrefix()

	var chatID string
	var chatType types.ChatType
	var senderID string
	var senderName string
	var mentionBotIDs []string

	if msg.IsSent {
		// 小号发出的消息 → SenderID 是 Bot
		botID := c.findBotByQQNumber(msg.SelfID)
		if botID != "" {
			senderID = botID
		} else {
			senderID = fmt.Sprintf("%s%d", qqPrefix, msg.SelfID)
		}
		senderName = "Bot"
	} else {
		// 别人发来的消息 → SenderID 是用户
		logosUserID := c.findLogosUserByQQ(msg.UserID)
		if logosUserID != "" {
			senderID = logosUserID
		} else {
			senderID = fmt.Sprintf("%s%d", qqPrefix, msg.UserID)
		}
		senderName = msg.Sender.Nickname
		if msg.Sender.Card != "" {
			senderName = msg.Sender.Card
		}
	}

	switch msg.MessageType {
	case "private":
		chatType = types.ChatTypePrivate
		botID := c.findBotByQQNumber(msg.SelfID)
		if botID != "" {
			// 使用前端 Bot 聊天的 ChatID 格式: bot-{botUUID}
			// 这样消息会出现在已有的 Bot 聊天页面中，而不是创建新对话
			chatID = fmt.Sprintf("bot-%s", botID)
			// 私聊发给 Bot 小号时，自动 @Bot 触发 Bot 回复
			if !msg.IsSent {
				mentionBotIDs = append(mentionBotIDs, "bot_"+botID)
			}
		} else {
			chatID = fmt.Sprintf("private_%s_self", senderID)
		}
	case "group":
		chatType = types.ChatTypeGroup
		chatID = fmt.Sprintf("%s%d", groupPrefix, msg.GroupID)
	default:
		chatType = types.ChatTypePrivate
		chatID = fmt.Sprintf("private_%s_self", senderID)
	}

	// 提取文本内容和消息类型
	content, messageType := c.extractContent(msg)

	metadata := map[string]string{
		"source":          "qq",
		"qq_message_id":   strconv.FormatInt(msg.MessageID, 10),
		"qq_user_id":      strconv.FormatInt(msg.UserID, 10),
		"qq_self_id":      strconv.FormatInt(msg.SelfID, 10),
		"qq_message_type": msg.MessageType,
	}
	if msg.IsSent {
		metadata["qq_is_sent"] = "true"
	}
	if msg.GroupID != 0 {
		metadata["qq_group_id"] = strconv.FormatInt(msg.GroupID, 10)
	}
	if !msg.IsSent {
		logosUserID := c.findLogosUserByQQ(msg.UserID)
		if logosUserID != "" {
			metadata["qq_bound_user"] = "true"
		}
	}

	event := &types.MessageEvent{
		ID:             uuid.NewString(),
		EventType:      types.EventTypeMessage,
		ChatID:         chatID,
		ChatType:       chatType,
		SenderID:       senderID,
		SenderName:     senderName,
		MessageType:    messageType,
		Content:        content,
		Metadata:       metadata,
		Timestamp:      time.Now(),
		MentionUserIDs: mentionBotIDs,
		Channel:        "qq",
	}

	return event, nil
}

// extractContent 从 OneBot 消息段中提取内容和消息类型
func (c *MessageConverter) extractContent(msg *InboundMessage) (string, types.MessageType) {
	var textParts []string
	hasImage := false
	hasFile := false
	hasRecord := false
	hasVideo := false

	for _, seg := range msg.Message {
		switch seg.Type {
		case "text":
			textParts = append(textParts, seg.Data.Text)
		case "image":
			hasImage = true
			textParts = append(textParts, "[图片]")
		case "file":
			hasFile = true
			textParts = append(textParts, "[文件]")
		case "record":
			hasRecord = true
			textParts = append(textParts, "[语音]")
		case "video":
			hasVideo = true
			textParts = append(textParts, "[视频]")
		case "at":
			qqPrefix := c.getQQUserPrefix()
			textParts = append(textParts, fmt.Sprintf("@%s%d", qqPrefix, seg.Data.ID))
		case "face":
			textParts = append(textParts, "[表情]")
		case "reply":
			// 引用回复，记录但不改变内容
		}
	}

	content := strings.Join(textParts, "")

	messageType := types.MessageTypeText
	switch {
	case hasVideo:
		messageType = types.MessageTypeVideo
	case hasRecord:
		messageType = types.MessageTypeVoice
	case hasFile:
		messageType = types.MessageTypeFile
	case hasImage:
		messageType = types.MessageTypeImage
	}

	return content, messageType
}

// findBotByQQNumber 根据 QQ 号查找绑定的 Bot ID
// 先查 Redis 缓存，缓存未命中时需要外部设置
func (c *MessageConverter) findBotByQQNumber(qqNumber int64) string {
	qqStr := strconv.FormatInt(qqNumber, 10)
	key := fmt.Sprintf("qq:bot_bind:%s", qqStr)

	redis := cache.NewRedisCache()
	botID, err := redis.Get(context.Background(), key)
	if err != nil {
		logger.Debug("查询QQ-Bot绑定缓存失败",
			logger.StringField("qq_number", qqStr),
			logger.ErrorField(err))
		return ""
	}
	if botID == "" {
		return ""
	}
	return botID
}

// findLogosUserByQQ 根据 QQ 号查找绑定的 Logos UserID
// 这样 QQ 消息会以 Logos 用户身份出现在聊天页面
func (c *MessageConverter) findLogosUserByQQ(qqUserID int64) string {
	qqStr := strconv.FormatInt(qqUserID, 10)
	key := fmt.Sprintf("qq:user_bind:%s", qqStr)

	redis := cache.NewRedisCache()
	logosUserID, err := redis.Get(context.Background(), key)
	if err != nil {
		logger.Debug("查询QQ-User绑定缓存失败",
			logger.StringField("qq_user_id", qqStr),
			logger.ErrorField(err))
		return ""
	}
	if logosUserID == "" {
		return ""
	}
	return logosUserID
}

func (c *MessageConverter) getQQUserPrefix() string {
	if c.cfg.QQBridge.QQUserPrefix != "" {
		return c.cfg.QQBridge.QQUserPrefix
	}
	return "qq_"
}

func (c *MessageConverter) getQQGroupPrefix() string {
	if c.cfg.QQBridge.QQGroupPrefix != "" {
		return c.cfg.QQBridge.QQGroupPrefix
	}
	return "qqgroup_"
}
