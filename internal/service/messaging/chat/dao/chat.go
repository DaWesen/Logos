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

// notDeletedByUserSQL 从观察者视角排除其本人删除过的消息（删除是私有状态，不影响其他人）。
// 用 NOT EXISTS 而非 LEFT JOIN：同一消息即使有重复墓碑也不会放大结果行；
// 子查询命中 message_deletions 的 (user_id, message_id) 唯一索引，成本与消息条数同级。
// 占位符传入 userID。
const notDeletedByUserSQL = "NOT EXISTS (SELECT 1 FROM message_deletions d WHERE d.user_id = ? AND d.message_id = messages.id)"

type ChatRepository interface {
	CreateMessage(ctx context.Context, msg *model.Message) error
	// CreateMessageIdempotent 以 (sender_id, request_id) 幂等键写入消息。
	// 返回 created=false 表示该 request_id 已处理过，existing 为首次创建的消息（幂等回放）。
	CreateMessageIdempotent(ctx context.Context, msg *model.Message) (created bool, existing *model.Message, err error)
	UpdateMessage(ctx context.Context, msg *model.Message) error
	GetMessageByID(ctx context.Context, id string) (*model.Message, error)
	// GetMessagesByChatID 返回会话历史消息；minSeq > 0 时跳过 seq <= minSeq 的消息（用户已清空的部分）。
	// userID 是「观察者」，用于排除其本人删除过的消息：同一会话不同成员看到的集合可以不同。
	GetMessagesByChatID(ctx context.Context, userID, chatID string, beforeTime time.Time, limit int, minSeq int64) ([]*model.Message, error)
	// GetMessagesAfterSeq 返回会话内 seq 大于 afterSeq 的消息，按 seq 升序，用于断线重连后的增量补拉。
	// userID 同上，用于排除本人已删除的消息。
	GetMessagesAfterSeq(ctx context.Context, userID, chatID string, afterSeq int64, limit int) ([]*model.Message, error)
	// SearchMessages 会话内搜索；minSeq > 0 时跳过用户已清空的历史，且排除本人已删除的消息。
	SearchMessages(ctx context.Context, userID, chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int, minSeq int64) ([]*model.Message, int64, error)
	// MarkMessagesRead 逐条标记指定消息为已读。
	// 仅用于已读回执携带的显式、有界 msgIDs（供前端展示已读态）；
	// 未读数统计走 AdvanceReadPosition 的位点比对，不依赖本方法。
	MarkMessagesRead(ctx context.Context, msgIDs []string) error
	// GetMaxMessageSeq 返回会话当前最大消息 seq（无消息返回 0）
	GetMaxMessageSeq(ctx context.Context, chatID string) (int64, error)
	// GetMaxSeqByMessageIDs 返回指定消息中的最大 seq（用于按位点标记已读）
	GetMaxSeqByMessageIDs(ctx context.Context, msgIDs []string) (int64, error)
	// WithdrawMessage 在事务内撤回消息（写状态 + version+1）并追加一条原位变更日志。
	// 两者同事务，保证变更日志不会与消息内容分叉；任何一步失败都整体回滚。
	WithdrawMessage(ctx context.Context, msgID string, change *model.MessageChange) error
	// EditMessage 按版本号做乐观锁更新内容，并将 version +1；可选地在同一事务内追加原位变更日志。
	// updated=false 表示预期版本与服务端不一致（已被其他编辑推进）或消息已不存在，调用方应返回冲突。
	EditMessage(ctx context.Context, msgID, content string, expectedVersion int64, change *model.MessageChange) (updated bool, err error)
	// GetMessageChangesAfter 返回会话内 updated_seq > afterID 的原位变更，按 updated_seq 升序，
	// 供离线设备补拉编辑/撤回（这些变更不改消息 seq，无法由 LastSeq 游标表达）。
	GetMessageChangesAfter(ctx context.Context, chatID string, afterID int64, limit int) ([]*model.MessageChange, error)
	// PurgeMessageChangesBefore 按保留期回收原位变更日志，返回删除行数。
	// 变更日志是会话级共享的（不能按某个用户的 cleared_seq 删除，别人还要补拉），只能按时间回收。
	PurgeMessageChangesBefore(ctx context.Context, before time.Time) (int64, error)
	// GetMessagesByIDs 批量取消息，用于把客户端持有的 message_id 映射到会话内 seq。
	GetMessagesByIDs(ctx context.Context, msgIDs []string) ([]*model.Message, error)
	// GetReadPositions 返回会话内各成员的已读位点，已读回执由位点比对聚合得出。
	GetReadPositions(ctx context.Context, conversationID string) ([]ReadPosition, error)
	// GetParticipantWatermarks 批量取用户在各会话的位点；无记录即非成员，用于离线同步的访问校验。
	GetParticipantWatermarks(ctx context.Context, userID string, conversationIDs []string) (map[string]ParticipantWatermark, error)
	// GetMaxSeqByChatIDs 批量取各会话当前最大 seq，用于推进客户端同步游标。
	GetMaxSeqByChatIDs(ctx context.Context, chatIDs []string) (map[string]int64, error)
	// GetClearedSeq 返回用户在会话内的清空水位线（无记录时为 0）。
	GetClearedSeq(ctx context.Context, conversationID, userID string) (int64, error)
	// ClearConversationForUser 把用户的清空水位线单调推进到 seq（同时推进已读位点）。
	// 只隐藏本人视角的历史，不删除消息记录。
	ClearConversationForUser(ctx context.Context, conversationID, userID string, seq int64) error
	// DeleteMessageForUser 为 userID 写入「删除某条消息」的墓碑（幂等）。
	// created=false 表示该用户此前已删除过这条消息，调用方无需重复推送事件。
	DeleteMessageForUser(ctx context.Context, userID, messageID, chatID string, messageSeq int64) (created bool, err error)
	// GetMessageDeletionsAfter 返回用户在会话内删除序号 > afterID 的墓碑，按删除序号升序，
	// 供客户端用独立的 deleted_seq 游标补齐「删除」这类不可由消息 seq 表达的变更。
	GetMessageDeletionsAfter(ctx context.Context, userID, chatID string, afterID int64, limit int) ([]*model.MessageDeletion, error)
	// PurgeMessageDeletions 清理用户在会话内 seq <= upToSeq 的墓碑（本人已清空的历史无需再下发删除）。
	PurgeMessageDeletions(ctx context.Context, userID, chatID string, upToSeq int64) error

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
		return r.createMessageTx(tx, msg)
	})
}

