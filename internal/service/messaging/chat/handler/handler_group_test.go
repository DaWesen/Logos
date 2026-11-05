package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/chat/service"
	"Logos/pkg/auth"
	pb "Logos/proto_gen/chat"

	"google.golang.org/grpc/metadata"
)

// fakeGroupService 只实现建群/读群相关方法，其余方法沿用内嵌接口的 nil 实现。
// 这两个 RPC 的价值在于「读成员列表失败时的处置方式」不同：
// 建群已经落库不能判失败，读详情则可以要求客户端重试。
type fakeGroupService struct {
	service.ChatService

	created       *model.Group
	createErr     error
	createdName   string
	createdOwner  string
	createdMember []string

	member      *model.GroupMember
	memberErr   error
	group       *model.Group
	groupErr    error
	memberIDs   []string
	memberIDErr error
}

func (f *fakeGroupService) CreateGroup(name, ownerID string, memberIDs []string, _ map[string]string) (*model.Group, error) {
	f.createdName = name
	f.createdOwner = ownerID
	f.createdMember = memberIDs
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.created, nil
}

func (f *fakeGroupService) GetGroupMember(_ string, _ string) (*model.GroupMember, error) {
	return f.member, f.memberErr
}

func (f *fakeGroupService) GetGroup(_ string) (*model.Group, error) {
	return f.group, f.groupErr
}

func (f *fakeGroupService) GetGroupMemberIDs(_ string) ([]string, error) {
	return f.memberIDs, f.memberIDErr
}

func authedContext(userID string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(auth.UserIDKey, userID))
}

func TestCreateGroup_DegradesWhenMemberListLookupFails(t *testing.T) {
	created := &model.Group{
		ID:          "g1",
		Name:        "研发组",
		OwnerID:     "u1",
		MemberCount: 3,
		CreatedAt:   time.Unix(1700000000, 0),
		UpdatedAt:   time.Unix(1700000000, 0),
	}
	svc := &fakeGroupService{
		created:     created,
		memberIDs:   nil,
		memberIDErr: errors.New("db timeout"),
	}
	h := NewChatServiceImpl(svc)

	resp, err := h.CreateGroup(authedContext("u1"), &pb.CreateGroupRequest{
		Name:      "研发组",
		MemberIds: []string{"u2", "u3"},
	})
	if err != nil {
		t.Fatalf("CreateGroup 返回 RPC 错误: %v", err)
	}
	if resp.Code != 200 {
		t.Fatalf("code = %d, want 200（群已落库，不能判为失败）", resp.Code)
	}
	if len(resp.Data.MemberIds) != 0 {
		t.Fatalf("member_ids = %v, want 空列表", resp.Data.MemberIds)
	}
	// 前端用 member_count 兜底人数，所以降级时必须把它带上，否则群列表显示 0 人。
	if resp.Data.MemberCount != 3 {
		t.Fatalf("member_count = %d, want 3", resp.Data.MemberCount)
	}
}

func TestCreateGroup_ReturnsMemberList(t *testing.T) {
	svc := &fakeGroupService{
		created: &model.Group{
			ID: "g1", Name: "研发组", OwnerID: "u1", MemberCount: 3,
			CreatedAt: time.Unix(1700000000, 0), UpdatedAt: time.Unix(1700000000, 0),
		},
		memberIDs: []string{"u1", "u2", "u3"},
	}
	h := NewChatServiceImpl(svc)

	resp, err := h.CreateGroup(authedContext("u1"), &pb.CreateGroupRequest{
		Name:      "研发组",
		MemberIds: []string{"u2", "u3"},
	})
	if err != nil {
		t.Fatalf("CreateGroup 返回 RPC 错误: %v", err)
	}
	if resp.Code != 200 {
		t.Fatalf("code = %d, want 200", resp.Code)
	}
	if len(resp.Data.MemberIds) != 3 || resp.Data.MemberCount != 3 {
		t.Fatalf("member_ids = %v, member_count = %d", resp.Data.MemberIds, resp.Data.MemberCount)
	}
	if svc.createdOwner != "u1" {
		t.Fatalf("owner = %q, want u1（必须来自认证上下文）", svc.createdOwner)
	}
}

func TestCreateGroup_ServiceErrorIs500(t *testing.T) {
	h := NewChatServiceImpl(&fakeGroupService{createErr: errors.New("tx rollback")})

	resp, err := h.CreateGroup(authedContext("u1"), &pb.CreateGroupRequest{Name: "研发组"})
	if err != nil {
		t.Fatalf("CreateGroup 返回 RPC 错误: %v", err)
	}
	if resp.Code != 500 || resp.Data != nil {
		t.Fatalf("code = %d, data = %v, want 500 + nil", resp.Code, resp.Data)
	}
}

func TestGetGroup_MemberListLookupFailureIs500(t *testing.T) {
	svc := &fakeGroupService{
		member:      &model.GroupMember{GroupID: "g1", UserID: "u1", Role: model.GroupMemberRoleMember},
		group:       &model.Group{ID: "g1", Name: "研发组", OwnerID: "u1", MemberCount: 3},
		memberIDErr: errors.New("db timeout"),
	}
	h := NewChatServiceImpl(svc)

	resp, err := h.GetGroup(authedContext("u1"), &pb.GetGroupRequest{GroupId: "g1"})
	if err != nil {
		t.Fatalf("GetGroup 返回 RPC 错误: %v", err)
	}
	// 读接口没有副作用：返回 200 + 空成员列表会让前端把群渲染成「没有成员」，
	// 显式失败让客户端重试才是安全的一侧。
	if resp.Code != 500 {
		t.Fatalf("code = %d, want 500", resp.Code)
	}
	if resp.Message != "查询群成员列表失败" {
		t.Fatalf("message = %q", resp.Message)
	}
	if resp.Data != nil {
		t.Fatalf("失败响应不应携带 data: %v", resp.Data)
	}
}

func TestGetGroup_RejectsNonMember(t *testing.T) {
	svc := &fakeGroupService{member: nil}
	h := NewChatServiceImpl(svc)

	resp, err := h.GetGroup(authedContext("u9"), &pb.GetGroupRequest{GroupId: "g1"})
	if err != nil {
		t.Fatalf("GetGroup 返回 RPC 错误: %v", err)
	}
	if resp.Code != 403 {
		t.Fatalf("code = %d, want 403", resp.Code)
	}
}

func TestGetGroup_ReturnsMemberListAndCount(t *testing.T) {
	svc := &fakeGroupService{
		member:    &model.GroupMember{GroupID: "g1", UserID: "u1", Role: model.GroupMemberRoleMember},
		group:     &model.Group{ID: "g1", Name: "研发组", OwnerID: "u1", MemberCount: 2},
		memberIDs: []string{"u1", "u2"},
	}
	h := NewChatServiceImpl(svc)

	resp, err := h.GetGroup(authedContext("u1"), &pb.GetGroupRequest{GroupId: "g1"})
	if err != nil {
		t.Fatalf("GetGroup 返回 RPC 错误: %v", err)
	}
	if resp.Code != 200 {
		t.Fatalf("code = %d, want 200", resp.Code)
	}
	if len(resp.Data.MemberIds) != 2 || resp.Data.MemberCount != 2 {
		t.Fatalf("member_ids = %v, member_count = %d", resp.Data.MemberIds, resp.Data.MemberCount)
	}
}
