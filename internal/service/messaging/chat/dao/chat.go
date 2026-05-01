package dao

import (
	"context"
	"errors"
	"time"

	"Logos/internal/service/messaging/chat/model"

	"github.com/google/uuid"

	"gorm.io/gorm"
)

// ChatRepository 聊天数据访问层接口
type ChatRepository interface {
	CreateMessage(ctx context.Context, msg *model.Message) error
	GetMessageByID(ctx context.Context, id string) (*model.Message, error)
	GetMessagesByChatID(ctx context.Context, chatID string, beforeTime time.Time, limit int) ([]*model.Message, error)
	SearchMessages(ctx context.Context, chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error)
	UpdateMessageStatus(ctx context.Context, msgID string, status model.MessageStatus) error
	MarkMessagesRead(ctx context.Context, msgIDs []string) error
	WithdrawMessage(ctx context.Context, msgID string) error
	EditMessage(ctx context.Context, msgID, content string) error

	GetOrCreatePrivateConversation(ctx context.Context, userID1, userID2 string) (*model.Conversation, error)
	UpdateConversationLastMessage(ctx context.Context, convID, msgID string) error

	CreateGroup(ctx context.Context, group *model.Group) error
	GetGroupByID(ctx context.Context, groupID string) (*model.Group, error)
	AddGroupMember(ctx context.Context, member *model.GroupMember) error
	AddGroupMembers(ctx context.Context, members []*model.GroupMember) error
	RemoveGroupMember(ctx context.Context, groupID, userID string) error
	GetGroupMembers(ctx context.Context, groupID string, page, pageSize int) ([]*model.GroupMember, int64, error)
	GetGroupMember(ctx context.Context, groupID, userID string) (*model.GroupMember, error)
	UpdateGroupMemberRole(ctx context.Context, groupID, userID string, role model.GroupMemberRole) error
	UpdateGroupMemberMute(ctx context.Context, groupID, userID string, muteType model.MuteType, muteUntil time.Time) error
	TransferGroupOwner(ctx context.Context, groupID, newOwnerID string) error
	UpdateGroupAnnouncement(ctx context.Context, groupID, announcement string) error
	GetAllGroupMemberIDs(ctx context.Context, groupID string) ([]string, error)
	GetAllUserIDs(ctx context.Context) ([]string, error)

	WithTransaction(ctx context.Context, fn func(txRepo ChatRepository) error) error

	// 同步相关方法
	GetSyncPoint(ctx context.Context, userID, deviceID string) (*model.SyncPoint, error)
	UpsertSyncPoint(ctx context.Context, userID, deviceID string) (*model.SyncPoint, error)
	GetMessagesAfterTime(ctx context.Context, chatIDs []string, afterTime time.Time, limit int) ([]*model.Message, error)
	GetUserChatIDs(ctx context.Context, userID string) ([]string, error)
	GetMessageStatesAfterTime(ctx context.Context, userID string, afterTime time.Time, limit int) ([]*model.MessageState, error)
	UpsertDevice(ctx context.Context, userID, deviceID, deviceType, deviceName string) (*model.Device, error)
	UpdateDeviceOnline(ctx context.Context, userID, deviceID string) error
	GetUserDevices(ctx context.Context, userID string) ([]*model.Device, error)
	RecordMessageState(ctx context.Context, msgID, userID, chatID, state string) error
}

// chatRepositoryImpl 聊天数据访问层实现
type chatRepositoryImpl struct {
	db *gorm.DB
}

// NewChatRepository 创建聊天仓库
func NewChatRepository(db *gorm.DB) ChatRepository {
	return &chatRepositoryImpl{db: db}
}

// CreateMessage 创建消息
func (r *chatRepositoryImpl) CreateMessage(ctx context.Context, msg *model.Message) error {
	return r.db.WithContext(ctx).Create(msg).Error
}

