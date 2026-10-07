package memory

import (
	"fmt"
	"strings"
	"testing"

	botmodel "Logos/internal/service/ai/bot/model"
)

func TestClassifyRelation(t *testing.T) {
	tests := []struct {
		name         string
		existing     string
		newVal       string
		wantRelation Relation
	}{
		{"相同值", "喜欢 Go", "喜欢 Go", RelationConfirm},
		{"新值包含旧值", "喜欢 Go", "喜欢 Go 和 Rust", RelationSupplement},
		{"完全不同", "喜欢 Go", "喜欢 Java", RelationConflict},
		{"旧值为空", "", "喜欢 Go", RelationConflict}, // 空旧值不构成包含，按矛盾处理由调用方保证新库不为空
	}
	for _, tt := range tests {
		if got := ClassifyRelation(tt.existing, tt.newVal); got != tt.wantRelation {
			t.Errorf("%s: ClassifyRelation(%q, %q) = %v, want %v", tt.name, tt.existing, tt.newVal, got, tt.wantRelation)
		}
	}
}

func newExistingMemory(value string, confidence float64, source string) *botmodel.UserMemory {
	return &botmodel.UserMemory{
		ID: "m1", Key: "favorite_language", Value: value,
		Confidence: confidence, Source: source,
	}
}

func TestApplyMemoryMerge_Confirm(t *testing.T) {
	existing := newExistingMemory("喜欢 Go", 0.7, "auto_extract")
	merged, relation := ApplyMemoryMerge(existing, "喜欢 Go", 0.8, []string{"msg-2"})

	if relation != RelationConfirm {
		t.Fatalf("relation = %v, want confirm", relation)
	}
	// 值不重写，置信 +0.05
	if merged.Value != "喜欢 Go" {
		t.Errorf("confirm should not rewrite value, got %q", merged.Value)
	}
	if merged.Confidence != 0.75 {
		t.Errorf("confirm should bump confidence by 0.05, got %v", merged.Confidence)
	}
	// 证据合并
	if len(merged.Evidence) == 0 {
		t.Error("confirm should merge evidence")
	}
}

func TestApplyMemoryMerge_ConfirmCap(t *testing.T) {
	// 已在顶部的确认不再上涨（重复确认≠绝对真理）
	existing := newExistingMemory("喜欢 Go", 0.97, "auto_extract")
	merged, _ := ApplyMemoryMerge(existing, "喜欢 Go", 0.99, nil)
	if merged.Confidence != 0.98 {
		t.Errorf("confirm cap = %v, want 0.98", merged.Confidence)
	}
}

func TestApplyMemoryMerge_Supplement(t *testing.T) {
	existing := newExistingMemory("喜欢 Go", 0.6, "auto_extract")
	merged, relation := ApplyMemoryMerge(existing, "喜欢 Go 和 Rust", 0.5, nil)

	if relation != RelationSupplement {
		t.Fatalf("relation = %v, want supplement", relation)
	}
	if merged.Value != "喜欢 Go 和 Rust" {
		t.Errorf("supplement should take new (richer) value, got %q", merged.Value)
	}
	if merged.Confidence != 0.6 {
		t.Errorf("supplement should take max confidence, got %v", merged.Confidence)
	}
}

func TestApplyMemoryMerge_Conflict(t *testing.T) {
	existing := newExistingMemory("喜欢 Go", 0.85, "auto_extract")
	merged, relation := ApplyMemoryMerge(existing, "喜欢 Java", 0.9, []string{"msg-9"})

	if relation != RelationConflict {
		t.Fatalf("relation = %v, want conflict", relation)
	}
	// 置信 = min(0.85,0.9) - 0.15 = 0.7
	if merged.Confidence != 0.70 {
		t.Errorf("conflict confidence = %v, want 0.70", merged.Confidence)
	}
	// 双结论：值取新的，旧值保留
	if merged.Value != "喜欢 Java" || merged.ConflictValue != "喜欢 Go" {
		t.Errorf("conflict should keep both conclusions, got value=%q conflict=%q", merged.Value, merged.ConflictValue)
	}
}

func TestApplyMemoryMerge_ConflictFloor(t *testing.T) {
	// 连续矛盾降到底但不归零
	existing := newExistingMemory("喜欢 Go", 0.25, "auto_extract")
	merged, _ := ApplyMemoryMerge(existing, "喜欢 Java", 0.3, nil)
	if merged.Confidence != 0.2 {
		t.Errorf("conflict floor = %v, want 0.2", merged.Confidence)
	}
}

func TestApplyMemoryMerge_ConflictExitsInThreeRounds(t *testing.T) {
	// 从 0.8 出发连续矛盾三轮出局（<0.5 不再注入 prompt）：
	// 0.8 → 0.65 → 0.5 → 0.35
	mem := newExistingMemory("结论A", 0.8, "auto_extract")
	for round, want := range []float64{0.65, 0.5, 0.35} {
		merged, _ := ApplyMemoryMerge(mem, fmt.Sprintf("结论B%d", round), 0.9, nil)
		if merged.Confidence != want {
			t.Fatalf("round %d: confidence = %v, want %v", round+1, merged.Confidence, want)
		}
		mem = merged
	}
	if ShouldInjectMemory(mem) {
		t.Error("after 3 conflicts the memory should fall below injection threshold")
	}
}

func TestApplyMemoryMerge_UserOverrideProtected(t *testing.T) {
	existing := newExistingMemory("用户手改的值", 0.9, "manual")
	merged, relation := ApplyMemoryMerge(existing, "LLM 抽取的新值", 0.95, []string{"msg-1"})

	if relation != RelationUserOverride {
		t.Fatalf("relation = %v, want user_override", relation)
	}
	if merged != existing || merged.Value != "用户手改的值" {
		t.Error("manual memory must never be overwritten by LLM extraction")
	}
}

