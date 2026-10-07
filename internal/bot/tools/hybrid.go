package tools

import "sort"

// RRFK 是 Reciprocal Rank Fusion 的平滑常数，业界标准取 60
const RRFK = 60

// FuseSearchResults 用 RRF（倒数排名融合）融合多路检索结果。
//
// 每路结果按原始顺序视为一个排名列表，每个条目的融合得分为
// Σ 1/(k+rank)。同一 ID 出现在语义与关键词两路时得分累加——
// 这正是混合检索对"两种信号都认可"内容加权的方式。
// 返回按融合得分降序的前 topK 条，Source 标记为 hybrid。
func FuseSearchResults(lists [][]*KnowledgeSearchResult, topK int) []*KnowledgeSearchResult {
	scores := make(map[string]float64)
	byID := make(map[string]*KnowledgeSearchResult)

	for _, list := range lists {
		for rank, r := range list {
			if r == nil || r.ID == "" {
				continue
			}
			scores[r.ID] += 1.0 / float64(RRFK+rank+1)
			if _, ok := byID[r.ID]; !ok {
				byID[r.ID] = r
			}
		}
	}

	type scored struct {
		r     *KnowledgeSearchResult
		score float64
	}
	arr := make([]scored, 0, len(scores))
	for id, s := range scores {
		arr = append(arr, scored{byID[id], s})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].score > arr[j].score })

	if topK > 0 && len(arr) > topK {
		arr = arr[:topK]
	}

	out := make([]*KnowledgeSearchResult, 0, len(arr))
	for _, sc := range arr {
		sc.r.Score = sc.score
		sc.r.Source = "hybrid"
		out = append(out, sc.r)
	}
	return out
}
