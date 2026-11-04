package tools

import (
	"testing"
)

func docResult(id, doc string, source string) *KnowledgeSearchResult {
	r := &KnowledgeSearchResult{ID: id, Content: "内容" + id, Source: source}
	if doc != "" {
		r.Metadata = map[string]string{"document_id": doc}
	}
	return r
}

// TestLimitPerDocument_CapsAndPreservesOrder 单文档最多保留 N 条，顺序不变。
func TestLimitPerDocument_CapsAndPreservesOrder(t *testing.T) {
	results := []*KnowledgeSearchResult{
		docResult("a1", "docA", "s"),
		docResult("a2", "docA", "s"),
		docResult("b1", "docB", "s"),
		docResult("a3", "docA", "s"),
		docResult("a4", "docA", "s"),
		docResult("b2", "docB", "s"),
	}

	got := limitPerDocument(results, 2)
	want := []string{"a1", "a2", "b1", "b2"}
	if len(got) != len(want) {
		t.Fatalf("条数 %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("第 %d 条 = %s, want %s", i, got[i].ID, id)
		}
	}
}

// TestLimitPerDocument_FallsBackToSource metadata 缺失时用 Source 归属。
func TestLimitPerDocument_FallsBackToSource(t *testing.T) {
	results := []*KnowledgeSearchResult{
		docResult("x1", "", "同一文件"),
		docResult("x2", "", "同一文件"),
		docResult("x3", "", "同一文件"),
		docResult("y1", "", "另一文件"),
	}

	got := limitPerDocument(results, 2)
	if len(got) != 3 {
		t.Fatalf("条数 %d, want 3", len(got))
	}
}

// TestLimitPerDocument_UnknownSourceKept 无法归属的结果不参与限流。
func TestLimitPerDocument_UnknownSourceKept(t *testing.T) {
	results := []*KnowledgeSearchResult{
		{ID: "u1"},
		{ID: "u2"},
		{ID: "u3"},
		{ID: "u4"},
	}

	got := limitPerDocument(results, 1)
	if len(got) != 4 {
		t.Fatalf("无来源结果不应被限流, got %d", len(got))
	}
}

// TestLimitPerDocument_NilAndZeroBudget nil 元素跳过；非正预算视为不限。
func TestLimitPerDocument_NilAndZeroBudget(t *testing.T) {
	results := []*KnowledgeSearchResult{nil, docResult("a1", "docA", ""), nil}
	if got := limitPerDocument(results, 0); len(got) != 3 {
		t.Fatalf("预算为 0 时应原样返回, got %d", len(got))
	}
	if got := limitPerDocument(results, 3); len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("nil 应被剔除, got %v", got)
	}
}
