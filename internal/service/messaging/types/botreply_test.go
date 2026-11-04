package types

import "testing"

func TestBotReplyMessageID_IsDeterministic(t *testing.T) {
	first := BotReplyMessageID("b1", "msg-1")
	second := BotReplyMessageID("b1", "msg-1")
	if first == "" {
		t.Fatal("完整输入应生成 ID")
	}
	if first != second {
		t.Fatal("同一 Bot 与源消息必须生成相同 ID，否则重投会产生重复回复")
	}
}

func TestBotReplyMessageID_DistinguishesInputs(t *testing.T) {
	cases := []struct{ botID, source string }{
		{"b1", "msg-1"},
		{"b2", "msg-1"},
		{"b1", "msg-2"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		id := BotReplyMessageID(c.botID, c.source)
		if seen[id] {
			t.Fatalf("不同输入生成了相同 ID: %s", id)
		}
		seen[id] = true
	}
}

func TestBotReplyMessageID_RejectsIncompleteInput(t *testing.T) {
	for _, c := range []struct{ botID, source string }{
		{"", "msg-1"},
		{"b1", ""},
		{"", ""},
	} {
		if id := BotReplyMessageID(c.botID, c.source); id != "" {
			t.Fatalf("不完整输入应返回空字符串，实际 %q", id)
		}
	}
}

func TestBotReplyMessageID_FitsColumnWidth(t *testing.T) {
	// messages.id 是 varchar(64)，超长会在落库时被截断/报错。
	id := BotReplyMessageID("bot-uuid", "message-uuid")
	if len(id) > 64 {
		t.Fatalf("ID 超出列宽: %d", len(id))
	}
	if len(id) < 33 {
		t.Fatalf("ID 过短，碰撞风险不可接受: %d", len(id))
	}
}
