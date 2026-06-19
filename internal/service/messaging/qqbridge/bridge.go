package qqbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/client"
	"Logos/pkg/logger"
	"Logos/pkg/mq"

	"github.com/google/uuid"
	zero "github.com/wdvxdr1123/ZeroBot"
)

// Bridge QQ Bridge 核心结构
type Bridge struct {
	converter  *MessageConverter
	mapper     *Mapper
	mediaRelay *MediaRelay
	plugin     *ZeroBotPlugin
	cfg        *config.Config
	botClient  *client.BotClient // Bot gRPC 客户端，用于快速路径直接调用

	mu         sync.RWMutex
	running    bool
	cancelFunc context.CancelFunc
}

// NewBridge 创建 QQ Bridge 实例
func NewBridge(cfg *config.Config) *Bridge {
	b := &Bridge{
		converter: NewMessageConverter(cfg),
		mapper:    NewMapper(cfg),
		cfg:       cfg,
	}
	b.mediaRelay = NewMediaRelay(cfg)
	b.plugin = NewZeroBotPlugin(b, cfg)
	return b
}

// SetBotClient 设置 Bot gRPC 客户端（由 main.go 注入）
func (b *Bridge) SetBotClient(botClient *client.BotClient) {
	b.botClient = botClient
}

// Start 启动 Bridge
func (b *Bridge) Start(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		logger.Warn("QQ Bridge 已在运行")
		return nil
	}

	bridgeCtx, cancel := context.WithCancel(ctx)
	b.cancelFunc = cancel

	// 订阅 chat_outgoing，转发 Bot 回复到 QQ
	go b.subscribeOutgoing(bridgeCtx)

	b.running = true
	logger.Info("QQ Bridge 已启动（双向同步模式：QQ ↔ Logos）")

	return nil
}

// subscribeOutgoing 订阅 Kafka chat_outgoing，将 Bot 回复转发到 QQ
func (b *Bridge) subscribeOutgoing(ctx context.Context) {
	eventBus := types.GetEventBus()
	if eventBus == nil {
		logger.Error("EventBus 未初始化，无法订阅 chat_outgoing")
		return
	}

	handler := mq.MessageHandler(func(msg *mq.Message) error {
		var event types.MessageEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			return err
		}

		// 跳过从 QQ 来的用户消息（防回环），但允许 Bot 回复回 QQ
		if event.Metadata != nil {
			if source, ok := event.Metadata["source"]; ok && source == "qq" && !strings.HasPrefix(event.SenderID, "bot_") {
				return nil
			}
			// 跳过已通过快速路径回复的消息，避免重复发送
			if fast, ok := event.Metadata["qq_fast_replied"]; ok && fast == "true" {
				return nil
			}
		}

		// 只转发 Channel 为 "qq" 的消息（即该会话来源于 QQ）
		if event.Channel != "qq" {
			return nil
		}

		// 只转发 Bot 发送的消息（Bot 回复）
		if !strings.HasPrefix(event.SenderID, "bot_") && !isBotSender(event.SenderID) {
			return nil
		}

		b.handleOutboundMessage(ctx, &event)
		return nil
	})

	if err := eventBus.SubscribeChatOutgoing(ctx, handler, "qq-bridge-outgoing-consumer"); err != nil {
		logger.Error("订阅 chat_outgoing 失败", logger.ErrorField(err))
	}
}

// isBotSender 检查是否是 Bot 发送者
func isBotSender(senderID string) bool {
	// 检查 Redis 缓存中是否有 qq:bot_bind 反向映射
	// 这里简单判断：如果 senderID 是 UUID 格式且在 bot 表中存在
	// 实际由 converter 的 findBotByQQNumber 维护
	return strings.HasPrefix(senderID, "bot_")
}

