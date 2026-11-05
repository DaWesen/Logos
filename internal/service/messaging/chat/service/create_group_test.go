package service

import (
	"errors"
	"testing"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

// TestCreateGroupDeduplicatesOwnerAndMembers 客户端把群主本人或同一个人重复放进
// member_ids 时，不能原样插入：(group_id, user_id) 有唯一索引，重复行会让整个
// 建群事务回滚，而调用方只能看到一句「建群失败」，无法判断是参数问题。
func TestCreateGroupDeduplicatesOwnerAndMembers(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newAccessService(repo)

	input := []string{"1002", "1001", "1002", " ", "1003"}
	group, err := svc.CreateGroup("测试群", "1001", input, nil)
	if err != nil {
		t.Fatalf("建群失败: %v", err)
	}

	if len(input) != 5 {
		t.Fatalf("不得原地修改调用方的 member_ids，实际 %v", input)
	}
	if len(repo.groupCreations) != 1 {
		t.Fatalf("建群应只走一次事务写入，实际 %d", len(repo.groupCreations))
	}

	call := repo.groupCreations[0]
	if call.conv.ID != group.ID || call.conv.ChatID != group.ID {
		t.Fatalf("会话记录必须与群同 ID，实际 %+v", call.conv)
	}
	if len(call.members) != 3 {
		t.Fatalf("去重后应有 3 个成员（群主 + 1002 + 1003），实际 %+v", call.members)
	}
	roles := make(map[string]model.GroupMemberRole, len(call.members))
	for _, m := range call.members {
		roles[m.UserID] = m.Role
	}
	if roles["1001"] != model.GroupMemberRoleOwner {
		t.Fatalf("群主角色不正确，实际 %v", roles)
	}
	if roles["1002"] != model.GroupMemberRoleMember || roles["1003"] != model.GroupMemberRoleMember {
		t.Fatalf("普通成员角色不正确，实际 %v", roles)
	}
	if len(call.participants) != 3 {
		t.Fatalf("每个真实成员都应写入会话参与者，实际 %+v", call.participants)
	}
	if group.MemberCount != 3 {
		t.Fatalf("成员数应按去重后的成员重算，实际 %d", group.MemberCount)
	}
	if len(repo.created) != 1 || repo.created[0].Content != "群组创建成功" {
		t.Fatalf("建群系统消息应落库一次，实际 %+v", repo.created)
	}
}

// TestCreateGroupTransactionalFailurePropagates 群/会话/成员/参与者任一步失败
// 都必须整体上抛，且不再写系统消息：半个群比建群失败更难修复。
func TestCreateGroupTransactionalFailurePropagates(t *testing.T) {
	repo := &stubChatRepo{createGroupErr: errors.New("insert conversation failed")}
	svc := newAccessService(repo)

	group, err := svc.CreateGroup("半成品群", "1001", []string{"1002"}, nil)
	if err == nil {
		t.Fatal("事务写入失败应返回 error，而不是留下半个群")
	}
	if group != nil {
		t.Fatalf("失败时不得返回群对象，实际 %+v", group)
	}
	if len(repo.created) != 0 {
		t.Fatalf("事务失败后不得继续写系统消息，实际 %d 条", len(repo.created))
	}
}

// TestCreateGroupRejectsEmptyOwner 没有群主的群无法管理，入口直接拒绝。
func TestCreateGroupRejectsEmptyOwner(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newAccessService(repo)

	if _, err := svc.CreateGroup("无主群", "", []string{"1002"}, nil); err != types.ErrChatBadRequest {
		t.Fatalf("空群主应返回 ErrChatBadRequest，实际 %v", err)
	}
	if len(repo.groupCreations) != 0 {
		t.Fatalf("非法请求不得触达仓储，实际 %d 次", len(repo.groupCreations))
	}
}

// TestCreateGroupSkipsBotParticipants Bot 只作为群成员存在，
// 不能写进 conversation_participants——它不会登录，也不该出现在会话列表。
func TestCreateGroupSkipsBotParticipants(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newAccessService(repo)

	group, err := svc.CreateGroup("带 Bot 的群", "1001", []string{"bot_abc", "1002"}, nil)
	if err != nil {
		t.Fatalf("建群失败: %v", err)
	}
	call := repo.groupCreations[0]
	if len(call.members) != 3 || len(call.participants) != 2 {
		t.Fatalf("Bot 应只进成员表，成员=%d 参与者=%d", len(call.members), len(call.participants))
	}
	for _, m := range call.members {
		if m.UserID == "bot_abc" && m.Role != model.GroupMemberRoleBot {
			t.Fatalf("Bot 角色不正确: %+v", m)
		}
	}
	for _, p := range call.participants {
		if p.UserID == "bot_abc" {
			t.Fatal("Bot 不应出现在会话参与者中")
		}
	}
	if group.MemberCount != 3 {
		t.Fatalf("成员数应包含 Bot，实际 %d", group.MemberCount)
	}
}
