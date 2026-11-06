package service

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestMemoryContextMessages_EmptyPromptProducesNothing(t *testing.T) {
	for _, prompt := range []string{"", "   ", "\n\t"} {
		if got := memoryContextMessages(prompt); got != nil {
			t.Errorf("empty memory prompt %q should not inject messages, got %d", prompt, len(got))
		}
	}
}

func TestMemoryContextMessages_IsPerRequestUserPair(t *testing.T) {
	memory := "以下是...\n【用户偏好】\n- 喜欢喝咖啡"
	msgs := memoryContextMessages(memory)

	if len(msgs) != 2 {
		t.Fatalf("memory should be injected as a user/assistant pair, got %d", len(msgs))
	}
	// 关键回归：记忆不能是 system 消息。system prompt 会随 Agent 按 bot.ID
	// 缓存，只有 user 消息能保证每次请求、每个用户各算各的。
	if msgs[0].Role != schema.User {
		t.Errorf("memory context must start with user role, got %s", msgs[0].Role)
	}
	if msgs[1].Role != schema.Assistant {
		t.Errorf("memory context must keep role alternation, got %s", msgs[1].Role)
	}
	for _, m := range msgs {
		if m.Role == schema.System {
			t.Fatal("per-user memory must never be a system message")
		}
	}
	if !strings.Contains(msgs[0].Content, "喜欢喝咖啡") {
		t.Errorf("memory body missing: %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "不要向用户复述") {
		t.Errorf("memory context should tell the model not to read it back: %q", msgs[0].Content)
	}
}
