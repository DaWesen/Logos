package tools

import "testing"

func TestFuseSearchResults_RRFBoostsBothSignals(t *testing.T) {
	// ID "a" 在语义路排第1、关键词路排第2 → 融合得分最高
	semantic := []*KnowledgeSearchResult{
		{ID: "a", Content: "语义第一"},
		{ID: "b", Content: "语义第二"},
		{ID: "c", Content: "语义第三"},
	}
	keyword := []*KnowledgeSearchResult{
		{ID: "x", Content: "关键词第一"},
		{ID: "a", Content: "关键词第二"},
	}

	merged := FuseSearchResults([][]*KnowledgeSearchResult{semantic, keyword}, 10)
	if len(merged) != 4 {
		t.Fatalf("expected 4 merged results, got %d", len(merged))
	}

	// 两路都出现的 "a" 应排第一
	if merged[0].ID != "a" {
		t.Errorf("RRF should rank result present in both lists first, got %q", merged[0].ID)
	}
	// 融合得分验证：a = 1/(60+1) + 1/(60+2)；x = 1/(60+1)
	wantA := 1.0/61 + 1.0/62
	if merged[0].Score-1.0/61 < 1e-9 {
		t.Errorf("score for 'a' looks like single-list score, want RRF sum ~%f, got %f", wantA, merged[0].Score)
	}
	if merged[0].Source != "hybrid" {
		t.Errorf("merged source should be hybrid, got %q", merged[0].Source)
	}
}

func TestFuseSearchResults_RespectsTopK(t *testing.T) {
	list := []*KnowledgeSearchResult{}
	for i := 0; i < 30; i++ {
		list = append(list, &KnowledgeSearchResult{ID: string(rune('a' + i))})
	}
	merged := FuseSearchResults([][]*KnowledgeSearchResult{list}, 5)
	if len(merged) != 5 {
		t.Fatalf("expected top 5, got %d", len(merged))
	}
}

func TestFuseSearchResults_EmptyInputs(t *testing.T) {
	if got := FuseSearchResults(nil, 10); len(got) != 0 {
		t.Errorf("nil input should return empty, got %d", len(got))
	}
	if got := FuseSearchResults([][]*KnowledgeSearchResult{{}}, 10); len(got) != 0 {
		t.Errorf("empty list input should return empty, got %d", len(got))
	}
	// 含 nil 条目与空 ID 的容错
	bad := []*KnowledgeSearchResult{nil, {ID: ""}, {ID: "ok"}}
	got := FuseSearchResults([][]*KnowledgeSearchResult{bad}, 10)
	if len(got) != 1 || got[0].ID != "ok" {
		t.Errorf("should skip nil/empty-ID entries, got %v", got)
	}
}

func TestFuseSearchResults_SingleListPreservesOrder(t *testing.T) {
	list := []*KnowledgeSearchResult{{ID: "1"}, {ID: "2"}, {ID: "3"}}
	merged := FuseSearchResults([][]*KnowledgeSearchResult{list}, 10)
	for i, r := range merged {
		want := string(rune('1' + i))
		if r.ID != want {
			t.Errorf("position %d: got %q, want %q (order should be preserved for single list)", i, r.ID, want)
		}
	}
}
