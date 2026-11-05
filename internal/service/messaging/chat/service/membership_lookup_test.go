package service

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

// zeroValueOnErrorRepo 故意违反 DAO 契约：查询报错时仍返回非 nil 零值成员，
// 用于证明服务层即使遇到违约实现也必须失败关闭，而不是依赖存储层自觉。
type zeroValueOnErrorRepo struct {
	*stubChatRepo
	err error
}

func (r *zeroValueOnErrorRepo) GetGroupMember(_ context.Context, _, _ string) (*model.GroupMember, error) {
	return &model.GroupMember{}, r.err
}

// 群成员查询失败必须失败关闭：旧 DAO 在数据库出错时返回非 nil 零值指针，
// `member == nil` 不成立，入站消息会被当成“群成员”放行。
func TestAuthorizeIncomingMessageRejectsMemberLookupFailure(t *testing.T) {
	repo := &zeroValueOnErrorRepo{
		stubChatRepo: &stubChatRepo{},
		err:          errors.New("db down"),
	}
	svc := newAccessService(repo)

	allowed := svc.authorizeIncomingMessage(&types.MessageEvent{
		ChatType: types.ChatTypeGroup,
		ChatID:   "g1",
		SenderID: "u1",
	})

	if allowed {
		t.Fatal("成员查询失败时应拒绝入站消息，实际放行")
	}
}

// 对照用例：查询成功且成员存在时仍需放行，避免修复变成“一律拒绝”。
func TestAuthorizeIncomingMessageAllowsExistingMember(t *testing.T) {
	repo := &stubChatRepo{
		members: map[string]*model.GroupMember{
			"g1|u1": {GroupID: "g1", UserID: "u1", Role: model.GroupMemberRoleMember},
		},
	}
	svc := newAccessService(repo)

	allowed := svc.authorizeIncomingMessage(&types.MessageEvent{
		ChatType: types.ChatTypeGroup,
		ChatID:   "g1",
		SenderID: "u1",
	})

	if !allowed {
		t.Fatal("群成员发送消息应放行，实际被拒绝")
	}
}

// 发送入口必须把“查不到成员”和“查询失败”区分开：
// 前者是 403，后者是 503，且都不能落库。
func TestSendGroupMessageRejectsMemberLookupFailure(t *testing.T) {
	repo := &zeroValueOnErrorRepo{
		stubChatRepo: &stubChatRepo{},
		err:          errors.New("db down"),
	}
	svc := newAccessService(repo)

	msg, _, _, err := svc.SendMessage(
		"u1", "g1", model.ChatTypeGroup, model.MessageTypeText,
		"hello", nil, "", nil, "")

	if !errors.Is(err, types.ErrChatMembershipUnavailable) {
		t.Fatalf("期望 ErrChatMembershipUnavailable，实际: %v", err)
	}
	if msg != nil {
		t.Fatalf("查询失败时不得返回消息，实际: %+v", msg)
	}
	if len(repo.created) != 0 {
		t.Fatalf("查询失败时不得落库，实际写入 %d 条", len(repo.created))
	}
}

// 服务层直通方法不能把零值成员返回给 RPC 调用方。
func TestGetGroupMemberPropagatesLookupFailure(t *testing.T) {
	lookupErr := errors.New("db down")
	repo := &stubChatRepo{
		groupMemberErrFor: map[string]error{"g1|u1": lookupErr},
	}
	svc := newAccessService(repo)

	member, err := svc.GetGroupMember("g1", "u1")

	if !errors.Is(err, lookupErr) {
		t.Fatalf("查询失败应原样上抛，实际: %v", err)
	}
	if member != nil {
		t.Fatalf("查询失败时不得返回成员，实际: %+v", member)
	}
}
