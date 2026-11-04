package memory

import (
	"testing"
)

func TestNormalizeMemoryKey(t *testing.T) {
	cases := map[string]string{
		"  喜欢喝咖啡  ": "喜欢喝咖啡",
		"喜欢 喝\t咖啡":  "喜欢 喝 咖啡",
		"喜欢喝咖啡。":    "喜欢喝咖啡",
		"喜欢喝咖啡！！":   "喜欢喝咖啡",
		":喜欢喝咖啡;":   "喜欢喝咖啡",
	}

	for in, want := range cases {
		if got := normalizeMemoryKey(in); got != want {
			t.Fatalf("normalizeMemoryKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPrepareExtractedMemory 归一化 + 批次内去重 + 默认分类。
func TestPrepareExtractedMemory(t *testing.T) {
	seen := map[string]struct{}{}

	first := &ExtractedMemory{Key: " 喜欢喝咖啡。", Value: " 每天一杯 "}
	if !prepareExtractedMemory(first, seen) {
		t.Fatal("首条应通过")
	}
	if first.Key != "喜欢喝咖啡" || first.Value != "每天一杯" {
		t.Fatalf("字段未归一化: %+v", first)
	}
	if first.Category != "fact" {
		t.Fatalf("空分类应默认为 fact, got %q", first.Category)
	}

	// 同一批次的格式变体必须被识别为重复
	dup := &ExtractedMemory{Key: "喜欢喝咖啡", Value: "另一句"}
	if prepareExtractedMemory(dup, seen) {
		t.Fatal("归一化后同键应判重")
	}

	empty := &ExtractedMemory{Key: "   ", Value: "x"}
	if prepareExtractedMemory(empty, seen) {
		t.Fatal("空键应丢弃")
	}

	emptyVal := &ExtractedMemory{Key: "新键", Value: "   "}
	if prepareExtractedMemory(emptyVal, seen) {
		t.Fatal("空值应丢弃")
	}

	if prepareExtractedMemory(nil, seen) {
		t.Fatal("nil 应丢弃")
	}
}

func TestPrepareExtractedMemory_KeepsExplicitCategory(t *testing.T) {
	seen := map[string]struct{}{}
	mem := &ExtractedMemory{Key: "住在上海", Value: "徐汇", Category: "location"}
	if !prepareExtractedMemory(mem, seen) {
		t.Fatal("应通过")
	}
	if mem.Category != "location" {
		t.Fatalf("不应覆盖显式分类，got %q", mem.Category)
	}
}
