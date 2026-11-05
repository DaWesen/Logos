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
	"Logos/pkg/obs"

	"github.com/google/uuid"
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
	fastSlots  chan struct{}
	// fastReply 可在测试中替换；默认走真实的 Bot 调用。
	// 返回值决定是否需要发布补偿事件。
	fastReply func(*types.MessageEvent, *InboundMessage) fastReplyOutcome
	// publishRecovery 可在测试中替换；默认发 Kafka 补偿事件。
	publishRecovery func(*types.MessageEvent, fastReplyOutcome)
}

// fastReplyOutcome 描述快速路径的处理结果。
//
// 只有「值得再试一次」的失败才交回慢路径：没找到 Bot 绑定这类错误
// 再跑一次结果也一样，补偿只会制造重复调用。
type fastReplyOutcome string

const (
	fastReplyOK           fastReplyOutcome = ""
	fastReplyPanic        fastReplyOutcome = "panic"
	fastReplyNoBinding    fastReplyOutcome = "no_binding"
	fastReplyBotFailed    fastReplyOutcome = "bot_call_failed"
	fastReplyEmptyContent fastReplyOutcome = "empty_response"
	// fastReplyUndeliverable 表示 Bot 已经回复，但直发 QQ 和写回 Kafka 都失败，
	// 用户侧和前端都看不到这条回复。
	fastReplyUndeliverable fastReplyOutcome = "reply_undeliverable"
)

// compensable 表示这条失败值得让慢路径再试一次。
func (o fastReplyOutcome) compensable() bool {
	switch o {
	case fastReplyPanic, fastReplyBotFailed, fastReplyEmptyContent, fastReplyUndeliverable:
		return true
	default:
		return false
	}
}

// NewBridge 创建 QQ Bridge 实例
func NewBridge(cfg *config.Config) *Bridge {
	b := &Bridge{
		converter: NewMessageConverter(cfg),
		mapper:    NewMapper(cfg),
		cfg:       cfg,
		// 快速路径每条消息都会调一次 LLM，必须限并发：
		// 否则用户连续发消息会瞬间打满 Bot 服务与模型配额。
		fastSlots: make(chan struct{}, fastConcurrency(cfg)),
	}
	b.fastReply = b.fastBotReply
	b.publishRecovery = b.publishFastRecovery
	b.mediaRelay = NewMediaRelay(cfg)
	b.plugin = NewZeroBotPlugin(b, cfg)
	return b
}

// SetBotClient 设置 Bot gRPC 客户端（由 main.go 注入）
func (b *Bridge) SetBotClient(botClient *client.BotClient) {
	b.botClient = botClient
}

// SetBindingLookup 注入缓存未命中时的 QQ→Bot 回源。
func (b *Bridge) SetBindingLookup(lookup BindingLookup) {
	b.converter.SetBindingLookup(lookup)
	b.plugin.converter.SetBindingLookup(lookup)
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

	// 进程被 kill/OOM 时 defer 不会执行，启动时先扫一遍上次遗留的临时文件；
	// 之后周期清理，避免长期运行时零星的 Cleanup 失败慢慢占满磁盘。
	go b.runMediaJanitor(bridgeCtx)

	b.running = true
	logger.Info("QQ Bridge 已启动（双向同步模式：QQ ↔ Logos）")

	return nil
}

// runMediaJanitor 定期清理超过 1 小时的 QQ 媒体临时文件。
func (b *Bridge) runMediaJanitor(ctx context.Context) {
	const interval = 6 * time.Hour
	const maxAge = time.Hour

	if _, err := b.mediaRelay.SweepTempDir(ctx, maxAge); err != nil && ctx.Err() == nil {
		logger.Warn("QQ媒体临时目录启动清理失败", logger.ErrorField(err))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := b.mediaRelay.SweepTempDir(ctx, maxAge); err != nil && ctx.Err() == nil {
				logger.Warn("QQ媒体临时目录周期清理失败", logger.ErrorField(err))
			}
		}
	}
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
			if fast, ok := event.Metadata[types.MetaQQFastReplied]; ok && fast == "true" {
				return nil
			}
		}

		// 只转发 Channel 为 "qq" 的消息（即该会话来源于 QQ）
		if event.Channel != "qq" {
			return nil
		}

		// 只转发 Bot 发送的消息（Bot 回复），Bot 消息的 SenderID 统一以 "bot_" 为前缀
		if !strings.HasPrefix(event.SenderID, "bot_") {
			return nil
		}

		// 发送失败必须把 error 抛给消费者：返回 nil 会提交 offset，
		// 这条 Bot 回复就永久丢了。不可恢复的错误已在内部显式标记并跳过。
		return b.handleOutboundMessage(ctx, &event)
	})

	if err := eventBus.SubscribeChatOutgoing(ctx, handler, "qq-bridge-outgoing-consumer"); err != nil {
		logger.Error("订阅 chat_outgoing 失败", logger.ErrorField(err))
	}
}

