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
	SendMessage(senderID, chatID string, chatType model.ChatType, msgType model.MessageType, content string, metadata map[string]string, replyToID string, mentionIDs []string) (*model.Message, []string, error)
	GetMessageHistory(chatID string, chatType model.ChatType, beforeTime time.Time, limit int) ([]*model.Message, bool, error)
	SearchMessages(chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error)
	MarkMessagesRead(msgIDs []string, userID, chatID string) error
	WithdrawMessage(msgID, userID string) error
	EditMessage(msgID, userID, content string) (*model.Message, error)
	DeleteChatHistory(chatID string) error

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

		// 群聊消息：检查发送者是否是群成员
		if event.ChatType == types.ChatTypeGroup {
			member, err := s.repo.GetGroupMember(s.ctx, event.ChatID, event.SenderID)
			if err != nil {
				logger.Warn("检查群成员身份失败", logger.ErrorField(err))
			}
			if member == nil {
				logger.Warn("非群组成员尝试发送消息，已拒绝",
					logger.StringField("sender_id", event.SenderID),
					logger.StringField("chat_id", event.ChatID))
				return nil
			}
		}

		// 🔴 3️⃣ 收到 Kafka 消息
		if event.ChatType == types.ChatTypePrivate {
			logger.Info("\x1b[34m🔴 3️⃣ [Kafka-ChatService] 收到私聊消息\x1b[0m",
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
		if event.ChatType == types.ChatTypePrivate {
			recipientIDs = s.getPrivateChatRecipient(event.ChatID, event.SenderID)
		} else if event.ChatType == types.ChatTypeGroup {
			recipientIDs, _ = s.repo.GetAllGroupMemberIDs(s.ctx, event.ChatID)
		}
		if len(recipientIDs) == 0 && event.ChatType == types.ChatTypePrivate {
			// 保底，确保有接收者
			recipientIDs = []string{event.SenderID}
		}
		event.RecipientIDs = recipientIDs

		// 🔴 4️⃣ 直接发布到 outgoing Kafka！不等待数据库保存和审核！
		if event.ChatType == types.ChatTypePrivate {
			logger.Info("\x1b[35m🔴 4️⃣ [ChatService-Kafka] 发布私聊消息到 outgoing topic\x1b[0m",
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

		// 2️⃣ 异步处理所有其他逻辑（保存数据库、审核、翻译、推送等）
		go func() {
			// 确保会话和参与者存在！
			if event.ChatType == types.ChatTypePrivate {
				parts := strings.Split(event.ChatID, "_")
				if len(parts) == 3 && parts[0] == "private" {
					// private_{a}_{b}
					_, _ = s.repo.GetOrCreatePrivateConversation(s.ctx, parts[1], parts[2])
				} else if len(parts) == 2 {
					// {a}_{b}
					_, _ = s.repo.GetOrCreatePrivateConversation(s.ctx, parts[0], parts[1])
				}
			}

			go func() {
				if s.moderationClient != nil {
					s.moderateMessage(event)
				}
			}()

			// 保存消息到数据库（使用原始ID）
			msgID := event.ID // 保持原始ID不变！
			now := time.Now()
			msg := &model.Message{
				ID:             msgID,
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

			if err := s.repo.CreateMessage(s.ctx, msg); err != nil {
				logger.Error("创建消息失败", logger.ErrorField(err))
				return
			}

			if err := s.repo.UpdateConversationLastMessage(s.ctx, event.ChatID, msgID); err != nil {
				logger.Warn("更新会话最后消息失败", logger.ErrorField(err))
			}

			// 异步处理翻译和推送
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

var botMentionRegex = regexp.MustCompile(`@(\S+)`)

func (s *ChatServiceImpl) handleBotMentions(event *types.MessageEvent) {
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
		rawID := rawID
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
		logger.Debug("内容审核跳过", logger.ErrorField(err))
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

	if err := s.repo.MarkMessagesRead(s.ctx, event.MessageIDs); err != nil {
		logger.Error("标记消息已读失败", logger.ErrorField(err))
		return err
	}

	if event.ChatID != "" && event.ReaderID != "" {
		_ = s.repo.UpdateParticipantLastRead(s.ctx, event.ChatID, event.ReaderID)
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

func (s *ChatServiceImpl) getPrivateChatRecipient(chatID, senderID string) []string {
	parts := strings.Split(chatID, "_")
	if len(parts) == 3 && parts[0] == "private" {
		switch senderID {
		case parts[1]:
			return []string{parts[2], senderID} // 返回对方和自己
		case parts[2]:
			return []string{parts[1], senderID} // 返回对方和自己
		}
	} else if len(parts) == 2 {
		switch senderID {
		case parts[0]:
			return []string{parts[1], senderID} // 返回对方和自己
		case parts[1]:
			return []string{parts[0], senderID} // 返回对方和自己
		}
	}

	logger.Warn("无法解析单聊会话ID获取接收者",
		logger.StringField("chat_id", chatID),
		logger.StringField("sender_id", senderID),
	)
	return []string{}
}

func (s *ChatServiceImpl) SendMessage(senderID, chatID string, chatType model.ChatType, msgType model.MessageType, content string, metadata map[string]string, replyToID string, mentionIDs []string) (*model.Message, []string, error) {
	var actualChatID string
	var targetUserID string

	if chatType == model.ChatTypePrivate {
		parts := strings.Split(chatID, "_")
		if len(parts) == 3 && parts[0] == "private" {
			// 格式: private_{a}_{b}
			if parts[1] == senderID {
				targetUserID = parts[2]
			} else {
				targetUserID = parts[1]
			}
			// 确保 conversation 存在
			conv, err := s.repo.GetOrCreatePrivateConversation(s.ctx, parts[1], parts[2])
			if err != nil {
				logger.Error("获取或创建私聊会话失败", logger.ErrorField(err))
				return nil, nil, err
			}
			actualChatID = conv.ID
			logger.Info("私聊消息，使用会话 ID",
				logger.StringField("sender_id", senderID),
				logger.StringField("target_user_id", targetUserID),
				logger.StringField("conversation_id", actualChatID))
		} else if len(parts) == 2 {
			// 格式: {a}_{b}
			if parts[0] == senderID {
				targetUserID = parts[1]
			} else {
				targetUserID = parts[0]
			}
			conv, err := s.repo.GetOrCreatePrivateConversation(s.ctx, parts[0], parts[1])
			if err != nil {
				logger.Error("获取或创建私聊会话失败", logger.ErrorField(err))
				return nil, nil, err
			}
			actualChatID = conv.ID
		} else {
			// 格式: 单个用户ID
			targetUserID = chatID
			conv, err := s.repo.GetOrCreatePrivateConversation(s.ctx, senderID, targetUserID)
			if err != nil {
				logger.Error("获取或创建私聊会话失败", logger.ErrorField(err))
				return nil, nil, err
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
			return nil, nil, fmt.Errorf("您不是该群组成员，无法发送消息")
		}
	}

	// 私聊跳过审核，只有群聊才审核
	if s.enableModeration && s.moderationClient != nil && senderID != "system" && senderID != "" && content != "" && chatType == model.ChatTypeGroup {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		rejected, err := s.moderationClient.ModerateContent(ctx, content, actualChatID, "text")
		cancel()
		if err != nil {
			logger.Warn("消息审核调用失败，放行消息", logger.ErrorField(err))
		} else if rejected {
			return nil, nil, types.ErrContentRejected
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

	if err := s.repo.CreateMessage(s.ctx, msg); err != nil {
		logger.Error("创建消息失败", logger.ErrorField(err))
		return nil, nil, err
	}

	if err := s.repo.UpdateConversationLastMessage(s.ctx, actualChatID, msgID); err != nil {
		logger.Warn("更新会话最后消息失败", logger.ErrorField(err))
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
		logger.StringField("msg_id", msgID),
		logger.StringField("chat_id", actualChatID),
		logger.IntField("chat_type", int(chatType)),
		logger.IntField("recipient_count", len(recipientIDs)),
	)

	return msg, recipientIDs, nil
}

func (s *ChatServiceImpl) GetMessageHistory(chatID string, chatType model.ChatType, beforeTime time.Time, limit int) ([]*model.Message, bool, error) {
	// 如果是私聊且 chatID 是用户 ID，需要先获取当前用户 ID 来构建会话 ID
	// 注意：这里我们假设调用方会处理这个问题，或者我们需要修改方法签名来传入 senderID
	// 暂时保持原样，先让发送消息能工作
	messages, err := s.repo.GetMessagesByChatID(s.ctx, chatID, beforeTime, limit+1)
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

func (s *ChatServiceImpl) SearchMessages(chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error) {
	return s.repo.SearchMessages(s.ctx, chatID, chatType, keyword, startTime, endTime, page, pageSize)
}

func (s *ChatServiceImpl) MarkMessagesRead(msgIDs []string, userID, chatID string) error {
	if len(msgIDs) == 0 {
		if chatID != "" && userID != "" {
			_ = s.repo.UpdateParticipantLastRead(s.ctx, chatID, userID)
			_ = s.repo.MarkAllChatMessagesRead(s.ctx, chatID, userID)
		}
		return nil
	}
	if err := s.repo.MarkMessagesRead(s.ctx, msgIDs); err != nil {
		return err
	}
	if chatID != "" && userID != "" {
		_ = s.repo.UpdateParticipantLastRead(s.ctx, chatID, userID)
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

	if err := s.repo.WithdrawMessage(s.ctx, msgID); err != nil {
		logger.Error("撤回消息失败", logger.ErrorField(err))
		return err
	}

	if s.eventBus != nil {
		var recipientIDs []string
		chatType := types.ChatType(msg.ChatType)
		if chatType == types.ChatTypePrivate {
			recipientIDs = s.getPrivateChatRecipient(msg.ChatID, msg.SenderID)
		} else if chatType == types.ChatTypeGroup {
			recipientIDs, _ = s.repo.GetAllGroupMemberIDs(s.ctx, msg.ChatID)
		}
		if len(recipientIDs) == 0 {
			recipientIDs = []string{msg.SenderID}
		}

		withdrawEvent := &types.MessageWithdrawEvent{
			EventType:    types.EventTypeMessageWithdraw,
			MessageID:    msgID,
			ChatID:       msg.ChatID,
			ChatType:     chatType,
			SenderID:     msg.SenderID,
			RecipientIDs: recipientIDs,
			Timestamp:    time.Now(),
		}
		if err := s.eventBus.PublishOutgoingWithdrawEvent(s.ctx, withdrawEvent); err != nil {
			logger.Error("发布撤回事件失败", logger.ErrorField(err))
		}
	}

	return nil
}

func (s *ChatServiceImpl) EditMessage(msgID, userID, content string) (*model.Message, error) {
	msg, err := s.repo.GetMessageByID(s.ctx, msgID)
	if err != nil {
		logger.Error("获取消息失败", logger.ErrorField(err))
		return nil, fmt.Errorf("消息不存在")
	}
	if msg == nil {
		return nil, fmt.Errorf("消息不存在")
	}

	if msg.SenderID != userID {
		return nil, fmt.Errorf("只能编辑自己的消息")
	}

	if err := s.repo.EditMessage(s.ctx, msgID, content); err != nil {
		logger.Error("编辑消息失败", logger.ErrorField(err))
		return nil, err
	}

	return s.repo.GetMessageByID(s.ctx, msgID)
}

func (s *ChatServiceImpl) DeleteChatHistory(chatID string) error {
	logger.Info("\x1b[36m🔵 [Chat] 清理聊天记录\x1b[0m", logger.StringField("chat_id", chatID))
	if err := s.repo.DeleteChatHistory(s.ctx, chatID); err != nil {
		logger.Error("\x1b[31m🔴 [Chat] 清理聊天记录失败\x1b[0m", logger.ErrorField(err))
		return err
	}
	logger.Info("\x1b[32m🟢 [Chat] 清理聊天记录成功\x1b[0m", logger.StringField("chat_id", chatID))
	return nil
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

func (s *ChatServiceImpl) ForwardMessage(messageID string, targetChatIDs []string, senderID string) ([]*model.Message, error) {
	originalMsg, err := s.repo.GetMessageByID(s.ctx, messageID)
	if err != nil || originalMsg == nil {
		return nil, fmt.Errorf("原始消息不存在")
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

		msg, recipientIDs, err := s.SendMessage(senderID, targetChatID, chatType, model.MessageType(originalMsg.MessageType), originalMsg.Content, metadata, "", nil)
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

	if chatType == model.ChatTypePrivate {
		// 私聊：删除当前用户的会话参与记录
		if err := s.repo.DeleteConversationForUser(s.ctx, chatID, userID); err != nil {
			logger.Error("删除私聊会话失败", logger.ErrorField(err))
			return err
		}
	} else if chatType == model.ChatTypeGroup {
		// 群聊：退出群组
		if err := s.LeaveGroup(chatID, userID); err != nil {
			logger.Error("退出群组失败", logger.ErrorField(err))
			return err
		}
	}

	return nil
}
