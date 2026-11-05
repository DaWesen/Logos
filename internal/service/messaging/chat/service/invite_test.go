package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/messaging/chat/model"
)

func newInviteService(repo *stubChatRepo, operatorRole model.GroupMemberRole) *ChatServiceImpl {
	if repo.members == nil {
		repo.members = make(map[string]*model.GroupMember)
	}
	repo.members["g1|admin"] = &model.GroupMember{
		GroupID: "g1",
		UserID:  "admin",
		Role:    operatorRole,
	}
	return &ChatServiceImpl{repo: repo, ctx: context.Background()}
}

// 查询待邀请成员失败时整批拒绝：旧实现把错误当成「不是成员」继续插入，
// 已存在的成员会撞唯一约束，让整批邀请以一条无关的 duplicate key 失败。
func TestInviteGroupMemberRejectsWhenLookupFails(t *testing.T) {
	repo := &stubChatRepo{groupMemberErrFor: map[string]error{
		"g1|u2": errors.New("db down"),
	}}
	svc := newInviteService(repo, model.GroupMemberRoleAdmin)

	err := svc.InviteGroupMember("g1", "admin", []string{"u2", "u3"})
	if err == nil || !strings.Contains(err.Error(), "u2") {
		t.Fatalf("应返回带成员定位的查询失败错误，实际: %v", err)
	}
	if len(repo.invites) != 0 {
		t.Fatalf("查询失败后不得写入任何成员，实际写入 %d 批", len(repo.invites))
	}
}

// 同一请求里的重复成员必须先去重：批量 INSERT 撞唯一约束会让整批失败。
func TestInviteGroupMemberDeduplicatesInput(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newInviteService(repo, model.GroupMemberRoleAdmin)

	if err := svc.InviteGroupMember("g1", "admin", []string{"u2", "u2", " ", "u3"}); err != nil {
		t.Fatalf("邀请失败: %v", err)
	}
	if len(repo.invites) != 1 {
		t.Fatalf("应只写入一批，实际 %d 批", len(repo.invites))
	}
	got := repo.invites[0]
	if len(got.members) != 2 {
		t.Fatalf("重复与空白 ID 应被过滤，期望 2 名成员，实际 %d", len(got.members))
	}
	if got.members[0].UserID != "u2" || got.members[1].UserID != "u3" {
		t.Fatalf("成员顺序或内容不符: %+v", got.members)
	}
	if len(got.participants) != 2 {
		t.Fatalf("普通成员应同时写入会话参与者，实际 %d", len(got.participants))
	}
}

// 已在群里的成员直接跳过，不进入本批写入；Bot 只写成员表，不写会话参与者。
func TestInviteGroupMemberSkipsExistingAndBotParticipant(t *testing.T) {
	repo := &stubChatRepo{members: map[string]*model.GroupMember{
		"g1|u2": {GroupID: "g1", UserID: "u2", Role: model.GroupMemberRoleMember},
	}}
	svc := newInviteService(repo, model.GroupMemberRoleOwner)

	if err := svc.InviteGroupMember("g1", "admin", []string{"u2", "bot_x"}); err != nil {
		t.Fatalf("邀请失败: %v", err)
	}
	if len(repo.invites) != 1 {
		t.Fatalf("应只写入一批，实际 %d 批", len(repo.invites))
	}
	got := repo.invites[0]
	if len(got.members) != 1 || got.members[0].UserID != "bot_x" {
		t.Fatalf("已存在成员应被跳过，实际: %+v", got.members)
	}
	if got.members[0].Role != model.GroupMemberRoleBot {
		t.Fatalf("Bot 成员角色错误: %v", got.members[0].Role)
	}
	if len(got.participants) != 0 {
		t.Fatalf("Bot 不应写入会话参与者，实际 %d", len(got.participants))
	}
}

// 普通成员没有邀请权限；拒绝必须发生在任何写入之前。
func TestInviteGroupMemberRejectsPlainMember(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newInviteService(repo, model.GroupMemberRoleMember)

	if err := svc.InviteGroupMember("g1", "admin", []string{"u2"}); err == nil {
		t.Fatal("普通成员邀请应被拒绝")
	}
	if len(repo.invites) != 0 {
		t.Fatalf("无权限时不得写入，实际 %d 批", len(repo.invites))
	}
}
