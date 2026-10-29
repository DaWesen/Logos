package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

// TestMain 切换到仓库根目录：config.GetConfig 以 ./config 为相对路径，
// 而 service 层的日志依赖配置初始化。
func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				_ = os.Chdir(dir)
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	os.Exit(m.Run())
}

// stubChatRepo 只实现访问控制路径用到的查询，其余方法由内嵌接口兜底（误调用会 panic）。
type stubChatRepo struct {
	dao.ChatRepository
	groups     map[string]*model.Group
	members    map[string]*model.GroupMember
	maxSeqs    map[string]int64
	cleared    []clearCall
	messages   map[string]*model.Message
	positions  map[string][]dao.ReadPosition
	watermarks map[string]dao.ParticipantWatermark
	chatSeqs   map[string]int64
	chatMsgs   map[string][]*model.Message
	// deleted 按用户维度记录已删除消息 ID（删除是私有状态，必须按 viewer 过滤）
	deleted map[string]map[string]bool
	// deletions 按写入顺序保存墓碑，ID 单调递增，模拟自增主键
	deletions []*model.MessageDeletion
	// changes 按写入顺序保存原位变更日志，ID 单调递增，模拟自增主键
	changes []*model.MessageChange
	purged  []purgeCall
}

type purgeCall struct {
	chatID  string
	userID  string
	upToSeq int64
}

type clearCall struct {
	chatID string
	userID string
	seq    int64
}

func (r *stubChatRepo) GetGroupByID(_ context.Context, groupID string) (*model.Group, error) {
	return r.groups[groupID], nil
}

func (r *stubChatRepo) GetGroupMember(_ context.Context, groupID, userID string) (*model.GroupMember, error) {
	return r.members[groupID+"|"+userID], nil
}

func (r *stubChatRepo) GetMaxMessageSeq(_ context.Context, chatID string) (int64, error) {
	return r.maxSeqs[chatID], nil
}

// GetAllGroupMemberIDs 按「groupID|userID」键前缀汇总群成员，供编辑事件接收者计算使用。
func (r *stubChatRepo) GetAllGroupMemberIDs(_ context.Context, groupID string) ([]string, error) {
	prefix := groupID + "|"
	var ids []string
	for key := range r.members {
		if strings.HasPrefix(key, prefix) {
			ids = append(ids, strings.TrimPrefix(key, prefix))
		}
	}
	return ids, nil
}

func (r *stubChatRepo) ClearConversationForUser(_ context.Context, conversationID, userID string, seq int64) error {
	r.cleared = append(r.cleared, clearCall{chatID: conversationID, userID: userID, seq: seq})
	return nil
}