// handleOutboundMessage 处理出站消息 (Logos → QQ)。
// 返回可重试错误时由 Kafka 消费者就地重试，超过上限后转死信队列。
func (b *Bridge) handleOutboundMessage(ctx context.Context, event *types.MessageEvent) error {
	// 从 Metadata 中获取目标 QQ 号
	targetQQ := parseQQID(event.Metadata[types.MetaQQUserID])

	if targetQQ == 0 {
		logger.Warn("出站消息缺少目标QQ号，跳过",
			logger.StringField("chat_id", event.ChatID))
		obs.RecordQQOutbound(ctx, "skipped", "missing_target")
		return fmt.Errorf("%w: 缺少目标QQ号", mq.ErrSkipMessage)
	}

	// 通过 ZeroBot API 发送消息
	selfID := b.getSelfID(event)
	if selfID == 0 {
		logger.Warn("无法确定 Bot 的 QQ 号，跳过发送",
			logger.StringField("chat_id", event.ChatID))
		obs.RecordQQOutbound(ctx, "skipped", "missing_self_id")
		return fmt.Errorf("%w: 缺少 Bot QQ 号", mq.ErrSkipMessage)
	}

	// 统一走插件转换：它负责把 MediaURL 变成 QQ 可访问的绝对地址，
	// 并生成 text/image/record/video 多段消息。直接拼纯文本会把媒体丢掉。
	// 文本和媒体都为空时不发送：转换函数会兜底成「(空消息)」，
	// 那是给内部调试用的占位，发到 QQ 只会变成噪音。
	if event.Content == "" && event.MediaURL == "" {
		obs.RecordQQOutbound(ctx, "skipped", "empty_message")
		return fmt.Errorf("%w: 消息内容为空", mq.ErrSkipMessage)
	}
	qqMessage := b.plugin.LogosEventToQQMessage(event)
	if len(qqMessage) == 0 {
		obs.RecordQQOutbound(ctx, "skipped", "empty_message")
		return fmt.Errorf("%w: 消息内容为空", mq.ErrSkipMessage)
	}

	// 根据会话类型决定发送方式
	switch event.ChatType {
	case types.ChatTypePrivate:
		if err := b.plugin.SendPrivateMessage(ctx, selfID, targetQQ, qqMessage); err != nil {
			logger.Error("发送QQ私聊消息失败", logger.ErrorField(err))
			obs.RecordQQOutbound(ctx, "failed", "send_private")
			return fmt.Errorf("发送QQ私聊消息失败: %w", err)
		}
		obs.RecordQQOutbound(ctx, "ok", "private")
		logger.Info("Bot回复已发送到QQ",
			logger.IntField("target_qq", int(targetQQ)),
			logger.StringField("content", truncate(event.Content, 50)))

	case types.ChatTypeGroup:
		groupID := parseQQID(event.Metadata[types.MetaQQGroupID])
		if groupID == 0 {
			obs.RecordQQOutbound(ctx, "skipped", "missing_group_id")
			return fmt.Errorf("%w: 缺少目标QQ群号", mq.ErrSkipMessage)
		}
		if err := b.plugin.SendGroupMessage(ctx, selfID, groupID, qqMessage); err != nil {
			logger.Error("发送QQ群聊消息失败", logger.ErrorField(err))
			obs.RecordQQOutbound(ctx, "failed", "send_group")
			return fmt.Errorf("发送QQ群聊消息失败: %w", err)
		}
		obs.RecordQQOutbound(ctx, "ok", "group")
		logger.Info("Bot回复已发送到QQ群",
			logger.IntField("target_group", int(groupID)),
			logger.StringField("content", truncate(event.Content, 50)))

	default:
		obs.RecordQQOutbound(ctx, "skipped", "unsupported_chat_type")
		return fmt.Errorf("%w: 不支持的会话类型 %d", mq.ErrSkipMessage, event.ChatType)
	}
	return nil
}

