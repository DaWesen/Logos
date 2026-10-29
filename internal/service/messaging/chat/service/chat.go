package service

import (
	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/offline"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/client"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

func toInt64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

type ChatService interface {
	// SendMessage 发送消息；requestID 非空时按 (senderID, requestID) 幂等，
	// replayed=true 表示本次为幂等回放（消息已存在，调用方不应再次推送）。
	SendMessage(senderID, chatID string, chatType model.ChatType, msgType model.MessageType, content string, metadata map[string]string, replyToID string, mentionIDs []string, requestID string) (*model.Message, []string, bool, error)
	GetMessageHistory(userID, chatID string, chatType model.ChatType, beforeTime time.Time, limit int, afterSeq int64) ([]*model.Message, bool, error)
	SearchMessages(userID, chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error)
	MarkMessagesRead(msgIDs []string, userID, chatID string) error
	WithdrawMessage(msgID, userID string) error
	// EditMessage 编辑消息内容；expectedVersion 为客户端持有的消息版本（乐观锁），
	// 与服务端版本不一致时返回 ErrChatMessageConflict，不做覆盖写入。
	EditMessage(msgID, userID, content string, expectedVersion int64) (*model.Message, error)
	DeleteChatHistory(userID, chatID string) error
	// DeleteMessage 单条消息删除：仅对请求者本人隐藏，不影响会话内其他成员（与撤回不同）。
	DeleteMessage(userID, messageID string) error
	EnsureChatAccess(userID, chatID string, chatType model.ChatType) error

	CreateGroup(name, ownerID string, memberIDs []string, metadata map[string]string) (*model.Group, error)
	InviteGroupMember(groupID, operatorID string, userIDs []string) error
	KickGroupMember(groupID, operatorID, userID string) error
	MuteGroupMember(groupID, operatorID, userID string, muteType model.MuteType, muteUntil time.Time) error
	TransferGroupOwner(groupID, operatorID, newOwnerID string) error
	UpdateGroupAnnouncement(groupID, operatorID, announcement string) error
	UpdateGroupAvatar(groupID, operatorID, avatar string) error
	SetGroupAdmin(groupID, operatorID, userID string, isAdmin bool) error
	GetGroupMembers(groupID string, page, pageSize int) ([]*model.GroupMember, int64, error)
	GetGroupMember(groupID, userID string) (*model.GroupMember, error)
	GetGroupMemberIDs(groupID string) ([]string, error)
	GetGroup(groupID string) (*model.Group, error)
	JoinGroup(groupID, userID string) error
	LeaveGroup(groupID, userID string) error

	StartEventConsumer() error

	GetConversationList(userID string, page, pageSize int) ([]*model.ConversationItem, int64, error)
	GetUnreadCounts(userID string, chatIDs []string) (map[string]int64, error)
	GetTotalUnreadCount(userID string) (int64, error)
	GetMessageReadReceipts(userID, chatID string, messageIDs []string, includeReaders bool) ([]*model.MessageReadReceipt, error)
	SyncOfflineMessages(userID string, cursors []model.SyncCursor, limit int) ([]*model.ChatSyncResult, error)
	ForwardMessage(messageID string, targetChatIDs []string, senderID string) ([]*model.Message, error)
	DeleteChat(chatID string, userID string, chatType model.ChatType) error
}

type BotClient interface {
	SendBotMessage(ctx context.Context, botID, content, userID, chatID string, modelConfig map[string]string) (string, error)
	ResolveBotID(ctx context.Context, botName string) (string, error)
}

type ModerationClient interface {
	ModerateContent(ctx context.Context, content, contentID, contentType string) (bool, error)
}

type ContactChecker interface {
	IsFriend(ctx context.Context, userID, friendID string) (bool, error)
	IsBlocked(ctx context.Context, userID, friendID string) (bool, error)
	CheckFriendship(ctx context.Context, userID, friendID string) (*FriendshipResult, error)
}

type ChatServiceImpl struct {
	repo              dao.ChatRepository
	eventBus          *types.EventBus
	ctx               context.Context
	cancel            context.CancelFunc
	botClient         BotClient
	moderationClient  ModerationClient
	translationClient TranslationClient
	contactChecker    ContactChecker
	userClient        *client.UserClient
	enableModeration  bool
	enableTranslation bool
	defaultTargetLang string
	userNameCache     sync.Map
}

func NewChatService(repo dao.ChatRepository, eventBus *types.EventBus, ctx context.Context) ChatService {
	c, cancel := context.WithCancel(ctx)
	return &ChatServiceImpl{
		repo:     repo,
		eventBus: eventBus,
		ctx:      c,
		cancel:   cancel,
	}
}

func NewChatServiceWithAI(repo dao.ChatRepository, eventBus *types.EventBus, ctx context.Context, botClient BotClient, moderationClient ModerationClient, translationClient TranslationClient) ChatService {
	c, cancel := context.WithCancel(ctx)
	return &ChatServiceImpl{
		repo:              repo,
		eventBus:          eventBus,
		ctx:               c,
		cancel:            cancel,
		botClient:         botClient,
		moderationClient:  moderationClient,
		translationClient: translationClient,
		enableModeration:  moderationClient != nil,
		enableTranslation: translationClient != nil,
		defaultTargetLang: "zh",
	}
}

func NewChatServiceWithContact(repo dao.ChatRepository, eventBus *types.EventBus, ctx context.Context, botClient BotClient, moderationClient ModerationClient, translationClient TranslationClient, contactChecker ContactChecker, userClient *client.UserClient) ChatService {
	c, cancel := context.WithCancel(ctx)
	return &ChatServiceImpl{
		repo:              repo,
		eventBus:          eventBus,
		ctx:               c,
		cancel:            cancel,
		botClient:         botClient,
		moderationClient:  moderationClient,
		translationClient: translationClient,
		contactChecker:    contactChecker,
		userClient:        userClient,
		enableModeration:  moderationClient != nil,
		enableTranslation: translationClient != nil,
		defaultTargetLang: "zh",
	}
}

func (s *ChatServiceImpl) StartEventConsumer() error {
	logger.Info("启动聊天服务事件消费者")

	go func() {
		handler := func(msg *mq.Message) error {
			return s.HandleMessageEvent(msg)
		}
		if err := s.eventBus.SubscribeChatEvents(s.ctx, handler, "chat-service-consumer"); err != nil {
			logger.Error("订阅聊天事件失败", logger.ErrorField(err))
		}
	}()

	s.startChangeLogCleanup()

	return nil
}

func (s *ChatServiceImpl) HandleMessageEvent(msg *mq.Message) error {
	logger.Debug("收到消息事件", logger.StringField("topic", msg.Topic))

	eventType := types.DetectEventType(msg.Value)

	switch eventType {
	case types.EventTypeTyping:
		typingEvent, err := types.TypingEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析输入状态事件失败", logger.ErrorField(err))
			return err
		}
		return s.handleTypingEvent(typingEvent)

	case types.EventTypeMessageRead:
		readEvent, err := types.MessageReadEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析已读回执事件失败", logger.ErrorField(err))
			return err
		}
		return s.handleMessageReadEvent(readEvent)

	case types.EventTypeMessageWithdraw, types.EventTypeMessageDelete, types.EventTypeMessageEdit:
		// 这三类事件由本服务发布，仅用于通知客户端；服务层不再二次消费，
		// 显式忽略以避免落进 default 刷"未知事件类型"告警。
		return nil

	case types.EventTypeMessage, "":
		event, err := types.MessageEventFromJSON(msg.Value)
		if err != nil {
			logger.Error("解析消息事件失败", logger.ErrorField(err))
			return err
		}

		if len(event.RecipientIDs) > 0 {
			logger.Debug("事件已处理，跳过", logger.StringField("msg_id", event.ID))
			return nil
		}

		// 发送者必须有权向该会话发送消息，否则拒绝（失败关闭）。
		if !s.authorizeIncomingMessage(event) {
			return nil
		}

		// 收到 Kafka 消息
		if event.ChatType == types.ChatTypePrivate {
			logger.Debug("收到私聊消息",
				logger.StringField("msg_id", event.ID),
				logger.StringField("chat_id", event.ChatID),
				logger.StringField("sender_id", event.SenderID),
				logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
		}

		logger.Info("处理消息事件",
			logger.StringField("msg_id", event.ID),
			logger.StringField("chat_id", event.ChatID),
			logger.StringField("sender_id", event.SenderID),
		)

		// 1️⃣ 先获取接收者列表并直接发布 outgoing 消息！不等待任何其他操作！
		var recipientIDs []string
		switch event.ChatType {
		case types.ChatTypePrivate:
			recipientIDs = s.getPrivateChatRecipient(event.ChatID, event.SenderID)
		case types.ChatTypeGroup:
			recipientIDs, _ = s.repo.GetAllGroupMemberIDs(s.ctx, event.ChatID)
		}
		if len(recipientIDs) == 0 && event.ChatType == types.ChatTypePrivate {
			// bot-{botUUID} 格式：Bot 回复时，从 MentionUserIDs 获取接收者
			if strings.HasPrefix(event.ChatID, "bot-") && strings.HasPrefix(event.SenderID, "bot_") && len(event.MentionUserIDs) > 0 {
				recipientIDs = event.MentionUserIDs
			} else {
				// 保底，确保有接收者
				recipientIDs = []string{event.SenderID}
			}
		}
		if event.Channel == "qq" && event.Metadata != nil && event.Metadata["qq_bound_user"] == "true" {
			recipientIDs = appendUnique(recipientIDs, event.SenderID)
		}
		event.RecipientIDs = recipientIDs

		// 直接发布到 outgoing Kafka！不等待数据库保存和审核！
		if event.ChatType == types.ChatTypePrivate {
			logger.Debug("发布私聊消息到 outgoing topic",
				logger.StringField("msg_id", event.ID),
				logger.StringField("time", time.Now().Format("2006-01-02 15:04:05.000000")))
		}
		if err := s.eventBus.PublishOutgoingMessageEvent(s.ctx, event); err != nil {
			logger.Error("发布出站消息事件失败", logger.ErrorField(err))
		}

		logger.Info("事件已发布出站，异步处理后续逻辑",
			logger.StringField("msg_id", event.ID),
			logger.IntField("recipient_count", len(event.RecipientIDs)),
		)

		// 2️⃣ 同步落库：只有落库成功才返回 nil，Kafka 才会提交 offset；
		// 落库失败返回 error → Subscribe 不提交 offset → 消息重投（at-least-once，不丢消息）。
		// CreateMessage 按主键判重，重投不会产生重复数据。
		if err := s.persistIncomingMessage(event); err != nil {
			logger.Error("消息落库失败，不提交 offset 等待 Kafka 重投",
				logger.StringField("msg_id", event.ID),
				logger.StringField("chat_id", event.ChatID),
				logger.ErrorField(err))
			return err
		}

		// 3️⃣ 异步处理尽力而为的后续逻辑（审核、翻译、Bot 回复、离线推送）
		// 这些失败不影响消息可靠性，因此不阻塞 offset 提交
		go func() {
			if s.moderationClient != nil {
				s.moderateMessage(event)
			}
			if s.botClient != nil && event.SenderID != "system" {
				s.handleBotMentions(event)
			}
			s.pushOfflineNotification(event)
		}()

	default:
		logger.Warn("未知事件类型", logger.StringField("event_type", string(eventType)))
	}

	return nil
}

// authorizeIncomingMessage 校验事件发送者是否有权向该会话发送消息（失败关闭）。
//
// 群聊要求发送者是群成员；私聊要求发送者是会话参与方——文本与媒体共用同一条事件链路，
// 若不校验，拿到会话 ID 就能向任意两人的私聊注入消息（含图片/文件）。
// 例外：QQ 桥接事件由服务端转换并携带 channel=qq；system 为可信系统消息；
// bot-{uuid} 的 Bot 共享会话在 checkPrivateChatAccess 内放行。
func (s *ChatServiceImpl) authorizeIncomingMessage(event *types.MessageEvent) bool {
	switch event.ChatType {
	case types.ChatTypeGroup:
		member, err := s.repo.GetGroupMember(s.ctx, event.ChatID, event.SenderID)
		if err != nil {
			logger.Warn("检查群成员身份失败", logger.ErrorField(err))
		}
		if member == nil {
			logger.Warn("非群组成员尝试发送消息，已拒绝",
				logger.StringField("sender_id", event.SenderID),
				logger.StringField("chat_id", event.ChatID))
			return false
		}
	case types.ChatTypePrivate:
		if event.Channel == "qq" || event.SenderID == "system" {
			return true
		}
		if err := checkPrivateChatAccess(event.ChatID, event.SenderID); err != nil {
			logger.Warn("非私聊会话参与方尝试发送消息，已拒绝",
				logger.StringField("sender_id", event.SenderID),
				logger.StringField("chat_id", event.ChatID),
				logger.IntField("message_type", int(event.MessageType)))
			return false
		}
	}
	return true
}

