package service

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

// fakeModerationClient 记录被送审的消息 ID，并按预设结果返回，
// 用于验证补充审核只处理真正需要审核的消息。
type fakeModerationClient struct {
	rejected bool
	err      error
	reviewed []string
}

func (f *fakeModerationClient) ModerateContent(_ context.Context, _, contentID, _ string) (bool, error) {
	f.reviewed = append(f.reviewed, contentID)
	return f.rejected, f.err
}

func TestModerateMessagePersistsBlockedNotice(t *testing.T) {
	repo := &stubChatRepo{members: map[string]*model.GroupMember{
		"g1|u1": {GroupID: "g1", UserID: "u1"},
		"g1|u2": {GroupID: "g1", UserID: "u2"},
	}}
	moderator := &fakeModerationClient{rejected: true}
	svc := &ChatServiceImpl{
		repo:             repo,
		moderationClient: moderator,
		ctx:              context.Background(),
	}

	svc.moderateMessage(&types.MessageEvent{
		ID:          "msg-1",
		ChatID:      "g1",
		ChatType:    types.ChatTypeGroup,
		SenderID:    "u1",
		MessageType: types.MessageTypeText,
		Content:     "违规内容",
	})

	if len(moderator.reviewed) != 1 || moderator.reviewed[0] != "msg-1" {
		t.Fatalf("审核调用参数不符: %+v", moderator.reviewed)
	}
	if len(repo.created) != 1 {
		t.Fatalf("拦截提示应落库一条，实际 %d 条", len(repo.created))
	}
	notice := repo.created[0]
	if notice.SenderID != "system" || notice.MessageType != int(model.MessageTypeSystem) {
		t.Fatalf("拦截提示应为 system/系统消息: sender=%s type=%d", notice.SenderID, notice.MessageType)
	}
	if notice.ChatID != "g1" || notice.ConversationID != "g1" || notice.ChatType != int(types.ChatTypeGroup) {
		t.Fatalf("拦截提示会话归属错误: chat_id=%s conv=%s type=%d", notice.ChatID, notice.ConversationID, notice.ChatType)
	}
	if notice.Content != moderationBlockedNotice {
		t.Fatalf("拦截文案不符: %q", notice.Content)
	}
	if notice.Channel != "system" || notice.Status != "sent" {
		t.Fatalf("拦截提示状态不符: channel=%s status=%s", notice.Channel, notice.Status)
	}
}

func TestModerateMessageSkipsWhenModerationFails(t *testing.T) {
	repo := &stubChatRepo{}
	svc := &ChatServiceImpl{
		repo:             repo,
		moderationClient: &fakeModerationClient{err: errors.New("moderation unavailable")},
		ctx:              context.Background(),
	}

	svc.moderateMessage(&types.MessageEvent{
		ID: "msg-1", ChatID: "private_u1_u2", ChatType: types.ChatTypePrivate,
		SenderID: "u1", MessageType: types.MessageTypeText, Content: "hello",
	})

	if len(repo.created) != 0 {
		t.Fatalf("审核服务报错时不应落库拦截提示，实际写了 %d 条", len(repo.created))
	}
}

func TestModerateMessageIgnoresPassedContent(t *testing.T) {
	repo := &stubChatRepo{}
	svc := &ChatServiceImpl{
		repo:             repo,
		moderationClient: &fakeModerationClient{rejected: false},
		ctx:              context.Background(),
	}

	svc.moderateMessage(&types.MessageEvent{
		ID: "msg-1", ChatID: "private_u1_u2", ChatType: types.ChatTypePrivate,
		SenderID: "u1", MessageType: types.MessageTypeText, Content: "hello",
	})

	if len(repo.created) != 0 {
		t.Fatalf("审核通过时不应落库拦截提示，实际写了 %d 条", len(repo.created))
	}
}

func TestModerateMessageSkipsSystemMessages(t *testing.T) {
	moderator := &fakeModerationClient{rejected: true}
	svc := &ChatServiceImpl{
		repo:             &stubChatRepo{},
		moderationClient: moderator,
		ctx:              context.Background(),
	}

	svc.moderateMessage(&types.MessageEvent{
		ID: "notice-1", ChatID: "g1", ChatType: types.ChatTypeGroup,
		SenderID: "system", MessageType: types.MessageTypeSystem, Content: moderationBlockedNotice,
	})

	if len(moderator.reviewed) != 0 {
		t.Fatalf("系统消息不应再送审，实际送审 %v", moderator.reviewed)
	}
}

func TestModerationNoticeRecipients(t *testing.T) {
	repo := &stubChatRepo{members: map[string]*model.GroupMember{
		"g1|u1": {GroupID: "g1", UserID: "u1"},
		"g1|u2": {GroupID: "g1", UserID: "u2"},
	}}
	svc := &ChatServiceImpl{repo: repo, ctx: context.Background()}

	groupRecipients, err := svc.moderationNoticeRecipients(context.Background(), &types.MessageEvent{
		ChatID: "g1", ChatType: types.ChatTypeGroup, SenderID: "u1",
	})
	if err != nil {
		t.Fatalf("查询群接收者失败: %v", err)
	}
	if len(groupRecipients) != 2 {
		t.Fatalf("群拦截提示应广播给全体成员，实际 %v", groupRecipients)
	}

	privateRecipients, err := svc.moderationNoticeRecipients(context.Background(), &types.MessageEvent{
		ChatID: "private_u1_u2", ChatType: types.ChatTypePrivate, SenderID: "u1",
	})
	if err != nil {
		t.Fatalf("计算私聊接收者失败: %v", err)
	}
	if len(privateRecipients) != 1 || privateRecipients[0] != "u1" {
		t.Fatalf("私聊拦截提示应只回给发送者，实际 %v", privateRecipients)
	}
}

// TestModerationNoticeRecipientsPropagatesLookupFailure 群成员查询失败必须
// 上抛，由调用方记录 Error 并降级为「只落库不推送」，不能当成没有成员。
func TestModerationNoticeRecipientsPropagatesLookupFailure(t *testing.T) {
	svc := &ChatServiceImpl{
		repo: &stubChatRepo{groupMembersErr: errors.New("db down")},
		ctx:  context.Background(),
	}

	_, err := svc.moderationNoticeRecipients(context.Background(), &types.MessageEvent{
		ChatID: "g1", ChatType: types.ChatTypeGroup, SenderID: "u1",
	})
	if err == nil {
		t.Fatal("群成员查询失败时应返回错误，而不是返回空接收者")
	}
}
