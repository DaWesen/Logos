package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
	"Logos/pkg/mq"
)

// TestHandleGroupMessage_MemberLookupFailureReturnsError
// 群成员查询失败时不能「继续走」：否则消息会落库但接收者列表为空，
// 在线成员收不到实时推送，只能等下次同步。必须返回 error 让 Kafka 重投。
func TestHandleGroupMessage_MemberLookupFailureReturnsError(t *testing.T) {
	repo := &stubChatRepo{
		members: map[string]*model.GroupMember{
			"g1|1001": {GroupID: "g1", UserID: "1001"},
		},
		groupMembersErr: errors.New("db unavailable"),
	}
	svc := &ChatServiceImpl{repo: repo, ctx: context.Background()}

	event := types.MessageEvent{
		ID:          "m1",
		EventType:   types.EventTypeMessage,
		ChatID:      "g1",
		ChatType:    types.ChatTypeGroup,
		SenderID:    "1001",
		Content:     "hello",
		MessageType: types.MessageType(1),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	err = svc.HandleMessageEvent(&mq.Message{Topic: "chat.inbound", Value: payload})
	if err == nil {
		t.Fatal("群成员查询失败时应返回 error，让 Kafka 不提交 offset")
	}
	if !strings.Contains(err.Error(), "获取群成员失败") {
		t.Fatalf("错误信息应包含上下文，实际: %v", err)
	}
}

// TestPersistIncomingMessage_ConversationFailurePropagates
// 会话创建失败时不能吞掉错误：消息会落库但会话/参与者缺失，
// 会话列表里看不到这条消息。返回 error 交给 Kafka 重投。
func TestPersistIncomingMessage_ConversationFailurePropagates(t *testing.T) {
	repo := &stubChatRepo{privateConvErr: errors.New("insert failed")}
	svc := &ChatServiceImpl{repo: repo, ctx: context.Background()}

	event := &types.MessageEvent{
		ID:          "m2",
		EventType:   types.EventTypeMessage,
		ChatID:      "private_1001_1002",
		ChatType:    types.ChatTypePrivate,
		SenderID:    "1001",
		Content:     "hello",
		MessageType: types.MessageType(1),
	}

	err := svc.persistIncomingMessage(event)
	if err == nil {
		t.Fatal("会话创建失败时应返回 error，而不是静默继续落库")
	}
}