// persistIncomingMessage 同步落库：确保会话存在 + 写入消息（幂等）+ 更新会话最后消息。
//
// 之所以同步执行而非放进 goroutine：Kafka 消费者只有收到 handler 返回 nil 才提交 offset。
// 若落库失败而 handler 已返回 nil，offset 会被提交，该消息将永久丢失（无法重投）。
// 同步落库让失败能被消费者感知，从而不提交 offset、由 Kafka 重投恢复，
// 配合 CreateMessage 的主键判重实现 at-least-once 且不产生重复数据。
func (s *ChatServiceImpl) persistIncomingMessage(event *types.MessageEvent) error {
	// 确保会话和参与者存在（GetOrCreatePrivateConversation 自身幂等）
	if event.ChatType == types.ChatTypePrivate {
		if strings.HasPrefix(event.ChatID, "bot-") {
			// bot-{botUUID} 格式：Bot 聊天
			botID := strings.TrimPrefix(event.ChatID, "bot-")
			botSenderID := "bot_" + botID
			// 从 MentionUserIDs 或 RecipientIDs 中获取用户 ID
			var userID string
			for _, uid := range event.MentionUserIDs {
				if !strings.HasPrefix(uid, "bot_") {
					userID = uid
					break
				}
			}
			if userID == "" {
				for _, uid := range event.RecipientIDs {
					if !strings.HasPrefix(uid, "bot_") {
						userID = uid
						break
					}
				}
			}
			if userID != "" {
				_, _ = s.repo.GetOrCreatePrivateConversation(s.ctx, botSenderID, userID)
			}
		} else {
			parts := strings.Split(event.ChatID, "_")
			if len(parts) == 3 && parts[0] == "private" {
				// private_{a}_{b}
				_, _ = s.repo.GetOrCreatePrivateConversation(s.ctx, parts[1], parts[2])
			} else if len(parts) == 2 {
				// {a}_{b}
				_, _ = s.repo.GetOrCreatePrivateConversation(s.ctx, parts[0], parts[1])
			}
		}
	}

	// 保存消息到数据库（使用原始ID）
	msgID := event.ID // 保持原始ID不变！
	now := time.Now()
	msg := &model.Message{
		ID:             msgID,
		RequestID:      event.RequestID,
		ConversationID: event.ChatID,
		ChatID:         event.ChatID,
		ChatType:       int(event.ChatType),
		SenderID:       event.SenderID,
		SenderName:     event.SenderName,
		SenderAvatar:   event.SenderAvatar,
		MessageType:    int(event.MessageType),
		Content:        event.Content,
		MediaURL:       event.MediaURL,
		MediaMeta:      model.JSONRaw(event.MediaMeta),
		Metadata:       event.Metadata,
		Status:         "sent",
		Channel:        "web",
		Role:           "user",
		CreatedAt:      now,
		UpdatedAt:      now,
		ReplyToMessage: event.ReplyToMessage,
		MentionUserIDs: model.StringArray(event.MentionUserIDs),
	}

	logger.Info("HandleMessageEvent: saving message",
		logger.StringField("msg_id", msgID),
		logger.StringField("media_url", event.MediaURL),
		logger.IntField("type", int(event.MessageType)))

	// 携带客户端幂等键时按 (sender_id, request_id) 落库：
	// 网关重发（WS/TCP 重试）或客户端切换到 REST 兜底时只会留下一条消息。
	replayed := false
	if event.RequestID != "" {
		created, stored, err := s.repo.CreateMessageIdempotent(s.ctx, msg)
		if err != nil {
			return err
		}
		if stored != nil {
			msg = stored
		}
		replayed = !created
		logger.Info("按幂等键落库消息",
			logger.StringField("msg_id", msg.ID),
			logger.StringField("sender_id", event.SenderID),
			logger.StringField("request_id", event.RequestID),
			logger.BoolField("replayed", replayed))
	} else if err := s.repo.CreateMessage(s.ctx, msg); err != nil {
		return err
	}

	if !replayed {
		if err := s.repo.UpdateConversationLastMessage(s.ctx, event.ChatID, msg.ID); err != nil {
			logger.Warn("更新会话最后消息失败", logger.ErrorField(err))
		}
	}

	return nil
}

var botMentionRegex = regexp.MustCompile(`@(\S+)`)

func (s *ChatServiceImpl) handleBotMentions(event *types.MessageEvent) {
	// QQ Bridge 快速路径已直接调用 Bot，跳过避免重复调用
	if event.Metadata != nil {
		if direct, ok := event.Metadata["qq_direct_bot"]; ok && direct == "true" {
			logger.Debug("QQ快速路径消息，跳过Bot调用",
				logger.StringField("msg_id", event.ID))
			return
		}
	}

	var botIDs []string

	for _, uid := range event.MentionUserIDs {
		if strings.HasPrefix(uid, "bot_") {
			botID := strings.TrimPrefix(uid, "bot_")
			botIDs = append(botIDs, botID)
		}
	}

	if len(botIDs) == 0 {
		mentions := botMentionRegex.FindAllStringSubmatch(event.Content, -1)
		if len(mentions) == 0 {
			return
		}
		for _, m := range mentions {
			botIDs = append(botIDs, m[1])
		}
	}

	logger.Info("检测到Bot提及",
		logger.StringField("msg_id", event.ID),
		logger.StringField("bots", strings.Join(botIDs, ",")))

	go s.invokeBotForMention(event, botIDs)
}

func (s *ChatServiceImpl) invokeBotForMention(event *types.MessageEvent, botIDs []string) {
	query := event.Content
	mentions := botMentionRegex.FindAllStringSubmatch(event.Content, -1)
	for _, m := range mentions {
		query = strings.Replace(query, "@"+m[1], "", 1)
	}
	for _, botID := range botIDs {
		query = strings.Replace(query, "@bot_"+botID, "", 1)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		query = "你好"
	}

	for _, rawID := range botIDs {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			botID := rawID
			if !isValidUUID(botID) {
				resolved, err := s.botClient.ResolveBotID(ctx, botID)
				if err != nil {
					logger.Warn("无法解析Bot名称到ID", logger.StringField("name", botID), logger.ErrorField(err))
					s.publishBotErrorMessage(ctx, event.ChatID, event.ChatType, botID, event.ID, "Bot 名称解析失败")
					return
				}
				botID = resolved
			}

			logger.Info("开始调用Bot", logger.StringField("bot_id", botID), logger.StringField("chat_id", event.ChatID))
			callStart := time.Now()

			var modelConfig map[string]string
			if event.Extra != nil {
				if mc, ok := event.Extra["model_config"]; ok {
					if mcMap, ok := mc.(map[string]interface{}); ok {
						modelConfig = make(map[string]string)
						for k, v := range mcMap {
							if s, ok := v.(string); ok {
								modelConfig[k] = s
							}
						}
					}
				}
			}

			content, err := s.botClient.SendBotMessage(ctx, botID, query, event.SenderID, event.ChatID, modelConfig)
			if err != nil {
				logger.Error("调用Bot失败",
					logger.StringField("bot_id", botID),
					logger.StringField("elapsed", time.Since(callStart).String()),
					logger.ErrorField(err))
				errMsg := "Bot 暂时无法回复，请稍后再试"
				if strings.Contains(err.Error(), "insufficient") {
					errMsg = "Bot 余额不足，请联系管理员充值"
				} else if strings.Contains(err.Error(), "not initialized") || strings.Contains(err.Error(), "API") {
					errMsg = "Bot 服务配置异常，请联系管理员"
				} else if ctx.Err() == context.DeadlineExceeded {
					errMsg = "Bot 响应超时，请稍后再试"
				}
				s.publishBotErrorMessage(ctx, event.ChatID, event.ChatType, botID, event.ID, errMsg)
				return
			}

			logger.Info("Bot调用完成", logger.StringField("bot_id", botID), logger.StringField("elapsed", time.Since(callStart).String()))

			if content == "" {
				logger.Warn("Bot返回空响应", logger.StringField("bot_id", botID))
				s.publishBotErrorMessage(ctx, event.ChatID, event.ChatType, botID, event.ID, "Bot 返回了空响应")
				return
			}

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
				Metadata:       event.Metadata,
				Channel:        event.Channel,
			}

			if err := s.eventBus.PublishMessageEvent(ctx, botResponseEvent); err != nil {
				logger.Error("发布Bot回复事件失败", logger.ErrorField(err))
				return
			}

			logger.Info("Bot回复已注入聊天流",
				logger.StringField("bot_id", botID),
				logger.StringField("chat_id", event.ChatID),
				logger.StringField("reply_to", event.ID))
		}()
	}
}

func (s *ChatServiceImpl) publishBotErrorMessage(_ context.Context, chatID string, chatType types.ChatType, botID, replyToMsgID, errMsg string) {
	msgCtx, msgCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer msgCancel()

	errorEvent := &types.MessageEvent{
		ID:             uuid.New().String(),
		ChatID:         chatID,
		ChatType:       chatType,
		SenderID:       "bot_" + botID,
		MessageType:    types.MessageTypeText,
		Content:        errMsg,
		ReplyToMessage: replyToMsgID,
		Timestamp:      time.Now(),
	}
	if err := s.eventBus.PublishMessageEvent(msgCtx, errorEvent); err != nil {
		logger.Error("发布Bot错误消息失败", logger.ErrorField(err))
	}
}

func isValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func (s *ChatServiceImpl) moderateMessage(event *types.MessageEvent) {
	if s.moderationClient == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rejected, err := s.moderationClient.ModerateContent(ctx, event.Content, event.ID, "text")
	if err != nil {
		// 该路径是消息已落库后的补充审核，无法撤回；发送入口 SendMessage 已按
		// 失败关闭策略拦截，这里只记录告警供排查审核服务可用性。
		logger.Warn("补充审核调用失败，消息已发出无法撤回", logger.ErrorField(err))
		return
	}

	if rejected {
		blockedEvent := &types.MessageEvent{
			ID:          uuid.New().String(),
			ChatID:      event.ChatID,
			ChatType:    event.ChatType,
			SenderID:    "system",
			MessageType: types.MessageTypeSystem,
			Content:     "该消息因内容违规已被拦截",
			Timestamp:   time.Now(),
		}
		_ = s.eventBus.PublishMessageEvent(s.ctx, blockedEvent)
	}
}

func (s *ChatServiceImpl) autoTranslateMessage(event *types.MessageEvent) {
	if !s.enableTranslation || s.translationClient == nil {
		return
	}
	if event.MessageType != types.MessageTypeText || event.Content == "" {
		return
	}
	if event.SenderID == "system" {
		return
	}

	targetLang := s.defaultTargetLang
	if targetLang == "" {
		targetLang = "zh"
	}

	sourceLang := "auto"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	translated, err := s.translationClient.Translate(ctx, event.Content, sourceLang, targetLang, event.ID)
	if err != nil {
		logger.Warn("自动翻译失败", logger.ErrorField(err), logger.StringField("msg_id", event.ID))
		return
	}

	if translated != "" && translated != event.Content {
		if event.Metadata == nil {
			event.Metadata = make(map[string]string)
		}
		event.Metadata["translated_content"] = translated
		event.Metadata["translated_lang"] = targetLang
	}
}

func (s *ChatServiceImpl) pushOfflineNotification(event *types.MessageEvent) {
	pushService := offline.GetOfflinePushService()
	if pushService == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pushService.PushFromMessageEvent(ctx, event, nil)
}

func (s *ChatServiceImpl) handleMessageReadEvent(event *types.MessageReadEvent) error {
	logger.Info("处理消息已读事件",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("reader_id", event.ReaderID),
		logger.IntField("message_count", len(event.MessageIDs)))

	if len(event.RecipientIDs) > 0 {
		logger.Debug("已读回执事件已处理，跳过", logger.StringField("chat_id", event.ChatID))
		return nil
	}

	// 已读位点化：用本次回执携带消息的最大 seq 推进会话已读位点（单调不回退）。
	// 未读数由位点比对计算，无需逐条 UPDATE messages.is_read。
	if event.ChatID != "" && event.ReaderID != "" {
		targetSeq, err := s.repo.GetMaxSeqByMessageIDs(s.ctx, event.MessageIDs)
		if err != nil {
			logger.Warn("查询已读回执消息 seq 失败", logger.ErrorField(err))
		}
		if err := s.repo.AdvanceReadPosition(s.ctx, event.ChatID, event.ReaderID, targetSeq); err != nil {
			logger.Error("推进已读位点失败", logger.ErrorField(err))
			return err
		}
	}

	// 兼容既有已读态展示：仍标记显式 msgIDs 的 is_read（有界，非全量）
	if err := s.repo.MarkMessagesRead(s.ctx, event.MessageIDs); err != nil {
		logger.Warn("标记消息已读失败", logger.ErrorField(err))
	}

	recipientIDs := s.getReadReceiptRecipients(event)
	event.RecipientIDs = recipientIDs

	if err := s.eventBus.PublishOutgoingReadEvent(s.ctx, event); err != nil {
		logger.Error("发布出站已读回执事件失败", logger.ErrorField(err))
		return err
	}

	logger.Info("消息已读状态已更新，已发布出站事件",
		logger.StringField("reader_id", event.ReaderID),
		logger.IntField("message_count", len(event.MessageIDs)),
		logger.IntField("recipient_count", len(recipientIDs)))

	return nil
}