// createMessageTx 在给定事务内分配会话 seq 并写入消息。
func (r *chatRepositoryImpl) createMessageTx(tx *gorm.DB, msg *model.Message) error {
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
}

// CreateMessageIdempotent 以 (sender_id, request_id) 幂等键写入消息。
//
// 事务内先占位幂等键、再写消息：
//   - 幂等键插入成功：本次是首次请求，继续写消息，任一步失败则整体回滚（占位一起释放，客户端可重试）；
//   - 幂等键冲突（DoNothing 命中 0 行）：说明并发/重试请求已处理过，直接回放首次创建的消息。
//
// 冲突方会等待首个事务提交后再读到其消息，因此不会读到「占位但消息还没写」的中间态。
func (r *chatRepositoryImpl) CreateMessageIdempotent(ctx context.Context, msg *model.Message) (bool, *model.Message, error) {
	var created bool
	var result *model.Message

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := &model.MessageIdempotency{
			SenderID:  msg.SenderID,
			RequestID: msg.RequestID,
			MessageID: msg.ID,
			CreatedAt: time.Now(),
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(claim)
		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			var existingClaim model.MessageIdempotency
			if err := tx.Where("sender_id = ? AND request_id = ?", msg.SenderID, msg.RequestID).
				First(&existingClaim).Error; err != nil {
				return err
			}
			var existing model.Message
			if err := tx.Where("id = ?", existingClaim.MessageID).First(&existing).Error; err != nil {
				return err
			}
			result = &existing
			return nil
		}

		if err := r.createMessageTx(tx, msg); err != nil {
			return err
		}
		created = true
		result = msg
		return nil
	})
	return created, result, err
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
func (r *chatRepositoryImpl) GetMessagesByChatID(ctx context.Context, userID, chatID string, beforeTime time.Time, limit int, minSeq int64) ([]*model.Message, error) {
	var messages []*model.Message
	query := r.db.WithContext(ctx).Where("chat_id = ?", chatID).
		Where(notDeletedByUserSQL, userID)
	if minSeq > 0 {
		query = query.Where("seq > ?", minSeq)
	}
	if !beforeTime.IsZero() {
		query = query.Where("created_at < ?", beforeTime)
	}
	err := query.Order("seq DESC, created_at DESC").Limit(limit).Find(&messages).Error
	return messages, err
}

