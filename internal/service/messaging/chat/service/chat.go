package service

import (
	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ChatService 聊天服务接口
type ChatService interface {
	SendMessage(senderID, chatID string, chatType model.ChatType, msgType model.MessageType, content string, metadata map[string]string, replyToID string, mentionIDs []string) (*model.Message, []string, error)
	GetMessageHistory(chatID string, chatType model.ChatType, beforeTime time.Time, limit int) ([]*model.Message, bool, error)
	SearchMessages(chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error)
	MarkMessagesRead(msgIDs []string) error
	WithdrawMessage(msgID, userID string) error
	EditMessage(msgID, userID, content string) (*model.Message, error)

	CreateGroup(name, ownerID string, memberIDs []string, metadata map[string]string) (*model.Group, error)
	InviteGroupMember(groupID, operatorID string, userIDs []string) error
	KickGroupMember(groupID, operatorID, userID string) error
	MuteGroupMember(groupID, operatorID, userID string, muteType model.MuteType, muteUntil time.Time) error
	TransferGroupOwner(groupID, operatorID, newOwnerID string) error
	UpdateGroupAnnouncement(groupID, operatorID, announcement string) error
	SetGroupAdmin(groupID, operatorID, userID string, isAdmin bool) error
	GetGroupMembers(groupID string, page, pageSize int) ([]*model.GroupMember, int64, error)
	GetGroup(groupID string) (*model.Group, error)
	JoinGroup(groupID, userID string) error
	LeaveGroup(groupID, userID string) error

	StartEventConsumer() error

	// 同步相关方法
	GetSyncPoint(userID, deviceID string) (*model.SyncPoint, error)
	UpdateSyncPoint(userID, deviceID string) (*model.SyncPoint, error)
	SyncMessages(req *model.SyncRequest) (*model.SyncResponse, error)
	RegisterDevice(userID, deviceID, deviceType, deviceName string) (*model.Device, error)
	UpdateDeviceOnline(userID, deviceID string) error
	GetUserDevices(userID string) ([]*model.Device, error)
	RecordMessageState(msgID, userID, chatID, state string) error
}

// ChatServiceImpl 聊天服务实现
type ChatServiceImpl struct {
	repo     dao.ChatRepository
	eventBus *types.EventBus
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewChatService 创建聊天服务
func NewChatService(repo dao.ChatRepository, eventBus *types.EventBus, ctx context.Context) ChatService {
	c, cancel := context.WithCancel(ctx)
	return &ChatServiceImpl{
		repo:     repo,
		eventBus: eventBus,
		ctx:      c,
		cancel:   cancel,
	}
}

// StartEventConsumer 启动事件消费者
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

// HandleMessageEvent 处理消息事件
func (s *ChatServiceImpl) HandleMessageEvent(msg *mq.Message) error {
	logger.Debug("收到消息事件", logger.StringField("topic", msg.Topic))

	// 先尝试解析为 TypingEvent
	if typingEvent, err := types.TypingEventFromJSON(msg.Value); err == nil && typingEvent.UserID != "" {
		return s.handleTypingEvent(typingEvent)
	}

	// 然后尝试解析为 MessageReadEvent
	if readEvent, err := types.MessageReadEventFromJSON(msg.Value); err == nil && readEvent.ReaderID != "" {
		return s.handleMessageReadEvent(readEvent)
	}

	// 否则解析为 MessageEvent
	event, err := types.MessageEventFromJSON(msg.Value)
	if err != nil {
		logger.Error("解析消息事件失败", logger.ErrorField(err))
		return err
	}

	if len(event.RecipientIDs) > 0 {
		logger.Debug("事件已处理，跳过", logger.StringField("msg_id", event.ID))
		return nil
	}

	logger.Info("处理消息事件",
		logger.StringField("msg_id", event.ID),
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("sender_id", event.SenderID),
	)

	var recipientIDs []string
	_, recipientIDs, err = s.SendMessage(
		event.SenderID,
		event.ChatID,
		model.ChatType(event.ChatType),
		model.MessageType(event.MessageType),
		event.Content,
		event.Metadata,
		event.ReplyToMessage,
		event.MentionUserIDs,
	)

	if err != nil {
		logger.Error("保存消息失败", logger.ErrorField(err))
		return err
	}

	if event.ChatType == types.ChatTypePrivate && len(recipientIDs) == 0 {
		recipientIDs = s.getPrivateChatRecipient(event.ChatID, event.SenderID)
	}

	event.RecipientIDs = recipientIDs

	if err := s.eventBus.PublishMessageEvent(s.ctx, event); err != nil {
		logger.Error("发布完整事件失败", logger.ErrorField(err))
		return err
	}

	logger.Info("事件处理完成，已发布完整事件",
		logger.StringField("msg_id", event.ID),
		logger.IntField("recipient_count", len(event.RecipientIDs)),
	)

	return nil
}

// handleMessageReadEvent 处理消息已读事件
func (s *ChatServiceImpl) handleMessageReadEvent(event *types.MessageReadEvent) error {
	logger.Info("处理消息已读事件",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("reader_id", event.ReaderID),
		logger.IntField("message_count", len(event.MessageIDs)))

	// 更新数据库中的消息状态为已读
	if err := s.repo.MarkMessagesRead(s.ctx, event.MessageIDs); err != nil {
		logger.Error("标记消息已读失败", logger.ErrorField(err))
		return err
	}

	// 计算应该接收已读回执的用户列表
	recipientIDs := s.getReadReceiptRecipients(event)
	event.RecipientIDs = recipientIDs

	// 重新发布带有 RecipientIDs 的事件
	if err := s.eventBus.PublishMessageReadEvent(s.ctx, event); err != nil {
		logger.Error("发布完整已读回执事件失败", logger.ErrorField(err))
		return err
	}

	logger.Info("消息已读状态已更新，已发布完整事件",
		logger.StringField("reader_id", event.ReaderID),
		logger.IntField("message_count", len(event.MessageIDs)),
		logger.IntField("recipient_count", len(recipientIDs)))

	return nil
}

// getReadReceiptRecipients 获取应该接收已读回执的用户列表
func (s *ChatServiceImpl) getReadReceiptRecipients(event *types.MessageReadEvent) []string {
	var recipients []string

	if strings.HasPrefix(event.ChatID, "private_") {
		// 单聊：获取对方
		recipients = s.getPrivateChatRecipient(event.ChatID, event.ReaderID)
	} else {
		// 尝试从数据库获取群成员
		groupMembers, err := s.repo.GetAllGroupMemberIDs(s.ctx, event.ChatID)
		if err == nil {
			// 排除阅读者自己
			for _, uid := range groupMembers {
				if uid != event.ReaderID {
					recipients = append(recipients, uid)
				}
			}
		} else {
			logger.Debug("获取群成员失败，将由 Gateway 处理",
				logger.StringField("chat_id", event.ChatID),
				logger.ErrorField(err))
		}
	}

	return recipients
}

// handleTypingEvent 处理输入状态事件
func (s *ChatServiceImpl) handleTypingEvent(event *types.TypingEvent) error {
	logger.Info("处理输入状态事件",
		logger.StringField("chat_id", event.ChatID),
		logger.StringField("user_id", event.UserID),
		logger.BoolField("typing", event.IsTyping))

	// 如果已经有 RecipientIDs，说明已经处理过，跳过
	if len(event.RecipientIDs) > 0 {
		logger.Debug("输入状态事件已处理，跳过", logger.StringField("chat_id", event.ChatID))
		return nil
	}

	// 计算应该接收输入状态的用户列表
	recipientIDs := s.getReadReceiptRecipients(&types.MessageReadEvent{
		ReaderID: event.UserID,
		ChatID:   event.ChatID,
	})
	event.RecipientIDs = recipientIDs

	// 重新发布带有 RecipientIDs 的事件
	if err := s.eventBus.PublishTypingEvent(s.ctx, event); err != nil {
		logger.Error("发布完整输入状态事件失败", logger.ErrorField(err))
		return err
	}

	logger.Info("输入状态事件已处理并重新发布",
		logger.StringField("user_id", event.UserID),
		logger.IntField("recipient_count", len(recipientIDs)))

	return nil
}

// getPrivateChatRecipient 从单聊会话ID获取另一个参与者
func (s *ChatServiceImpl) getPrivateChatRecipient(chatID, senderID string) []string {
	parts := strings.Split(chatID, "_")
	if len(parts) == 2 {
		if parts[0] == senderID {
			return []string{parts[1]}
		}
		if parts[1] == senderID {
			return []string{parts[0]}
		}
	}

	logger.Warn("无法解析单聊会话ID获取接收者",
		logger.StringField("chat_id", chatID),
		logger.StringField("sender_id", senderID),
	)
	return []string{}
}

// SendMessage 发送消息
func (s *ChatServiceImpl) SendMessage(senderID, chatID string, chatType model.ChatType, msgType model.MessageType, content string, metadata map[string]string, replyToID string, mentionIDs []string) (*model.Message, []string, error) {
	msgID := uuid.New().String()
	now := time.Now()

	mentionJSON, _ := json.Marshal(mentionIDs)

	msg := &model.Message{
		ID:             msgID,
		ChatID:         chatID,
		ChatType:       chatType,
		SenderID:       senderID,
		MessageType:    msgType,
		Content:        content,
		Metadata:       metadata,
		Status:         model.MessageStatusSent,
		CreatedAt:      now,
		UpdatedAt:      now,
		ReplyToMessage: replyToID,
		MentionUserIDs: string(mentionJSON),
	}

	if err := s.repo.CreateMessage(s.ctx, msg); err != nil {
		logger.Error("创建消息失败", logger.ErrorField(err))
		return nil, nil, err
	}

	var recipientIDs []string

	switch chatType {
	case model.ChatTypePrivate:
		// 私聊：从 chatID 解析接收者
		recipientIDs = s.getPrivateChatRecipient(chatID, senderID)
	case model.ChatTypeGroup:
		// 群聊：获取所有群成员（除发送者）
		members, err := s.repo.GetAllGroupMemberIDs(s.ctx, chatID)
		if err == nil {
			for _, m := range members {
				if m != senderID {
					recipientIDs = append(recipientIDs, m)
				}
			}
		} else {
			logger.Warn("获取群成员失败", logger.ErrorField(err))
		}
	case model.ChatTypeBroadcast:
		// 广播：获取所有用户（除发送者）
		allUsers, err := s.repo.GetAllUserIDs(s.ctx)
		if err == nil {
			for _, userID := range allUsers {
				if userID != senderID {
					recipientIDs = append(recipientIDs, userID)
				}
			}
		} else {
			logger.Warn("获取广播用户列表失败", logger.ErrorField(err))
		}
	}

	logger.Info("发送消息",
		logger.StringField("msg_id", msgID),
		logger.StringField("chat_id", chatID),
		logger.IntField("chat_type", int(chatType)),
		logger.IntField("recipient_count", len(recipientIDs)),
	)

	return msg, recipientIDs, nil
}

// GetMessageHistory 获取消息历史
func (s *ChatServiceImpl) GetMessageHistory(chatID string, chatType model.ChatType, beforeTime time.Time, limit int) ([]*model.Message, bool, error) {
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

// SearchMessages 搜索消息
func (s *ChatServiceImpl) SearchMessages(chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error) {
	return s.repo.SearchMessages(s.ctx, chatID, chatType, keyword, startTime, endTime, page, pageSize)
}

// MarkMessagesRead 标记消息已读
func (s *ChatServiceImpl) MarkMessagesRead(msgIDs []string) error {
	if len(msgIDs) == 0 {
		return nil
	}
	return s.repo.MarkMessagesRead(s.ctx, msgIDs)
}

// WithdrawMessage 撤回消息
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

	return nil
}

// EditMessage 编辑消息
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

// CreateGroup 创建群组
func (s *ChatServiceImpl) CreateGroup(name, ownerID string, memberIDs []string, metadata map[string]string) (*model.Group, error) {
	groupID := uuid.New().String()
	now := time.Now()

	group := &model.Group{
		ID:        groupID,
		Name:      name,
		OwnerID:   ownerID,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.CreateGroup(s.ctx, group); err != nil {
		logger.Error("创建群组失败", logger.ErrorField(err))
		return nil, err
	}

	members := make([]*model.GroupMember, 0, len(memberIDs)+1)
	memberIDs = append(memberIDs, ownerID)

	for _, userID := range memberIDs {
		role := model.GroupMemberRoleMember
		if userID == ownerID {
			role = model.GroupMemberRoleOwner
		}

		member := &model.GroupMember{
			ID:        uuid.New().String(),
			GroupID:   groupID,
			UserID:    userID,
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

	sysMsg := &model.Message{
		ID:          uuid.New().String(),
		ChatID:      groupID,
		ChatType:    model.ChatTypeGroup,
		SenderID:    "system",
		MessageType: model.MessageTypeSystem,
		Content:     "群组创建成功",
		Status:      model.MessageStatusSent,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateMessage(s.ctx, sysMsg); err != nil {
		logger.Error("创建系统消息失败", logger.ErrorField(err))
	}

	logger.Info("创建群组成功",
		logger.StringField("group_id", groupID),
		logger.StringField("name", name),
		logger.IntField("member_count", len(members)),
	)

	return group, nil
}

// InviteGroupMember 邀请群成员
func (s *ChatServiceImpl) InviteGroupMember(groupID, operatorID string, userIDs []string) error {
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

	for _, userID := range userIDs {
		if existing, _ := s.repo.GetGroupMember(s.ctx, groupID, userID); existing != nil {
			continue
		}

		m := &model.GroupMember{
			ID:        uuid.New().String(),
			GroupID:   groupID,
			UserID:    userID,
			Role:      model.GroupMemberRoleMember,
			MuteType:  model.MuteTypeNone,
			JoinedAt:  now,
			CreatedAt: now,
			UpdatedAt: now,
		}
		members = append(members, m)
	}

	if len(members) > 0 {
		if err := s.repo.AddGroupMembers(s.ctx, members); err != nil {
			logger.Error("添加群成员失败", logger.ErrorField(err))
			return err
		}
	}

	logger.Info("邀请群成员",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.IntField("invite_count", len(members)),
	)

	return nil
}

// KickGroupMember 踢出群成员
func (s *ChatServiceImpl) KickGroupMember(groupID, operatorID, userID string) error {
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

	logger.Info("踢出群成员",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
		logger.StringField("user_id", userID),
	)

	return nil
}

// MuteGroupMember 禁言/解禁群成员
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

// TransferGroupOwner 转让群主
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

// UpdateGroupAnnouncement 更新群公告
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

	logger.Info("更新群公告",
		logger.StringField("group_id", groupID),
		logger.StringField("operator_id", operatorID),
	)

	return nil
}

// SetGroupAdmin 设置管理员
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

// GetGroupMembers 获取群成员列表
func (s *ChatServiceImpl) GetGroupMembers(groupID string, page, pageSize int) ([]*model.GroupMember, int64, error) {
	return s.repo.GetGroupMembers(s.ctx, groupID, page, pageSize)
}

// GetGroup 获取群组信息
func (s *ChatServiceImpl) GetGroup(groupID string) (*model.Group, error) {
	return s.repo.GetGroupByID(s.ctx, groupID)
}

func (s *ChatServiceImpl) JoinGroup(groupID, userID string) error {
	group, err := s.repo.GetGroupByID(s.ctx, groupID)
	if err != nil {
		return fmt.Errorf("查询群组失败: %w", err)
	}
	if group == nil {
		return fmt.Errorf("群组不存在")
	}

	members, _, err := s.repo.GetGroupMembers(s.ctx, groupID, 1, 1)
	if err == nil && len(members) > 0 {
		for _, m := range members {
			if m.UserID == userID {
				return fmt.Errorf("已在群组中")
			}
		}
	}

	return s.repo.AddGroupMember(s.ctx, &model.GroupMember{
		GroupID: groupID,
		UserID:  userID,
		Role:    model.GroupMemberRoleMember,
	})
}

func (s *ChatServiceImpl) LeaveGroup(groupID, userID string) error {
	group, err := s.repo.GetGroupByID(s.ctx, groupID)
	if err != nil {
		return fmt.Errorf("查询群组失败: %w", err)
	}
	if group == nil {
		return fmt.Errorf("群组不存在")
	}

	if group.OwnerID == userID {
		return fmt.Errorf("群主不能退出群组，请先转让群主")
	}

	return s.repo.RemoveGroupMember(s.ctx, groupID, userID)
}

// GetSyncPoint 获取同步点
func (s *ChatServiceImpl) GetSyncPoint(userID, deviceID string) (*model.SyncPoint, error) {
	return s.repo.GetSyncPoint(s.ctx, userID, deviceID)
}

// UpdateSyncPoint 更新同步点
func (s *ChatServiceImpl) UpdateSyncPoint(userID, deviceID string) (*model.SyncPoint, error) {
	return s.repo.UpsertSyncPoint(s.ctx, userID, deviceID)
}

// SyncMessages 增量同步消息
func (s *ChatServiceImpl) SyncMessages(req *model.SyncRequest) (*model.SyncResponse, error) {
	// 获取用户参与的所有会话
	chatIDs := req.ChatIDs
	if len(chatIDs) == 0 {
		// 如果没有指定会话，获取用户的所有会话
		var err error
		chatIDs, err = s.repo.GetUserChatIDs(s.ctx, req.UserID)
		if err != nil {
			logger.Error("获取用户会话失败", logger.ErrorField(err))
			return nil, err
		}
	}

	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	// 获取指定时间之后的消息
	messages, err := s.repo.GetMessagesAfterTime(s.ctx, chatIDs, req.LastSyncAt, limit+1)
	if err != nil {
		logger.Error("获取同步消息失败", logger.ErrorField(err))
		return nil, err
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	// 获取消息状态变更（如果需要）
	var states []*model.MessageState
	if req.IncludeStates {
		states, err = s.repo.GetMessageStatesAfterTime(s.ctx, req.UserID, req.LastSyncAt, limit)
		if err != nil {
			logger.Error("获取消息状态失败", logger.ErrorField(err))
			return nil, err
		}
	}

	// 获取最后一条消息的时间
	lastMessageAt := req.LastSyncAt
	if len(messages) > 0 {
		lastMessageAt = messages[len(messages)-1].CreatedAt
	}

	// 计算下一次同步时间
	nextSyncTime := time.Now()

	resp := &model.SyncResponse{
		Messages:      messages,
		MessageStates: states,
		HasMore:       hasMore,
		NextSyncTime:  nextSyncTime,
		LastMessageAt: lastMessageAt,
		Version:       req.LastVersion + 1,
	}

	logger.Info("同步消息成功",
		logger.StringField("user_id", req.UserID),
		logger.IntField("message_count", len(messages)),
		logger.IntField("state_count", len(states)),
		logger.BoolField("has_more", hasMore))

	return resp, nil
}

// RegisterDevice 注册设备
func (s *ChatServiceImpl) RegisterDevice(userID, deviceID, deviceType, deviceName string) (*model.Device, error) {
	device, err := s.repo.UpsertDevice(s.ctx, userID, deviceID, deviceType, deviceName)
	if err != nil {
		logger.Error("注册设备失败", logger.ErrorField(err))
		return nil, err
	}
	logger.Info("设备注册成功",
		logger.StringField("user_id", userID),
		logger.StringField("device_id", deviceID),
		logger.StringField("device_type", deviceType))
	return device, nil
}

// UpdateDeviceOnline 更新设备在线状态
func (s *ChatServiceImpl) UpdateDeviceOnline(userID, deviceID string) error {
	if err := s.repo.UpdateDeviceOnline(s.ctx, userID, deviceID); err != nil {
		logger.Error("更新设备在线状态失败", logger.ErrorField(err))
		return err
	}
	return nil
}

// GetUserDevices 获取用户的所有设备
func (s *ChatServiceImpl) GetUserDevices(userID string) ([]*model.Device, error) {
	return s.repo.GetUserDevices(s.ctx, userID)
}

// RecordMessageState 记录消息状态变更
func (s *ChatServiceImpl) RecordMessageState(msgID, userID, chatID, state string) error {
	if err := s.repo.RecordMessageState(s.ctx, msgID, userID, chatID, state); err != nil {
		logger.Error("记录消息状态失败", logger.ErrorField(err))
		return err
	}
	return nil
}
