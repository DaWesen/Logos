package service

import (
	"context"
	"testing"
	"time"

	"Logos/internal/service/messaging/chat/dao"
	"Logos/internal/service/messaging/chat/model"
	"Logos/internal/service/messaging/types"
)

// stubSendRepo 模拟发送链路：私聊会话创建、消息写入与幂等键回放。
// 其余方法由内嵌接口兜底（误调用会 panic）。
type stubSendRepo struct {
	dao.ChatRepository
	convs              map[string]*model.Conversation
	messagesByKey      map[string]*model.Message
	messagesByID       map[string]*model.Message
	maxSeq             int64
	createCalls        int
	lastMessageUpdates int
}

func newStubSendRepo() *stubSendRepo {
	return &stubSendRepo{
		convs:         map[string]*model.Conversation{},
		messagesByKey: map[string]*model.Message{},
		messagesByID:  map[string]*model.Message{},
	}
}

func (r *stubSendRepo) GetOrCreatePrivateConversation(_ context.Context, userID1, userID2 string) (*model.Conversation, error) {
	key := userID1 + "|" + userID2
	if conv, ok := r.convs[key]; ok {
		return conv, nil
	}
	conv := &model.Conversation{ID: "conv_" + key}
	r.convs[key] = conv
	return conv, nil
}

func (r *stubSendRepo) CreateMessage(_ context.Context, msg *model.Message) error {
	r.createCalls++
	r.maxSeq++
	msg.Seq = r.maxSeq
	r.messagesByID[msg.ID] = msg
	return nil
}

func (r *stubSendRepo) CreateMessageIdempotent(_ context.Context, msg *model.Message) (bool, *model.Message, error) {
	key := msg.SenderID + "|" + msg.RequestID
	if existing, ok := r.messagesByKey[key]; ok {
		return false, existing, nil
	}
	r.maxSeq++
	msg.Seq = r.maxSeq
	r.messagesByKey[key] = msg
	r.messagesByID[msg.ID] = msg
	return true, msg, nil
}

func (r *stubSendRepo) UpdateConversationLastMessage(_ context.Context, _, _ string) error {
	r.lastMessageUpdates++
	return nil
}

// TestPersistIncomingMessageIdempotency 网关重发同一 request_id（消息 ID 可能不同）
// 时，落库层按幂等键只保留一条消息，且不重复推进会话最后消息。
func TestPersistIncomingMessageIdempotency(t *testing.T) {
	repo := newStubSendRepo()
	svc := newAccessService(repo)

	now := time.Now()
	event := &types.MessageEvent{
		ID:          "msg-1",
		RequestID:   "ws-req-1",
		ChatID:      "private_1001_1002",
		ChatType:    types.ChatTypePrivate,
		SenderID:    "1001",
		MessageType: types.MessageTypeText,
		Content:     "hello",
		Timestamp:   now,
	}
	if err := svc.persistIncomingMessage(event); err != nil {
		t.Fatalf("首次落库失败: %v", err)
	}

	// 模拟客户端重发：request_id 相同，但网关生成了新的消息 ID
	retry := *event
	retry.ID = "msg-2"
	if err := svc.persistIncomingMessage(&retry); err != nil {
		t.Fatalf("重发落库失败: %v", err)
	}

	if len(repo.messagesByID) != 1 {
		t.Fatalf("重发不应产生第二条消息，实际 %d 条", len(repo.messagesByID))
	}
	if repo.lastMessageUpdates != 1 {
		t.Fatalf("重发不应再次推进会话最后消息，实际 %d", repo.lastMessageUpdates)
	}
}

// TestSendMessageIdempotency 覆盖客户端幂等键的四条路径：首次创建、重复回放、
// 无幂等键走普通写入、跨会话复用同一 request_id 被拒绝。
func TestSendMessageIdempotency(t *testing.T) {
	t.Run("首次携带 request_id 正常创建", func(t *testing.T) {
		repo := newStubSendRepo()
		svc := newAccessService(repo)

		msg, recipients, replayed, err := svc.SendMessage(
			"1001", "private_1001_1002", model.ChatTypePrivate, model.MessageTypeText,
			"hello", nil, "", nil, "req-1")
		if err != nil {
			t.Fatalf("发送失败: %v", err)
		}
		if replayed {
			t.Fatal("首次请求不应标记为回放")
		}
		if msg.ID == "" || msg.RequestID != "req-1" {
			t.Fatalf("消息应携带幂等键，实际 %+v", msg)
		}
		if repo.lastMessageUpdates != 1 {
			t.Fatalf("首次发送应推进会话最后消息一次，实际 %d", repo.lastMessageUpdates)
		}
		if len(recipients) != 2 {
			t.Fatalf("私聊接收者应包含双方，实际 %v", recipients)
		}
	})

	t.Run("重复 request_id 回放同一条消息", func(t *testing.T) {
		repo := newStubSendRepo()
		svc := newAccessService(repo)

		first, _, _, err := svc.SendMessage(
			"1001", "private_1001_1002", model.ChatTypePrivate, model.MessageTypeText,
			"hello", nil, "", nil, "req-1")
		if err != nil {
			t.Fatalf("首次发送失败: %v", err)
		}

		second, _, replayed, err := svc.SendMessage(
			"1001", "private_1001_1002", model.ChatTypePrivate, model.MessageTypeText,
			"hello", nil, "", nil, "req-1")
		if err != nil {
			t.Fatalf("重试发送失败: %v", err)
		}
		if !replayed {
			t.Fatal("重复请求应标记为回放")
		}
		if second.ID != first.ID {
			t.Fatalf("回放应返回首次消息 %s，实际 %s", first.ID, second.ID)
		}
		if second.Seq != first.Seq {
			t.Fatalf("回放应保持首次 seq %d，实际 %d", first.Seq, second.Seq)
		}
		if repo.lastMessageUpdates != 1 {
			t.Fatalf("回放不应再次推进会话最后消息，实际 %d", repo.lastMessageUpdates)
		}
	})

	t.Run("无 request_id 走普通写入", func(t *testing.T) {
		repo := newStubSendRepo()
		svc := newAccessService(repo)

		_, _, replayed, err := svc.SendMessage(
			"1001", "private_1001_1002", model.ChatTypePrivate, model.MessageTypeText,
			"hello", nil, "", nil, "")
		if err != nil {
			t.Fatalf("发送失败: %v", err)
		}
		if replayed {
			t.Fatal("无幂等键不应标记为回放")
		}
		if repo.createCalls != 1 {
			t.Fatalf("应走一次普通写入，实际 %d", repo.createCalls)
		}
	})

	t.Run("同一 request_id 跨会话复用被拒绝", func(t *testing.T) {
		repo := newStubSendRepo()
		svc := newAccessService(repo)

		if _, _, _, err := svc.SendMessage(
			"1001", "private_1001_1002", model.ChatTypePrivate, model.MessageTypeText,
			"hello", nil, "", nil, "req-shared"); err != nil {
			t.Fatalf("首次发送失败: %v", err)
		}

		_, _, _, err := svc.SendMessage(
			"1001", "private_1001_1003", model.ChatTypePrivate, model.MessageTypeText,
			"hello", nil, "", nil, "req-shared")
		if err != types.ErrChatBadRequest {
			t.Fatalf("应拒绝跨会话复用，期望 ErrChatBadRequest，实际 %v", err)
		}
	})
}