// getSelfID 从事件中获取 Bot 的 QQ 号
func (b *Bridge) getSelfID(event *types.MessageEvent) int64 {
	if event.Metadata != nil {
		return parseQQID(event.Metadata[types.MetaQQSelfID])
	}
	return 0
}

// parseQQID 把 Metadata 里的 QQ 号字符串转成 int64。
//
// 旧写法是 `groupID, _ := strconv.ParseInt(...)` 再判 `== 0`：解析失败、
// 没填、以及字面量 "0" 被压成同一个零值，三类完全不同的输入在日志和
// 监控里都只表现为 missing_group_id。更关键的是负数没有被拦——"‑123"
// 会解析成功并通过 `== 0` 检查，直接被下发到 ZeroBot 插件。
//
// QQ 号是正整数，因此这里把「解析失败」和「非正数」统一收敛成 0（调用方
// 按「不可恢复、跳过」处理）：解析失败重试多少次都不会变好，把它留在
// 可重试路径只会让毒消息反复占用消费位。
func parseQQID(raw string) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
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

	// 媒体中转失败只降级为文本占位，不能让整条消息丢失。
	if b.mediaRelay != nil {
		mediaCtx, cancel := context.WithTimeout(ctx, mediaTimeout(b.cfg))
		if err := b.mediaRelay.RelayInboundMedia(mediaCtx, event, msg); err != nil {
			logger.Warn("QQ媒体中转失败，保留文本占位",
				logger.StringField("chat_id", event.ChatID),
				logger.StringField("message_type", event.MessageType.String()),
				logger.ErrorField(err))
		}
		cancel()
	}

	// 顺序很重要：
	// 1) 先预留槽位（失败就不写标记，交给慢路径）；
	// 2) 写 qq_direct_bot，让标记随事件一起进 Kafka；
	// 3) 发布事件；发布失败则释放槽位并撤销标记（事件本身也不会被消费）；
	// 4) 发布成功后才真正启动 Bot 调用，避免「QQ 已回复但前端查不到消息」。
	fastReserved := b.canDispatchFastReply(msg) && b.reserveFastReply(ctx)
	if fastReserved {
		if event.Metadata == nil {
			event.Metadata = make(map[string]string)
		}
		event.Metadata[types.MetaQQDirectBot] = "true"
	}

	if err := b.publishEvent(ctx, event); err != nil {
		logger.Error("发布入站消息到Kafka失败", logger.ErrorField(err))
		if fastReserved {
			b.releaseFastSlot()
		}
		// 发布失败：事件没有进 Kafka，慢路径不会处理；也不要启动快速路径，
		// 让 ZeroBot 层记录失败，避免产生「已回复但无记录」的孤儿消息。
		return err
	}

	// 发布成功后再真正派发快速路径；槽位已在发布前预留，这里只负责提交。
	if fastReserved {
		b.commitFastReply(event, msg)
	}

	logger.Info("入站消息已发布",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("sender_id", event.SenderID),
		logger.BoolField("is_sent", msg.IsSent),
		logger.StringField("channel", "qq"))

	return nil
}

// canDispatchFastReply 判断这条消息是否有资格走快速路径。
// 只做资格判断，不占槽位；槽位在发布前单独预留。
func (b *Bridge) canDispatchFastReply(msg *InboundMessage) bool {
	return msg != nil && !msg.IsSent && msg.MessageType == "private" && b.botClient != nil
}

// reserveFastReply 预留一个并发槽位；失败时调用方必须回退 qq_direct_bot 标记，
// 让 Chat Service 走慢路径。
func (b *Bridge) reserveFastReply(ctx context.Context) bool {
	select {
	case b.fastSlots <- struct{}{}:
		return true
	default:
		logger.Debug("快速路径繁忙，交由慢路径处理")
		// 槽位打满意味着「快速路径其实没跑，全部退回 Kafka 往返」。
		// 这是容量信号，必须能从指标看到，否则只能靠用户抱怨变慢。
		obs.RecordQQFastPath(ctx, "busy", "slots_full")
		return false
	}
}