// GetMessagesAfterSeq 增量补拉：只取 seq 严格大于 afterSeq 的消息，按 seq 升序返回，
// 使调用方无需再反转即可直接追加到会话末尾。用于 WS 断线重连后填补 PubSub 丢失的窗口。
// 同时排除观察者本人删除过的消息（删除是私有状态，不影响其他成员）。
func (r *chatRepositoryImpl) GetMessagesAfterSeq(ctx context.Context, userID, chatID string, afterSeq int64, limit int) ([]*model.Message, error) {
	var messages []*model.Message
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND seq > ?", chatID, afterSeq).
		Where(notDeletedByUserSQL, userID).
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

// ReadPosition 是会话成员的已读位点投影，用于聚合已读回执。
type ReadPosition struct {
	UserID      string `gorm:"column:user_id"`
	LastReadSeq int64  `gorm:"column:last_read_seq"`
}

func (r *chatRepositoryImpl) GetMessagesByIDs(ctx context.Context, msgIDs []string) ([]*model.Message, error) {
	if len(msgIDs) == 0 {
		return nil, nil
	}
	var messages []*model.Message
	err := r.db.WithContext(ctx).Where("id IN ?", msgIDs).Find(&messages).Error
	return messages, err
}

// ParticipantWatermark 是会话参与者的位点投影（已读位点 + 清空水位线）。
type ParticipantWatermark struct {
	ConversationID string `gorm:"column:conversation_id"`
	LastReadSeq    int64  `gorm:"column:last_read_seq"`
	ClearedSeq     int64  `gorm:"column:cleared_seq"`
}

// GetParticipantWatermarks 用一次查询拿到用户在多个会话的位点。
// 返回的 map 同时充当访问白名单：没有参与记录（非成员、已退群、被踢）的会话不会出现在结果里。
func (r *chatRepositoryImpl) GetParticipantWatermarks(ctx context.Context, userID string, conversationIDs []string) (map[string]ParticipantWatermark, error) {
	result := make(map[string]ParticipantWatermark, len(conversationIDs))
	if userID == "" || len(conversationIDs) == 0 {
		return result, nil
	}
	var rows []ParticipantWatermark
	err := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("user_id = ? AND conversation_id IN ?", userID, conversationIDs).
		Select("conversation_id, last_read_seq, cleared_seq").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ConversationID] = row
	}
	return result, nil
}

// GetMaxSeqByChatIDs 用一次 GROUP BY 取各会话当前最大 seq，避免按会话逐个查询。
func (r *chatRepositoryImpl) GetMaxSeqByChatIDs(ctx context.Context, chatIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(chatIDs))
	if len(chatIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		ChatID string `gorm:"column:chat_id"`
		Seq    int64  `gorm:"column:seq"`
	}
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Where("chat_id IN ?", chatIDs).
		Select("chat_id, COALESCE(MAX(seq), 0) AS seq").
		Group("chat_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ChatID] = row.Seq
	}
	return result, nil
}

// GetReadPositions 一次取回会话内所有成员的已读位点（会话成员数有上限），
// 由服务层在内存中按消息 seq 聚合，避免为每条消息各发一次 COUNT。
func (r *chatRepositoryImpl) GetReadPositions(ctx context.Context, conversationID string) ([]ReadPosition, error) {
	var positions []ReadPosition
	err := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("conversation_id = ?", conversationID).
		Select("user_id, last_read_seq").
		Scan(&positions).Error
	return positions, err
}

