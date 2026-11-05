package service

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

type fakeContactChecker struct {
	blocked map[string]bool
	err     error
	calls   int
}

func (f *fakeContactChecker) IsFriend(_ context.Context, userID, friendID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return !f.blocked[userID+"|"+friendID], nil
}

func (f *fakeContactChecker) IsBlocked(_ context.Context, userID, friendID string) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.blocked[userID+"|"+friendID], nil
}

func (f *fakeContactChecker) CheckFriendship(ctx context.Context, userID, friendID string) (*FriendshipResult, error) {
	blocked, err := f.IsBlocked(ctx, userID, friendID)
	if err != nil {
		return nil, err
	}
	return &FriendshipResult{IsBlocked: blocked, IsFriend: !blocked}, nil
}

func newBlockService(repo *stubChatRepo, checker ContactChecker) *ChatServiceImpl {
	return &ChatServiceImpl{repo: repo, ctx: context.Background(), contactChecker: checker}
}

func sendPrivate(t *testing.T, svc *ChatServiceImpl, senderID string) (*model.Message, error) {
	t.Helper()
	msg, _, _, err := svc.SendMessage(senderID, "u1_u2", model.ChatTypePrivate, model.MessageTypeText, "hello", nil, "", nil, "")
	return msg, err
}

// 被对方拉黑后消息必须止步于发送入口：不建会话、不落库。
func TestSendPrivateMessageBlockedByTarget(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newBlockService(repo, &fakeContactChecker{blocked: map[string]bool{"u2|u1": true}})

	if _, err := sendPrivate(t, svc, "u1"); !errors.Is(err, types.ErrChatBlocked) {
		t.Fatalf("被对方拉黑应返回 ErrChatBlocked，实际: %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("拦截后不得落库，实际写入 %d 条", len(repo.created))
	}
	if repo.privateConvCalls != 0 {
		t.Fatalf("拦截后不得创建私聊会话，实际创建 %d 次", repo.privateConvCalls)
	}
}

// 自己拉黑对方后也不应继续发出消息，否则黑名单只影响接收侧。
func TestSendPrivateMessageBlockedBySender(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newBlockService(repo, &fakeContactChecker{blocked: map[string]bool{"u1|u2": true}})

	if _, err := sendPrivate(t, svc, "u1"); !errors.Is(err, types.ErrChatBlocked) {
		t.Fatalf("发送方已拉黑应返回 ErrChatBlocked，实际: %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("拦截后不得落库，实际写入 %d 条", len(repo.created))
	}
}

// 关系服务不可用时不能默认「没拉黑」，否则一次故障就绕过黑名单。
func TestSendPrivateMessageFailsClosedWhenRelationshipCheckFails(t *testing.T) {
	repo := &stubChatRepo{}
	svc := newBlockService(repo, &fakeContactChecker{err: errors.New("contact service unavailable")})

	if _, err := sendPrivate(t, svc, "u1"); !errors.Is(err, types.ErrChatRelationshipUnavailable) {
		t.Fatalf("关系查询失败应返回 ErrChatRelationshipUnavailable，实际: %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("失败关闭时不得落库，实际写入 %d 条", len(repo.created))
	}
	if repo.privateConvCalls != 0 {
		t.Fatalf("失败关闭时不得创建私聊会话，实际创建 %d 次", repo.privateConvCalls)
	}
}

func TestSendPrivateMessageAllowedWithoutBlock(t *testing.T) {
	repo := &stubChatRepo{}
	checker := &fakeContactChecker{}
	svc := newBlockService(repo, checker)

	if _, err := sendPrivate(t, svc, "u1"); err != nil {
		t.Fatalf("无拉黑关系应放行，实际: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应写入 1 条消息，实际 %d 条", len(repo.created))
	}
	if checker.calls != 2 {
		t.Fatalf("必须双向检查拉黑关系，实际调用 %d 次", checker.calls)
	}
}

// Bot 不是自然人关系，不应拿 Bot 身份去查联系人库（也避免把 Bot 消息误拦）。
func TestSendPrivateMessageSkipsRelationshipCheckForBot(t *testing.T) {
	repo := &stubChatRepo{}
	checker := &fakeContactChecker{}
	svc := newBlockService(repo, checker)

	_, _, _, err := svc.SendMessage("bot_123", "bot-abc", model.ChatTypePrivate, model.MessageTypeText, "hello", nil, "", nil, "")
	if err != nil {
		t.Fatalf("Bot 私聊应放行，实际: %v", err)
	}
	if checker.calls != 0 {
		t.Fatalf("Bot 消息不应查询联系人关系，实际调用 %d 次", checker.calls)
	}
}
