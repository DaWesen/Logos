package qqbridge

import (
	"context"
	"fmt"
	"strconv"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/logger"

	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

// ZeroBotPlugin ZeroBot 插件，桥接 ZeroBot 与 Logos EventBus
type ZeroBotPlugin struct {
	bridge    *Bridge
	converter *MessageConverter
	cfg       *config.Config
}

// NewZeroBotPlugin 创建 ZeroBot 插件
func NewZeroBotPlugin(bridge *Bridge, cfg *config.Config) *ZeroBotPlugin {
	return &ZeroBotPlugin{
		bridge:    bridge,
		converter: NewMessageConverter(cfg),
		cfg:       cfg,
	}
}

// Register 注册 ZeroBot 插件
func (p *ZeroBotPlugin) Register() {
	// 处理收到的私聊消息（别人发给小号的）
	// 注意：ZeroBot 将 message_sent 和 message 统一作为 "message" 类型处理
	// 所以 OnMessage 会同时收到别人发来的和小号自己发出的消息
	zero.OnMessage(zero.OnlyPrivate).SetBlock(false).Handle(p.handlePrivateMessage)

	// 处理收到的群聊消息
	zero.OnMessage(zero.OnlyGroup).SetBlock(false).Handle(p.handleGroupMessage)

	logger.Info("ZeroBot QQ Bridge 插件已注册")
}

// handlePrivateMessage 处理私聊消息 (QQ → Logos)
// ZeroBot 将 message 和 message_sent 统一走 OnMessage 匹配
// 通过 UserID == SelfID 判断是否为小号自己发出的消息
func (p *ZeroBotPlugin) handlePrivateMessage(ctx *zero.Ctx) {
	isSent := ctx.Event.UserID == ctx.Event.SelfID

	if isSent {
		// 小号自己发出的消息（message_sent 事件）
		msg := p.convertSentMessageEvent(ctx)
		logger.Info("收到QQ小号发出消息",
			logger.StringField("target_id", strconv.FormatInt(msg.UserID, 10)),
			logger.StringField("content", msg.RawMessage))

		if err := p.bridge.HandleInboundMessage(context.Background(), msg); err != nil {
			logger.Error("处理QQ小号发出消息失败", logger.ErrorField(err))
		}
	} else {
		// 别人发给小号的消息
		msg := p.convertZeroBotEvent(ctx)
		logger.Info("收到QQ私聊消息",
			logger.StringField("user_id", strconv.FormatInt(msg.UserID, 10)),
			logger.StringField("content", msg.RawMessage))

		if err := p.bridge.HandleInboundMessage(context.Background(), msg); err != nil {
			logger.Error("处理QQ私聊消息失败", logger.ErrorField(err))
		}
	}
}

// handleGroupMessage 处理群聊消息 (QQ → Logos)
func (p *ZeroBotPlugin) handleGroupMessage(ctx *zero.Ctx) {
	msg := p.convertZeroBotEvent(ctx)

	logger.Info("收到QQ群聊消息",
		logger.StringField("group_id", strconv.FormatInt(msg.GroupID, 10)),
		logger.StringField("user_id", strconv.FormatInt(msg.UserID, 10)),
		logger.StringField("content", msg.RawMessage))

	if err := p.bridge.HandleInboundMessage(context.Background(), msg); err != nil {
		logger.Error("处理QQ群聊消息失败", logger.ErrorField(err))
	}
}

// convertSentMessageEvent 将小号发出消息的事件转换为 InboundMessage
// message_sent 事件中：UserID == SelfID（小号自己），TargetID 是对方QQ号
func (p *ZeroBotPlugin) convertSentMessageEvent(ctx *zero.Ctx) *InboundMessage {
	msg := &InboundMessage{
		MessageType: "private",
		UserID:      ctx.Event.TargetID, // 对方的QQ号
		SelfID:      ctx.Event.SelfID,   // 小号的QQ号
		RawMessage:  ctx.Event.RawMessage,
		IsSent:      true, // 标记这是小号发出的消息
	}

	if mid, ok := ctx.Event.MessageID.(int64); ok {
		msg.MessageID = mid
	}

	// Sender 是小号自己
	msg.Sender.UserID = ctx.Event.SelfID
	msg.Sender.Nickname = "Bot"

	if ctx.Event.GroupID != 0 {
		msg.MessageType = "group"
		msg.GroupID = ctx.Event.GroupID
	}

	msg.Message = p.convertMessageSegments(ctx.Event.Message)

	return msg
}

// convertZeroBotEvent 将 ZeroBot 上下文转换为 InboundMessage
func (p *ZeroBotPlugin) convertZeroBotEvent(ctx *zero.Ctx) *InboundMessage {
	msg := &InboundMessage{
		MessageType: "private",
		UserID:      ctx.Event.UserID,
		SelfID:      ctx.Event.SelfID,
		RawMessage:  ctx.Event.RawMessage,
	}

	// MessageID 是 interface{}，QQ 消息为 int64
	if mid, ok := ctx.Event.MessageID.(int64); ok {
		msg.MessageID = mid
	}

	if ctx.Event.Sender != nil {
		msg.Sender.UserID = ctx.Event.Sender.ID
		msg.Sender.Nickname = ctx.Event.Sender.NickName
		msg.Sender.Card = ctx.Event.Sender.Card
	}

	if ctx.Event.GroupID != 0 {
		msg.MessageType = "group"
		msg.GroupID = ctx.Event.GroupID
	}

	// 转换消息段
	msg.Message = p.convertMessageSegments(ctx.Event.Message)

	return msg
}

// convertMessageSegments 转换 ZeroBot 消息段
func (p *ZeroBotPlugin) convertMessageSegments(msg message.Message) []OneBotSegment {
	var segments []OneBotSegment

	for _, seg := range msg {
		obSeg := OneBotSegment{
			Type: seg.Type,
		}
		for k, v := range seg.Data {
			switch k {
			case "text":
				obSeg.Data.Text = v
			case "url":
				obSeg.Data.URL = v
			case "file":
				obSeg.Data.File = v
			case "id":
				if id, err := strconv.ParseInt(v, 10, 64); err == nil {
					obSeg.Data.ID = id
				}
			case "qq":
				if id, err := strconv.ParseInt(v, 10, 64); err == nil {
					obSeg.Data.ID = id
				}
			}
		}
		segments = append(segments, obSeg)
	}

	return segments
}

// getCaller 获取 ZeroBot APICaller 实例
// ZeroBot 的 APICallers 全局变量存储了所有已连接的 APICaller（按 selfID 索引）
// 出站消息必须通过 APICaller 发送，否则无法调用 OneBot API
func (p *ZeroBotPlugin) getCaller(selfID int64) zero.APICaller {
	if selfID != 0 {
		if caller, ok := zero.APICallers.Load(selfID); ok {
			return caller
		}
	}
	// 回退：取第一个可用的 caller
	var fallback zero.APICaller
	zero.APICallers.Range(func(key int64, value zero.APICaller) bool {
		fallback = value
		return false // 只取第一个
	})
	return fallback
}

// SendPrivateMessage 通过 ZeroBot 发送私聊消息 (Logos → QQ)
func (p *ZeroBotPlugin) SendPrivateMessage(ctx context.Context, selfID, qqUserID int64, msg message.Message) error {
	caller := p.getCaller(selfID)
	if caller == nil {
		return fmt.Errorf("无可用的ZeroBot APICaller，QQ连接可能未建立")
	}

	c, cancel := context.WithTimeout(ctx, zero.BotConfig.MaxProcessTime)
	defer cancel()

	_, err := caller.CallAPI(c, zero.APIRequest{
		Action: "send_private_msg",
		Params: zero.Params{
			"user_id": qqUserID,
			"message": msg,
		},
	})
	if err != nil {
		return fmt.Errorf("发送QQ私聊消息失败: %w", err)
	}

	logger.Info("已通过ZeroBot发送私聊消息",
		logger.StringField("qq_user_id", strconv.FormatInt(qqUserID, 10)))
	return nil
}

// SendGroupMessage 通过 ZeroBot 发送群聊消息 (Logos → QQ)
func (p *ZeroBotPlugin) SendGroupMessage(ctx context.Context, selfID, qqGroupID int64, msg message.Message) error {
	caller := p.getCaller(selfID)
	if caller == nil {
		return fmt.Errorf("无可用的ZeroBot APICaller，QQ连接可能未建立")
	}

	c, cancel := context.WithTimeout(ctx, zero.BotConfig.MaxProcessTime)
	defer cancel()

	_, err := caller.CallAPI(c, zero.APIRequest{
		Action: "send_group_msg",
		Params: zero.Params{
			"group_id": qqGroupID,
			"message":  msg,
		},
	})
	if err != nil {
		return fmt.Errorf("发送QQ群聊消息失败: %w", err)
	}

	logger.Info("已通过ZeroBot发送群聊消息",
		logger.StringField("qq_group_id", strconv.FormatInt(qqGroupID, 10)))
	return nil
}

// LogosEventToQQMessage 将 Logos MessageEvent 转换为 ZeroBot 消息段
func (p *ZeroBotPlugin) LogosEventToQQMessage(event *types.MessageEvent) message.Message {
	var msg message.Message

	// 文本内容
	if event.Content != "" {
		msg = append(msg, message.Text(event.Content))
	}

	// 媒体内容
	if event.MediaURL != "" {
		switch event.MessageType {
		case types.MessageTypeImage:
			msg = append(msg, message.Image(event.MediaURL))
		case types.MessageTypeVoice:
			msg = append(msg, message.Record(event.MediaURL))
		case types.MessageTypeVideo:
			msg = append(msg, message.Video(event.MediaURL))
		}
	}

	// 如果消息为空，至少发送文本
	if len(msg) == 0 {
		msg = append(msg, message.Text("(空消息)"))
	}

	return msg
}

// ExtractQQNumberFromLogosID 从 Logos 用户ID 中提取 QQ 号
func (p *ZeroBotPlugin) ExtractQQNumberFromLogosID(logosID string) (int64, error) {
	qqPrefix := p.cfg.QQBridge.QQUserPrefix
	if qqPrefix == "" {
		qqPrefix = "qq_"
	}
	if len(logosID) <= len(qqPrefix) || logosID[:len(qqPrefix)] != qqPrefix {
		return 0, fmt.Errorf("不是QQ用户ID: %s", logosID)
	}
	qqStr := logosID[len(qqPrefix):]
	return strconv.ParseInt(qqStr, 10, 64)
}

// ExtractQQGroupFromChatID 从 Logos ChatID 中提取 QQ 群号
func (p *ZeroBotPlugin) ExtractQQGroupFromChatID(chatID string) (int64, error) {
	grpPrefix := p.cfg.QQBridge.QQGroupPrefix
	if grpPrefix == "" {
		grpPrefix = "qqgroup_"
	}
	if len(chatID) <= len(grpPrefix) || chatID[:len(grpPrefix)] != grpPrefix {
		return 0, fmt.Errorf("不是QQ群ChatID: %s", chatID)
	}
	qqStr := chatID[len(grpPrefix):]
	return strconv.ParseInt(qqStr, 10, 64)
}

// ExtractSelfIDFromEvent 从 MessageEvent 的 Metadata 中提取 selfID
func (p *ZeroBotPlugin) ExtractSelfIDFromEvent(event *types.MessageEvent) int64 {
	if event.Metadata != nil {
		if selfIDStr, ok := event.Metadata["qq_self_id"]; ok {
			if selfID, err := strconv.ParseInt(selfIDStr, 10, 64); err == nil {
				return selfID
			}
		}
	}
	return 0
}