// handleOutboundMessage 处理出站消息 (Logos → QQ)
func (b *Bridge) handleOutboundMessage(ctx context.Context, event *types.MessageEvent) {
	// 从 Metadata 中获取目标 QQ 号
	var targetQQ int64
	if event.Metadata != nil {
		if qqUserID, ok := event.Metadata["qq_user_id"]; ok {
			if id, err := strconv.ParseInt(qqUserID, 10, 64); err == nil {
				targetQQ = id
			}
		}
	}

	if targetQQ == 0 {
		logger.Warn("出站消息缺少目标QQ号，跳过",
			logger.StringField("chat_id", event.ChatID))
		return
	}

	// 通过 ZeroBot API 发送消息
	selfID := b.getSelfID(event)
	if selfID == 0 {
		logger.Warn("无法确定 Bot 的 QQ 号，跳转发送")
		return
	}

	caller, ok := zero.APICallers.Load(selfID)
	if !ok {
		logger.Warn("未找到 Bot 的 API Caller",
			logger.IntField("self_id", int(selfID)))
		return
	}

	content := event.Content
	if content == "" {
		return
	}

	// 根据会话类型决定发送方式
	switch event.ChatType {
	case types.ChatTypePrivate:
		_, err := caller.CallAPI(context.Background(), zero.APIRequest{
			Action: "send_private_msg",
			Params: zero.Params{
				"user_id": targetQQ,
				"message": []map[string]interface{}{
					{"type": "text", "data": map[string]string{"text": content}},
				},
			},
		})
		if err != nil {
			logger.Error("发送QQ私聊消息失败", logger.ErrorField(err))
			return
		}
		logger.Info("Bot回复已发送到QQ",
			logger.IntField("target_qq", int(targetQQ)),
			logger.StringField("content", truncate(content, 50)))

	case types.ChatTypeGroup:
		groupID, _ := strconv.ParseInt(event.Metadata["qq_group_id"], 10, 64)
		if groupID == 0 {
			return
		}
		_, err := caller.CallAPI(context.Background(), zero.APIRequest{
			Action: "send_group_msg",
			Params: zero.Params{
				"group_id": groupID,
				"message": []map[string]interface{}{
					{"type": "text", "data": map[string]string{"text": content}},
				},
			},
		})
		if err != nil {
			logger.Error("发送QQ群聊消息失败", logger.ErrorField(err))
			return
		}
		logger.Info("Bot回复已发送到QQ群",
			logger.IntField("target_group", int(groupID)),
			logger.StringField("content", truncate(content, 50)))
	}
}

// getSelfID 从事件中获取 Bot 的 QQ 号
func (b *Bridge) getSelfID(event *types.MessageEvent) int64 {
	if event.Metadata != nil {
		if qqSelfID, ok := event.Metadata["qq_self_id"]; ok {
			if id, err := strconv.ParseInt(qqSelfID, 10, 64); err == nil {
				return id
			}
		}
	}
	return 0
}

// truncate 截断字符串
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Stop 停止 Bridge
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		return
	}

	if b.cancelFunc != nil {
		b.cancelFunc()
	}
	b.running = false
	logger.Info("QQ Bridge 已停止")
}

// GetPlugin 获取 ZeroBot 插件实例
func (b *Bridge) GetPlugin() *ZeroBotPlugin {
	return b.plugin
}

// HandleInboundMessage 处理入站消息 (QQ → Logos)
// 包括：别人发给小号的消息 + 小号自己发出的消息（message_sent）
func (b *Bridge) HandleInboundMessage(ctx context.Context, msg *InboundMessage) error {
	event, err := b.converter.ToMessageEvent(ctx, msg)
	if err != nil {
		logger.Error("转换入站消息失败", logger.ErrorField(err))
		return err
	}

	// 标记此消息由 QQ Bridge 直接处理 Bot 调用，Chat Service 无需再调用 Bot
	if event.Metadata == nil {
		event.Metadata = make(map[string]string)
	}
	event.Metadata["qq_direct_bot"] = "true"

	if err := b.publishEvent(ctx, event); err != nil {
		logger.Error("发布入站消息到Kafka失败", logger.ErrorField(err))
		return err
	}

	logger.Info("入站消息已发布",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("sender_id", event.SenderID),
		logger.BoolField("is_sent", msg.IsSent),
		logger.StringField("channel", "qq"))

	// 快速路径：用户发给 Bot 的私聊消息，直接调用 Bot 并回复到 QQ
	// 不走 Chat Service → Kafka → QQ Bridge 的长链路
	if !msg.IsSent && msg.MessageType == "private" && b.botClient != nil {
		go b.fastBotReply(event, msg)
	}

	return nil
}

