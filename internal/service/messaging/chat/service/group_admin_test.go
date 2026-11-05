package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/model"
)

// newGroupAdminRepo 构造一个包含群主、管理员、普通成员的群，
// 供群管理写接口验证「先确认目标存在，再执行写入」。
func newGroupAdminRepo() *stubChatRepo {
	return &stubChatRepo{
		groups: map[string]*model.Group{
			"g1": {ID: "g1", OwnerID: "u1"},
		},
		members: map[string]*model.GroupMember{
			"g1|u1": {GroupID: "g1", UserID: "u1", Role: model.GroupMemberRoleOwner},
			"g1|u2": {GroupID: "g1", UserID: "u2", Role: model.GroupMemberRoleMember},
			"g1|u3": {GroupID: "g1", UserID: "u3", Role: model.GroupMemberRoleAdmin},
		},
		groupMemberErrFor: map[string]error{},
	}
}

func TestMuteGroupMember(t *testing.T) {
	t.Run("操作者查询失败不写入", func(t *testing.T) {
		repo := newGroupAdminRepo()
		repo.groupMemberErrFor["g1|u1"] = errors.New("db down")

		err := newAccessService(repo).MuteGroupMember("g1", "u1", "u2", model.MuteTypePermanent, time.Time{})
		if err == nil || !strings.Contains(err.Error(), "查询群成员身份失败") {
			t.Fatalf("应上抛操作者查询失败，实际: %v", err)
		}
		if len(repo.muteCalls) != 0 {
			t.Fatalf("查询失败时不应写入禁言，实际写入 %d 次", len(repo.muteCalls))
		}
	})

	t.Run("目标查询失败不写入", func(t *testing.T) {
		repo := newGroupAdminRepo()
		repo.groupMemberErrFor["g1|u2"] = errors.New("db down")

		err := newAccessService(repo).MuteGroupMember("g1", "u3", "u2", model.MuteTypePermanent, time.Time{})
		if err == nil || !strings.Contains(err.Error(), "查询目标成员身份失败") {
			t.Fatalf("应上抛目标查询失败，实际: %v", err)
		}
		if len(repo.muteCalls) != 0 {
			t.Fatalf("查询失败时不应写入禁言，实际写入 %d 次", len(repo.muteCalls))
		}
	})

	t.Run("目标不在群中不写入", func(t *testing.T) {
		repo := newGroupAdminRepo()

		err := newAccessService(repo).MuteGroupMember("g1", "u3", "u99", model.MuteTypePermanent, time.Time{})
		if err == nil || !strings.Contains(err.Error(), "目标用户不在群中") {
			t.Fatalf("应拒绝非群成员，实际: %v", err)
		}
		if len(repo.muteCalls) != 0 {
			t.Fatalf("目标不存在时不应调用 Update，实际写入 %d 次", len(repo.muteCalls))
		}
	})

	t.Run("正常禁言写入一次", func(t *testing.T) {
		repo := newGroupAdminRepo()
		until := time.Now().Add(time.Hour)

		if err := newAccessService(repo).MuteGroupMember("g1", "u3", "u2", model.MuteTypeTemporary, until); err != nil {
			t.Fatalf("禁言应成功: %v", err)
		}
		if len(repo.muteCalls) != 1 {
			t.Fatalf("应写入一次禁言，实际 %d 次", len(repo.muteCalls))
		}
		call := repo.muteCalls[0]
		if call.groupID != "g1" || call.userID != "u2" || call.muteType != model.MuteTypeTemporary {
			t.Fatalf("禁言参数不符: %+v", call)
		}
	})
}

