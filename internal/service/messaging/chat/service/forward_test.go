package service

import (
	"errors"
	"testing"

	"Logos/internal/service/messaging/chat/model"
)

// 转发前必须先把全部目标会话类型解析完：任一查询失败都整批拒绝，
// 否则前面已经写入的目标无法回滚，后面失败的目标还可能被误判为私聊。
func TestForwardMessageFailsClosedWhenGroupLookupFails(t *testing.T) {
	lookupErr := errors.New("db timeout")
	repo := &stubChatRepo{
		messages: map[string]*model.Message{
			"m1": {
				ID:          "m1",
				ChatID:      "private_u1_u2",
				ChatType:    int(model.ChatTypePrivate),
				MessageType: int(model.MessageTypeText),
				Content:     "hello",
			},
		},
		members: map[string]*model.GroupMember{
			"g-fail|u1": {GroupID: "g-fail", UserID: "u1"},
		},
		groupErrFor: "g-fail",
		groupErr:    lookupErr,
	}

	_, err := newAccessService(repo).ForwardMessage("m1", []string{"private_u1_u2", "g-fail"}, "u1")
	if !errors.Is(err, lookupErr) {
		t.Fatalf("目标会话类型查询失败时应返回原错误，实际: %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("解析失败时必须一条都不写，实际写入 %d 条", len(repo.created))
	}
}

// 群会话存在时必须按群聊转发；若类型解析丢失，消息会按私聊写进幽灵会话。
func TestForwardMessageKeepsGroupChatType(t *testing.T) {
	repo := &stubChatRepo{
		messages: map[string]*model.Message{
			"m1": {
				ID:          "m1",
				ChatID:      "private_u1_u2",
				ChatType:    int(model.ChatTypePrivate),
				MessageType: int(model.MessageTypeText),
				Content:     "hello",
			},
		},
		groups: map[string]*model.Group{
			"g1": {ID: "g1", Name: "研发群"},
		},
		members: map[string]*model.GroupMember{
			"g1|u1": {GroupID: "g1", UserID: "u1", Role: model.GroupMemberRoleMember},
		},
	}

	forwarded, err := newAccessService(repo).ForwardMessage("m1", []string{"g1"}, "u1")
	if err != nil {
		t.Fatalf("转发到已有群聊失败: %v", err)
	}
	if len(forwarded) != 1 {
		t.Fatalf("应转发 1 条消息，实际 %d 条", len(forwarded))
	}
	if forwarded[0].ChatType != int(model.ChatTypeGroup) {
		t.Fatalf("群会话必须按群聊类型落库，实际 chat_type=%d", forwarded[0].ChatType)
	}
	if forwarded[0].ChatID != "g1" {
		t.Fatalf("消息应写入目标群 g1，实际 %s", forwarded[0].ChatID)
	}
}
