package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/messaging/chat/model"
)

func newLeaveService(repo *stubChatRepo) *ChatServiceImpl {
	return &ChatServiceImpl{repo: repo, ctx: context.Background()}
}

// leaveGroupFixture 三个成员、群主是 u1，用于覆盖退出/踢出的关键分支。
func leaveGroupFixture() *stubChatRepo {
	return &stubChatRepo{
		groups: map[string]*model.Group{
			"g1": {ID: "g1", OwnerID: "u1", MemberCount: 3},
		},
		members: map[string]*model.GroupMember{
			"g1|u1": {GroupID: "g1", UserID: "u1", Role: model.GroupMemberRoleOwner},
			"g1|u2": {GroupID: "g1", UserID: "u2", Role: model.GroupMemberRoleMember},
			"g1|u3": {GroupID: "g1", UserID: "u3", Role: model.GroupMemberRoleMember},
		},
	}
}

// TestLeaveGroupMemberListFailureDoesNotDissolveGroup 回归最危险的一条：
// 成员列表查询失败时 memberIDs 为空，旧实现用 len(memberIDs) <= 1 判断
// 「最后一人」，会把一次普通退出放大成删除整个群。
func TestLeaveGroupMemberListFailureDoesNotDissolveGroup(t *testing.T) {
	repo := leaveGroupFixture()
	repo.groupMembersErr = errors.New("db down")

	err := newLeaveService(repo).LeaveGroup("g1", "u2")

	if err == nil {
		t.Fatal("成员列表查询失败时应返回错误，而不是当作最后一人退出")
	}
	if len(repo.deleteGroupCalls) != 0 {
		t.Fatalf("成员列表查询失败时绝不能解散群，实际解散了 %v", repo.deleteGroupCalls)
	}
	if len(repo.leaveCalls) != 0 {
		t.Fatalf("成员列表查询失败时不应移除任何成员，实际 %+v", repo.leaveCalls)
	}
}

// TestLeaveGroupMemberLookupFailureIsNotPermissionError 查询失败必须与
// 「不是群成员」区分开，否则数据库故障会被错误地报成 403。
func TestLeaveGroupMemberLookupFailureIsNotPermissionError(t *testing.T) {
	repo := leaveGroupFixture()
	repo.groupMemberErrFor = map[string]error{"g1|u2": errors.New("db down")}

	err := newLeaveService(repo).LeaveGroup("g1", "u2")

	if err == nil {
		t.Fatal("成员身份查询失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "查询群成员身份失败") {
		t.Fatalf("错误应说明是查询失败，实际: %v", err)
	}
	if strings.Contains(err.Error(), "不是该群组成员") {
		t.Fatalf("不能把数据库故障报成权限问题，实际: %v", err)
	}
	if len(repo.leaveCalls) != 0 {
		t.Fatalf("查询失败时不应写任何成员数据，实际 %+v", repo.leaveCalls)
	}
}

func TestLeaveGroupWritesInSingleTransaction(t *testing.T) {
	repo := leaveGroupFixture()

	if err := newLeaveService(repo).LeaveGroup("g1", "u2"); err != nil {
		t.Fatalf("普通成员退出应成功: %v", err)
	}

	if len(repo.leaveCalls) != 1 {
		t.Fatalf("成员/参与者/成员数应一次事务写入，实际 %+v", repo.leaveCalls)
	}
	if repo.leaveCalls[0].groupID != "g1" || repo.leaveCalls[0].userID != "u2" {
		t.Fatalf("事务写入参数不符: %+v", repo.leaveCalls[0])
	}
	if len(repo.deleteGroupCalls) != 0 {
		t.Fatalf("还有人没退出，不应解散群: %v", repo.deleteGroupCalls)
	}
}

// TestLeaveGroupOwnerTransferFailureAbortsLeave 群主退出时若转让失败，
// 必须中止退出；否则群主字段会指向一个已经不在群里的用户。
func TestLeaveGroupOwnerTransferFailureAbortsLeave(t *testing.T) {
	repo := leaveGroupFixture()
	repo.transferOwnerErr = errors.New("db down")

	err := newLeaveService(repo).LeaveGroup("g1", "u1")

	if err == nil {
		t.Fatal("群主转让失败时应中止退出")
	}
	if len(repo.transferOwnerCalls) != 1 || repo.transferOwnerCalls[0] != "g1|u2" {
		t.Fatalf("应尝试把群主转给首个剩余成员，实际 %v", repo.transferOwnerCalls)
	}
	if len(repo.leaveCalls) != 0 {
		t.Fatalf("转让失败后不能移除群主，实际 %+v", repo.leaveCalls)
	}
}

func TestLeaveGroupLastMemberDissolvesGroup(t *testing.T) {
	repo := &stubChatRepo{
		groups: map[string]*model.Group{
			"g1": {ID: "g1", OwnerID: "u1", MemberCount: 1},
		},
		members: map[string]*model.GroupMember{
			"g1|u1": {GroupID: "g1", UserID: "u1", Role: model.GroupMemberRoleOwner},
		},
	}

	if err := newLeaveService(repo).LeaveGroup("g1", "u1"); err != nil {
		t.Fatalf("最后一人退出应成功: %v", err)
	}
	if len(repo.deleteGroupCalls) != 1 || repo.deleteGroupCalls[0] != "g1" {
		t.Fatalf("只剩一人时应解散群，实际 %v", repo.deleteGroupCalls)
	}
	if len(repo.leaveCalls) != 0 {
		t.Fatalf("解散群时不应再逐条移除成员，实际 %+v", repo.leaveCalls)
	}
}

func TestKickGroupMemberLookupFailureIsNotPermissionError(t *testing.T) {
	repo := leaveGroupFixture()
	repo.groupMemberErrFor = map[string]error{"g1|u1": errors.New("db down")}

	err := newLeaveService(repo).KickGroupMember("g1", "u1", "u2")

	if err == nil || !strings.Contains(err.Error(), "查询群成员身份失败") {
		t.Fatalf("操作者身份查询失败应原样上抛，实际: %v", err)
	}
	if len(repo.leaveCalls) != 0 {
		t.Fatalf("查询失败时不应踢人，实际 %+v", repo.leaveCalls)
	}
}

// TestKickGroupMemberMemberListFailureDoesNotDissolveGroup 踢人成功后读
// 剩余成员失败，旧实现会把空列表当成「群已空」调用 DeleteGroup，
// 把剩下所有成员一起删掉。
func TestKickGroupMemberMemberListFailureDoesNotDissolveGroup(t *testing.T) {
	repo := leaveGroupFixture()
	repo.groupMembersErr = errors.New("db down")

	if err := newLeaveService(repo).KickGroupMember("g1", "u1", "u2"); err != nil {
		t.Fatalf("成员移除已提交，踢人本身应算成功: %v", err)
	}
	if len(repo.leaveCalls) != 1 {
		t.Fatalf("踢人应走一次事务写入，实际 %+v", repo.leaveCalls)
	}
	if len(repo.deleteGroupCalls) != 0 {
		t.Fatalf("读剩余成员失败时绝不能解散群，实际 %v", repo.deleteGroupCalls)
	}
}