func (r *chatRepositoryImpl) SearchMessages(ctx context.Context, userID, chatID string, chatType model.ChatType, keyword string, startTime, endTime time.Time, page, pageSize int, minSeq int64) ([]*model.Message, int64, error) {
	var messages []*model.Message
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Message{}).
		Where(notDeletedByUserSQL, userID)
	if chatID != "" {
		query = query.Where("chat_id = ?", chatID)
	}
	if minSeq > 0 {
		query = query.Where("seq > ?", minSeq)
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

// WithdrawMessage 在同一事务内撤回消息并追加原位变更日志。
//
// 撤回收不到 edit 的版本校验保护，这里直接把 version +1：让「撤回」也能像编辑一样
// 参与版本比较，客户端才不会把更早发出的编辑事件应用回已撤回的消息上。
func (r *chatRepositoryImpl) WithdrawMessage(ctx context.Context, msgID string, change *model.MessageChange) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.Message{}).
			Where("id = ?", msgID).
			Updates(map[string]interface{}{
				"status":       model.MessageStatusWithdrawn.String(),
				"content":      "[消息已撤回]",
				"message_type": int(model.MessageTypeText),
				"media_url":    "",
				"media_meta":   nil,
				"version":      gorm.Expr("version + 1"),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return appendMessageChange(tx, change)
	})
}

// EditMessage 以 version 为乐观锁更新内容：SQL 层原子执行
// `UPDATE messages SET content=?, status='edited', version=version+1 WHERE id=? AND version=?`。
// 只有客户端读到的版本仍是服务端当前版本时才写入，因此两个设备并发编辑时只有一个能成功，
// 另一个拿到 updated=false（而不是把对方的编辑静默覆盖）。条件更新无需显式事务与行锁。
func (r *chatRepositoryImpl) EditMessage(ctx context.Context, msgID, content string, expectedVersion int64, change *model.MessageChange) (bool, error) {
	var updated bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.Message{}).
			Where("id = ? AND version = ?", msgID, expectedVersion).
			Updates(map[string]interface{}{
				"content": content,
				"status":  model.MessageStatusEdited.String(),
				"version": gorm.Expr("version + 1"),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 版本已被其他编辑推进（或消息在读取后被删除/撤回）：不做任何写入，事务空提交。
			return nil
		}
		updated = true
		if change != nil {
			// 变更日志与内容同事务提交：否则日志缺失，离线端永远补不到这次编辑。
			change.Version = expectedVersion + 1
			return appendMessageChange(tx, change)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return updated, nil
}

// appendMessageChange 写入一条原位变更日志；change.CreatedAt 为空时补当前时间。
func appendMessageChange(tx *gorm.DB, change *model.MessageChange) error {
	if change == nil {
		return nil
	}
	if change.CreatedAt.IsZero() {
		change.CreatedAt = time.Now()
	}
	return tx.Create(change).Error
}

// GetMessageChangesAfter 按 updated_seq（自增 ID）升序取增量，过滤条件命中 (chat_id) 索引。
func (r *chatRepositoryImpl) GetMessageChangesAfter(ctx context.Context, chatID string, afterID int64, limit int) ([]*model.MessageChange, error) {
	if limit <= 0 {
		return nil, nil
	}
	var changes []*model.MessageChange
	err := r.db.WithContext(ctx).
		Where("chat_id = ? AND id > ?", chatID, afterID).
		Order("id ASC").
		Limit(limit).
		Find(&changes).Error
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// PurgeMessageChangesBefore 按保留期回收原位变更日志。
func (r *chatRepositoryImpl) PurgeMessageChangesBefore(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, nil
	}
	res := r.db.WithContext(ctx).
		Where("created_at < ?", before).
		Delete(&model.MessageChange{})
	return res.RowsAffected, res.Error
}

// GetClearedSeq 读取用户的清空水位线；没有参与者记录时返回 0（等价于未清空）。
func (r *chatRepositoryImpl) GetClearedSeq(ctx context.Context, conversationID, userID string) (int64, error) {
	var seq int64
	err := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("conversation_id = ? AND user_id = ?", conversationID, userID).
		Select("COALESCE(cleared_seq, 0)").
		Scan(&seq).Error
	return seq, err
}

// ClearConversationForUser 单调推进用户的清空水位线：
//   - 使用 GREATEST 保证重复调用/乱序调用不会把水位线回退（幂等）；
//   - 同时推进 last_read_seq，避免清空后未读数仍按旧位点计算；
//   - 参与者记录不存在时补建，保证新会话也能清空。
func (r *chatRepositoryImpl) ClearConversationForUser(ctx context.Context, conversationID, userID string, seq int64) error {
	now := time.Now()
	updates := map[string]interface{}{
		"cleared_seq":   gorm.Expr("GREATEST(cleared_seq, ?)", seq),
		"last_read_seq": gorm.Expr("GREATEST(last_read_seq, ?)", seq),
		"updated_at":    now,
	}
	res := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
		Where("conversation_id = ? AND user_id = ?", conversationID, userID).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}

	p := &model.ConversationParticipant{
		ConversationID: conversationID,
		UserID:         userID,
		ClearedSeq:     seq,
		LastReadSeq:    seq,
		JoinedAt:       now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		// 并发补建可能撞唯一索引：对方已插入时再更新一次即可，仍保持幂等。
		res := r.db.WithContext(ctx).Model(&model.ConversationParticipant{}).
			Where("conversation_id = ? AND user_id = ?", conversationID, userID).
			Updates(updates)
		return res.Error
	}
	return nil
}

