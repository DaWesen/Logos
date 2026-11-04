package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	botmodel "Logos/internal/service/ai/bot/model"
)

func memFixture(id, key, value string, conf float64, updated time.Time) *botmodel.UserMemory {
	return &botmodel.UserMemory{
		ID:         id,
		Key:        key,
		Value:      value,
		Category:   "preference",
		Confidence: conf,
		UpdatedAt:  updated,
	}
}

// TestSelectMemories_RelevanceFirst 与当前问题相关的记忆应排在无关记忆之前，
// 即便两者置信度相同。
func TestSelectMemories_RelevanceFirst(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		memFixture("m1", "讨厌香菜", "不喜欢吃香菜", 0.9, now),
		memFixture("m2", "喜欢喝咖啡", "每天早上一杯美式", 0.9, now),
	}

	selected := selectMemories(mems, "我想喝点咖啡", now)
	if selected[0].ID != "m2" {
		t.Fatalf("相关记忆应排第一，got %s", selected[0].ID)
	}
}

// TestSelectMemories_RecencyBreaksTie 相关度与置信度相同时，新记忆优先。
func TestSelectMemories_RecencyBreaksTie(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		memFixture("old", "喜欢咖啡", "以前每天喝", 0.9, now.Add(-60*24*time.Hour)),
		memFixture("new", "喜欢咖啡", "现在改喝拿铁", 0.9, now),
	}

	selected := selectMemories(mems, "咖啡", now)
	if selected[0].ID != "new" {
		t.Fatalf("较新的记忆应排第一，got %s", selected[0].ID)
	}
}

// TestSelectMemories_ManualPinned 手工记忆不能被相关性检索淘汰：
// 用户明确要求记住的信息，当前问题无关也要带上。
func TestSelectMemories_ManualPinned(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		{ID: "manual", Key: "对花生过敏", Value: "严重过敏", Category: "fact", Confidence: 0.9, Source: "manual", UpdatedAt: now},
	}
	for i := 0; i < memoryPromptLimit+5; i++ {
		mems = append(mems, memFixture(fmt.Sprintf("auto%d", i), "喜欢咖啡", "每天一杯", 0.9, now))
	}

	selected := selectMemories(mems, "帮我推荐咖啡豆", now)
	if len(selected) > memoryPromptLimit {
		t.Fatalf("超过总量上限: %d", len(selected))
	}
	if selected[0].ID != "manual" {
		t.Fatalf("手工记忆必须保留且优先，got %s", selected[0].ID)
	}
}

// TestSelectMemories_Deterministic 同一输入必须给出同一顺序。
func TestSelectMemories_Deterministic(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		memFixture("c", "喜欢咖啡", "拿铁", 0.9, now),
		memFixture("a", "喜欢咖啡", "美式", 0.9, now),
		memFixture("b", "喜欢咖啡", "卡布", 0.9, now),
	}

	first := selectMemories(mems, "咖啡", now)
	for i := 0; i < 10; i++ {
		again := selectMemories(mems, "咖啡", now)
		for j := range first {
			if first[j].ID != again[j].ID {
				t.Fatalf("顺序不稳定: %v vs %v", first, again)
			}
		}
	}
}

// TestSelectMemories_EmptyQueryFallsBackToConfidence 没有对话上下文时退化
// 为「置信度 + 新鲜度」，不因缺少 query 而改变原有行为。
func TestSelectMemories_EmptyQueryFallsBackToConfidence(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		memFixture("low", "k1", "v1", 0.6, now),
		memFixture("high", "k2", "v2", 0.95, now),
	}

	selected := selectMemories(mems, "", now)
	if selected[0].ID != "high" {
		t.Fatalf("无 query 时应按置信度，got %s", selected[0].ID)
	}
}

// TestBuildMemoryPromptFor_DeclaresUnselected 被相关性淘汰的条数必须声明，
// 否则模型会以为记忆就这些。
func TestBuildMemoryPromptFor_DeclaresUnselected(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		memFixture("m1", "喜欢咖啡", "每天一杯", 0.9, now),
	}
	for i := 0; i < memoryPromptLimit+3; i++ {
		mems = append(mems, memFixture(fmt.Sprintf("x%d", i), "无关内容", "别的信息", 0.9, now))
	}
	m := newTestManager(&mockMemoryRepo{memories: mems})

	got := m.BuildMemoryPromptFor(context.Background(), "u1", "b1", "咖啡")
	if !strings.Contains(got, "未列出") {
		t.Fatalf("缺少未列出提示: %q", got)
	}
}

func TestMemoryTokens(t *testing.T) {
	tokens := memoryTokens("我喜欢喝咖啡 Python 3")
	joined := strings.Join(tokens, "|")
	for _, want := range []string{"咖啡", "喜欢", "python", "3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少 token %q: %s", want, joined)
		}
	}

	// 单字查询保留单字，否则「猫」匹配不到「养猫」
	single := memoryTokens("猫")
	if len(single) != 1 || single[0] != "猫" {
		t.Fatalf("单字查询 token 错误: %v", single)
	}
}

func TestRecencyScore(t *testing.T) {
	now := time.Now()
	if got := recencyScore(now, now); got < 0.99 {
		t.Fatalf("刚更新应为 1 附近, got %v", got)
	}
	if got := recencyScore(now.Add(-memoryRecencyHalfLife), now); got < 0.49 || got > 0.51 {
		t.Fatalf("半个半衰期应为 0.5, got %v", got)
	}
	if got := recencyScore(time.Time{}, now); got != 0.5 {
		t.Fatalf("未知时间应为 0.5, got %v", got)
	}
}
