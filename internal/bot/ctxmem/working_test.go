package ctxmem

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

// runeEstimator 用「1 rune = 1 token」让测试能精确构造预算边界，
// 不去猜 DefaultEstimator 的浮点换算。
type runeEstimator struct{}

func (runeEstimator) Estimate(text string) int { return utf8.RuneCountInString(text) }

func (runeEstimator) EstimateMessage(msg *schema.Message) int {
	if msg == nil {
		return 0
	}
	return utf8.RuneCountInString(msg.Content)
}

func TestPackWorking_KeepsWholeTurnsAndToolPairing(t *testing.T) {
	messages := []Message{
		{ID: "u1", Role: "user", Content: "q1"},
		{ID: "a1", Role: "assistant", Content: "a1"},
		{ID: "u2", Role: "user", Content: "q2"},
		{ID: "a2", Role: "assistant", Content: "a2"},
		{ID: "t2", Role: "tool", Content: "result"},
	}

	// 最新一轮成本 2+2+6=10，加上上一轮 4 会超过 12。
	kept, overflow, omitted := PackWorking(messages, 12, runeEstimator{})

	if len(kept) != 3 || len(overflow) != 2 || omitted != 2 {
		t.Fatalf("kept=%d overflow=%d omitted=%d, want 3/2/2", len(kept), len(overflow), omitted)
	}
	if kept[0].ID != "u2" {
		t.Fatalf("kept must start at a turn boundary, got %q", kept[0].ID)
	}
	if kept[len(kept)-1].Role != "tool" || kept[len(kept)-2].Role != "assistant" {
		t.Fatalf("assistant(tool_calls) and tool result were split: %+v", kept)
	}
	if overflow[0].ID != "u1" {
		t.Fatalf("overflow should keep chronological order, got %q first", overflow[0].ID)
	}
}

// TestPackWorking_ForcedKeepPicksUser 单轮超预算时，只能保一条：
// 必须保当前问题（user），不能保最后的 tool 结果，否则会产生孤儿工具消息。
func TestPackWorking_ForcedKeepPicksUser(t *testing.T) {
	messages := []Message{
		{ID: "u1", Role: "user", Content: "old question"},
		{ID: "u2", Role: "user", Content: "q2"},
		{ID: "a2", Role: "assistant", Content: "a2"},
		{ID: "t2", Role: "tool", Content: "result"},
	}

	kept, overflow, _ := PackWorking(messages, 5, runeEstimator{})

	if len(kept) != 1 || kept[0].ID != "u2" {
		t.Fatalf("forced keep = %+v, want only u2", kept)
	}
	if len(overflow) != 3 {
		t.Fatalf("overflow = %d, want 3", len(overflow))
	}
}

func TestPackWorking_ZeroBudgetKeepsNothing(t *testing.T) {
	messages := []Message{{ID: "u1", Role: "user", Content: "q"}}
	kept, overflow, omitted := PackWorking(messages, 0, runeEstimator{})
	if len(kept) != 0 || len(overflow) != 1 || omitted != 1 {
		t.Fatalf("kept=%d overflow=%d omitted=%d, want 0/1/1", len(kept), len(overflow), omitted)
	}
}

func TestPackWorking_NeverExceedsBudget(t *testing.T) {
	messages := []Message{
		{ID: "u1", Role: "user", Content: strings.Repeat("a", 40)},
		{ID: "a1", Role: "assistant", Content: strings.Repeat("b", 40)},
		{ID: "u2", Role: "user", Content: strings.Repeat("c", 40)},
		{ID: "a2", Role: "assistant", Content: strings.Repeat("d", 40)},
		{ID: "u3", Role: "user", Content: strings.Repeat("e", 40)},
	}

	for budget := 1; budget <= 200; budget++ {
		kept, _, _ := PackWorking(messages, budget, runeEstimator{})
		if got := MessagesTokens(kept, runeEstimator{}); got > budget {
			t.Fatalf("budget=%d: kept tokens=%d", budget, got)
		}
	}
}

func TestTruncateToTokens_IsRuneSafe(t *testing.T) {
	text := strings.Repeat("中", 100)
	got := TruncateToTokens(text, 60, DefaultEstimator{})

	if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) {
		t.Fatal("truncation produced invalid UTF-8")
	}
	if (DefaultEstimator{}).Estimate(got) > 60 {
		t.Fatalf("truncated text still exceeds budget: %d", (DefaultEstimator{}).Estimate(got))
	}
	if got == "" {
		t.Fatal("budget should fit at least one CJK rune")
	}
}