// DeleteMessageForUser 写入单条消息删除墓碑。
// 唯一键 (user_id, message_id) 保证幂等：重复删除不会产生第二行，也不会报错。
// created=false 表示此前已删除过，调用方据此跳过重复推送。
func (r *chatRepositoryImpl) DeleteMessageForUser(ctx context.Context, userID, messageID, chatID string, messageSeq int64) (bool, error) {
	res := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&model.MessageDeletion{
			UserID:     userID,
			MessageID:  messageID,
			ChatID:     chatID,
			MessageSeq: messageSeq,
			CreatedAt:  time.Now(),
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// GetMessageDeletionsAfter 按删除序号升序返回墓碑，索引 (user_id, chat_id) + 主键避免额外排序。
func (r *chatRepositoryImpl) GetMessageDeletionsAfter(ctx context.Context, userID, chatID string, afterID int64, limit int) ([]*model.MessageDeletion, error) {
	var rows []*model.MessageDeletion
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND chat_id = ? AND id > ?", userID, chatID, afterID).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

// PurgeMessageDeletions 清理已被清空水位线覆盖的墓碑：
// 这些消息本来就不会再下发给本人，墓碑失去信息价值；失败只影响存储体积，不影响正确性。
func (r *chatRepositoryImpl) PurgeMessageDeletions(ctx context.Context, userID, chatID string, upToSeq int64) error {
	if upToSeq <= 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Where("user_id = ? AND chat_id = ? AND message_seq <= ?", userID, chatID, upToSeq).
		Delete(&model.MessageDeletion{}).Error
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

// GetUnreadCounts 返回各会话的未读数。
//
// 未读数 = 会话内 seq 大于本人已读位点、且大于清空水位线的他人消息数。
// 早期实现按会话逐条 COUNT（N+1：会话列表 100 条就是 100 次查询），
// 现改为 JOIN 参与者表后一次 GROUP BY 聚合，查询次数与会话数无关。
// 本人删除过的消息不计入未读：删除是显式的「我已处理」，但不再逐条回写位点。
func (r *chatRepositoryImpl) GetUnreadCounts(ctx context.Context, userID string, chatIDs []string) (map[string]int64, error) {
	result := make(map[string]int64)
	if len(chatIDs) == 0 {
		return result, nil
	}

	var rows []struct {
		ChatID string
		Count  int64
	}
	err := r.db.WithContext(ctx).Model(&model.Message{}).
		Select("messages.chat_id AS chat_id, COUNT(*) AS count").
		Joins("JOIN conversation_participants p ON p.conversation_id = messages.chat_id AND p.user_id = ?", userID).
		Where("messages.chat_id IN ?", chatIDs).
		Where("messages.seq > p.last_read_seq AND messages.seq > p.cleared_seq").
		Where("messages.sender_id != ?", userID).
		Where(notDeletedByUserSQL, userID).
		Group("messages.chat_id").
		Scan(&rows).Error
	if err != nil {
		return result, err
	}

	for _, row := range rows {
		result[row.ChatID] = row.Count
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
