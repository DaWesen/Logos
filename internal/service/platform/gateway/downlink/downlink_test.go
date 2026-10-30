package downlink

import (
	"encoding/json"
	"testing"
	"time"

	"Logos/internal/service/messaging/types"
)

type fakeSender struct {
	toUsers [][]string
	toUser  []string
	bcasts  int
	excepts []string
}

func (f *fakeSender) SendToUser(userID string, _ []byte) { f.toUser = append(f.toUser, userID) }
func (f *fakeSender) SendToUsers(userIDs []string, _ []byte) {
	f.toUsers = append(f.toUsers, userIDs)
}
func (f *fakeSender) BroadcastMessage(_ []byte) { f.bcasts++ }
func (f *fakeSender) BroadcastMessageExcept(_ []byte, exceptUserID string) {
	f.excepts = append(f.excepts, exceptUserID)
}

func mustDecode(t *testing.T, env Envelope) map[string]interface{} {
	t.Helper()
	raw, err := env.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func dataOf(t *testing.T, env Envelope) map[string]interface{} {
	t.Helper()
	data, ok := mustDecode(t, env)["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not an object: %v", env)
	}
	return data
}

// TestEnvelopeShape 校验信封的固定字段：type / data / timestamp，request_id 仅在应答帧出现。
func TestEnvelopeShape(t *testing.T) {
	push := mustDecode(t, New(TypeTyping, map[string]string{}))
	for _, k := range []string{"type", "data", "timestamp"} {
		if _, ok := push[k]; !ok {
			t.Fatalf("push envelope missing %q: %v", k, push)
		}
	}
	if _, ok := push["request_id"]; ok {
		t.Fatalf("push envelope should omit request_id: %v", push)
	}

	ack := mustDecode(t, NewRequest(TypeAck, "req-1", map[string]string{}))
	if ack["request_id"] != "req-1" {
		t.Fatalf("ack request_id = %v, want req-1", ack["request_id"])
	}
}

// TestChatEventPayloads WS 与 TCP 共用这些构造函数，字段名必须稳定。
func TestChatEventPayloads(t *testing.T) {
	now := time.Now()

	t.Run("message", func(t *testing.T) {
		data := dataOf(t, Message(&types.MessageEvent{ID: "m1", ChatID: "private_1_2", Content: "hi", Timestamp: now}))
		if data["id"] != "m1" || data["chat_id"] != "private_1_2" || data["content"] != "hi" {
			t.Fatalf("unexpected message data: %v", data)
		}
	})

	t.Run("read_receipt", func(t *testing.T) {
		data := dataOf(t, ReadReceipt(&types.MessageReadEvent{
			ReaderID: "2", ChatID: "private_1_2", MessageIDs: []string{"m1"}, Timestamp: now,
		}))
		if data["reader_id"] != "2" || data["chat_id"] != "private_1_2" {
			t.Fatalf("unexpected read data: %v", data)
		}
		if ids, ok := data["message_ids"].([]interface{}); !ok || len(ids) != 1 {
			t.Fatalf("message_ids missing: %v", data)
		}
	})

	t.Run("typing", func(t *testing.T) {
		data := dataOf(t, Typing(&types.TypingEvent{UserID: "1", ChatID: "private_1_2", IsTyping: true}))
		if data["user_id"] != "1" || data["typing"] != true {
			t.Fatalf("unexpected typing data: %v", data)
		}
		if _, ok := data["event_type"]; ok {
			t.Fatalf("typing data must not leak event_type: %v", data)
		}
	})

	t.Run("withdraw", func(t *testing.T) {
		data := dataOf(t, Withdraw(&types.MessageWithdrawEvent{
			MessageID: "m1", ChatID: "private_1_2", SenderID: "1", Version: 3, Timestamp: now,
		}))
		if data["message_id"] != "m1" || data["version"] != float64(3) {
			t.Fatalf("unexpected withdraw data: %v", data)
		}
	})

	t.Run("edit", func(t *testing.T) {
		data := dataOf(t, Edit(&types.MessageEditEvent{
			MessageID: "m1", ChatID: "private_1_2", SenderID: "1", Content: "new", Version: 2, Timestamp: now,
		}))
		if data["content"] != "new" || data["version"] != float64(2) {
			t.Fatalf("unexpected edit data: %v", data)
		}
	})

	t.Run("delete", func(t *testing.T) {
		data := dataOf(t, Delete(&types.MessageDeleteEvent{MessageID: "m1", ChatID: "private_1_2", Timestamp: now}))
		if data["message_id"] != "m1" {
			t.Fatalf("unexpected delete data: %v", data)
		}
		if _, ok := data["owner_id"]; ok {
			t.Fatalf("delete data must not expose owner_id: %v", data)
		}
	})
}

