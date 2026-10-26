package service

import (
	"testing"
)

// TestPrivateChatTarget 私聊 chatID 由客户端传入，
// 必须保证发送者是会话双方之一，否则可向任意两人的私聊会话注入消息。
func TestPrivateChatTarget(t *testing.T) {
	tests := []struct {
		name     string
		chatID   string
		senderID string
		wantA    string
		wantB    string
		wantTgt  string
		paired   bool
		denied   bool
	}{
		{
			name:   "private 格式 参与者方向正",
			chatID: "private_alice_bob", senderID: "alice",
			wantA: "alice", wantB: "bob", wantTgt: "bob", paired: true,
		},
		{
			name:   "private 格式 参与者方向反",
			chatID: "private_alice_bob", senderID: "bob",
			wantA: "alice", wantB: "bob", wantTgt: "alice", paired: true,
		},
		{
			name:   "private 格式 非参与者拒绝",
			chatID: "private_alice_bob", senderID: "mallory",
			denied: true,
		},
		{
			name:   "a_b 格式 参与者",
			chatID: "alice_bob", senderID: "bob",
			wantA: "alice", wantB: "bob", wantTgt: "alice", paired: true,
		},
		{
			name:   "a_b 格式 非参与者拒绝",
			chatID: "alice_bob", senderID: "mallory",
			denied: true,
		},
		{
			name:   "单段 chatID 视为目标用户",
			chatID: "bob", senderID: "alice",
			wantTgt: "bob", paired: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b, target, paired, err := privateChatTarget(tt.chatID, tt.senderID)
			if tt.denied {
				if err == nil {
					t.Fatalf("非参与者应被拒绝，实际放行")
				}
				return
			}
			if err != nil {
				t.Fatalf("不应拒绝: %v", err)
			}
			if a != tt.wantA || b != tt.wantB || target != tt.wantTgt || paired != tt.paired {
				t.Fatalf("解析结果不符: got=(%q,%q,%q,%v), want=(%q,%q,%q,%v)",
					a, b, target, paired, tt.wantA, tt.wantB, tt.wantTgt, tt.paired)
			}
		})
	}
}