func (s *ChatServiceImpl) getReadReceiptRecipients(event *types.MessageReadEvent) []string {
	var recipients []string

	parts := strings.Split(event.ChatID, "_")
	if len(parts) == 2 {
		recipients = s.getPrivateChatRecipient(event.ChatID, event.ReaderID)
		recipients = append(recipients, event.ReaderID)
	} else {
		groupMembers, err := s.repo.GetAllGroupMemberIDs(s.ctx, event.ChatID)
		if err == nil {
			recipients = groupMembers
		} else {
			logger.Debug("获取群成员失败，将由 Gateway 处理",
				logger.StringField("chat_id", event.ChatID),
				logger.ErrorField(err))
		}
	}

	return recipients
}

func (s *ChatServiceImpl) handleTypingEvent(event *types.TypingEvent) error {
	logger.Info("处理输入状态事件",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("user_id", event.UserID),
		logger.BoolField("typing", event.IsTyping))

	if len(event.RecipientIDs) > 0 {
		logger.Debug("输入状态事件已处理，跳过", logger.StringField("chat_id", event.ChatID))
		return nil
	}

	recipientIDs := s.getReadReceiptRecipients(&types.MessageReadEvent{
		ReaderID: event.UserID,
		ChatID:   event.ChatID,
	})
	event.RecipientIDs = recipientIDs

	if err := s.eventBus.PublishOutgoingTypingEvent(s.ctx, event); err != nil {
		logger.Error("发布出站输入状态事件失败", logger.ErrorField(err))
		return err
	}

	logger.Info("输入状态事件已处理并发布出站事件",
		logger.StringField("user_id", event.UserID),
		logger.IntField("recipient_count", len(recipientIDs)))

	return nil
}