// releaseFastSlot 归还一个已预留但未提交的槽位。
// 只由「预留成功但事件发布失败」的调用方使用；commitFastReply 的 goroutine
// 退出时自己归还，不能重复调用，否则会把别的请求占用的槽位误释放。
func (b *Bridge) releaseFastSlot() {
	if b.fastSlots == nil {
		return
	}
	select {
	case <-b.fastSlots:
	default:
	}
}

// commitFastReply 在槽位已预留、事件已发布后启动异步 Bot 调用。
func (b *Bridge) commitFastReply(event *types.MessageEvent, msg *InboundMessage) {
	// event 会被异步读取；这里复制一份，避免与调用方后续修改共享 map。
	snapshot := cloneEvent(event)
	go func() {
		outcome := fastReplyPanic
		// 快速路径是后台任务：一个 panic 不能让整个 Bridge 进程退出。
		// 记录后仍然走补偿，让慢路径兜底。
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("快速路径 panic",
						logger.StringField("chat_id", event.ChatID),
						logger.AnyField("panic", r))
				}
			}()
			outcome = b.fastReply(snapshot, msg)
		}()

		// 先归还槽位再发补偿：补偿要走 Kafka，抖动时最长 5s。
		// 如果还握着槽位发补偿，Kafka 一慢就会把快速路径容量一起拖住，
		// 把「一次 Bot 失败」放大成「四路并发全被占满」。
		b.releaseFastSlot()
		if outcome.compensable() && b.publishRecovery != nil {
			b.publishRecovery(snapshot, outcome)
		}
	}()
}

// publishFastRecovery 发布补偿事件，让 Chat Service 重新执行 Bot 调用。
//
// 为什么需要补偿：qq_direct_bot 随原事件进了 Kafka，Chat Service 已经跳过；
// 如果快速路径随后失败，没有任何一方会再回复。补偿事件换一个 event_type，
// 消费方据此只补跑 Bot，不重复落库、不重复推送。
func (b *Bridge) publishFastRecovery(event *types.MessageEvent, outcome fastReplyOutcome) {
	recovery := buildFastRecoveryEvent(event, outcome)
	if recovery == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.publishEvent(ctx, recovery); err != nil {
		logger.Error("快速路径：发布补偿事件失败，用户可能收不到回复",
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("reason", string(outcome)),
			logger.ErrorField(err))
		obs.RecordQQFastPath(ctx, "failed", string(outcome))
		return
	}

	obs.RecordQQFastPath(ctx, "recovered", string(outcome))
	logger.Warn("快速路径失败，已交回慢路径补偿",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("reason", string(outcome)))
}

// buildFastRecoveryEvent 构造补偿事件；输入不合法时返回 nil。
// 抽成纯函数，测试不需要 Kafka 就能验证字段与标记。
func buildFastRecoveryEvent(event *types.MessageEvent, outcome fastReplyOutcome) *types.MessageEvent {
	if event == nil || !outcome.compensable() {
		return nil
	}

	metadata := make(map[string]string, len(event.Metadata)+2)
	for k, v := range event.Metadata {
		metadata[k] = v
	}
	// 原事件已经带着 qq_direct_bot 进 Kafka，补偿事件必须显式撤销这个契约，
	// 否则消费方还是会认为「已经有人处理了」。
	delete(metadata, types.MetaQQDirectBot)
	metadata[types.MetaQQFastRecovery] = "true"
	metadata[types.MetaQQFastReason] = string(outcome)

	recovery := cloneEvent(event)
	recovery.ID = uuid.NewString()
	recovery.EventType = types.EventTypeBotRecovery
	recovery.Metadata = metadata
	recovery.RecipientIDs = nil
	return recovery
}

func cloneEvent(event *types.MessageEvent) *types.MessageEvent {
	if event == nil {
		return nil
	}
	cloned := *event
	if event.Metadata != nil {
		cloned.Metadata = make(map[string]string, len(event.Metadata))
		for k, v := range event.Metadata {
			cloned.Metadata[k] = v
		}
	}
	if event.MentionUserIDs != nil {
		cloned.MentionUserIDs = append([]string(nil), event.MentionUserIDs...)
	}
	if event.MediaMeta != nil {
		cloned.MediaMeta = append(json.RawMessage(nil), event.MediaMeta...)
	}
	if event.Extra != nil {
		// Extra 的值可能是 map/slice：只复制顶层 map 仍会和调用方共享
		// 嵌套对象，异步路径读取时可能与发布方并发修改产生 data race。
		cloned.Extra = cloneExtra(event.Extra)
	}
	return &cloned
}

