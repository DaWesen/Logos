package dao

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"Logos/internal/service/messaging/chat/model"
	"Logos/pkg/logger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func sortUserIDs(id1, id2 string) (smaller, larger string) {
	n1, err1 := strconv.ParseInt(id1, 10, 64)
	n2, err2 := strconv.ParseInt(id2, 10, 64)
	if err1 != nil || err2 != nil {
		if id1 < id2 {
			return id1, id2
		}
		return id2, id1
	}
	if n1 < n2 {
		return id1, id2
	}
	return id2, id1
}

func toInt64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func toInt64s(ss []string) []int64 {
	result := make([]int64, 0, len(ss))
	for _, s := range ss {
		result = append(result, toInt64(s))
	}
	return result
}

func toStrings(is []int64) []string {
	result := make([]string, 0, len(is))
	for _, i := range is {
		result = append(result, strconv.FormatInt(i, 10))
	}
	return result
}

type ChatRepository interface {
	CreateMessage(ctx context.Context, msg *model.Message) error
	UpdateMessage(ctx context.Context, msg *model.Message) error
	GetMessageByID(ctx context.Context, id string) (*model.Message, error)
	GetMessagesByChatID(ctx context.Context, chatID string, beforeTime time.Time, limit int) ([]*model.Message, error)
	// GetMessagesAfterSeq 返回会话内 seq 大于 afterSeq 的消息，按 seq 升序，用于断线重连后的增量补拉。
	GetMessagesAfterSeq(ctx context.Context, chatID string, afterSeq int64, limit int) ([]*model.Message, error)
	SearchMessages(ctx context.Context, chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int) ([]*model.Message, int64, error)
	// MarkMessagesRead 逐条标记指定消息为已读。
	// 仅用于已读回执携带的显式、有界 msgIDs（供前端展示已读态）；
	// 未读数统计走 AdvanceReadPosition 的位点比对，不依赖本方法。
	MarkMessagesRead(ctx context.Context, msgIDs []string) error
	// GetMaxMessageSeq 返回会话当前最大消息 seq（无消息返回 0）
	GetMaxMessageSeq(ctx context.Context, chatID string) (int64, error)
	// GetMaxSeqByMessageIDs 返回指定消息中的最大 seq（用于按位点标记已读）
	GetMaxSeqByMessageIDs(ctx context.Context, msgIDs []string) (int64, error)
	WithdrawMessage(ctx context.Context, msgID string) error
	EditMessage(ctx context.Context, msgID, content string) error
	DeleteChatHistory(ctx context.Context, chatID string) error

	GetOrCreatePrivateConversation(ctx context.Context, userID1, userID2 string) (*model.Conversation, error)
	CreateConversation(ctx context.Context, conv *model.Conversation) error
	UpdateConversationLastMessage(ctx context.Context, convID, msgID string) error
	AddConversationParticipant(ctx context.Context, p *model.ConversationParticipant) error
	AddConversationParticipants(ctx context.Context, ps []*model.ConversationParticipant) error

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
	UpdateGroupAvatar(ctx context.Context, groupID, avatar string) error
	GetAllGroupMemberIDs(ctx context.Context, groupID string) ([]string, error)
	GetAllUserIDs(ctx context.Context) ([]string, error)
	UpdateGroupMemberCount(ctx context.Context, groupID string) error
	DeleteGroup(ctx context.Context, groupID string) error
	DeleteAllGroupMembers(ctx context.Context, groupID string) error

	GetConversationList(ctx context.Context, userID string, page, pageSize int) ([]*model.ConversationItem, int64, error)
	GetUnreadCounts(ctx context.Context, userID string, chatIDs []string) (map[string]int64, error)
	GetTotalUnreadCount(ctx context.Context, userID string) (int64, error)
	// AdvanceReadPosition 推进会话已读位点（单调不回退），并同步 last_read_at
	AdvanceReadPosition(ctx context.Context, conversationID, userID string, seq int64) error
	DeleteConversationForUser(ctx context.Context, conversationID, userID string) error
	GetUsernameByID(ctx context.Context, userID int64) (string, error)
}

type chatRepositoryImpl struct {
	db *gorm.DB
}

func NewChatRepository(db *gorm.DB) ChatRepository {
	MigrateExpandUserID(db)
	MigrateExpandSenderID(db)
	return &chatRepositoryImpl{db: db}
}