func TestTransferGroupOwner(t *testing.T) {
	t.Run("操作者查询失败不上抛转让", func(t *testing.T) {
		repo := newGroupAdminRepo()
		repo.groupMemberErrFor["g1|u1"] = errors.New("db down")

		err := newAccessService(repo).TransferGroupOwner("g1", "u1", "u2")
		if err == nil || !strings.Contains(err.Error(), "查询群成员身份失败") {
			t.Fatalf("应上抛操作者查询失败，实际: %v", err)
		}
		if len(repo.transferOwnerCalls) != 0 {
			t.Fatalf("查询失败时不应转让，实际调用 %d 次", len(repo.transferOwnerCalls))
		}
	})

	t.Run("新群主查询失败不上抛转让", func(t *testing.T) {
		repo := newGroupAdminRepo()
		repo.groupMemberErrFor["g1|u2"] = errors.New("db down")

		err := newAccessService(repo).TransferGroupOwner("g1", "u1", "u2")
		if err == nil || !strings.Contains(err.Error(), "查询新群主身份失败") {
			t.Fatalf("应上抛新群主查询失败，实际: %v", err)
		}
		if len(repo.transferOwnerCalls) != 0 {
			t.Fatalf("查询失败时不应转让，实际调用 %d 次", len(repo.transferOwnerCalls))
		}
	})

	t.Run("新群主不在群中不转让", func(t *testing.T) {
		repo := newGroupAdminRepo()

		err := newAccessService(repo).TransferGroupOwner("g1", "u1", "u99")
		if err == nil || !strings.Contains(err.Error(), "新群主不在群中") {
			t.Fatalf("应拒绝非群成员，实际: %v", err)
		}
		if len(repo.transferOwnerCalls) != 0 {
			t.Fatalf("目标不存在时不应转让，实际调用 %d 次", len(repo.transferOwnerCalls))
		}
	})

	t.Run("正常转让写入一次", func(t *testing.T) {
		repo := newGroupAdminRepo()

		if err := newAccessService(repo).TransferGroupOwner("g1", "u1", "u3"); err != nil {
			t.Fatalf("转让应成功: %v", err)
		}
		if len(repo.transferOwnerCalls) != 1 || repo.transferOwnerCalls[0] != "g1|u3" {
			t.Fatalf("转让参数不符: %v", repo.transferOwnerCalls)
		}
	})
}

func TestSetGroupAdmin(t *testing.T) {
	t.Run("目标查询失败不写入", func(t *testing.T) {
		repo := newGroupAdminRepo()
		repo.groupMemberErrFor["g1|u2"] = errors.New("db down")

		err := newAccessService(repo).SetGroupAdmin("g1", "u1", "u2", true)
		if err == nil || !strings.Contains(err.Error(), "查询目标成员身份失败") {
			t.Fatalf("应上抛目标查询失败，实际: %v", err)
		}
		if len(repo.roleCalls) != 0 {
			t.Fatalf("查询失败时不应写角色，实际写入 %d 次", len(repo.roleCalls))
		}
	})

	t.Run("目标不在群中不写入", func(t *testing.T) {
		repo := newGroupAdminRepo()

		err := newAccessService(repo).SetGroupAdmin("g1", "u1", "u99", true)
		if err == nil || !strings.Contains(err.Error(), "目标用户不在群中") {
			t.Fatalf("应拒绝非群成员，实际: %v", err)
		}
		if len(repo.roleCalls) != 0 {
			t.Fatalf("目标不存在时不应写角色，实际写入 %d 次", len(repo.roleCalls))
		}
	})

	t.Run("提升与降级写入正确角色", func(t *testing.T) {
		repo := newGroupAdminRepo()
		svc := newAccessService(repo)

		if err := svc.SetGroupAdmin("g1", "u1", "u2", true); err != nil {
			t.Fatalf("提升管理员应成功: %v", err)
		}
		if err := svc.SetGroupAdmin("g1", "u1", "u3", false); err != nil {
			t.Fatalf("取消管理员应成功: %v", err)
		}
		if len(repo.roleCalls) != 2 {
			t.Fatalf("应写入两次角色，实际 %d 次", len(repo.roleCalls))
		}
		if repo.roleCalls[0].role != model.GroupMemberRoleAdmin || repo.roleCalls[1].role != model.GroupMemberRoleMember {
			t.Fatalf("角色写入不符: %+v", repo.roleCalls)
		}
	})
}