func TestApplyMemoryMerge_DoesNotMutateExisting(t *testing.T) {
	existing := newExistingMemory("喜欢 Go", 0.7, "auto_extract")
	existing.Evidence = botmodel.StringSlice{"old-1"}

	merged, _ := ApplyMemoryMerge(existing, "喜欢 Go", 0.8, []string{"new-1"})
	if existing.Confidence != 0.7 {
		t.Error("merge must not mutate the input memory (copy-on-write)")
	}
	if len(merged.Evidence) != 2 || merged.Evidence[0] != "new-1" {
		t.Errorf("evidence should merge new-first, got %v", merged.Evidence)
	}
}

func TestMergeEvidence(t *testing.T) {
	// 新证据在前、去重、截断
	got := mergeEvidence([]string{"a", "b"}, []string{"b", "c"})
	if len(got) != 3 || got[0] != "b" || got[1] != "c" || got[2] != "a" {
		t.Errorf("mergeEvidence order/dedup wrong: %v", got)
	}

	many := make([]string, 100)
	for i := range many {
		many[i] = fmt.Sprintf("m-%d", i)
	}
	if got := mergeEvidence(nil, many); len(got) != maxEvidence {
		t.Errorf("evidence should be capped at %d, got %d", maxEvidence, len(got))
	}
}

func TestShouldInjectMemory(t *testing.T) {
	if ShouldInjectMemory(&botmodel.UserMemory{Confidence: 0.49}) {
		t.Error("0.49 should not be injected")
	}
	if !ShouldInjectMemory(&botmodel.UserMemory{Confidence: 0.5}) {
		t.Error("0.5 (threshold, inclusive) should be injected")
	}
}

func TestRenderMemoryLine(t *testing.T) {
	// 高置信无标注
	high := &botmodel.UserMemory{Key: "k", Value: "v", Confidence: 0.9}
	if got := RenderMemoryLine(high); got != "- k: v" {
		t.Errorf("high confidence line = %q", got)
	}

	// 中置信标注
	thin := &botmodel.UserMemory{Key: "k", Value: "v", Confidence: 0.55}
	if got := RenderMemoryLine(thin); !strings.Contains(got, "⚠︎证据较少") {
		t.Errorf("thin confidence should be marked, got %q", got)
	}

	// 矛盾双结论声明
	conflicted := &botmodel.UserMemory{Key: "k", Value: "新", Confidence: 0.7, ConflictValue: "旧"}
	if got := RenderMemoryLine(conflicted); !strings.Contains(got, "旧") || !strings.Contains(got, "另有较早的不同说法") {
		t.Errorf("conflict should surface old value, got %q", got)
	}
}

func TestBuildMemoryPrompt_TieredInjection(t *testing.T) {
	repo := &mockMemoryRepo{
		memories: []*botmodel.UserMemory{
			{Key: "low", Value: "低置信", Category: "fact", Confidence: 0.3},
			{Key: "thin", Value: "中置信", Category: "fact", Confidence: 0.55},
			{Key: "high", Value: "高置信", Category: "fact", Confidence: 0.9},
		},
	}
	m := newTestManager(repo)
	got := m.BuildMemoryPrompt(t.Context(), "u1", "b1")

	// 低置信记忆的值不进 prompt（注意与"未注入"声明区分开）
	if strings.Contains(got, "低置信\n") || strings.Contains(got, "low: ") {
		t.Errorf("memory below 0.5 must not be injected, got: %s", got)
	}
	if !strings.Contains(got, "高置信") || !strings.Contains(got, "中置信") {
		t.Error("memories >= 0.5 should be injected")
	}
	if !strings.Contains(got, "⚠︎证据较少") {
		t.Error("thin memory should carry annotation")
	}
	// 排序声明：高置信在中置信之前
	if strings.Index(got, "高置信") > strings.Index(got, "中置信") {
		t.Error("memories should be ordered by confidence descending")
	}
	// 被挡条数对调用方可见
	if !strings.Contains(got, "1 条低置信度记忆未注入") {
		t.Errorf("dropped count should be declared, got: %s", got)
	}
}

func TestNormalizeConfidence(t *testing.T) {
	tests := []struct {
		in   float64
		want float64
	}{
		{0.8, 0.8},
		{0, 0.5},      // 未打分 → 不知道
		{-0.1, 0.5},   // 越界
		{1.5, 0.5},    // 越界
		{1.0, 1.0},    // 边界合法
	}
	for _, tt := range tests {
		if got := normalizeConfidence(tt.in); got != tt.want {
			t.Errorf("normalizeConfidence(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestResolveEvidenceIDs(t *testing.T) {
	msgs := []*botmodel.Message{
		{ID: "m1", Role: "user"},
		{ID: "m2", Role: "assistant"},
		{ID: "m3", Role: "user"},
	}

	if got := resolveEvidenceIDs([]int{1, 3}, msgs); len(got) != 2 || got[0] != "m1" || got[1] != "m3" {
		t.Errorf("valid evidence = %v, want [m1 m3]", got)
	}
	// 编造序号 → 整条作废
	if got := resolveEvidenceIDs([]int{1, 99}, msgs); got != nil {
		t.Errorf("fabricated evidence must void the memory, got %v", got)
	}
	if got := resolveEvidenceIDs(nil, msgs); got != nil {
		t.Errorf("no evidence should return nil, got %v", got)
	}
}