func (r *stubChatRepo) GetMessagesByIDs(_ context.Context, msgIDs []string) ([]*model.Message, error) {
	out := make([]*model.Message, 0, len(msgIDs))
	for _, id := range msgIDs {
		if m, ok := r.messages[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *stubChatRepo) GetReadPositions(_ context.Context, conversationID string) ([]dao.ReadPosition, error) {
	return r.positions[conversationID], nil
}

func (r *stubChatRepo) GetParticipantWatermarks(_ context.Context, _ string, conversationIDs []string) (map[string]dao.ParticipantWatermark, error) {
	out := make(map[string]dao.ParticipantWatermark, len(conversationIDs))
	for _, id := range conversationIDs {
		if wm, ok := r.watermarks[id]; ok {
			out[id] = wm
		}
	}
	return out, nil
}

func (r *stubChatRepo) GetMaxSeqByChatIDs(_ context.Context, chatIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(chatIDs))
	for _, id := range chatIDs {
		if seq, ok := r.chatSeqs[id]; ok {
			out[id] = seq
		}
	}
	return out, nil
}

// GetMessagesAfterSeq 按 seq 升序返回增量，行为与真实实现一致（limit 由调用方给 limit+1）：
// 先过滤本人删除过的消息（删除是私有状态），再按 seq 截断。
func (r *stubChatRepo) GetMessagesAfterSeq(_ context.Context, userID, chatID string, afterSeq int64, limit int) ([]*model.Message, error) {
	var out []*model.Message
	for _, m := range r.chatMsgs[chatID] {
		if m.Seq <= afterSeq {
			continue
		}
		if r.deleted[userID][m.ID] {
			continue
		}
		out = append(out, m)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *stubChatRepo) GetMessageByID(_ context.Context, id string) (*model.Message, error) {
	return r.messages[id], nil
}

// EditMessage 模拟 SQL 条件更新（WHERE id=? AND version=?）：只有版本匹配才写入并自增，
// 与真实实现一致，同时在「同一事务」内追加变更日志。
func (r *stubChatRepo) EditMessage(_ context.Context, msgID, content string, expectedVersion int64, change *model.MessageChange) (bool, error) {
	m, ok := r.messages[msgID]
	if !ok || m == nil || m.Version != expectedVersion {
		return false, nil
	}
	m.Content = content
	m.Status = model.MessageStatusEdited.String()
	m.Version++
	if change != nil {
		change.Version = expectedVersion + 1
		change.CreatedAt = time.Now()
		r.appendChange(change)
	}
	return true, nil
}

// WithdrawMessage 模拟真实事务：改状态 + version+1，并追加撤回变更日志。
func (r *stubChatRepo) WithdrawMessage(_ context.Context, msgID string, change *model.MessageChange) error {
	m := r.messages[msgID]
	if m == nil {
		return nil
	}
	m.Status = model.MessageStatusWithdrawn.String()
	m.Content = "[消息已撤回]"
	m.Version++
	if change != nil {
		change.Version = m.Version
		change.CreatedAt = time.Now()
		r.appendChange(change)
	}
	return nil
}

// appendChange 给日志分配自增 ID（模拟数据库自增主键）并保存。
func (r *stubChatRepo) appendChange(change *model.MessageChange) {
	change.ID = int64(len(r.changes) + 1)
	r.changes = append(r.changes, change)
}

// GetMessageChangesAfter 模拟 `WHERE chat_id=? AND id>? ORDER BY id ASC LIMIT`。
func (r *stubChatRepo) GetMessageChangesAfter(_ context.Context, chatID string, afterID int64, limit int) ([]*model.MessageChange, error) {
	var out []*model.MessageChange
	for _, ch := range r.changes {
		if ch.ChatID != chatID || ch.ID <= afterID {
			continue
		}
		out = append(out, ch)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PurgeMessageChangesBefore 模拟按保留期删除日志，返回删除行数。
func (r *stubChatRepo) PurgeMessageChangesBefore(_ context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, nil
	}
	kept := make([]*model.MessageChange, 0, len(r.changes))
	var removed int64
	for _, ch := range r.changes {
		if ch.CreatedAt.Before(before) {
			removed++
			continue
		}
		kept = append(kept, ch)
	}
	r.changes = kept
	return removed, nil
}

func (r *stubChatRepo) GetClearedSeq(_ context.Context, conversationID, _ string) (int64, error) {
	return r.watermarks[conversationID].ClearedSeq, nil
}

// DeleteMessageForUser 模拟唯一键 (user_id, message_id) + ON CONFLICT DO NOTHING：
// 重复删除返回 created=false 且不产生第二条墓碑。
func (r *stubChatRepo) DeleteMessageForUser(_ context.Context, userID, messageID, chatID string, messageSeq int64) (bool, error) {
	if r.deleted == nil {
		r.deleted = make(map[string]map[string]bool)
	}
	if r.deleted[userID] == nil {
		r.deleted[userID] = make(map[string]bool)
	}
	if r.deleted[userID][messageID] {
		return false, nil
	}
	r.deleted[userID][messageID] = true
	// 自增主键按写入顺序单调递增，与真实表一致。
	r.deletions = append(r.deletions, &model.MessageDeletion{
		ID:         int64(len(r.deletions) + 1),
		UserID:     userID,
		MessageID:  messageID,
		ChatID:     chatID,
		MessageSeq: messageSeq,
	})
	return true, nil
}

func (r *stubChatRepo) GetMessageDeletionsAfter(_ context.Context, userID, chatID string, afterID int64, limit int) ([]*model.MessageDeletion, error) {
	var out []*model.MessageDeletion
	for _, d := range r.deletions {
		if d.UserID != userID || d.ChatID != chatID || d.ID <= afterID {
			continue
		}
		out = append(out, d)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PurgeMessageDeletions 只记录调用参数并从墓碑列表移除；
// 真实实现中这些消息已位于清空水位线之下，客户端不会再拉取，故 stub 不模拟"恢复可见性"。
func (r *stubChatRepo) PurgeMessageDeletions(_ context.Context, userID, chatID string, upToSeq int64) error {
	r.purged = append(r.purged, purgeCall{chatID: chatID, userID: userID, upToSeq: upToSeq})
	if upToSeq <= 0 {
		return nil
	}
	kept := make([]*model.MessageDeletion, 0, len(r.deletions))
	for _, d := range r.deletions {
		if d.UserID == userID && d.ChatID == chatID && d.MessageSeq <= upToSeq {
			continue
		}
		kept = append(kept, d)
	}
	r.deletions = kept
	return nil
}

func newAccessService(repo dao.ChatRepository) *ChatServiceImpl {
	return &ChatServiceImpl{repo: repo, ctx: context.Background()}
}

func TestCheckPrivateChatAccess(t *testing.T) {
	tests := []struct {
		name    string
		chatID  string
		userID  string
		allowed bool
	}{
		{name: "private 格式 参与者", chatID: "private_1001_1002", userID: "1002", allowed: true},
		{name: "private 格式 非参与者", chatID: "private_1001_1002", userID: "1003", allowed: false},
		{name: "a_b 格式 参与者", chatID: "1001_1002", userID: "1001", allowed: true},
		{name: "a_b 格式 非参与者", chatID: "1001_1002", userID: "1003", allowed: false},
		{name: "单段 ID 仅本人命名空间", chatID: "1001", userID: "1001", allowed: true},
		{name: "单段 ID 他人命名空间拒绝", chatID: "1001", userID: "1002", allowed: false},
		{name: "bot 会话放行", chatID: "bot-uuid-1", userID: "any", allowed: true},
		{name: "空会话拒绝", chatID: "", userID: "1001", allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkPrivateChatAccess(tt.chatID, tt.userID)
			if tt.allowed && err != nil {
				t.Fatalf("应放行，实际拒绝: %v", err)
			}
			if !tt.allowed && err != types.ErrChatAccessDenied {
				t.Fatalf("应返回 ErrChatAccessDenied，实际: %v", err)
			}
		})
	}
}

func TestEnsureChatAccess(t *testing.T) {
	repo := &stubChatRepo{
		groups: map[string]*model.Group{
			"group-1": {ID: "group-1"},
		},
		members: map[string]*model.GroupMember{
			"group-1|member": {GroupID: "group-1", UserID: "member"},
		},
	}
	svc := newAccessService(repo)

	tests := []struct {
		name     string
		userID   string
		chatID   string
		chatType model.ChatType
		want     error
	}{
		{name: "群成员放行", userID: "member", chatID: "group-1", chatType: model.ChatTypeGroup},
		{name: "非群成员拒绝", userID: "outsider", chatID: "group-1", chatType: model.ChatTypeGroup, want: types.ErrChatAccessDenied},
		{name: "缺省 chatType 反查为群聊并校验", userID: "outsider", chatID: "group-1", chatType: 0, want: types.ErrChatAccessDenied},
		{name: "私聊参与者放行", userID: "1001", chatID: "private_1001_1002", chatType: model.ChatTypePrivate},
		{name: "私聊旁观者拒绝", userID: "1003", chatID: "private_1001_1002", chatType: 0, want: types.ErrChatAccessDenied},
		{name: "空 chatID 拒绝", userID: "1001", chatID: "", chatType: model.ChatTypePrivate, want: types.ErrChatAccessDenied},
		{name: "空 userID 拒绝", userID: "", chatID: "private_1001_1002", chatType: model.ChatTypePrivate, want: types.ErrChatAccessDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.EnsureChatAccess(tt.userID, tt.chatID, tt.chatType)
			if err != tt.want {
				t.Fatalf("期望 %v，实际 %v", tt.want, err)
			}
		})
	}
}

func TestAuthorizeIncomingMessage(t *testing.T) {
	tests := []struct {
		name    string
		event   types.MessageEvent
		allowed bool
	}{
		{
			name:    "群成员放行",
			event:   types.MessageEvent{ChatType: types.ChatTypeGroup, ChatID: "group-1", SenderID: "member"},
			allowed: true,
		},
		{
			name:  "非群成员拒绝",
			event: types.MessageEvent{ChatType: types.ChatTypeGroup, ChatID: "group-1", SenderID: "outsider"},
		},
		{
			name:    "私聊参与者放行（文本）",
			event:   types.MessageEvent{ChatType: types.ChatTypePrivate, ChatID: "private_1001_1002", SenderID: "1001"},
			allowed: true,
		},
		{
			name:    "私聊参与者放行（媒体）",
			event:   types.MessageEvent{ChatType: types.ChatTypePrivate, ChatID: "private_1001_1002", SenderID: "1002", MessageType: types.MessageTypeFile},
			allowed: true,
		},
		{
			name:  "私聊旁观者拒绝（媒体注入）",
			event: types.MessageEvent{ChatType: types.ChatTypePrivate, ChatID: "private_1001_1002", SenderID: "1003", MessageType: types.MessageTypeImage},
		},
		{
			name:    "QQ 桥接私聊放行",
			event:   types.MessageEvent{ChatType: types.ChatTypePrivate, ChatID: "private_1001_self", SenderID: "1001", Channel: "qq"},
			allowed: true,
		},
		{
			name:    "系统消息放行",
			event:   types.MessageEvent{ChatType: types.ChatTypePrivate, ChatID: "private_1001_1002", SenderID: "system"},
			allowed: true,
		},
		{
			name:    "Bot 会话放行",
			event:   types.MessageEvent{ChatType: types.ChatTypePrivate, ChatID: "bot-uuid-1", SenderID: "bot_bot-uuid-1"},
			allowed: true,
		},
		{
			name:    "广播类型不限制",
			event:   types.MessageEvent{ChatType: types.ChatTypeBroadcast, ChatID: "broadcast", SenderID: "1001"},
			allowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &stubChatRepo{
				groups: map[string]*model.Group{},
				members: map[string]*model.GroupMember{
					"group-1|member": {GroupID: "group-1", UserID: "member"},
				},
			}
			svc := newAccessService(repo)

			if got := svc.authorizeIncomingMessage(&tt.event); got != tt.allowed {
				t.Fatalf("期望放行=%v，实际=%v", tt.allowed, got)
			}
		})
	}
}

// TestClearChatHistoryWatermark 清空历史不再是全局删除：只把请求者自己的 cleared_seq
// 推进到会话当前最大 seq，其他成员的历史保持完整。
func TestClearChatHistoryWatermark(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		chatID     string
		memberRole model.GroupMemberRole
		isGroup    bool
		want       error
		cleared    bool
	}{
		{name: "群主可清空自己的历史", userID: "u1", chatID: "group-1", isGroup: true, memberRole: model.GroupMemberRoleOwner, cleared: true},
		{name: "普通成员也可清空自己的历史", userID: "u1", chatID: "group-1", isGroup: true, memberRole: model.GroupMemberRoleMember, cleared: true},
		{name: "非群成员不可清空", userID: "u2", chatID: "group-1", isGroup: true, memberRole: model.GroupMemberRoleMember, want: types.ErrChatAccessDenied},
		{name: "私聊参与者可清空", userID: "1001", chatID: "private_1001_1002", cleared: true},
		{name: "私聊旁观者不可清空", userID: "1003", chatID: "private_1001_1002", want: types.ErrChatAccessDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &stubChatRepo{
				groups:  map[string]*model.Group{},
				members: map[string]*model.GroupMember{},
				maxSeqs: map[string]int64{tt.chatID: 42},
			}
			if tt.isGroup {
				repo.groups[tt.chatID] = &model.Group{ID: tt.chatID}
			}
			// 只有 u1 在成员表里出现（群主/普通成员由用例决定），u2 查不到记录
			if tt.isGroup && tt.userID == "u1" {
				repo.members[tt.chatID+"|"+tt.userID] = &model.GroupMember{GroupID: tt.chatID, UserID: tt.userID, Role: tt.memberRole}
			}
			svc := newAccessService(repo)

			err := svc.DeleteChatHistory(tt.userID, tt.chatID)
			if err != tt.want {
				t.Fatalf("期望错误 %v，实际 %v", tt.want, err)
			}
			if tt.cleared {
				if len(repo.cleared) != 1 {
					t.Fatalf("期望推进一次水位线，实际 %v", repo.cleared)
				}
				got := repo.cleared[0]
				if got.userID != tt.userID || got.chatID != tt.chatID || got.seq != 42 {
					t.Fatalf("水位线参数不符: %+v", got)
				}
			} else if len(repo.cleared) != 0 {
				t.Fatalf("不应推进水位线，实际 %v", repo.cleared)
			}
		})
	}
}