func appendUnique(items []string, item string) []string {
	if item == "" {
		return items
	}
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func (s *ChatServiceImpl) getPrivateChatRecipient(chatID, senderID string) []string {
	parts := strings.Split(chatID, "_")
	if len(parts) == 3 && parts[0] == "private" {
		switch senderID {
		case parts[1]:
			return []string{parts[2]} // 只返回对方
		case parts[2]:
			return []string{parts[1]} // 只返回对方
		}
	} else if len(parts) == 2 {
		switch senderID {
		case parts[0]:
			return []string{parts[1]} // 只返回对方
		case parts[1]:
			return []string{parts[0]} // 只返回对方
		}
	}

	// bot-{botUUID} 格式：Bot 聊天的 ChatID
	if strings.HasPrefix(chatID, "bot-") {
		// 如果发送者是 Bot（bot_ 前缀），接收者无法仅从 ChatID 推断
		// 返回空，由调用方通过 MentionUserIDs 等补充
		if strings.HasPrefix(senderID, "bot_") {
			return []string{}
		}
		// 用户发送消息给 Bot，接收者是 Bot
		botID := strings.TrimPrefix(chatID, "bot-")
		return []string{"bot_" + botID}
	}

	logger.Warn("无法解析单聊会话ID获取接收者",
		logger.StringField("chat_id", chatID),
		logger.StringField("sender_id", senderID),
	)
	return []string{}
}

// privateChatTarget 解析私聊 chatID 并校验发送者是否为会话参与者。
// 支持 private_{a}_{b} 与 {a}_{b} 两种会话 ID 格式；单段 chatID 视为目标用户 ID（paired=false），
// 由调用方与发送者创建会话，不存在越权。
// chatID 由客户端传入，非参与者必须拒绝，否则可把消息注入任意两人的私聊会话。
func privateChatTarget(chatID, senderID string) (userA, userB, target string, paired bool, err error) {
	parts := strings.Split(chatID, "_")
	switch {
	case len(parts) == 3 && parts[0] == "private":
		userA, userB = parts[1], parts[2]
	case len(parts) == 2:
		userA, userB = parts[0], parts[1]
	default:
		return "", "", chatID, false, nil
	}
	if userA != senderID && userB != senderID {
		return "", "", "", false, fmt.Errorf("无权向该会话发送消息")
	}
	if userA == senderID {
		return userA, userB, userB, true, nil
	}
	return userA, userB, userA, true, nil
}

func (s *ChatServiceImpl) SendMessage(senderID, chatID string, chatType model.ChatType, msgType model.MessageType, content string, metadata map[string]string, replyToID string, mentionIDs []string, requestID string) (*model.Message, []string, bool, error) {
	var actualChatID string
	var targetUserID string

	if chatType == model.ChatTypePrivate {
		userA, userB, target, paired, err := privateChatTarget(chatID, senderID)
		if err != nil {
			logger.Warn("越权向他人私聊会话发送消息已拦截",
				logger.StringField("sender_id", senderID),
				logger.StringField("chat_id", chatID))
			return nil, nil, false, err
		}
		targetUserID = target
		if paired {
			conv, err := s.repo.GetOrCreatePrivateConversation(s.ctx, userA, userB)
			if err != nil {
				logger.Error("获取或创建私聊会话失败", logger.ErrorField(err))
				return nil, nil, false, err
			}
			actualChatID = conv.ID
			logger.Info("私聊消息，使用会话 ID",
				logger.StringField("sender_id", senderID),
				logger.StringField("target_user_id", targetUserID),
				logger.StringField("conversation_id", actualChatID))
		} else {
			conv, err := s.repo.GetOrCreatePrivateConversation(s.ctx, senderID, targetUserID)
			if err != nil {
				logger.Error("获取或创建私聊会话失败", logger.ErrorField(err))
				return nil, nil, false, err
			}
			actualChatID = conv.ID
			logger.Info("私聊消息，使用会话 ID",
				logger.StringField("sender_id", senderID),
				logger.StringField("target_user_id", targetUserID),
				logger.StringField("conversation_id", actualChatID))
		}
	} else {
		actualChatID = chatID
	}

	if chatType == model.ChatTypePrivate && s.contactChecker != nil && senderID != "system" && targetUserID != "" {
		// 异步检查关系，不阻塞消息发送，但降低日志级别
		go func() {
			relCtx, relCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer relCancel()
			isBlocked, _ := s.contactChecker.IsBlocked(relCtx, senderID, targetUserID)
			blockedByTarget, _ := s.contactChecker.IsBlocked(relCtx, targetUserID, senderID)
			isFriend, _ := s.contactChecker.IsFriend(relCtx, senderID, targetUserID)

			// 只在有异常情况时才记录日志
			if isBlocked || blockedByTarget || !isFriend {
				logger.Debug("关系检查结果",
					logger.StringField("sender_id", senderID),
					logger.StringField("target_user_id", targetUserID),
					logger.BoolField("is_blocked", isBlocked),
					logger.BoolField("blocked_by_target", blockedByTarget),
					logger.BoolField("is_friend", isFriend))
			}
		}()
	}

	if chatType == model.ChatTypeGroup {
		member, err := s.repo.GetGroupMember(s.ctx, actualChatID, senderID)
		if err != nil {
			logger.Warn("检查群成员身份失败", logger.ErrorField(err))
		}
		if member == nil {
			return nil, nil, false, fmt.Errorf("您不是该群组成员，无法发送消息")
		}
		// 禁言拦截：只把禁言状态写库而不拦发送等于禁言无效
		if err := checkMuted(member, time.Now()); err != nil {
			logger.Warn("群成员禁言拦截",
				logger.StringField("group_id", actualChatID),
				logger.StringField("user_id", senderID),
				logger.StringField("mute_type", member.MuteType.String()),
				logger.ErrorField(err))
			return nil, nil, false, err
		}
	}

	// 私聊跳过审核，只有群聊才审核
	if s.enableModeration && s.moderationClient != nil && senderID != "system" && senderID != "" && content != "" && chatType == model.ChatTypeGroup {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rejected, err := s.moderationClient.ModerateContent(ctx, content, actualChatID, "text")
		cancel()
		if err != nil {
			// 失败关闭：审核服务不可用时拒绝发送，避免未经审核的内容流入群聊
			logger.Error("消息审核调用失败，按失败关闭策略拒绝发送", logger.ErrorField(err))
			return nil, nil, false, types.ErrContentModerationUnavailable
		}
		if rejected {
			return nil, nil, false, types.ErrContentRejected
		}
	}

	msgID := uuid.New().String()
	now := time.Now()

	msg := &model.Message{
		ID:             msgID,
		ConversationID: actualChatID,
		ChatID:         actualChatID,
		ChatType:       int(chatType),
		SenderID:       senderID,
		MessageType:    int(msgType),
		Content:        content,
		Metadata:       metadata,
		Status:         "sent",
		Channel:        "web",
		Role:           "user",
		CreatedAt:      now,
		UpdatedAt:      now,
		ReplyToMessage: replyToID,
		MentionUserIDs: model.StringArray(mentionIDs),
	}

	replayed := false
	if requestID != "" {
		// 幂等路径：同一发送者重复提交同一 request_id 只会落一条消息，
		// 重复请求回放首次创建的消息（replayed=true），避免网络重试造成重复消息。
		msg.RequestID = requestID
		created, stored, err := s.repo.CreateMessageIdempotent(s.ctx, msg)
		if err != nil {
			logger.Error("创建消息失败", logger.ErrorField(err))
			return nil, nil, false, err
		}
		if stored == nil {
			logger.Error("幂等写入未返回消息")
			return nil, nil, false, fmt.Errorf("幂等写入未返回消息")
		}
		// 防跨会话复用：同一 request_id 只能命中同一会话，否则说明客户端复用了键。
		if !created && stored.ChatID != actualChatID {
			logger.Warn("幂等键跨会话复用已拒绝",
				logger.StringField("sender_id", senderID),
				logger.StringField("request_id", requestID),
				logger.StringField("expected_chat_id", actualChatID),
				logger.StringField("got_chat_id", stored.ChatID))
			return nil, nil, false, types.ErrChatBadRequest
		}
		msg = stored
		replayed = !created
	} else if err := s.repo.CreateMessage(s.ctx, msg); err != nil {
		logger.Error("创建消息失败", logger.ErrorField(err))
		return nil, nil, false, err
	}

	// 回放请求不能再次推进会话最后消息，否则并发重复请求会把指针反复指向同一旧消息。
	if !replayed {
		if err := s.repo.UpdateConversationLastMessage(s.ctx, actualChatID, msg.ID); err != nil {
			logger.Warn("更新会话最后消息失败", logger.ErrorField(err))
		}
	}

	var recipientIDs []string

	switch chatType {
	case model.ChatTypePrivate:
		// 直接构造接收者列表
		recipientIDs = []string{targetUserID, senderID}
	case model.ChatTypeGroup:
		members, err := s.repo.GetAllGroupMemberIDs(s.ctx, actualChatID)
		if err == nil {
			recipientIDs = members
		} else {
			logger.Warn("获取群成员失败", logger.ErrorField(err))
		}
	case model.ChatTypeBroadcast:
		allUsers, err := s.repo.GetAllUserIDs(s.ctx)
		if err == nil {
			recipientIDs = allUsers
		} else {
			logger.Warn("获取广播用户列表失败", logger.ErrorField(err))
		}
	}

	logger.Info("发送消息",
		logger.StringField("msg_id", msg.ID),
		logger.StringField("chat_id", actualChatID),
		logger.IntField("chat_type", int(chatType)),
		logger.IntField("recipient_count", len(recipientIDs)),
		logger.BoolField("replayed", replayed),
	)

	return msg, recipientIDs, replayed, nil
}

func (s *ChatServiceImpl) GetMessageHistory(userID, chatID string, chatType model.ChatType, beforeTime time.Time, limit int, afterSeq int64) ([]*model.Message, bool, error) {
	if err := s.EnsureChatAccess(userID, chatID, chatType); err != nil {
		logger.Warn("拒绝越权读取会话历史",
			logger.StringField("user_id", userID),
			logger.StringField("chat_id", chatID),
			logger.ErrorField(err))
		return nil, false, err
	}

	// 清空历史后，用户视角里 seq <= clearedSeq 的消息不可见；
	// 增量补拉的游标同样不能低于该水位线，否则重连会把已清空的历史重新拉回来。
	clearedSeq := s.clearedSeqFor(chatID, userID)
	if afterSeq < clearedSeq {
		afterSeq = clearedSeq
	}

	// afterSeq > 0：增量补拉路径。只返回 seq 大于该游标的消息，已按 seq 升序，无需反转。
	// 用于客户端 WS 断线重连后，填补 Redis PubSub 实时扇出丢失的窗口。
	// afterSeq == 0：保持原有「取最近 limit 条 + 反转成升序」的历史分页语义。
	if afterSeq > 0 {
		messages, err := s.repo.GetMessagesAfterSeq(s.ctx, userID, chatID, afterSeq, limit+1)
		if err != nil {
			logger.Error("获取增量消息失败", logger.ErrorField(err))
			return nil, false, err
		}
		hasMore := len(messages) > limit
		if hasMore {
			messages = messages[:limit]
		}
		return messages, hasMore, nil
	}

	// 如果是私聊且 chatID 是用户 ID，需要先获取当前用户 ID 来构建会话 ID
	// 注意：这里我们假设调用方会处理这个问题，或者我们需要修改方法签名来传入 senderID
	// 暂时保持原样，先让发送消息能工作
	messages, err := s.repo.GetMessagesByChatID(s.ctx, userID, chatID, beforeTime, limit+1, clearedSeq)
	if err != nil {
		logger.Error("获取消息历史失败", logger.ErrorField(err))
		return nil, false, err
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, hasMore, nil
}

func (s *ChatServiceImpl) SearchMessages(userID, chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error) {
	// 搜索必须限定在单个可访问会话内：既防止用空 chat_id 全库检索，
	// 也防止通过猜 chat_id 越权检索他人会话内容。
	if chatID == "" {
		return nil, 0, types.ErrChatAccessDenied
	}
	if err := s.EnsureChatAccess(userID, chatID, chatType); err != nil {
		logger.Warn("拒绝越权搜索会话消息",
			logger.StringField("user_id", userID),
			logger.StringField("chat_id", chatID),
			logger.ErrorField(err))
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return s.repo.SearchMessages(s.ctx, userID, chatID, chatType, keyword, startTime, endTime, page, pageSize, s.clearedSeqFor(chatID, userID))
}

// MarkMessagesRead 将会话已读位点推进到位（位点化）：
//   - 带 msgIDs：取这些消息的最大 seq 作为目标位点；
//   - 不带 msgIDs（进入会话"全部已读"）：取会话当前最大 seq。
//
// 未读数由 conversation_participants.last_read_seq 比对得出，因此不再全量
// UPDATE messages.is_read（避免大会话下的写放大），仅对显式 msgIDs 保留标记以兼容展示。
func (s *ChatServiceImpl) MarkMessagesRead(msgIDs []string, userID, chatID string) error {
	if chatID == "" || userID == "" {
		// 缺少会话/用户信息时不再静默退化为全局标记，直接拒绝。
		return types.ErrChatAccessDenied
	}
	if err := s.EnsureChatAccess(userID, chatID, model.ChatType(0)); err != nil {
		logger.Warn("拒绝越权标记会话已读",
			logger.StringField("user_id", userID),
			logger.StringField("chat_id", chatID),
			logger.ErrorField(err))
		return err
	}

	var targetSeq int64
	if len(msgIDs) > 0 {
		var err error
		if targetSeq, err = s.repo.GetMaxSeqByMessageIDs(s.ctx, msgIDs); err != nil {
			return err
		}
	} else {
		var err error
		if targetSeq, err = s.repo.GetMaxMessageSeq(s.ctx, chatID); err != nil {
			return err
		}
	}

	if err := s.repo.AdvanceReadPosition(s.ctx, chatID, userID, targetSeq); err != nil {
		return err
	}

	if len(msgIDs) > 0 {
		_ = s.repo.MarkMessagesRead(s.ctx, msgIDs)
	}
	return nil
}

func (s *ChatServiceImpl) WithdrawMessage(msgID, userID string) error {
	msg, err := s.repo.GetMessageByID(s.ctx, msgID)
	if err != nil {
		logger.Error("获取消息失败", logger.ErrorField(err))
		return fmt.Errorf("消息不存在")
	}
	if msg == nil {
		return fmt.Errorf("消息不存在")
	}

	if msg.SenderID != userID {
		return fmt.Errorf("只能撤回自己的消息")
	}

	if time.Since(msg.CreatedAt) > 2*time.Minute {
		return fmt.Errorf("超过撤回时限")
	}

	if err := s.repo.WithdrawMessage(s.ctx, msgID, &model.MessageChange{
		ChatID:     msg.ChatID,
		MessageID:  msgID,
		ChangeType: model.MessageChangeWithdraw,
		Content:    "[消息已撤回]",
		Version:    msg.Version + 1,
		MessageSeq: msg.Seq,
		ActorID:    userID,
	}); err != nil {
		logger.Error("撤回消息失败", logger.ErrorField(err))
		return err
	}

	if s.eventBus != nil {
		var recipientIDs []string
		chatType := types.ChatType(msg.ChatType)
		switch chatType {
		case types.ChatTypePrivate:
			recipientIDs = s.getPrivateChatRecipient(msg.ChatID, msg.SenderID)
		case types.ChatTypeGroup:
			recipientIDs, _ = s.repo.GetAllGroupMemberIDs(s.ctx, msg.ChatID)
		}
		// 撤回同样要通知发送者本人的其他在线设备，否则同账号多端会一直显示原文。
		recipientIDs = appendUnique(recipientIDs, msg.SenderID)
		if len(recipientIDs) == 0 {
			recipientIDs = []string{msg.SenderID}
		}

		withdrawEvent := &types.MessageWithdrawEvent{
			EventType:    types.EventTypeMessageWithdraw,
			MessageID:    msgID,
			ChatID:       msg.ChatID,
			ChatType:     chatType,
			SenderID:     msg.SenderID,
			Version:      msg.Version + 1,
			RecipientIDs: recipientIDs,
			Timestamp:    time.Now(),
		}
		if err := s.eventBus.PublishOutgoingWithdrawEvent(s.ctx, withdrawEvent); err != nil {
			logger.Error("发布撤回事件失败", logger.ErrorField(err))
		}
	}

	return nil
}

// EditMessage 编辑本人发送的消息。
//
// 并发控制采用乐观锁：客户端把读到的 message.version 原样回传，服务端以
// `WHERE id=? AND version=?` 原子条件更新。两个端同时编辑时只有一个成功，
// 另一个收到 ErrChatMessageConflict（409），由客户端刷新后再提交，
// 而不是让后写者静默覆盖先写者的内容（丢失更新）。
func (s *ChatServiceImpl) EditMessage(msgID, userID, content string, expectedVersion int64) (*model.Message, error) {
	if userID == "" || msgID == "" {
		return nil, types.ErrChatBadRequest
	}
	if expectedVersion < 0 {
		return nil, types.ErrChatBadRequest
	}

	msg, err := s.repo.GetMessageByID(s.ctx, msgID)
	if err != nil {
		logger.Error("获取消息失败", logger.ErrorField(err))
		return nil, err
	}
	// 不存在与非发送者统一返回同一错误，避免用错误码差异探测他人消息。
	if msg == nil || msg.SenderID != userID {
		return nil, types.ErrChatMessageNotFound
	}
	// 已撤回的消息内容已不可恢复，不允许再编辑（否则会把"已撤回"覆盖回正文）。
	if msg.Status == model.MessageStatusWithdrawn.String() {
		return nil, types.ErrChatMessageNotFound
	}

	updated, err := s.repo.EditMessage(s.ctx, msgID, content, expectedVersion, &model.MessageChange{
		ChatID:     msg.ChatID,
		MessageID:  msgID,
		ChangeType: model.MessageChangeEdit,
		Content:    content,
		MessageSeq: msg.Seq,
		ActorID:    userID,
	})
	if err != nil {
		logger.Error("编辑消息失败", logger.ErrorField(err))
		return nil, err
	}
	if !updated {
		// 条件更新未命中：版本已被其他端推进（或消息在读取后被删除/撤回）。
		logger.Warn("编辑消息版本冲突",
			logger.StringField("message_id", msgID),
			logger.StringField("user_id", userID),
			logger.IntField("expected_version", int(expectedVersion)))
		return nil, types.ErrChatMessageConflict
	}

	edited, err := s.repo.GetMessageByID(s.ctx, msgID)
	if err != nil {
		logger.Error("读取编辑后的消息失败", logger.ErrorField(err))
		return nil, err
	}
	s.publishEditEvent(edited)
	return edited, nil
}

// publishEditEvent 把编辑结果广播给会话内全部成员（含发送者自己的其他在线设备）。
//
// 与删除（只投递本人）不同，编辑是全局状态：所有成员都应看到最新内容。
// 事件带 version，接收端只在版本更新时应用，因此重复/乱序投递不会回退内容。
// 发布失败只告警：内容已落库，接收端重新拉历史仍能拿到新版本，不把通知失败升级成请求失败。
func (s *ChatServiceImpl) publishEditEvent(msg *model.Message) {
	if s.eventBus == nil || msg == nil {
		return
	}

	recipientIDs := s.editRecipients(msg)
	chatType := types.ChatType(msg.ChatType)

	event := &types.MessageEditEvent{
		EventType:    types.EventTypeMessageEdit,
		MessageID:    msg.ID,
		ChatID:       msg.ChatID,
		ChatType:     chatType,
		SenderID:     msg.SenderID,
		Content:      msg.Content,
		Version:      msg.Version,
		RecipientIDs: recipientIDs,
		Timestamp:    time.Now(),
	}
	if err := s.eventBus.PublishOutgoingEditEvent(s.ctx, event); err != nil {
		logger.Error("发布编辑事件失败", logger.ErrorField(err))
	}
}

// editRecipients 计算编辑事件的接收者：会话内全部成员，且一定包含发送者本人。
// getPrivateChatRecipient 只返回对方，发送者本人的其他在线设备必须显式补上，
// 否则同账号多端中只有发起编辑的那一端能看到新内容。
func (s *ChatServiceImpl) editRecipients(msg *model.Message) []string {
	var recipientIDs []string
	switch types.ChatType(msg.ChatType) {
	case types.ChatTypePrivate:
		recipientIDs = s.getPrivateChatRecipient(msg.ChatID, msg.SenderID)
	case types.ChatTypeGroup:
		recipientIDs, _ = s.repo.GetAllGroupMemberIDs(s.ctx, msg.ChatID)
	}
	recipientIDs = appendUnique(recipientIDs, msg.SenderID)
	if len(recipientIDs) == 0 {
		recipientIDs = []string{msg.SenderID}
	}
	return recipientIDs
}

// DeleteChatHistory 清空「本人视角」的会话历史。
//
// 早先实现直接 DELETE 会话全部消息，私聊里会把对方的记录一并销毁、群聊里普通成员的清空诉求
// 又因权限被拒；现改为按参与者维护 cleared_seq 水位线（与已读位点同构）：
//   - 只隐藏本人 seq <= 水位线的消息，对方/其他成员的历史与审计数据不受影响；
//   - 水位线单调推进（GREATEST），重复调用幂等，不会因并发回退；
//   - 同时推进 last_read_seq，清空后未读数归零。
//
// 任何成员只能清空自己，因此不再需要区分群主/管理员。
func (s *ChatServiceImpl) DeleteChatHistory(userID, chatID string) error {
	if err := s.EnsureChatAccess(userID, chatID, 0); err != nil {
		logger.Warn("拒绝越权清空会话历史",
			logger.StringField("user_id", userID),
			logger.StringField("chat_id", chatID),
			logger.ErrorField(err))
		return err
	}

	// 以当前最大 seq 作为水位线：此刻之前的消息视为已清空。
	// 失败时不清空（失败关闭），避免水位线取到 0 导致"清空无效"。
	maxSeq, err := s.repo.GetMaxMessageSeq(s.ctx, chatID)
	if err != nil {
		logger.Error("查询会话最大消息位点失败", logger.ErrorField(err))
		return err
	}

	logger.Info("清空聊天记录（按用户水位线）",
		logger.StringField("chat_id", chatID),
		logger.StringField("operator_id", userID),
		logger.IntField("cleared_seq", int(maxSeq)))
	if err := s.repo.ClearConversationForUser(s.ctx, chatID, userID, maxSeq); err != nil {
		logger.Error("清空聊天记录失败", logger.ErrorField(err))
		return err
	}

	// 墓碑清理：seq <= 水位线的消息已被清空隐藏，客户端也不会再拉取，
	// 对应的删除墓碑失去信息价值。清理失败只影响存储体积，不影响正确性，因此只告警。
	if err := s.repo.PurgeMessageDeletions(s.ctx, userID, chatID, maxSeq); err != nil {
		logger.Warn("清理已清空历史的删除墓碑失败",
			logger.StringField("chat_id", chatID),
			logger.StringField("user_id", userID),
			logger.ErrorField(err))
	}
	return nil
}

// DeleteMessage 单条消息删除：只对请求者本人隐藏，不改原文、不影响会话内其他成员。
//
// 与撤回的边界（面试常问）：
//   - 撤回由发送者发起、有 2 分钟时限、对所有人生效，落库方式是改写 messages.status；
//   - 删除由任意成员发起、无时限、仅对本人生效，落库方式是写 message_deletions 墓碑；
//   - 两者可以叠加：已撤回的消息仍可被删除，删除不会把状态改回 sent。
//
// 幂等：唯一键 (user_id, message_id) + ON CONFLICT DO NOTHING。重复删除（弱网重试、
// 多端同时操作）返回成功，且不会产生第二条墓碑。
//
// 防枚举：消息不存在与无权访问返回同一个 ErrChatMessageNotFound，
// 避免攻击者用错误码差异探测他人会话里的 message_id。
func (s *ChatServiceImpl) DeleteMessage(userID, messageID string) error {
	if userID == "" || messageID == "" {
		return types.ErrChatBadRequest
	}

	msg, err := s.repo.GetMessageByID(s.ctx, messageID)
	if err != nil {
		logger.Error("查询待删除消息失败", logger.ErrorField(err))
		return err
	}
	if msg == nil {
		return types.ErrChatMessageNotFound
	}
	if err := s.EnsureChatAccess(userID, msg.ChatID, model.ChatType(msg.ChatType)); err != nil {
		logger.Warn("拒绝越权删除消息",
			logger.StringField("user_id", userID),
			logger.StringField("message_id", messageID),
			logger.ErrorField(err))
		return types.ErrChatMessageNotFound
	}

	created, err := s.repo.DeleteMessageForUser(s.ctx, userID, msg.ID, msg.ChatID, msg.Seq)
	if err != nil {
		logger.Error("写入删除墓碑失败", logger.ErrorField(err))
		return err
	}

	logger.Info("删除消息（仅本人视角）",
		logger.StringField("user_id", userID),
		logger.StringField("chat_id", msg.ChatID),
		logger.StringField("message_id", msg.ID),
		logger.BoolField("duplicated", !created))

	// 只投递给本人：会话内其他成员不应知道「谁删了哪条消息」。
	// 首次删除才推送；重复删除已由首次推送覆盖，离线设备靠同步接口的墓碑补齐。
	if created && s.eventBus != nil {
		event := &types.MessageDeleteEvent{
			EventType:    types.EventTypeMessageDelete,
			MessageID:    msg.ID,
			ChatID:       msg.ChatID,
			ChatType:     types.ChatType(msg.ChatType),
			OwnerID:      userID,
			RecipientIDs: []string{userID},
			Timestamp:    time.Now(),
		}
		if err := s.eventBus.PublishOutgoingDeleteEvent(s.ctx, event); err != nil {
			// 推送失败不影响删除结果：墓碑已落库，其他设备会在下次同步时补齐。
			logger.Error("发布删除事件失败", logger.ErrorField(err))
		}
	}

	return nil
}

// clearedSeqFor 返回用户在会话内的清空水位线。
// 读取失败按 0（未清空）处理：这只影响本人历史可见性，不构成越权或数据丢失。
func (s *ChatServiceImpl) clearedSeqFor(chatID, userID string) int64 {
	seq, err := s.repo.GetClearedSeq(s.ctx, chatID, userID)
	if err != nil {
		logger.Warn("读取清空水位线失败，按未清空处理",
			logger.StringField("chat_id", chatID),
			logger.StringField("user_id", userID),
			logger.ErrorField(err))
		return 0
	}
	return seq
}

// EnsureChatAccess 是 IM 所有读写入口的统一访问闸门（失败关闭）：
//   - 群聊：必须是群成员；
//   - 私聊：必须能从 chatID 解析出会话双方且请求者是其中一方（bot- 会话除外）；
//   - chatType 缺失时按 chatID 反查类型（历史/搜索接口由网关透传，常为 UNSPECIFIED）。
func (s *ChatServiceImpl) EnsureChatAccess(userID, chatID string, chatType model.ChatType) error {
	if userID == "" || chatID == "" {
		return types.ErrChatAccessDenied
	}

	if chatType != model.ChatTypeGroup && chatType != model.ChatTypePrivate && chatType != model.ChatTypeBroadcast {
		switch {
		case chatID == "broadcast":
			chatType = model.ChatTypeBroadcast
		default:
			group, err := s.repo.GetGroupByID(s.ctx, chatID)
			if err != nil {
				logger.Error("查询会话类型失败", logger.ErrorField(err))
				return err
			}
			if group != nil {
				chatType = model.ChatTypeGroup
			} else {
				chatType = model.ChatTypePrivate
			}
		}
	}

	switch chatType {
	case model.ChatTypeBroadcast:
		// 广播会话对所有登录用户可见
		return nil
	case model.ChatTypeGroup:
		member, err := s.repo.GetGroupMember(s.ctx, chatID, userID)
		if err != nil {
			logger.Error("查询群成员失败", logger.ErrorField(err))
			return err
		}
		if member == nil {
			return types.ErrChatAccessDenied
		}
		return nil
	case model.ChatTypePrivate:
		return checkPrivateChatAccess(chatID, userID)
	default:
		return types.ErrChatAccessDenied
	}
}

// checkPrivateChatAccess 校验私聊 chatID 与请求者的归属关系。
// 支持 private_{a}_{b}、{a}_{b} 两种会话 ID 格式；bot-{uuid} 为 Bot 共享会话，不做参与者解析。
// 单段 chatID 仅在请求自身命名空间时放行，避免被当作任意用户的历史入口。
func checkPrivateChatAccess(chatID, userID string) error {
	if strings.HasPrefix(chatID, "bot-") {
		return nil
	}
	if chatID == "" || userID == "" {
		return types.ErrChatAccessDenied
	}

	parts := strings.Split(chatID, "_")
	switch {
	case len(parts) == 3 && parts[0] == "private":
		if parts[1] == userID || parts[2] == userID {
			return nil
		}
	case len(parts) == 2:
		if parts[0] == userID || parts[1] == userID {
			return nil
		}
	case len(parts) == 1:
		if chatID == userID {
			return nil
		}
	}
	return types.ErrChatAccessDenied
}

func (s *ChatServiceImpl) CreateGroup(name, ownerID string, memberIDs []string, metadata map[string]string) (*model.Group, error) {
	now := time.Now()
	groupID := uuid.New().String()

	group := &model.Group{
		ID:        groupID,
		Name:      name,
		OwnerID:   ownerID,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.CreateGroup(s.ctx, group); err != nil {
		logger.Error("创建群组失败", logger.ErrorField(err))
		return nil, err
	}

	conv := &model.Conversation{
		ID:        groupID,
		ChatID:    groupID,
		ChatType:  int(model.ChatTypeGroup),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateConversation(s.ctx, conv); err != nil {
		logger.Warn("创建会话记录失败", logger.ErrorField(err))
	}

	members := make([]*model.GroupMember, 0, len(memberIDs)+1)
	memberIDs = append(memberIDs, ownerID)

	for _, uid := range memberIDs {
		role := model.GroupMemberRoleMember
		if uid == ownerID {
			role = model.GroupMemberRoleOwner
		}
		isBot := strings.HasPrefix(uid, "bot_")
		if isBot {
			role = model.GroupMemberRoleBot
		}

		member := &model.GroupMember{
			GroupID:   groupID,
			UserID:    uid,
			Role:      role,
			MuteType:  model.MuteTypeNone,
			JoinedAt:  now,
			CreatedAt: now,
			UpdatedAt: now,
		}
		members = append(members, member)
	}

	if err := s.repo.AddGroupMembers(s.ctx, members); err != nil {
		logger.Error("添加群成员失败", logger.ErrorField(err))
		return nil, err
	}

	// 更新群组成员数
	if err := s.repo.UpdateGroupMemberCount(s.ctx, groupID); err != nil {
		logger.Warn("更新群组成员数失败", logger.ErrorField(err))
	}

	// 添加 ConversationParticipant 记录，这样群成员才能在会话列表里看到
	participants := make([]*model.ConversationParticipant, 0, len(memberIDs))
	for _, uid := range memberIDs {
		if strings.HasPrefix(uid, "bot_") {
			continue
		}
		p := &model.ConversationParticipant{
			ConversationID: groupID,
			UserID:         uid,
			LastReadAt:     now,
			JoinedAt:       now,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		participants = append(participants, p)
		logger.Info("[DEBUG] Will add participant", logger.StringField("user_id", uid), logger.StringField("group_id", groupID))
	}
	if err := s.repo.AddConversationParticipants(s.ctx, participants); err != nil {
		logger.Warn("添加会话参与者失败", logger.ErrorField(err))
	} else {
		logger.Info("[DEBUG] Successfully added participants", logger.StringField("group_id", groupID), logger.AnyField("participants", participants))
	}

	sysMsg := &model.Message{
		ID:             uuid.New().String(),
		ConversationID: groupID,
		ChatID:         groupID,
		ChatType:       int(model.ChatTypeGroup),
		SenderID:       "system",
		MessageType:    int(model.MessageTypeSystem),
		Content:        "群组创建成功",
		Status:         "sent",
		Channel:        "system",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.repo.CreateMessage(s.ctx, sysMsg); err != nil {
		logger.Warn("创建系统消息失败", logger.ErrorField(err))
	} else {
		if err := s.repo.UpdateConversationLastMessage(s.ctx, groupID, sysMsg.ID); err != nil {
			logger.Warn("更新会话最后消息失败", logger.ErrorField(err))
		}
	}

	logger.Info("创建群组成功",
		logger.StringField("group_id", groupID),
		logger.StringField("name", name),
		logger.IntField("member_count", len(members)),
	)

	return group, nil
}

func (s *ChatServiceImpl) InviteGroupMember(groupID, operatorID string, userIDs []string) error {
	logger.Info("📋 [群组-邀请] InviteGroupMember 被调用",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.StringField("user_ids", strings.Join(userIDs, ",")))
	member, err := s.repo.GetGroupMember(s.ctx, groupID, operatorID)
	if err != nil {
		return fmt.Errorf("您不是群成员")
	}
	if member == nil {
		return fmt.Errorf("您不是群成员")
	}
	if member.Role == model.GroupMemberRoleMember {
		return fmt.Errorf("没有权限邀请成员")
	}

	now := time.Now()
	members := make([]*model.GroupMember, 0, len(userIDs))
	participants := make([]*model.ConversationParticipant, 0, len(userIDs))

	for _, userID := range userIDs {
		if existing, _ := s.repo.GetGroupMember(s.ctx, groupID, userID); existing != nil {
			continue
		}

		isBot := strings.HasPrefix(userID, "bot_")

		role := model.GroupMemberRoleMember
		if isBot {
			role = model.GroupMemberRoleBot
		}

		m := &model.GroupMember{
			GroupID:   groupID,
			UserID:    userID,
			Role:      role,
			MuteType:  model.MuteTypeNone,
			JoinedAt:  now,
			CreatedAt: now,
			UpdatedAt: now,
		}
		members = append(members, m)

		if !isBot {
			p := &model.ConversationParticipant{
				ConversationID: groupID,
				UserID:         userID,
				LastReadAt:     now,
				JoinedAt:       now,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			participants = append(participants, p)
		}
	}

	if len(members) > 0 {
		if err := s.repo.AddGroupMembers(s.ctx, members); err != nil {
			logger.Error("添加群成员失败", logger.ErrorField(err))
			return err
		}

		// 更新群组成员数
		if err := s.repo.UpdateGroupMemberCount(s.ctx, groupID); err != nil {
			logger.Warn("更新群组成员数失败", logger.ErrorField(err))
		}
	}

	if len(participants) > 0 {
		if err := s.repo.AddConversationParticipants(s.ctx, participants); err != nil {
			logger.Warn("添加会话参与者失败", logger.ErrorField(err))
		}
	}

	if len(members) > 0 {
		var userIDs []string
		var botIDs []string
		for _, m := range members {
			if strings.HasPrefix(m.UserID, "bot_") {
				botIDs = append(botIDs, m.UserID)
			} else {
				userIDs = append(userIDs, m.UserID)
			}
		}
		var parts []string
		if len(userIDs) > 0 {
			names := s.resolveUserNames(userIDs)
			parts = append(parts, strings.Join(names, ", "))
		}
		if len(botIDs) > 0 {
			for _, bid := range botIDs {
				parts = append(parts, "Bot "+bid)
			}
		}
		msgContent := fmt.Sprintf("%s 已加入群组", strings.Join(parts, ", "))
		logger.Info("\x1b[32m📋 [群组-邀请] 发送系统消息\x1b[0m",
			logger.StringField("group_id", groupID),
			logger.StringField("content", msgContent),
			logger.StringField("user_ids", strings.Join(userIDs, ",")),
			logger.StringField("bot_ids", strings.Join(botIDs, ",")))
		s.sendGroupSystemMessage(groupID, msgContent)
	}

	logger.Info("邀请群成员",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.IntField("invite_count", len(members)),
	)

	return nil
}

func (s *ChatServiceImpl) KickGroupMember(groupID, operatorID, userID string) error {
	logger.Info("📋 [群组-踢出] KickGroupMember 被调用",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.StringField("target_user_id", userID))
	operator, err := s.repo.GetGroupMember(s.ctx, groupID, operatorID)
	if err != nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator == nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator.Role == model.GroupMemberRoleMember {
		return fmt.Errorf("没有权限踢出成员")
	}

	target, err := s.repo.GetGroupMember(s.ctx, groupID, userID)
	if err != nil {
		return fmt.Errorf("目标用户不在群中")
	}
	if target == nil {
		return fmt.Errorf("目标用户不在群中")
	}

	if target.Role == model.GroupMemberRoleOwner {
		return fmt.Errorf("不能踢群主")
	}

	if operator.Role == model.GroupMemberRoleAdmin && target.Role == model.GroupMemberRoleAdmin {
		return fmt.Errorf("管理员不能互踢")
	}

	if err := s.repo.RemoveGroupMember(s.ctx, groupID, userID); err != nil {
		logger.Error("踢出群成员失败", logger.ErrorField(err))
		return err
	}

	if err := s.repo.DeleteConversationForUser(s.ctx, groupID, userID); err != nil {
		logger.Warn("删除会话参与者失败", logger.ErrorField(err))
	}

	if err := s.repo.UpdateGroupMemberCount(s.ctx, groupID); err != nil {
		logger.Warn("更新群组成员数失败", logger.ErrorField(err))
	}

	remainingMemberIDs, err := s.repo.GetAllGroupMemberIDs(s.ctx, groupID)
	if err != nil {
		logger.Warn("获取剩余群成员失败", logger.ErrorField(err))
	}

	if len(remainingMemberIDs) == 0 {
		if err := s.repo.DeleteGroup(s.ctx, groupID); err != nil {
			logger.Error("解散群组失败", logger.ErrorField(err))
		} else {
			logger.Info("📋 [群组-解散] 踢出成员后群组无成员，已解散",
				logger.StringField("group_id", groupID))
		}
		return nil
	}

	var userName string
	if target.Nickname != "" {
		userName = target.Nickname
	} else {
		userName = s.resolveUserName(userID)
	}
	msgContent := fmt.Sprintf("%s 已被管理员踢出群组", userName)
	logger.Info("\x1b[31m📋 [群组-踢出] 发送系统消息\x1b[0m",
		logger.StringField("group_id", groupID),
		logger.StringField("user_id", userID),
		logger.StringField("username", userName),
		logger.StringField("content", msgContent))
	s.sendGroupSystemMessage(groupID, msgContent)

	logger.Info("踢出群成员",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.StringField("user_id", userID),
	)

	return nil
}

// checkMuted 判断群成员当前是否处于禁言状态。
// 永久禁言始终生效；临时禁言在 MuteUntil 之前生效（未设置截止时间视为仍生效）。
// 禁言到期后不再拦截，成员可正常发言。
func checkMuted(member *model.GroupMember, now time.Time) error {
	switch member.MuteType {
	case model.MuteTypePermanent:
		return fmt.Errorf("您已被永久禁言，无法发送消息")
	case model.MuteTypeTemporary:
		if member.MuteUntil == nil || member.MuteUntil.After(now) {
			return fmt.Errorf("您已被禁言，无法发送消息")
		}
	}
	return nil
}

func (s *ChatServiceImpl) MuteGroupMember(groupID, operatorID, userID string, muteType model.MuteType, muteUntil time.Time) error {
	operator, err := s.repo.GetGroupMember(s.ctx, groupID, operatorID)
	if err != nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator == nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator.Role == model.GroupMemberRoleMember {
		return fmt.Errorf("没有权限禁言")
	}

	if _, err := s.repo.GetGroupMember(s.ctx, groupID, userID); err != nil {
		return fmt.Errorf("目标用户不在群中")
	}

	if err := s.repo.UpdateGroupMemberMute(s.ctx, groupID, userID, muteType, muteUntil); err != nil {
		logger.Error("更新禁言状态失败", logger.ErrorField(err))
		return err
	}

	logger.Info("设置群成员禁言",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.StringField("user_id", userID),
		logger.IntField("mute_type", int(muteType)),
	)

	return nil
}

func (s *ChatServiceImpl) TransferGroupOwner(groupID, operatorID, newOwnerID string) error {
	operator, err := s.repo.GetGroupMember(s.ctx, groupID, operatorID)
	if err != nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator == nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator.Role != model.GroupMemberRoleOwner {
		return fmt.Errorf("只有群主可以转让")
	}

	if _, err := s.repo.GetGroupMember(s.ctx, groupID, newOwnerID); err != nil {
		return fmt.Errorf("新群主不在群中")
	}

	if err := s.repo.TransferGroupOwner(s.ctx, groupID, newOwnerID); err != nil {
		logger.Error("转让群主失败", logger.ErrorField(err))
		return err
	}

	logger.Info("转让群主",
		logger.StringField("group_id", groupID),
		logger.StringField("old_owner_id", operatorID),
		logger.StringField("new_owner_id", newOwnerID),
	)

	return nil
}

func (s *ChatServiceImpl) UpdateGroupAnnouncement(groupID, operatorID, announcement string) error {
	operator, err := s.repo.GetGroupMember(s.ctx, groupID, operatorID)
	if err != nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator == nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator.Role == model.GroupMemberRoleMember {
		return fmt.Errorf("没有权限修改公告")
	}

	if err := s.repo.UpdateGroupAnnouncement(s.ctx, groupID, announcement); err != nil {
		logger.Error("更新群公告失败", logger.ErrorField(err))
		return err
	}

	msgContent := "群公告已更新"
	if announcement != "" {
		msgContent = fmt.Sprintf("群公告已更新：%s", announcement)
	}
	s.sendGroupSystemMessage(groupID, msgContent)

	logger.Info("更新群公告",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
	)

	return nil
}

func (s *ChatServiceImpl) UpdateGroupAvatar(groupID, operatorID, avatar string) error {
	operator, err := s.repo.GetGroupMember(s.ctx, groupID, operatorID)
	if err != nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator == nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator.Role == model.GroupMemberRoleMember {
		return fmt.Errorf("没有权限修改群头像")
	}

	if err := s.repo.UpdateGroupAvatar(s.ctx, groupID, avatar); err != nil {
		logger.Error("更新群头像失败", logger.ErrorField(err))
		return err
	}

	logger.Info("更新群头像",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
	)

	return nil
}

func (s *ChatServiceImpl) SetGroupAdmin(groupID, operatorID, userID string, isAdmin bool) error {
	operator, err := s.repo.GetGroupMember(s.ctx, groupID, operatorID)
	if err != nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator == nil {
		return fmt.Errorf("您不是群成员")
	}
	if operator.Role != model.GroupMemberRoleOwner {
		return fmt.Errorf("只有群主可以设置管理员")
	}

	role := model.GroupMemberRoleMember
	if isAdmin {
		role = model.GroupMemberRoleAdmin
	}

	if err := s.repo.UpdateGroupMemberRole(s.ctx, groupID, userID, role); err != nil {
		logger.Error("设置管理员失败", logger.ErrorField(err))
		return err
	}

	logger.Info("设置管理员",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.StringField("user_id", userID),
		logger.BoolField("is_admin", isAdmin),
	)

	return nil
}

func (s *ChatServiceImpl) GetGroupMembers(groupID string, page, pageSize int) ([]*model.GroupMember, int64, error) {
	return s.repo.GetGroupMembers(s.ctx, groupID, page, pageSize)
}

func (s *ChatServiceImpl) GetGroupMember(groupID, userID string) (*model.GroupMember, error) {
	return s.repo.GetGroupMember(s.ctx, groupID, userID)
}

func (s *ChatServiceImpl) GetGroupMemberIDs(groupID string) ([]string, error) {
	return s.repo.GetAllGroupMemberIDs(s.ctx, groupID)
}

func (s *ChatServiceImpl) GetGroup(groupID string) (*model.Group, error) {
	return s.repo.GetGroupByID(s.ctx, groupID)
}

func (s *ChatServiceImpl) JoinGroup(groupID, userID string) error {
	logger.Info("📋 [群组-加入] JoinGroup 被调用",
		logger.StringField("group_id", groupID),
		logger.StringField("user_id", userID))
	group, err := s.repo.GetGroupByID(s.ctx, groupID)
	if err != nil {
		return fmt.Errorf("查询群组失败: %w", err)
	}
	if group == nil {
		return fmt.Errorf("群组不存在")
	}

	existing, err := s.repo.GetGroupMember(s.ctx, groupID, userID)
	if err == nil && existing != nil {
		return fmt.Errorf("已在群组中")
	}

	now := time.Now()
	if err := s.repo.AddGroupMember(s.ctx, &model.GroupMember{
		GroupID:   groupID,
		UserID:    userID,
		Role:      model.GroupMemberRoleMember,
		MuteType:  model.MuteTypeNone,
		JoinedAt:  now,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return err
	}

	// 更新群组成员数
	if err := s.repo.UpdateGroupMemberCount(s.ctx, groupID); err != nil {
		logger.Warn("更新群组成员数失败", logger.ErrorField(err))
	}

	// 添加 ConversationParticipant 记录
	if err := s.repo.AddConversationParticipant(s.ctx, &model.ConversationParticipant{
		ConversationID: groupID,
		UserID:         userID,
		LastReadAt:     now,
		JoinedAt:       now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		logger.Warn("添加会话参与者失败", logger.ErrorField(err))
	}

	userName := s.resolveUserName(userID)
	msgContent := fmt.Sprintf("%s 已加入群组", userName)
	logger.Info("\x1b[36m📋 [群组-加入] 发送系统消息\x1b[0m",
		logger.StringField("group_id", groupID),
		logger.StringField("user_id", userID),
		logger.StringField("username", userName),
		logger.StringField("content", msgContent))
	s.sendGroupSystemMessage(groupID, msgContent)

	return nil
}

func (s *ChatServiceImpl) LeaveGroup(groupID, userID string) error {
	logger.Info("📋 [群组-退出] LeaveGroup 被调用",
		logger.StringField("group_id", groupID),
		logger.StringField("user_id", userID))
	group, err := s.repo.GetGroupByID(s.ctx, groupID)
	if err != nil {
		return fmt.Errorf("查询群组失败: %w", err)
	}
	if group == nil {
		return fmt.Errorf("群组不存在")
	}

	member, err := s.repo.GetGroupMember(s.ctx, groupID, userID)
	if err != nil || member == nil {
		return fmt.Errorf("您不是该群组成员")
	}

	memberIDs, err := s.repo.GetAllGroupMemberIDs(s.ctx, groupID)
	if err != nil {
		logger.Warn("获取群成员失败", logger.ErrorField(err))
	}

	isLastMember := len(memberIDs) <= 1

	if isLastMember {
		if err := s.repo.DeleteGroup(s.ctx, groupID); err != nil {
			logger.Error("解散群组失败", logger.ErrorField(err))
			return fmt.Errorf("退出群组失败")
		}
		logger.Info("📋 [群组-解散] 最后一个成员退出，群组已解散",
			logger.StringField("group_id", groupID),
			logger.StringField("user_id", userID))
		return nil
	}

	if group.OwnerID == userID {
		var newOwnerID string
		for _, mid := range memberIDs {
			if mid != userID {
				newOwnerID = mid
				break
			}
		}
		if newOwnerID != "" {
			if err := s.repo.TransferGroupOwner(s.ctx, groupID, newOwnerID); err != nil {
				logger.Warn("自动转让群主失败", logger.ErrorField(err))
			} else {
				logger.Info("📋 [群组-退出] 群主退出，自动转让群主",
					logger.StringField("group_id", groupID),
					logger.StringField("old_owner", userID),
					logger.StringField("new_owner", newOwnerID))
				newOwnerName := s.resolveUserName(newOwnerID)
				s.sendGroupSystemMessage(groupID, fmt.Sprintf("群主已退出，%s 已成为新群主", newOwnerName))
			}
		}
	}

	if err := s.repo.RemoveGroupMember(s.ctx, groupID, userID); err != nil {
		return err
	}

	if err := s.repo.UpdateGroupMemberCount(s.ctx, groupID); err != nil {
		logger.Warn("更新群组成员数失败", logger.ErrorField(err))
	}

	if err := s.repo.DeleteConversationForUser(s.ctx, groupID, userID); err != nil {
		logger.Warn("删除会话参与者失败", logger.ErrorField(err))
	}

	if group.OwnerID != userID {
		userName := s.resolveUserName(userID)
		msgContent := fmt.Sprintf("%s 已退出群组", userName)
		logger.Info("\x1b[33m📋 [群组-退出] 发送系统消息\x1b[0m",
			logger.StringField("group_id", groupID),
			logger.StringField("user_id", userID),
			logger.StringField("username", userName),
			logger.StringField("content", msgContent))
		s.sendGroupSystemMessage(groupID, msgContent)
	}

	return nil
}

func (s *ChatServiceImpl) resolveUserName(userID string) string {
	if cached, ok := s.userNameCache.Load(userID); ok {
		if name, ok2 := cached.(string); ok2 && name != "" {
			return name
		}
	}

	uid := toInt64(userID)
	if uid <= 0 {
		return userID
	}

	if s.userClient != nil {
		resolveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		userInfo, err := s.userClient.GetUserInfo(resolveCtx, uid)
		cancel()

		if err == nil && userInfo != nil && userInfo.Username != "" {
			s.userNameCache.Store(userID, userInfo.Username)
			return userInfo.Username
		}
		if err != nil {
			logger.Warn("📋 [群组] gRPC解析用户名失败，尝试数据库直查",
				logger.StringField("user_id", userID),
				logger.ErrorField(err))
		}
	}

	username, dbErr := s.repo.GetUsernameByID(s.ctx, uid)
	if dbErr == nil && username != "" {
		s.userNameCache.Store(userID, username)
		logger.Info("📋 [群组] 数据库直查解析用户名成功",
			logger.StringField("user_id", userID),
			logger.StringField("username", username))
		return username
	}
	if dbErr != nil {
		logger.Warn("📋 [群组] 数据库直查也失败",
			logger.StringField("user_id", userID),
			logger.ErrorField(dbErr))
	}

	return userID
}

func (s *ChatServiceImpl) resolveUserNames(userIDs []string) []string {
	logger.Info("📋 [群组] resolveUserNames 被调用",
		logger.StringField("user_ids", strings.Join(userIDs, ",")))
	names := make([]string, len(userIDs))
	for i, uid := range userIDs {
		names[i] = s.resolveUserName(uid)
	}
	logger.Info("📋 [群组] resolveUserNames 完成",
		logger.StringField("resolved_names", strings.Join(names, ",")))
	return names
}

func (s *ChatServiceImpl) sendGroupSystemMessage(groupID, content string) {
	logger.Info("📋 [群组] sendGroupSystemMessage 被调用",
		logger.StringField("group_id", groupID),
		logger.StringField("content", content))
	now := time.Now()
	sysMsg := &model.Message{
		ID:             uuid.New().String(),
		ConversationID: groupID,
		ChatID:         groupID,
		ChatType:       int(model.ChatTypeGroup),
		SenderID:       "system",
		MessageType:    int(model.MessageTypeSystem),
		Content:        content,
		Status:         "sent",
		Channel:        "system",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.repo.CreateMessage(s.ctx, sysMsg); err != nil {
		logger.Warn("创建群系统消息失败", logger.ErrorField(err))
		return
	}
	if err := s.repo.UpdateConversationLastMessage(s.ctx, groupID, sysMsg.ID); err != nil {
		logger.Warn("更新会话最后消息失败", logger.ErrorField(err))
	}

	if s.eventBus != nil {
		memberIDs, err := s.repo.GetAllGroupMemberIDs(s.ctx, groupID)
		if err != nil {
			logger.Warn("获取群成员列表失败，系统消息不会实时推送", logger.ErrorField(err))
			return
		}
		if len(memberIDs) == 0 {
			logger.Warn("群里没有成员了，不发送系统消息推送")
			return
		}
		event := &types.MessageEvent{
			ID:           sysMsg.ID,
			ChatID:       groupID,
			ChatType:     types.ChatTypeGroup,
			SenderID:     "system",
			MessageType:  types.MessageTypeSystem,
			Content:      content,
			Timestamp:    now,
			RecipientIDs: memberIDs,
		}
		if err := s.eventBus.PublishOutgoingMessageEvent(s.ctx, event); err != nil {
			logger.Warn("推送群系统消息事件失败", logger.ErrorField(err))
		}
	}
}

func (s *ChatServiceImpl) GetConversationList(userID string, page, pageSize int) ([]*model.ConversationItem, int64, error) {
	items, total, err := s.repo.GetConversationList(s.ctx, userID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	logger.Info("\x1b[36m🔵 [后端] GetConversationList 开始\x1b[0m",
		logger.StringField("user_id", userID),
		logger.IntField("item_count", len(items)),
		logger.BoolField("has_user_client", s.userClient != nil))

	// 默认非群组会话设为非好友，需要检查
	for _, item := range items {
		if item.ChatType == int(model.ChatTypeGroup) || item.ChatType == 3 {
			item.IsFriend = true
		} else {
			item.IsFriend = false
		}
	}

	// 如果有 userClient，并行获取私聊会话的对方用户信息
	if s.userClient != nil {
		type userResult struct {
			idx    int
			name   string
			avatar string
		}
		resultsChan := make(chan userResult, len(items))

		var wg sync.WaitGroup
		for idx, item := range items {
			if item.ChatType != int(model.ChatTypePrivate) {
				continue
			}

			parts := strings.Split(item.ChatID, "_")
			var otherUserIDStr string
			if len(parts) == 3 && parts[0] == "private" {
				if parts[1] == userID {
					otherUserIDStr = parts[2]
				} else {
					otherUserIDStr = parts[1]
				}
			} else if len(parts) == 2 {
				if parts[0] == userID {
					otherUserIDStr = parts[1]
				} else {
					otherUserIDStr = parts[0]
				}
			}

			if otherUserIDStr == "" {
				continue
			}

			otherUserID := toInt64(otherUserIDStr)
			if otherUserID <= 0 {
				continue
			}

			wg.Add(1)
			go func(i int, oid int64) {
				defer wg.Done()
				infoCtx, infoCancel := context.WithTimeout(context.Background(), 10*time.Second)
				userInfo, err := s.userClient.GetUserInfo(infoCtx, oid)
				infoCancel()

				var name, avatar string
				if err == nil && userInfo != nil {
					name = userInfo.Username
					avatar = userInfo.Avatar
				}
				resultsChan <- userResult{
					idx:    i,
					name:   name,
					avatar: avatar,
				}
			}(idx, otherUserID)
		}

		// 等待所有查询完成
		go func() {
			wg.Wait()
			close(resultsChan)
		}()

		// 处理结果
		for res := range resultsChan {
			if res.name != "" {
				items[res.idx].Name = res.name
			}
			if res.avatar != "" {
				items[res.idx].Avatar = res.avatar
			}
		}
	}

	// 同步检查好友关系
	if s.contactChecker != nil {
		logger.Info("\x1b[36m🔵 [Chat] 开始检查好友关系\x1b[0m",
			logger.IntField("item_count", len(items)))

		type result struct {
			idx         int
			isFriend    bool
			isBlocked   bool
			err         error
			otherUserID string
		}
		resultsChan := make(chan result, len(items))

		// 并行检查所有好友关系
		var wg sync.WaitGroup
		for idx, item := range items {
			if item.ChatType != int(model.ChatTypePrivate) {
				continue
			}

			parts := strings.Split(item.ChatID, "_")
			var otherUserID string
			if len(parts) == 3 && parts[0] == "private" {
				if parts[1] == userID {
					otherUserID = parts[2]
				} else {
					otherUserID = parts[1]
				}
			} else if len(parts) == 2 {
				if parts[0] == userID {
					otherUserID = parts[1]
				} else {
					otherUserID = parts[0]
				}
			}

			if otherUserID == "" {
				continue
			}

			wg.Add(1)
			go func(i int, chatID, oid string) {
				defer wg.Done()
				relCtx, relCancel := context.WithTimeout(context.Background(), 10*time.Second)
				res, err := s.contactChecker.CheckFriendship(relCtx, userID, oid)
				relCancel()

				var isFriend, isBlocked bool
				if res != nil {
					isFriend = res.IsFriend
					isBlocked = res.IsBlocked
				}
				resultsChan <- result{
					idx:         i,
					isFriend:    isFriend,
					isBlocked:   isBlocked,
					err:         err,
					otherUserID: oid,
				}
			}(idx, item.ChatID, otherUserID)
		}

		// 等待所有检查完成
		go func() {
			wg.Wait()
			close(resultsChan)
		}()

		// 处理结果
		for res := range resultsChan {
			if res.err != nil {
				logger.Warn("\x1b[33m🟡 [Chat] 好友关系检查失败，保留默认值\x1b[0m",
					logger.StringField("chat_id", items[res.idx].ChatID),
					logger.StringField("other_user", res.otherUserID),
					logger.ErrorField(res.err))
			} else {
				items[res.idx].IsFriend = res.isFriend
				items[res.idx].IsBlocked = res.isBlocked
				logger.Info("\x1b[36m🔵 [Chat] 好友关系检查\x1b[0m",
					logger.StringField("chat_id", items[res.idx].ChatID),
					logger.StringField("other_user", res.otherUserID),
					logger.BoolField("is_friend", res.isFriend),
					logger.BoolField("is_blocked", res.isBlocked),
					logger.BoolField("cached", res.err == nil)) // cached 简化处理
			}
		}
	}

	return items, total, nil
}

func (s *ChatServiceImpl) GetUnreadCounts(userID string, chatIDs []string) (map[string]int64, error) {
	return s.repo.GetUnreadCounts(s.ctx, userID, chatIDs)
}

func (s *ChatServiceImpl) GetTotalUnreadCount(userID string) (int64, error) {
	return s.repo.GetTotalUnreadCount(s.ctx, userID)
}

const (
	// maxReadReceiptBatch 单次查询的消息条数上限，避免一次请求拉出整页历史成员矩阵。
	maxReadReceiptBatch = 50
	// maxReadReceiptReaders 返回已读成员明细的最大成员数，超过则只返回人数。
	maxReadReceiptReaders = 100
)

// GetMessageReadReceipts 按发送者视角聚合已读回执。
//
// 设计要点：
//   - 不落「消息 × 成员」的已读记录（大群下写放大为 O(消息数 × 成员数)），
//     而是复用会话已读位点 last_read_seq：成员位点 ≥ 消息 seq 即视为已读；
//   - 只返回请求者本人发送的消息，避免成员互相窥探阅读行为；
//   - 一次拉取参与者位点后在内存聚合，查询次数与消息条数无关；
//   - 成员数超过阈值时不返回明细，只返回人数，控制响应体积。
func (s *ChatServiceImpl) GetMessageReadReceipts(userID, chatID string, messageIDs []string, includeReaders bool) ([]*model.MessageReadReceipt, error) {
	if userID == "" || chatID == "" {
		return nil, types.ErrChatAccessDenied
	}
	if len(messageIDs) == 0 {
		return nil, types.ErrChatBadRequest
	}
	if len(messageIDs) > maxReadReceiptBatch {
		return nil, types.ErrChatBadRequest
	}
	if err := s.EnsureChatAccess(userID, chatID, model.ChatType(0)); err != nil {
		logger.Warn("拒绝越权查询已读回执",
			logger.StringField("user_id", userID),
			logger.StringField("chat_id", chatID),
			logger.ErrorField(err))
		return nil, err
	}

	// 广播会话没有成员位点概念，不提供回执。
	if chatID == "broadcast" {
		return []*model.MessageReadReceipt{}, nil
	}

	messages, err := s.repo.GetMessagesByIDs(s.ctx, messageIDs)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return []*model.MessageReadReceipt{}, nil
	}

	positions, err := s.repo.GetReadPositions(s.ctx, chatID)
	if err != nil {
		return nil, err
	}

	// 按请求顺序输出，同一会话内的成员位点只取一次。
	byID := make(map[string]*model.Message, len(messages))
	for _, msg := range messages {
		byID[msg.ID] = msg
	}

	receipts := make([]*model.MessageReadReceipt, 0, len(messageIDs))
	seen := make(map[string]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		msg := byID[id]
		// 消息必须属于当前会话且由请求者发送，否则不返回（既不报错也不泄露）。
		if msg == nil || msg.ChatID != chatID || msg.SenderID != userID {
			continue
		}

		receipt := &model.MessageReadReceipt{
			MessageID: msg.ID,
			Seq:       msg.Seq,
		}
		for _, p := range positions {
			if p.UserID == msg.SenderID {
				continue
			}
			receipt.MemberCount++
			if msg.Seq > 0 && p.LastReadSeq >= msg.Seq {
				receipt.ReadCount++
				if includeReaders && len(positions) <= maxReadReceiptReaders {
					receipt.ReaderIDs = append(receipt.ReaderIDs, p.UserID)
				}
			}
		}
		receipt.AllRead = receipt.MemberCount > 0 && receipt.ReadCount >= receipt.MemberCount
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

const (
	// maxSyncChats 单次离线同步的会话数上限，避免重连时一次拉出全部会话历史。
	maxSyncChats = 100
	// defaultSyncLimit / maxSyncLimit 每会话返回条数：默认 50，上限 200。
	defaultSyncLimit = 50
	maxSyncLimit     = 200
	// syncChatConcurrency 单次离线同步并行处理的会话数上限。
	// 每个会话要串行执行 3 次 DAO 查询，串行处理 100 个会话就是 300 次往返；
	// 固定 8 路能把重连首屏延迟压到约 1/8，同时每个 goroutine 会占用一条数据库连接，
	// 不设上限会在重连高峰打满连接池、挤占其他请求，因此取 8。
	syncChatConcurrency = 8
)

const (
	// changeLogRetention 原位变更日志保留期。超过保留期后，长期离线设备重新进入会话时
	// 会走完整历史拉取（拿到的是变更后的内容），不再需要增量补拉，日志可以回收。
	changeLogRetention = 7 * 24 * time.Hour
	// changeLogCleanupInterval 清理周期；清理是幂等的低优先级任务，不需要太频繁。
	changeLogCleanupInterval = 6 * time.Hour
)

// startChangeLogCleanup 启动原位变更日志的后台回收。
// message_changes 是会话级共享日志（同一会话的所有成员共用），
// 不能像墓碑那样按某个用户的 cleared_seq 删除（别人可能还需要补拉），只能按时间保留期回收。
func (s *ChatServiceImpl) startChangeLogCleanup() {
	go func() {
		s.purgeExpiredChanges(time.Now())
		ticker := time.NewTicker(changeLogCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.purgeExpiredChanges(time.Now())
			}
		}
	}()
}

// purgeExpiredChanges 删除保留期之外的变更日志，返回删除行数。
func (s *ChatServiceImpl) purgeExpiredChanges(now time.Time) int64 {
	n, err := s.repo.PurgeMessageChangesBefore(s.ctx, now.Add(-changeLogRetention))
	if err != nil {
		logger.Warn("清理过期原位变更日志失败", logger.ErrorField(err))
		return 0
	}
	if n > 0 {
		logger.Info("已清理过期原位变更日志", logger.Int64Field("rows", n))
	}
	return n
}

// SyncOfflineMessages 批量补齐客户端离线期间的增量消息。
//
// 为什么需要它：实时推送走 Redis PubSub，订阅者掉线期间的消息不会重投；
// 逐会话调用历史接口在会话多时是 N 次往返。这里一次请求带多个游标，
// 服务端一次查出访问白名单与各会话最大 seq，再按会话走 (chat_id, seq) 索引取增量。
//
// 语义：
//   - 游标低于本人清空水位线时抬升到水位线，避免把已清空的历史重新拉回；
//   - 非成员/已退群/被踢的会话静默跳过，不泄露会话是否存在；
//   - LatestSeq 即使本次没有新消息也返回，客户端可安全推进游标；
//   - 每会话最多 limit 条，HasMore 标记还有更多，客户端可用返回的最大 seq 继续拉。
func (s *ChatServiceImpl) SyncOfflineMessages(userID string, cursors []model.SyncCursor, limit int) ([]*model.ChatSyncResult, error) {
	if userID == "" {
		return nil, types.ErrChatAccessDenied
	}
	if len(cursors) == 0 || len(cursors) > maxSyncChats {
		return nil, types.ErrChatBadRequest
	}
	if limit <= 0 {
		limit = defaultSyncLimit
	}
	if limit > maxSyncLimit {
		limit = maxSyncLimit
	}

	chatIDs := make([]string, 0, len(cursors))
	cursorByChat := make(map[string]int64, len(cursors))
	deletedCursorByChat := make(map[string]int64, len(cursors))
	updatedCursorByChat := make(map[string]int64, len(cursors))
	for _, c := range cursors {
		if c.ChatID == "" {
			continue
		}
		if _, ok := cursorByChat[c.ChatID]; ok {
			continue
		}
		cursorByChat[c.ChatID] = c.LastSeq
		deletedCursorByChat[c.ChatID] = c.DeletedSeq
		updatedCursorByChat[c.ChatID] = c.UpdatedSeq
		chatIDs = append(chatIDs, c.ChatID)
	}
	if len(chatIDs) == 0 {
		return nil, types.ErrChatBadRequest
	}

	// 参与者位点既提供清空水位线，也充当访问白名单。
	watermarks, err := s.repo.GetParticipantWatermarks(s.ctx, userID, chatIDs)
	if err != nil {
		return nil, err
	}
	latestSeqs, err := s.repo.GetMaxSeqByChatIDs(s.ctx, chatIDs)
	if err != nil {
		return nil, err
	}

	// 固定 syncChatConcurrency 路的并发信号量 + WaitGroup：并发是为了压重连首屏延迟
	// （每会话 3 次串行往返，串行处理 100 个会话就是 300 次），但每个 goroutine 会占用
	// 一条数据库连接，无上限并发会在重连高峰打满连接池，所以用 channel 做硬上限。
	// 结果按下标落位到 results，每个 goroutine 只写自己的下标、不共享写，
	// 因此无需额外加锁，且返回顺序天然与入参 chatIDs 一致。
	results := make([]*model.ChatSyncResult, len(chatIDs))
	errs := make([]error, len(chatIDs))
	token := make(chan struct{}, syncChatConcurrency)
	var wg sync.WaitGroup
	for i, chatID := range chatIDs {
		wm, ok := watermarks[chatID]
		if !ok {
			logger.Debug("离线同步跳过非成员会话",
				logger.StringField("user_id", userID),
				logger.StringField("chat_id", chatID))
			continue
		}
		wg.Add(1)
		go func(i int, chatID string, wm dao.ParticipantWatermark) {
			defer wg.Done()
			token <- struct{}{}
			defer func() { <-token }()
			results[i], errs[i] = s.syncOneChat(userID, chatID, wm,
				latestSeqs[chatID], cursorByChat[chatID],
				deletedCursorByChat[chatID], updatedCursorByChat[chatID], limit)
		}(i, chatID, wm)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	// 非成员会话的下标处为空，按下标顺序压缩到返回切片（不依赖 append 的完成顺序）。
	ordered := make([]*model.ChatSyncResult, len(chatIDs))
	n := 0
	for _, r := range results {
		if r != nil {
			ordered[n] = r
			n++
		}
	}
	return ordered[:n], nil
}

// syncOneChat 处理单个会话的离线增量：消息、删除墓碑、原位变更各一次查询。
// 供 SyncOfflineMessages 并行调用，只读写入参与自身局部变量，不共享状态。
func (s *ChatServiceImpl) syncOneChat(userID, chatID string, wm dao.ParticipantWatermark, latestSeq, afterSeq, deletedAfter, updatedAfter int64, limit int) (*model.ChatSyncResult, error) {
	if afterSeq < wm.ClearedSeq {
		afterSeq = wm.ClearedSeq
	}

	messages, err := s.repo.GetMessagesAfterSeq(s.ctx, userID, chatID, afterSeq, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	// 删除墓碑：删除动作无法用消息 seq 游标表达（老消息也可能在今天被删），
	// 因此每条会话额外维护 deleted_seq 游标，按墓碑自增 ID 增量下发被删消息 ID。
	deletions, err := s.repo.GetMessageDeletionsAfter(s.ctx, userID, chatID, deletedAfter, limit+1)
	if err != nil {
		return nil, err
	}
	deletedHasMore := len(deletions) > limit
	if deletedHasMore {
		deletions = deletions[:limit]
	}
	deletedIDs := make([]string, 0, len(deletions))
	nextDeleted := deletedAfter
	for _, d := range deletions {
		deletedIDs = append(deletedIDs, d.MessageID)
		nextDeleted = d.ID
	}

	// 原位变更日志：编辑/撤回不改变消息 seq，必须用独立的 updated_seq 游标补拉，
	// 否则离线期间被改写的旧消息在重连后永远停留在旧内容。
	// 已被本人清空的历史（seq <= cleared_seq）不再可见，其变更无需下发，
	// 但游标仍要推进，避免每次同步都重复拉取同一批无效日志。
	changes, err := s.repo.GetMessageChangesAfter(s.ctx, chatID, updatedAfter, limit+1)
	if err != nil {
		return nil, err
	}
	updatedHasMore := len(changes) > limit
	if updatedHasMore {
		changes = changes[:limit]
	}
	nextUpdated := updatedAfter
	visibleChanges := make([]*model.MessageChange, 0, len(changes))
	for _, ch := range changes {
		if ch.ID > nextUpdated {
			nextUpdated = ch.ID
		}
		if wm.ClearedSeq > 0 && ch.MessageSeq > 0 && ch.MessageSeq <= wm.ClearedSeq {
			continue
		}
		visibleChanges = append(visibleChanges, ch)
	}

	return &model.ChatSyncResult{
		ChatID:            chatID,
		Messages:          messages,
		LatestSeq:         latestSeq,
		ClearedSeq:        wm.ClearedSeq,
		HasMore:           hasMore,
		DeletedMessageIDs: deletedIDs,
		DeletedSeq:        nextDeleted,
		DeletedHasMore:    deletedHasMore,
		Changes:           visibleChanges,
		UpdatedSeq:        nextUpdated,
		UpdatedHasMore:    updatedHasMore,
	}, nil
}

func (s *ChatServiceImpl) ForwardMessage(messageID string, targetChatIDs []string, senderID string) ([]*model.Message, error) {
	originalMsg, err := s.repo.GetMessageByID(s.ctx, messageID)
	if err != nil || originalMsg == nil {
		return nil, fmt.Errorf("原始消息不存在")
	}

	// 转发等价于「读原消息 + 写目标会话」：原会话不可访问时拒绝，
	// 否则任何人只要拿到 message_id 就能把他人消息转发到自己会话里查看。
	if err := s.EnsureChatAccess(senderID, originalMsg.ChatID, model.ChatType(originalMsg.ChatType)); err != nil {
		logger.Warn("拒绝越权转发消息",
			logger.StringField("user_id", senderID),
			logger.StringField("message_id", messageID),
			logger.StringField("chat_id", originalMsg.ChatID))
		return nil, err
	}

	var forwardedMessages []*model.Message
	for _, targetChatID := range targetChatIDs {
		chatType := model.ChatTypePrivate
		group, _ := s.repo.GetGroupByID(s.ctx, targetChatID)
		if group != nil {
			chatType = model.ChatTypeGroup
		}

		metadata := map[string]string{
			"forwarded_from":  originalMsg.ChatID,
			"original_msg_id": originalMsg.ID,
		}

		// 转发是服务端内部动作，不携带客户端幂等键（每个目标会话各自生成新消息）。
		msg, recipientIDs, _, err := s.SendMessage(senderID, targetChatID, chatType, model.MessageType(originalMsg.MessageType), originalMsg.Content, metadata, "", nil, "")
		if err != nil {
			logger.Error("转发消息失败",
				logger.StringField("target_chat_id", targetChatID),
				logger.StringField("chat_type", fmt.Sprintf("%d", int(chatType))),
				logger.ErrorField(err))
			continue
		}

		if originalMsg.MediaURL != "" {
			msg.MediaURL = originalMsg.MediaURL
			msg.MediaMeta = originalMsg.MediaMeta
			if err := s.repo.UpdateMessage(s.ctx, msg); err != nil {
				logger.Error("更新转发消息媒体信息失败", logger.ErrorField(err))
			}
		}

		if s.eventBus != nil {
			var mediaMeta json.RawMessage
			if msg.MediaMeta != nil {
				mediaMeta = json.RawMessage(msg.MediaMeta)
			}
			event := types.NewMediaMessageEvent(
				msg.ID,
				msg.ChatID,
				types.ChatType(msg.ChatType),
				msg.SenderID,
				types.MessageType(msg.MessageType),
				msg.Content,
				msg.MediaURL,
				mediaMeta,
				metadata,
				"",
				nil,
				recipientIDs,
			)
			if err := s.eventBus.PublishOutgoingMessageEvent(s.ctx, event); err != nil {
				logger.Error("发布转发消息出站事件失败", logger.ErrorField(err))
			}
		}

		forwardedMessages = append(forwardedMessages, msg)
	}

	return forwardedMessages, nil
}

func (s *ChatServiceImpl) DeleteChat(chatID string, userID string, chatType model.ChatType) error {
	logger.Info("删除聊天",
		logger.StringField("chat_id", chatID),
		logger.StringField("user_id", userID),
		logger.IntField("chat_type", int(chatType)))

	switch chatType {
	case model.ChatTypePrivate:
		// 私聊：删除当前用户的会话参与记录
		if err := s.repo.DeleteConversationForUser(s.ctx, chatID, userID); err != nil {
			logger.Error("删除私聊会话失败", logger.ErrorField(err))
			return err
		}
	case model.ChatTypeGroup:
		// 群聊：退出群组
		if err := s.LeaveGroup(chatID, userID); err != nil {
			logger.Error("退出群组失败", logger.ErrorField(err))
			return err
		}
	}

	return nil
}