// fastBotReply 快速路径：直接调用 Bot 服务，回复通过 ZeroBot 发回 QQ
// 同时把 Bot 回复也发布到 Kafka，让前端能看到
func (b *Bridge) fastBotReply(event *types.MessageEvent, msg *InboundMessage) {
	botID := b.converter.findBotByQQNumber(msg.SelfID)
	if botID == "" {
		logger.Debug("快速路径跳过：未找到绑定的Bot",
			logger.IntField("self_qq", int(msg.SelfID)))
		return
	}

	// 提取纯文本内容（去掉 @Bot 部分）
	query := event.Content
	for _, uid := range event.MentionUserIDs {
		query = strings.Replace(query, "@"+uid, "", 1)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		query = "你好"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	logger.Info("快速路径：直接调用Bot",
		logger.StringField("bot_id", botID),
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("query", truncate(query, 50)))

	result, err := b.botClient.SendBotMessage(ctx, botID, query, event.SenderID, event.ChatID, false, nil)
	if err != nil {
		logger.Error("快速路径：Bot调用失败",
			logger.StringField("bot_id", botID),
			logger.ErrorField(err))
		return
	}

	content := result.Content
	if content == "" {
		logger.Warn("快速路径：Bot返回空响应", logger.StringField("bot_id", botID))
		return
	}

	// 1. 通过 ZeroBot API 直接发回 QQ（快速路径核心）
	selfID := msg.SelfID
	caller, ok := zero.APICallers.Load(selfID)
	if ok {
		_, err := caller.CallAPI(context.Background(), zero.APIRequest{
			Action: "send_private_msg",
			Params: zero.Params{
				"user_id": msg.UserID,
				"message": []map[string]interface{}{
					{"type": "text", "data": map[string]string{"text": content}},
				},
			},
		})
		if err != nil {
			logger.Error("快速路径：发送QQ消息失败", logger.ErrorField(err))
		} else {
			logger.Info("快速路径：Bot回复已发送到QQ",
				logger.IntField("target_qq", int(msg.UserID)),
				logger.StringField("content", truncate(content, 50)))
		}
	} else {
		logger.Warn("快速路径：未找到ZeroBot APICaller",
			logger.IntField("self_id", int(selfID)))
	}

	// 2. 发布 Bot 回复到 Kafka，让前端也能看到
	// 标记已通过快速路径回复，防止 subscribeOutgoing 重复发送到 QQ
	botMetadata := make(map[string]string)
	for k, v := range event.Metadata {
		botMetadata[k] = v
	}
	botMetadata["qq_fast_replied"] = "true"

	botResponseEvent := &types.MessageEvent{
		ID:             "bot_" + uuid.New().String(),
		ChatID:         event.ChatID,
		ChatType:       event.ChatType,
		SenderID:       "bot_" + botID,
		MessageType:    types.MessageTypeText,
		Content:        content,
		ReplyToMessage: event.ID,
		Timestamp:      time.Now(),
		MentionUserIDs: []string{event.SenderID},
		Metadata:       botMetadata,
		Channel:        event.Channel,
	}

	if err := b.publishEvent(context.Background(), botResponseEvent); err != nil {
		logger.Error("快速路径：发布Bot回复到Kafka失败", logger.ErrorField(err))
	} else {
		logger.Info("快速路径：Bot回复已发布到Kafka",
			logger.StringField("bot_id", botID),
			logger.StringField("chat_id", event.ChatID))
	}
}

// publishEvent 发布事件到 Kafka EventBus
func (b *Bridge) publishEvent(ctx context.Context, event *types.MessageEvent) error {
	eventBus := types.GetEventBus()
	if eventBus == nil {
		return fmt.Errorf("EventBus 未初始化")
	}

	return eventBus.PublishMessageEvent(ctx, event)
}
