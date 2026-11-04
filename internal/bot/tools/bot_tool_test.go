package tools

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"Logos/internal/bot/agent"

	"github.com/cloudwego/eino/schema"
)

type fakeBotAgent struct{ name string }

func (f *fakeBotAgent) Chat(context.Context, string) (string, error) { return "", nil }

func (f *fakeBotAgent) ChatStream(context.Context, string, func(string) error) error { return nil }

func (f *fakeBotAgent) ChatWithHistory(context.Context, []*schema.Message) (string, error) {
	return "", nil
}

func (f *fakeBotAgent) ChatStreamWithHistory(context.Context, []*schema.Message, func(string) error) error {
	return nil
}

func (f *fakeBotAgent) GetName() string         { return f.name }
func (f *fakeBotAgent) GetDescription() string  { return "" }
func (f *fakeBotAgent) GetSystemPrompt() string { return "" }

// TestBotToolBaseName_RespectsTotalBudget 长度预算必须包含 "bot_" 前缀。
// 旧实现先截断 64 再拼前缀 → 68 字符，超供应商上限后整次请求 400。
func TestBotToolBaseName_RespectsTotalBudget(t *testing.T) {
	long := strings.Repeat("A", 100)

	got := botToolBaseName(long)
	if n := utf8.RuneCountInString(got); n > maxToolNameRunes {
		t.Fatalf("%q 长度 %d 超过上限 %d", got, n, maxToolNameRunes)
	}
	if !strings.HasPrefix(got, "bot_") {
		t.Fatalf("缺少前缀: %q", got)
	}
	if got := botToolBaseName("   "); got != "bot_bot" {
		t.Fatalf("空名兜底 = %q, want bot_bot", got)
	}
}

// TestBuildBotTools_DedupesCollapsedNames 清洗后塌缩成同一个名字的多个 Bot
// 必须拿到互不冲突的函数名，否则模型会看到两个同名工具。
func TestBuildBotTools_DedupesCollapsedNames(t *testing.T) {
	agents := []agent.BotAgent{
		&fakeBotAgent{name: "My Bot"},
		&fakeBotAgent{name: "My_Bot"},
		&fakeBotAgent{name: "My-Bot"},
		&fakeBotAgent{name: ""},
	}

	built := BuildBotTools(agents)
	if len(built) != len(agents) {
		t.Fatalf("built %d tools, want %d", len(built), len(agents))
	}

	seen := make(map[string]struct{}, len(built))
	for _, b := range built {
		info, err := b.Info(context.Background())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		if _, dup := seen[info.Name]; dup {
			t.Fatalf("重复的工具名: %q", info.Name)
		}
		seen[info.Name] = struct{}{}

		if n := utf8.RuneCountInString(info.Name); n > maxToolNameRunes {
			t.Fatalf("%q 长度 %d 超过上限 %d", info.Name, n, maxToolNameRunes)
		}
	}
}