func MigrateExpandUserID(db *gorm.DB) {
	if db.Dialector.Name() == "postgres" {
		db.Exec("ALTER TABLE group_members ALTER COLUMN user_id TYPE varchar(64)")
		db.Exec("ALTER TABLE conversation_participants ALTER COLUMN user_id TYPE varchar(64)")
	} else {
		db.Exec("ALTER TABLE group_members MODIFY COLUMN user_id varchar(64)")
		db.Exec("ALTER TABLE conversation_participants MODIFY COLUMN user_id varchar(64)")
	}
}

func MigrateExpandSenderID(db *gorm.DB) {
	if db.Dialector.Name() == "postgres" {
		db.Exec("ALTER TABLE messages ALTER COLUMN sender_id TYPE varchar(64)")
	} else {
		db.Exec("ALTER TABLE messages MODIFY COLUMN sender_id varchar(64)")
	}
}

// CreateMessage 幂等写入消息，并在事务内按 chat_id 原子分配会话内单调递增的 seq。
//
// 幂等：先按主键判重，Kafka 重投不会产生重复数据。
// seq 分配：conversation_seqs 行锁（FOR UPDATE）保证多副本并发下同一会话 seq 严格递增且不重复。
// 若因唯一键冲突回滚，已自增的 seq 会被跳过（产生空洞），这是可接受的——
// seq 只要求单调，不要求连续。
func (r *chatRepositoryImpl) CreateMessage(ctx context.Context, msg *model.Message) error {
	var existing model.Message
	err := r.db.WithContext(ctx).Select("id").First(&existing, "id = ?", msg.ID).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 确保分配器行存在（并发下可能已被创建，DoNothing 忽略冲突）
		seqRow := &model.ConversationSeq{ChatID: msg.ChatID, LastSeq: 0, UpdatedAt: time.Now()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(seqRow).Error; err != nil {
			return err
		}

		// 2. 行锁 + 自增，原子取得本会话下一个 seq
		var locked model.ConversationSeq
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("chat_id = ?", msg.ChatID).First(&locked).Error; err != nil {
			return err
		}
		locked.LastSeq++
		if err := tx.Model(&model.ConversationSeq{}).
			Where("chat_id = ?", msg.ChatID).
			Update("last_seq", locked.LastSeq).Error; err != nil {
			return err
		}
		msg.Seq = locked.LastSeq

		// 3. 写入消息（Seq 已带上）
		return tx.Create(msg).Error
	})
}

func (r *chatRepositoryImpl) UpdateMessage(ctx context.Context, msg *model.Message) error {
	return r.db.WithContext(ctx).Save(msg).Error
}

func (r *chatRepositoryImpl) GetMessageByID(ctx context.Context, id string) (*model.Message, error) {
	var msg model.Message
	err := r.db.WithContext(ctx).First(&msg, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &msg, err
}

// GetMessagesByChatID 返回会话历史消息。
// 排序优先按会话内 seq（单调且无时间戳并列问题），seq 为 0 的历史兜底行排在最后；
// beforeTime 作为游标保留：仅返回 created_at 早于该时间的消息，兼容既有分页调用方。
func (r *chatRepositoryImpl) GetMessagesByChatID(ctx context.Context, chatID string, beforeTime time.Time, limit int) ([]*model.Message, error) {
	var messages []*model.Message
	query := r.db.WithContext(ctx).Where("chat_id = ?", chatID)
	if !beforeTime.IsZero() {
		query = query.Where("created_at < ?", beforeTime)
	}
	err := query.Order("seq DESC, created_at DESC").Limit(limit).Find(&messages).Error
	return messages, err
}

// GetMessagesAfterSeq 增量补拉：只取 seq 严格大于 afterSeq 的消息，按 seq 升序返回，
// 使调用方无需再反转即可直接追加到会话末尾。用于 WS 断线重连后填补 PubSub 丢失的窗口。
func (r *chatRepositoryImpl) GetMessagesAfterSeq(ctx context.Context, chatID string, afterSeq int64, limit int) ([]*model.Message, error) {
	var messages []*model.Message
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND seq > ?", chatID, afterSeq).
		Order("seq ASC").
		Limit(limit).
		Find(&messages).Error
	return messages, err
}

func (r *chatRepositoryImpl) GetMaxMessageSeq(ctx context.Context, chatID string) (int64, error) {
	var maxSeq int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("chat_id = ?", chatID).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&maxSeq).Error
	return maxSeq, err
}

