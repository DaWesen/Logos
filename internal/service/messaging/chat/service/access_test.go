package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	groups  map[string]*model.Group
	members map[string]*model.GroupMember
	maxSeqs map[string]int64
	cleared []clearCall
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

func (r *stubChatRepo) ClearConversationForUser(_ context.Context, conversationID, userID string, seq int64) error {
	r.cleared = append(r.cleared, clearCall{chatID: conversationID, userID: userID, seq: seq})
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