func TestJoinGroup(t *testing.T) {
	t.Run("判重查询失败不插入", func(t *testing.T) {
		repo := newGroupAdminRepo()
		repo.groupMemberErrFor["g1|u4"] = errors.New("db down")

		err := newAccessService(repo).JoinGroup("g1", "u4")
		if err == nil || !strings.Contains(err.Error(), "查询群成员身份失败") {
			t.Fatalf("应上抛判重查询失败，实际: %v", err)
		}
		if len(repo.addMemberCalls) != 0 {
			t.Fatalf("查询失败时不应插入成员，实际插入 %d 次", len(repo.addMemberCalls))
		}
	})

	t.Run("已在群中不重复插入", func(t *testing.T) {
		repo := newGroupAdminRepo()

		err := newAccessService(repo).JoinGroup("g1", "u2")
		if err == nil || !strings.Contains(err.Error(), "已在群组中") {
			t.Fatalf("应拒绝重复加入，实际: %v", err)
		}
		if len(repo.addMemberCalls) != 0 {
			t.Fatalf("已在群中时不应插入，实际插入 %d 次", len(repo.addMemberCalls))
		}
	})

	t.Run("新成员正常加入", func(t *testing.T) {
		repo := newGroupAdminRepo()

		if err := newAccessService(repo).JoinGroup("g1", "u4"); err != nil {
			t.Fatalf("加入应成功: %v", err)
		}
		if len(repo.addMemberCalls) != 1 {
			t.Fatalf("应插入一次成员，实际 %d 次", len(repo.addMemberCalls))
		}
		member := repo.addMemberCalls[0]
		if member.GroupID != "g1" || member.UserID != "u4" || member.Role != model.GroupMemberRoleMember {
			t.Fatalf("成员写入不符: %+v", member)
		}
		if len(repo.participantCalls) != 1 || repo.participantCalls[0].UserID != "u4" {
			t.Fatalf("应同步写入会话参与者: %+v", repo.participantCalls)
		}
		if len(repo.memberCountCalls) != 1 || repo.memberCountCalls[0] != "g1" {
			t.Fatalf("应刷新群成员数: %v", repo.memberCountCalls)
		}
	})
}

// TestGroupAdminOperationsFailClosedOnLookupError 覆盖其余管理写接口：
// 操作者身份读不出来时必须报错，而不是当成「不是成员」继续往下写。
func TestGroupAdminOperationsFailClosedOnLookupError(t *testing.T) {
	operations := []struct {
		name string
		call func(svc *ChatServiceImpl) error
	}{
		{name: "禁言", call: func(svc *ChatServiceImpl) error {
			return svc.MuteGroupMember("g1", "u1", "u2", model.MuteTypePermanent, time.Time{})
		}},
		{name: "转让群主", call: func(svc *ChatServiceImpl) error {
			return svc.TransferGroupOwner("g1", "u1", "u2")
		}},
		{name: "修改公告", call: func(svc *ChatServiceImpl) error {
			return svc.UpdateGroupAnnouncement("g1", "u1", "新公告")
		}},
		{name: "修改头像", call: func(svc *ChatServiceImpl) error {
			return svc.UpdateGroupAvatar("g1", "u1", "avatar.png")
		}},
		{name: "设置管理员", call: func(svc *ChatServiceImpl) error {
			return svc.SetGroupAdmin("g1", "u1", "u2", true)
		}},
	}

	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			repo := newGroupAdminRepo()
			repo.groupMemberErrFor["g1|u1"] = errors.New("db down")

			err := op.call(newAccessService(repo))
			if err == nil || !strings.Contains(err.Error(), "查询群成员身份失败") {
				t.Fatalf("应上抛查询失败，实际: %v", err)
			}
		})
	}
}