func (r *chatRepositoryImpl) GetMaxSeqByMessageIDs(ctx context.Context, msgIDs []string) (int64, error) {
	if len(msgIDs) == 0 {
		return 0, nil
	}
	var maxSeq int64
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id IN ?", msgIDs).
		Select("COALESCE(MAX(seq), 0)").
		Scan(&maxSeq).Error
	return maxSeq, err
}

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
	err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&messages).Error
	return messages, total, err
}

func (r *chatRepositoryImpl) MarkMessagesRead(ctx context.Context, msgIDs []string) error {
	if len(msgIDs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id IN ?", msgIDs).
		Update("is_read", true).Error
}

// AdvanceReadPosition 推进会话已读位点：seq 取较大值（GREATEST），保证位点单调不回退。
// 未读数由「seq > last_read_seq」计算，因此这里不再逐条 UPDATE messages.is_read，
// 把 O(未读条数) 的写放大降为 O(1) 的单行更新。
func (r *chatRepositoryImpl) AdvanceReadPosition(ctx context.Context, conversationID, userID string, seq int64) error {
	if conversationID == "" || userID == "" {
		return nil
	}
	return r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("conversation_id = ? AND user_id = ?", conversationID, userID).
		Updates(map[string]interface{}{
			"last_read_seq": gorm.Expr("GREATEST(last_read_seq, ?)", seq),
			"last_read_at":  time.Now(),
		}).Error
}

func (r *chatRepositoryImpl) WithdrawMessage(ctx context.Context, msgID string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", msgID).
		Updates(map[string]interface{}{
			"status":       model.MessageStatusWithdrawn.String(),
			"content":      "[消息已撤回]",
			"message_type": int(model.MessageTypeText),
			"media_url":    "",
			"media_meta":   nil,
		}).Error
}

func (r *chatRepositoryImpl) EditMessage(ctx context.Context, msgID, content string) error {
	return r.db.WithContext(ctx).Model(&model.Message{}).
		Where("id = ?", msgID).
		Updates(map[string]interface{}{
			"content": content,
			"status":  model.MessageStatusEdited.String(),
		}).Error
}

func (r *chatRepositoryImpl) DeleteChatHistory(ctx context.Context, chatID string) error {
	return r.db.WithContext(ctx).Where("chat_id = ?", chatID).Delete(&model.Message{}).Error
}

func (r *chatRepositoryImpl) GetOrCreatePrivateConversation(ctx context.Context, userID1, userID2 string) (*model.Conversation, error) {
	smallID, largeID := sortUserIDs(userID1, userID2)
	convID := fmt.Sprintf("private_%s_%s", smallID, largeID)

	var conv model.Conversation
	err := r.db.WithContext(ctx).Where("id = ?", convID).First(&conv).Error
	if err == nil {
		return &conv, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now()
	newConv := &model.Conversation{
		ID:        convID,
		ChatID:    convID,
		ChatType:  int(model.ChatTypePrivate),
		BotID:     "",
		UserID:    "",
		Title:     "",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := r.db.WithContext(ctx).Create(newConv).Error; err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "23505") {
			err := r.db.WithContext(ctx).Where("id = ?", convID).First(&conv).Error
			if err != nil {
				return nil, err
			}
			return &conv, nil
		}
		return nil, err
	}

	p1 := &model.ConversationParticipant{
		ConversationID: convID,
		UserID:         userID1,
		LastReadAt:     now,
		JoinedAt:       now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	p2 := &model.ConversationParticipant{
		ConversationID: convID,
		UserID:         userID2,
		LastReadAt:     now,
		JoinedAt:       now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := r.db.WithContext(ctx).Create(p1).Error; err != nil {
		if !strings.Contains(err.Error(), "duplicate key") && !strings.Contains(err.Error(), "23505") {
			return nil, err
		}
	}
	if err := r.db.WithContext(ctx).Create(p2).Error; err != nil {
		if !strings.Contains(err.Error(), "duplicate key") && !strings.Contains(err.Error(), "23505") {
			return nil, err
		}
	}

	return newConv, nil
}

func (r *chatRepositoryImpl) CreateConversation(ctx context.Context, conv *model.Conversation) error {
	return r.db.WithContext(ctx).Create(conv).Error
}

func (r *chatRepositoryImpl) UpdateConversationLastMessage(ctx context.Context, convID, msgID string) error {
	return r.db.WithContext(ctx).Model(&model.Conversation{}).
		Where("id = ?", convID).
		Update("updated_at", time.Now()).Error
}

func (r *chatRepositoryImpl) AddConversationParticipant(ctx context.Context, p *model.ConversationParticipant) error {
	return r.db.WithContext(ctx).Create(p).Error
}

func (r *chatRepositoryImpl) AddConversationParticipants(ctx context.Context, ps []*model.ConversationParticipant) error {
	return r.db.WithContext(ctx).Create(ps).Error
}

func (r *chatRepositoryImpl) CreateGroup(ctx context.Context, group *model.Group) error {
	return r.db.WithContext(ctx).Create(group).Error
}

func (r *chatRepositoryImpl) GetGroupByID(ctx context.Context, groupID string) (*model.Group, error) {
	var group model.Group
	err := r.db.WithContext(ctx).First(&group, "id = ?", groupID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &group, err
}

func (r *chatRepositoryImpl) AddGroupMember(ctx context.Context, member *model.GroupMember) error {
	return r.db.WithContext(ctx).Create(member).Error
}

func (r *chatRepositoryImpl) AddGroupMembers(ctx context.Context, members []*model.GroupMember) error {
	return r.db.WithContext(ctx).Create(members).Error
}

func (r *chatRepositoryImpl) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	return r.db.WithContext(ctx).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Delete(&model.GroupMember{}).Error
}

func (r *chatRepositoryImpl) GetGroupMembers(ctx context.Context, groupID string, page, pageSize int) ([]*model.GroupMember, int64, error) {
	var members []*model.GroupMember
	var total int64

	gid := groupID
	if err := r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ?", gid).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := r.db.WithContext(ctx).Where("group_id = ?", gid).
		Order("created_at ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&members).Error

	return members, total, err
}

func (r *chatRepositoryImpl) GetGroupMember(ctx context.Context, groupID, userID string) (*model.GroupMember, error) {
	var member model.GroupMember
	err := r.db.WithContext(ctx).First(&member, "group_id = ? AND user_id = ?", groupID, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &member, err
}

func (r *chatRepositoryImpl) UpdateGroupMemberRole(ctx context.Context, groupID, userID string, role model.GroupMemberRole) error {
	return r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Update("role", role).Error
}

func (r *chatRepositoryImpl) UpdateGroupMemberMute(ctx context.Context, groupID, userID string, muteType model.MuteType, muteUntil time.Time) error {
	updates := map[string]interface{}{
		"mute_type": muteType,
	}
	if muteType == model.MuteTypeNone {
		updates["mute_until"] = nil
	} else {
		updates["mute_until"] = muteUntil
	}
	return r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Updates(updates).Error
}

func (r *chatRepositoryImpl) TransferGroupOwner(ctx context.Context, groupID, newOwnerID string) error {
	gid := groupID
	nid := newOwnerID
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Group{}).
			Where("id = ?", gid).
			Update("owner_id", nid).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.GroupMember{}).
			Where("group_id = ? AND user_id = ?", gid, nid).
			Update("role", model.GroupMemberRoleOwner).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *chatRepositoryImpl) UpdateGroupAnnouncement(ctx context.Context, groupID, announcement string) error {
	return r.db.WithContext(ctx).Model(&model.Group{}).
		Where("id = ?", groupID).
		Update("announcement", announcement).Error
}

func (r *chatRepositoryImpl) UpdateGroupAvatar(ctx context.Context, groupID, avatar string) error {
	return r.db.WithContext(ctx).Model(&model.Group{}).
		Where("id = ?", groupID).
		Update("avatar", avatar).Error
}

func (r *chatRepositoryImpl) GetAllGroupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	var userIDs []string
	err := r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ?", groupID).
		Pluck("user_id", &userIDs).Error
	return userIDs, err
}

func (r *chatRepositoryImpl) GetAllUserIDs(ctx context.Context) ([]string, error) {
	var userIDs []string
	err := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Distinct("user_id").
		Pluck("user_id", &userIDs).Error
	return userIDs, err
}

func (r *chatRepositoryImpl) UpdateGroupMemberCount(ctx context.Context, groupID string) error {
	var count int64
	if err := r.db.WithContext(ctx).Model(&model.GroupMember{}).
		Where("group_id = ?", groupID).
		Count(&count).Error; err != nil {
		return err
	}

	return r.db.WithContext(ctx).Model(&model.Group{}).
		Where("id = ?", groupID).
		Update("member_count", int(count)).Error
}

func (r *chatRepositoryImpl) DeleteGroup(ctx context.Context, groupID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", groupID).Delete(&model.GroupMember{}).Error; err != nil {
			return err
		}
		if err := tx.Where("conversation_id = ?", groupID).Delete(&model.ConversationParticipant{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", groupID).Delete(&model.Group{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", groupID).Delete(&model.Conversation{}).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *chatRepositoryImpl) DeleteAllGroupMembers(ctx context.Context, groupID string) error {
	return r.db.WithContext(ctx).Where("group_id = ?", groupID).Delete(&model.GroupMember{}).Error
}

func (r *chatRepositoryImpl) GetConversationList(ctx context.Context, userID string, page, pageSize int) ([]*model.ConversationItem, int64, error) {
	var convIDs []string
	logger.Info("[DEBUG] GetConversationList called", logger.StringField("user_id", userID))
	if err := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("user_id = ?", userID).
		Pluck("conversation_id", &convIDs).Error; err != nil {
		logger.Error("[DEBUG] Failed to get conv IDs", logger.ErrorField(err))
		return nil, 0, err
	}
	logger.Info("[DEBUG] Found conv IDs", logger.StringField("user_id", userID), logger.AnyField("conv_ids", convIDs))

	if len(convIDs) == 0 {
		logger.Info("[DEBUG] No conv IDs found")
		return []*model.ConversationItem{}, 0, nil
	}

	var conversations []model.Conversation
	if err := r.db.WithContext(ctx).
		Where("id IN ?", convIDs).
		Order("updated_at DESC").
		Find(&conversations).Error; err != nil {
		logger.Error("[DEBUG] Failed to get conversations", logger.ErrorField(err))
		return nil, 0, err
	}
	logger.Info("[DEBUG] Found conversations", logger.IntField("count", len(conversations)), logger.AnyField("conversations", conversations))

	total := int64(len(conversations))
	offset := (page - 1) * pageSize
	end := offset + pageSize
	if offset > int(total) {
		return []*model.ConversationItem{}, total, nil
	}
	if end > int(total) {
		end = int(total)
	}
	conversations = conversations[offset:end]

	unreadMap, _ := r.GetUnreadCounts(ctx, userID, convIDs)

	var items []*model.ConversationItem
	for _, conv := range conversations {
		item := &model.ConversationItem{
			ChatID:      conv.ID,
			ChatType:    conv.ChatType,
			Name:        conv.Name,
			Avatar:      conv.Avatar,
			UpdatedAt:   conv.UpdatedAt,
			UnreadCount: unreadMap[conv.ID],
		}
		items = append(items, item)
	}

	return items, total, nil
}

func (r *chatRepositoryImpl) GetUnreadCounts(ctx context.Context, userID string, chatIDs []string) (map[string]int64, error) {
	result := make(map[string]int64)
	if len(chatIDs) == 0 {
		return result, nil
	}

	var participants []model.ConversationParticipant
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND conversation_id IN ?", userID, chatIDs).
		Find(&participants).Error; err != nil {
		return result, err
	}

	for _, p := range participants {
		var count int64
		// 未读数 = seq 大于已读位点的消息数（排除自己发的），不再依赖逐条 is_read
		r.db.WithContext(ctx).Model(&model.Message{}).
			Where("chat_id = ? AND seq > ? AND sender_id != ?",
				p.ConversationID, p.LastReadSeq, userID).
			Count(&count)
		result[p.ConversationID] = count
	}

	return result, nil
}

func (r *chatRepositoryImpl) GetTotalUnreadCount(ctx context.Context, userID string) (int64, error) {
	var convIDs []string
	if err := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("user_id = ?", userID).
		Pluck("conversation_id", &convIDs).Error; err != nil {
		return 0, err
	}

	unreadMap, err := r.GetUnreadCounts(ctx, userID, convIDs)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, count := range unreadMap {
		total += count
	}
	return total, nil
}

func (r *chatRepositoryImpl) DeleteConversationForUser(ctx context.Context, conversationID, userID string) error {
	err := r.db.WithContext(ctx).
		Where("conversation_id = ? AND user_id = ?", conversationID, userID).
		Delete(&model.ConversationParticipant{}).Error
	if err != nil {
		return err
	}

	var count int64
	err = r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("conversation_id = ?", conversationID).
		Count(&count).Error
	if err != nil {
		return err
	}

	if count == 0 {
		err = r.db.WithContext(ctx).
			Where("id = ?", conversationID).
			Delete(&model.Conversation{}).Error
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *chatRepositoryImpl) GetUsernameByID(ctx context.Context, userID int64) (string, error) {
	var username string
	err := r.db.WithContext(ctx).Table("users").
		Where("id = ? AND deleted_at IS NULL", userID).
		Pluck("username", &username).Error
	if err != nil {
		return "", err
	}
	return username, nil
}