// GetMessageByID 根据 ID 获取消息
func (r *chatRepositoryImpl) GetMessageByID(ctx context.Context, id string) (*model.Message, error) {
	var msg model.Message
	err := r.db.WithContext(ctx).First(&msg, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &msg, err
}

// GetMessagesByChatID 获取某个会话的消息
func (r *chatRepositoryImpl) GetMessagesByChatID(ctx context.Context, chatID string, beforeTime time.Time, limit int) ([]*model.Message, error) {
	var messages []*model.Message
	query := r.db.WithContext(ctx).Where("chat_id = ?", chatID)

	if !beforeTime.IsZero() {
		query = query.Where("created_at < ?", beforeTime)
	}

	err := query.Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	return messages, err
}

// SearchMessages 搜索消息
func (r *chatRepositoryImpl) SearchMessages(ctx context.Context, chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error) {
	var messages []*model.Message
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Message{})

	if chatID != "" {
		query = query.Where("chat_id = ?", chatID)
	}
	if chatType > 0 {
		query = query.Where("chat_type = ?", chatType)
	}
	if keyword != "" {
		query = query.Where("content LIKE ?", "%"+keyword+"%")
	}
	if !startTime.IsZero() {
		query = query.Where("created_at >= ?", startTime)
	}
	if !endTime.IsZero() {
		query = query.Where("created_at <= ?", endTime)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&messages).Error

	return messages, total, err
}

// UpdateMessageStatus 更新消息状态
func (r *chatRepositoryImpl) UpdateMessageStatus(ctx context.Context, msgID string, status model.MessageStatus) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", msgID).
		Update("status", status).Error
}

// MarkMessagesRead 批量标记消息已读
func (r *chatRepositoryImpl) MarkMessagesRead(ctx context.Context, msgIDs []string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id IN ?", msgIDs).
		Update("status", model.MessageStatusRead).Error
}

// WithdrawMessage 撤回消息
func (r *chatRepositoryImpl) WithdrawMessage(ctx context.Context, msgID string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", msgID).
		Updates(map[string]interface{}{
			"status":  model.MessageStatusWithdrawn,
			"content": "[消息已撤回]",
		}).Error
}

// EditMessage 编辑消息
func (r *chatRepositoryImpl) EditMessage(ctx context.Context, msgID, content string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", msgID).
		Updates(map[string]interface{}{
			"content": content,
			"status":  model.MessageStatusEdited,
		}).Error
}

// GetOrCreatePrivateConversation 获取或创建单聊会话
func (r *chatRepositoryImpl) GetOrCreatePrivateConversation(ctx context.Context, userID1, userID2 string) (*model.Conversation, error) {
	var conv model.Conversation
	err := r.db.WithContext(ctx).Joins("JOIN participants ON conversations.id = participants.conversation_id").
		Where("participants.user_id IN ?", []string{userID1, userID2}).
		Where("conversations.type = ?", model.ChatTypePrivate).
		Group("conversations.id").
		Having("COUNT(DISTINCT participants.user_id) = 2").
		First(&conv).Error

	if err == nil {
		return &conv, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	convID := "private_" + userID1 + "_" + userID2
	newConv := &model.Conversation{
		ID:   convID,
		Type: model.ChatTypePrivate,
	}

	if err := r.db.WithContext(ctx).Create(newConv).Error; err != nil {
		return nil, err
	}

	now := time.Now()
	p1 := &model.Participant{
		ID:             convID + "_" + userID1,
		ConversationID: convID,
		UserID:         userID1,
		LastReadAt:     now,
	}
	p2 := &model.Participant{
		ID:             convID + "_" + userID2,
		ConversationID: convID,
		UserID:         userID2,
		LastReadAt:     now,
	}

	if err := r.db.WithContext(ctx).Create(p1).Error; err != nil {
		return nil, err
	}
	if err := r.db.WithContext(ctx).Create(p2).Error; err != nil {
		return nil, err
	}

	return newConv, nil
}

// UpdateConversationLastMessage 更新会话最后消息
func (r *chatRepositoryImpl) UpdateConversationLastMessage(ctx context.Context, convID, msgID string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", convID).
		Updates(map[string]interface{}{
			"last_message_id": msgID,
		}).Error
}

// CreateGroup 创建群组
func (r *chatRepositoryImpl) CreateGroup(ctx context.Context, group *model.Group) error {
	return r.db.WithContext(ctx).Create(group).Error
}

// GetGroupByID 获取群组
func (r *chatRepositoryImpl) GetGroupByID(ctx context.Context, groupID string) (*model.Group, error) {
	var group model.Group
	err := r.db.WithContext(ctx).First(&group, "id = ?", groupID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &group, err
}

// AddGroupMember 添加群成员
func (r *chatRepositoryImpl) AddGroupMember(ctx context.Context, member *model.GroupMember) error {
	return r.db.WithContext(ctx).Create(member).Error
}

// AddGroupMembers 批量添加群成员
func (r *chatRepositoryImpl) AddGroupMembers(ctx context.Context, members []*model.GroupMember) error {
	return r.db.WithContext(ctx).Create(members).Error
}

// RemoveGroupMember 移除群成员
func (r *chatRepositoryImpl) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	return r.db.WithContext(ctx).Where("group_id = ? AND user_id = ?", groupID, userID).
		Delete(&model.GroupMember{}).Error
}

// GetGroupMembers 获取群成员
func (r *chatRepositoryImpl) GetGroupMembers(ctx context.Context, groupID string, page, pageSize int) ([]*model.GroupMember, int64, error) {
	var members []*model.GroupMember
	var total int64

	if err := r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ?", groupID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := r.db.WithContext(ctx).Where("group_id = ?", groupID).
		Order("created_at ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&members).Error

	return members, total, err
}

// GetGroupMember 获取群成员
func (r *chatRepositoryImpl) GetGroupMember(ctx context.Context, groupID, userID string) (*model.GroupMember, error) {
	var member model.GroupMember
	err := r.db.WithContext(ctx).First(&member, "group_id = ? AND user_id = ?", groupID, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &member, err
}

// UpdateGroupMemberRole 更新群成员角色
func (r *chatRepositoryImpl) UpdateGroupMemberRole(ctx context.Context, groupID, userID string, role model.GroupMemberRole) error {
	return r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Update("role", role).Error
}

// UpdateGroupMemberMute 更新群成员禁言状态
func (r *chatRepositoryImpl) UpdateGroupMemberMute(ctx context.Context, groupID, userID string, muteType model.MuteType, muteUntil time.Time) error {
	return r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Updates(map[string]interface{}{
			"mute_type":  muteType,
			"mute_until": muteUntil,
		}).Error
}

// TransferGroupOwner 转让群主
func (r *chatRepositoryImpl) TransferGroupOwner(ctx context.Context, groupID, newOwnerID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Group{}).
			Where("id = ?", groupID).
			Update("owner_id", newOwnerID).Error; err != nil {
			return err
		}

		if err := tx.Model(&model.GroupMember{}).
			Where("group_id = ? AND user_id = ?", groupID, newOwnerID).
			Update("role", model.GroupMemberRoleOwner).Error; err != nil {
			return err
		}

		return nil
	})
}