func TestPrivateChatPeer(t *testing.T) {
	cases := []struct {
		name   string
		chatID string
		actor  string
		want   string
		wantOK bool
	}{
		{"private 格式 发送者", "private_1001_1002", "1001", "1002", true},
		{"private 格式 接收者", "private_1001_1002", "1002", "1001", true},
		{"private 格式 旁观者", "private_1001_1002", "1003", "", false},
		{"历史格式", "1001_1002", "1002", "1001", true},
		{"QQ 单边会话", "private_1001_self", "1001", "", false},
		{"群聊 ID", "group_1001_1002", "1001", "", false},
		{"单段 ID", "1001", "1001", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PrivateChatPeer(tc.chatID, tc.actor)
			if got != tc.want || ok != tc.wantOK {
				t.Fatalf("PrivateChatPeer(%q,%q) = (%q,%v), want (%q,%v)",
					tc.chatID, tc.actor, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestDeliverToChat(t *testing.T) {
	t.Run("精确名单优先", func(t *testing.T) {
		s := &fakeSender{}
		if !DeliverToChat(s, []byte("{}"), []string{"7", "8"}, "1", "group_1", false) {
			t.Fatal("expected delivered")
		}
		if len(s.toUsers) != 1 || len(s.excepts) != 0 {
			t.Fatalf("unexpected calls: %+v", s)
		}
	})

	t.Run("私聊回落到对端", func(t *testing.T) {
		s := &fakeSender{}
		if !DeliverToChat(s, []byte("{}"), nil, "1001", "private_1001_1002", false) {
			t.Fatal("expected delivered")
		}
		if len(s.toUser) != 1 || s.toUser[0] != "1002" {
			t.Fatalf("expected peer 1002, got %+v", s.toUser)
		}
	})

	t.Run("私聊解析失败必须丢弃而不是广播", func(t *testing.T) {
		s := &fakeSender{}
		if DeliverToChat(s, []byte("{}"), nil, "1003", "private_1001_1002", false) {
			t.Fatal("expected dropped")
		}
		if s.bcasts != 0 || len(s.excepts) != 0 || len(s.toUser) != 0 {
			t.Fatalf("private chat must not be broadcast: %+v", s)
		}
	})

	t.Run("群聊缺少成员名单必须丢弃", func(t *testing.T) {
		s := &fakeSender{}
		if DeliverToChat(s, []byte("{}"), nil, "1001", "group_777", false) {
			t.Fatal("group event without recipients must be dropped, not broadcast")
		}
		if s.bcasts != 0 || len(s.excepts) != 0 {
			t.Fatalf("group chat must not fall back to broadcast: %+v", s)
		}
	})

	t.Run("广播会话允许全站广播", func(t *testing.T) {
		s := &fakeSender{}
		if !DeliverToChat(s, []byte("{}"), nil, "1001", "broadcast", false) {
			t.Fatal("expected delivered")
		}
		if len(s.excepts) != 1 || s.excepts[0] != "1001" {
			t.Fatalf("expected BroadcastMessageExcept(1001), got %+v", s)
		}
	})

	t.Run("调用方声明广播时可全站广播", func(t *testing.T) {
		s := &fakeSender{}
		if !DeliverToChat(s, []byte("{}"), nil, "1001", "group_777", true) {
			t.Fatal("expected delivered")
		}
		if len(s.excepts) != 1 || s.excepts[0] != "1001" {
			t.Fatalf("expected BroadcastMessageExcept(1001), got %+v", s)
		}
	})
}

func TestDeliverMessage(t *testing.T) {
	t.Run("私聊兜底发给双方", func(t *testing.T) {
		s := &fakeSender{}
		if !DeliverMessage(s, []byte("{}"), nil, "1001", "private_1001_1002", false) {
			t.Fatal("expected delivered")
		}
		if len(s.toUsers) != 1 || len(s.toUsers[0]) != 2 {
			t.Fatalf("expected both participants, got %+v", s.toUsers)
		}
	})

	t.Run("私聊解析失败丢弃", func(t *testing.T) {
		s := &fakeSender{}
		if DeliverMessage(s, []byte("{}"), nil, "1003", "private_1001_1002", false) {
			t.Fatal("expected dropped")
		}
	})

	t.Run("群消息缺少接收者必须丢弃", func(t *testing.T) {
		s := &fakeSender{}
		if DeliverMessage(s, []byte("{}"), nil, "1001", "group_777", false) {
			t.Fatal("group message without recipients must be dropped, not broadcast")
		}
		if s.bcasts != 0 || len(s.excepts) != 0 {
			t.Fatalf("group message must not fall back to broadcast: %+v", s)
		}
	})

	t.Run("广播消息允许全站广播", func(t *testing.T) {
		s := &fakeSender{}
		if !DeliverMessage(s, []byte("{}"), nil, "system", "broadcast", true) {
			t.Fatal("expected delivered")
		}
		if len(s.excepts) != 1 || s.excepts[0] != "system" {
			t.Fatalf("expected BroadcastMessageExcept(system), got %+v", s)
		}
	})
}

func TestDeliverToUsers(t *testing.T) {
	s := &fakeSender{}
	if DeliverToUsers(s, nil, []byte("{}")) {
		t.Fatal("empty recipients must not deliver")
	}
	if !DeliverToUsers(s, []string{"1"}, []byte("{}")) {
		t.Fatal("expected delivered")
	}
}
