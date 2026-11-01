package tools

import (
	"reflect"
	"testing"
)

func ids(results []*KnowledgeSearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.ID)
	}
	return out
}

// TestMergeKeywordResults_RoundRobin 交错合并：每个 pattern 的首条都要有位置。
func TestMergeKeywordResults_RoundRobin(t *testing.T) {
	lists := [][]*KnowledgeSearchResult{
		{{ID: "a1"}, {ID: "a2"}, {ID: "a3"}},
		{{ID: "b1"}},
	}
	got := ids(mergeKeywordResults(lists, 4))
	want := []string{"a1", "b1", "a2", "a3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v（应轮转交错，而不是一路占满）", got, want)
	}
}

// TestMergeKeywordResults_DedupeKeepsFirst 同一 ID 只保留首次出现，且首份的元数据不被覆盖。
func TestMergeKeywordResults_DedupeKeepsFirst(t *testing.T) {
	first := &KnowledgeSearchResult{ID: "dup", Content: "来自 pattern1"}
	lists := [][]*KnowledgeSearchResult{
		{first},
		{{ID: "dup", Content: "来自 pattern2"}},
	}
	got := mergeKeywordResults(lists, 10)
	if len(got) != 1 {
		t.Fatalf("dedupe failed: %v", ids(got))
	}
	if got[0] != first || got[0].Content != "来自 pattern1" {
		t.Fatalf("应保留首次出现的那份，got %+v", got[0])
	}
}

// TestMergeKeywordResults_LimitCapsTotal 总量按 limit 截断（否则 3 路 × 30 条会撑爆 prompt）。
func TestMergeKeywordResults_LimitCapsTotal(t *testing.T) {
	var list []*KnowledgeSearchResult
	for i := 0; i < 30; i++ {
		list = append(list, &KnowledgeSearchResult{ID: string(rune('a' + i))})
	}
	lists := [][]*KnowledgeSearchResult{list, list, list}

	if got := mergeKeywordResults(lists, 30); len(got) != 30 {
		t.Fatalf("三路重复 30 条去重后应剩 30 条，got %d", len(got))
	}
	if got := mergeKeywordResults(lists, 5); len(got) != 5 {
		t.Fatalf("limit=5 应只返回 5 条，got %d", len(got))
	}
	if got := mergeKeywordResults(lists, 0); len(got) != 0 {
		t.Fatalf("limit<=0 应返回空，got %d", len(got))
	}
}

// TestMergeKeywordResults_SkipsNilAndEmpty 容错：nil 条目与空 ID 直接跳过。
func TestMergeKeywordResults_SkipsNilAndEmpty(t *testing.T) {
	lists := [][]*KnowledgeSearchResult{
		{nil, {ID: ""}, {ID: "ok"}},
	}
	got := mergeKeywordResults(lists, 10)
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("got %v", ids(got))
	}
}

// TestMergeKeywordResults_SetsSource 合并后的条目来源统一标为 keyword（覆盖原始来源，
// 避免前端/模型把关键词结果误认成向量检索）。
func TestMergeKeywordResults_SetsSource(t *testing.T) {
	lists := [][]*KnowledgeSearchResult{{{ID: "a", Source: "vector"}}}
	got := mergeKeywordResults(lists, 10)
	if len(got) != 1 || got[0].Source != "keyword" {
		t.Fatalf("got %+v", got[0])
	}
}

// TestRetrievalFailure 只有「全部路都失败」才算故障；部分成功时空结果是正常业务返回。
func TestRetrievalFailure(t *testing.T) {
	if err := retrievalFailure(2, 0); err == nil {
		t.Fatal("全部失败必须返回 error，不能被当成「没有内容」")
	}
	if err := retrievalFailure(1, 1); err != nil {
		t.Fatalf("部分成功不应报错: %v", err)
	}
	if err := retrievalFailure(0, 2); err != nil {
		t.Fatalf("全部成功不应报错: %v", err)
	}
	if err := retrievalFailure(0, 0); err != nil {
		t.Fatalf("没有任何路（空查询）不应报错: %v", err)
	}
}

// TestFuseSearchResults_DeterministicTieBreak 同分结果顺序必须稳定：
// 旧实现由 map 遍历 + 不稳定排序决定，同一输入多次调用顺序会抖。
func TestFuseSearchResults_DeterministicTieBreak(t *testing.T) {
	semantic := []*KnowledgeSearchResult{{ID: "b"}}
	keyword := []*KnowledgeSearchResult{{ID: "a"}}

	want := []string{"a", "b"} // 同分按 ID 升序
	for i := 0; i < 20; i++ {
		merged := FuseSearchResults([][]*KnowledgeSearchResult{semantic, keyword}, 10)
		if got := ids(merged); !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 次调用顺序不稳定: %v, want %v", i+1, got, want)
		}
	}
}
