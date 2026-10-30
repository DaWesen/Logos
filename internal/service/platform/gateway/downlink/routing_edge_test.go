package downlink

import "testing"

// TestChatIDClassification 验证会话 ID 分类的边界。
// 分类决定了「能不能退化为广播」，错判一个字符就可能把私聊内容推给全站。
func TestChatIDClassification(t *testing.T) {
	privateCases := []struct {
		chatID string
		want   bool
	}{
		{"private_1001_1002", true},
		{"1001_1002", true},
		{"private_1001_self", true}, // 单边会话仍属私聊，只是没有对端
		{"private_1001", false},     // 段数不足
		{"private_1001_1002_1003", false},
		// 空参与者仍按「私聊形状」处理：此时 PrivateChatPeer 解析失败，
		// 投递会走「私聊必须丢弃」分支，比误判成群聊进而广播更安全。
		{"private__1002", true},
		{"1001_", false},
		{"1001_abc", false}, // 历史格式要求两端都是数字
		{"abc_def", false},
		{"", false},
		{"group_1001", false},
	}
	for _, tc := range privateCases {
		if got := IsPrivateChatID(tc.chatID); got != tc.want {
			t.Fatalf("IsPrivateChatID(%q) = %v, want %v", tc.chatID, got, tc.want)
		}
	}

	broadcastCases := []struct {
		chatID string
		want   bool
	}{
		{"broadcast", true},
		{"broadcast_all", true},
		{"Broadcast", false},  // 大小写敏感：避免与业务会话 ID 混淆
		{"broadcast2", false}, // 没有下划线前缀
		{"broadcast_", true},  // 空前缀仍按广播处理，宁可变宽不放宽
		{"system_broadcast", false},
	}
	for _, tc := range broadcastCases {
		if got := IsBroadcastChatID(tc.chatID); got != tc.want {
			t.Fatalf("IsBroadcastChatID(%q) = %v, want %v", tc.chatID, got, tc.want)
		}
	}
}

// TestPrivateChatPeerEmptyParticipant 空参与者必须解析失败，不能返回空对端 ID
// （否则调用方会向 "" 投递，最终在去重阶段静默丢弃）。
func TestPrivateChatPeerEmptyParticipant(t *testing.T) {
	if _, ok := PrivateChatPeer("private__1002", "1002"); ok {
		t.Fatal("空参与者不应解析成功")
	}
	if peer, ok := PrivateChatPeer("private_1001_1002", ""); ok || peer != "" {
		t.Fatalf("空 actor 不应解析成功，得到 (%q,%v)", peer, ok)
	}
}

// TestBlankRecipientsTreatedAsAbsent 只有空 ID 的名单等价于没有名单：
// 私聊应回落到对端，而不是「有名单但不投递」的静默丢失。
func TestBlankRecipientsTreatedAsAbsent(t *testing.T) {
	t.Run("私聊回落到对端", func(t *testing.T) {
		s := &fakeSender{}
		if !DeliverToChat(s, []byte("{}"), []string{""}, "1001", "private_1001_1002", false) {
			t.Fatal("空名单应回落到私聊对端，而不是静默丢弃")
		}
		if len(s.toUser) != 1 || s.toUser[0] != "1002" {
			t.Fatalf("expected peer 1002, got %+v", s.toUser)
		}
		if len(s.toUsers) != 0 {
			t.Fatalf("空 ID 名单不应触发精确投递: %+v", s.toUsers)
		}
	})

	t.Run("群聊仍然丢弃", func(t *testing.T) {
		s := &fakeSender{}
		if DeliverToChat(s, []byte("{}"), []string{""}, "1001", "group_777", false) {
			t.Fatal("群聊空名单仍应丢弃")
		}
	})

	t.Run("精确投递返回 false", func(t *testing.T) {
		s := &fakeSender{}
		if DeliverToUsers(s, []string{""}, []byte("{}")) {
			t.Fatal("只有空 ID 的名单不应报告投递成功")
		}
		if len(s.toUsers) != 0 {
			t.Fatalf("不应调用 SendToUsers: %+v", s.toUsers)
		}
	})
}

// TestDeliverMessageSenderOnlyChat 发送者与自己对私聊（单边会话）解析不出对端时，
// 消息按私聊规则丢弃而不是广播，避免把单边会话内容推给全站。
func TestDeliverMessageSenderOnlyChat(t *testing.T) {
	s := &fakeSender{}
	if DeliverMessage(s, []byte("{}"), nil, "1001", "private_1001_self", false) {
		t.Fatal("单边私聊无对端时应丢弃")
	}
	if s.bcasts != 0 || len(s.excepts) != 0 {
		t.Fatalf("单边私聊不应广播: %+v", s)
	}
}