// cloneExtra 递归复制 JSON 风格的 map/slice。
// 其他类型按值复制：Extra 来自 JSON 反序列化，只可能出现
// map[string]interface{}、[]interface{} 和标量。
func cloneExtra(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = cloneExtraValue(v)
	}
	return dst
}

func cloneExtraValue(v interface{}) interface{} {
	switch typed := v.(type) {
	case map[string]interface{}:
		return cloneExtra(typed)
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = cloneExtraValue(item)
		}
		return out
	default:
		return typed
	}
}

// fastBotReply 快速路径：直接调用 Bot 服务，回复通过 ZeroBot 发回 QQ
// 同时把 Bot 回复也发布到 Kafka，让前端能看到
func (b *Bridge) fastBotReply(event *types.MessageEvent, msg *InboundMessage) fastReplyOutcome {
	botID := b.converter.findBotByQQNumber(msg.SelfID)
	if botID == "" {
		logger.Debug("快速路径跳过：未找到绑定的Bot",
			logger.IntField("self_qq", int(msg.SelfID)))
		return fastReplyNoBinding
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
		return fastReplyBotFailed
	}

	content := result.Content
	if content == "" {
		logger.Warn("快速路径：Bot返回空响应", logger.StringField("bot_id", botID))
		return fastReplyEmptyContent
	}

	// 1. 通过 ZeroBot API 直接发回 QQ（快速路径核心）
	selfID := msg.SelfID
	replyEvent := &types.MessageEvent{
		ChatID:      event.ChatID,
		ChatType:    event.ChatType,
		SenderID:    "bot_" + botID,
		MessageType: types.MessageTypeText,
		Content:     content,
	}
	repliedDirectly := false
	if err := b.plugin.SendPrivateMessage(ctx, selfID, msg.UserID, b.plugin.LogosEventToQQMessage(replyEvent)); err != nil {
		logger.Warn("快速路径：直发QQ失败，交给出站订阅兜底",
			logger.IntField("target_qq", int(msg.UserID)),
			logger.ErrorField(err))
	} else {
		repliedDirectly = true
		logger.Info("快速路径：Bot回复已发送到QQ",
			logger.IntField("target_qq", int(msg.UserID)),
			logger.StringField("content", truncate(content, 50)))
	}

	// 2. 发布 Bot 回复到 Kafka，让前端也能看到
	// 标记已通过快速路径回复，防止 subscribeOutgoing 重复发送到 QQ
	botMetadata := make(map[string]string)
	for k, v := range event.Metadata {
		botMetadata[k] = v
	}
	// 只有直发成功才能标记「已回复」；否则出站订阅会跳过这条消息，
	// QQ 侧就永远收不到回复了。
	if repliedDirectly {
		botMetadata[types.MetaQQFastReplied] = "true"
	}

	// 与 Chat Service 的慢路径使用同一派生规则：快速路径已经发过 QQ、
	// 补偿事件又让慢路径再调一次 Bot 时，两条回复会落到同一个主键上。
	replyID := types.BotReplyMessageID(botID, event.ID)
	if replyID == "" {
		replyID = "bot_" + uuid.New().String()
	}

	botResponseEvent := &types.MessageEvent{
		ID:             replyID,
		RequestID:      replyID,
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
		if !repliedDirectly {
			// 直发失败 + 回写失败：用户和前端都看不到回复，交回慢路径。
			return fastReplyUndeliverable
		}
	} else {
		logger.Info("快速路径：Bot回复已发布到Kafka",
			logger.StringField("bot_id", botID),
			logger.StringField("chat_id", event.ChatID))
	}

	return fastReplyOK
}

// publishEvent 发布事件到 Kafka EventBus
func (b *Bridge) publishEvent(ctx context.Context, event *types.MessageEvent) error {
	eventBus := types.GetEventBus()
	if eventBus == nil {
		return fmt.Errorf("EventBus 未初始化")
	}

	return eventBus.PublishMessageEvent(ctx, event)
}
