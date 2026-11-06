package scache

import "math"

// CosineSimilarity 计算余弦相似度。
//
// 维度不一致直接返回 0：宁可 miss，也不拿两套 embedding 的向量硬比
// （D5 的第二道防线在 Entry.Valid，这里是最后一道）。
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