// UpdateGroupAnnouncement 更新群公告
func (r *chatRepositoryImpl) UpdateGroupAnnouncement(ctx context.Context, groupID, announcement string) error {
	return r.db.WithContext(ctx).Model(&model.Group{}).
		Where("id = ?", groupID).
		Update("announcement", announcement).Error
}

// GetAllGroupMemberIDs 获取所有群成员ID
func (r *chatRepositoryImpl) GetAllGroupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	var userIDs []string
	err := r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ?", groupID).
		Pluck("user_id", &userIDs).Error
	return userIDs, err
}

// GetAllUserIDs 获取所有用户ID（用于广播）
func (r *chatRepositoryImpl) GetAllUserIDs(ctx context.Context) ([]string, error) {
	var userIDs []string
	err := r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Distinct("user_id").
		Pluck("user_id", &userIDs).Error
	return userIDs, err
}

// WithTransaction 事务操作
func (r *chatRepositoryImpl) WithTransaction(ctx context.Context, fn func(txRepo ChatRepository) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txRepo := &chatRepositoryImpl{db: tx}
		return fn(txRepo)
	})
}

// GetSyncPoint 获取同步点
func (r *chatRepositoryImpl) GetSyncPoint(ctx context.Context, userID, deviceID string) (*model.SyncPoint, error) {
	var syncPoint model.SyncPoint
	err := r.db.WithContext(ctx).First(&syncPoint, "user_id = ? AND device_id = ?", userID, deviceID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &syncPoint, err
}

// UpsertSyncPoint 更新或创建同步点
func (r *chatRepositoryImpl) UpsertSyncPoint(ctx context.Context, userID, deviceID string) (*model.SyncPoint, error) {
	now := time.Now()
	var syncPoint model.SyncPoint
	err := r.db.WithContext(ctx).First(&syncPoint, "user_id = ? AND device_id = ?", userID, deviceID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		syncPoint = model.SyncPoint{
			ID:         uuid.New().String(),
			UserID:     userID,
			DeviceID:   deviceID,
			LastSyncAt: now,
			SyncType:   "full",
			Version:    1,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		return &syncPoint, r.db.WithContext(ctx).Create(&syncPoint).Error
	} else if err != nil {
		return nil, err
	}

	syncPoint.LastSyncAt = now
	syncPoint.Version += 1
	syncPoint.UpdatedAt = now
	return &syncPoint, r.db.WithContext(ctx).Save(&syncPoint).Error
}

// GetMessagesAfterTime 获取指定时间之后的消息
func (r *chatRepositoryImpl) GetMessagesAfterTime(ctx context.Context, chatIDs []string, afterTime time.Time, limit int) ([]*model.Message, error) {
	var messages []*model.Message
	query := r.db.WithContext(ctx).Model(&model.Message{})

	if len(chatIDs) > 0 {
		query = query.Where("chat_id IN ?", chatIDs)
	}
	if !afterTime.IsZero() {
		query = query.Where("created_at > ?", afterTime)
	}

	err := query.Order("created_at ASC").
		Limit(limit).
		Find(&messages).Error
	return messages, err
}

// GetUserChatIDs 获取用户参与的所有会话ID
func (r *chatRepositoryImpl) GetUserChatIDs(ctx context.Context, userID string) ([]string, error) {
	var chatIDs []string
	// 从群成员中获取
	err := r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("user_id = ?", userID).
		Pluck("group_id", &chatIDs).Error
	if err != nil {
		return nil, err
	}

	// 从参与者中获取单聊会话
	var privateChatIDs []string
	err = r.db.WithContext(ctx).Model(&model.Participant{}).
		Where("user_id = ?", userID).
		Pluck("conversation_id", &privateChatIDs).Error
	if err == nil {
		chatIDs = append(chatIDs, privateChatIDs...)
	}

	return chatIDs, nil
}

// GetMessageStatesAfterTime 获取消息状态变更
func (r *chatRepositoryImpl) GetMessageStatesAfterTime(ctx context.Context, userID string, afterTime time.Time, limit int) ([]*model.MessageState, error) {
	var states []*model.MessageState
	query := r.db.WithContext(ctx).Model(&model.MessageState{}).
		Where("user_id = ?", userID)
	if !afterTime.IsZero() {
		query = query.Where("updated_at > ?", afterTime)
	}
	err := query.Order("updated_at ASC").
		Limit(limit).
		Find(&states).Error
	return states, err
}

// UpsertDevice 更新或创建设备
func (r *chatRepositoryImpl) UpsertDevice(ctx context.Context, userID, deviceID, deviceType, deviceName string) (*model.Device, error) {
	now := time.Now()
	var device model.Device
	err := r.db.WithContext(ctx).First(&device, "user_id = ? AND id = ?", userID, deviceID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		device = model.Device{
			ID:         deviceID,
			UserID:     userID,
			DeviceType: deviceType,
			DeviceName: deviceName,
			LastOnline: now,
			Status:     "active",
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		return &device, r.db.WithContext(ctx).Create(&device).Error
	} else if err != nil {
		return nil, err
	}

	device.LastOnline = now
	device.UpdatedAt = now
	if deviceName != "" {
		device.DeviceName = deviceName
	}
	return &device, r.db.WithContext(ctx).Save(&device).Error
}

// UpdateDeviceOnline 更新设备在线状态
func (r *chatRepositoryImpl) UpdateDeviceOnline(ctx context.Context, userID, deviceID string) error {
	return r.db.WithContext(ctx).Model(&model.Device{}).
		Where("user_id = ? AND id = ?", userID, deviceID).
		Updates(map[string]interface{}{
			"last_online": time.Now(),
			"updated_at":  time.Now(),
		}).Error
}

// GetUserDevices 获取用户的所有设备
func (r *chatRepositoryImpl) GetUserDevices(ctx context.Context, userID string) ([]*model.Device, error) {
	var devices []*model.Device
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("last_online DESC").
		Find(&devices).Error
	return devices, err
}

// RecordMessageState 记录消息状态变更
func (r *chatRepositoryImpl) RecordMessageState(ctx context.Context, msgID, userID, chatID, state string) error {
	stateRecord := &model.MessageState{
		ID:        uuid.New().String(),
		MessageID: msgID,
		UserID:    userID,
		ChatID:    chatID,
		State:     state,
		UpdatedAt: time.Now(),
		CreatedAt: time.Now(),
	}
	return r.db.WithContext(ctx).Create(stateRecord).Error
}
