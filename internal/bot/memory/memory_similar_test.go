package memory

import (
	"testing"
	"time"

	botmodel "Logos/internal/service/ai/bot/model"
)

func TestMemoryKeySimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		min  float64
		max  float64
	}{
		{"喜欢喝咖啡", "喜欢喝咖啡", 0.99, 1.01},
		{"喜欢喝咖啡", "喜欢喝咖啡呢", 0.79, 0.81}, // 多一个语气词 → 0.8，可合并
		{"喜欢喝咖啡", "喜欢咖啡豆", 0.30, 0.35},  // 不同事实，不能合并
		{"咖啡", "咖啡", 0.99, 1.01},
		{"", "咖啡", 0, 0.01},
	}
	for _, c := range cases {
		got := memoryKeySimilarity(c.a, c.b)
		if got < c.min || got > c.max {
			t.Fatalf("similarity(%q,%q)=%.3f, want [%.2f,%.2f]", c.a, c.b, got, c.min, c.max)
		}
	}
}

func TestFindSimilarMemoryIndex_Boundary(t *testing.T) {
	mems := []*botmodel.UserMemory{
		{ID: "a", Key: "喜欢喝咖啡", Category: "preference", Confidence: 0.9, UpdatedAt: time.Now()},
	}

	// 相似度不足（0.4）：不同事实不应合并
	if idx := findSimilarMemoryIndex(mems, "喜欢咖啡豆", "preference"); idx != -1 {
		t.Fatalf("不同事实被合并, idx=%d", idx)
	}
	// 分类不同不合并
	if idx := findSimilarMemoryIndex(mems, "喜欢喝咖啡呢", "fact"); idx != -1 {
		t.Fatalf("跨分类被合并, idx=%d", idx)
	}
	// 同分类、近乎同写（0.8）→ 命中
	if idx := findSimilarMemoryIndex(mems, "喜欢喝咖啡呢", "preference"); idx != 0 {
		t.Fatalf("近乎同写未命中, idx=%d", idx)
	}
}

func TestFindSimilarMemoryIndex_PicksBestDeterministically(t *testing.T) {
	now := time.Now()
	mems := []*botmodel.UserMemory{
		nil,
		{ID: "low", Key: "喜欢喝咖啡", Category: "preference", Confidence: 0.5, UpdatedAt: now.Add(-time.Hour)},
		{ID: "high", Key: "喜欢喝咖啡", Category: "preference", Confidence: 0.9, UpdatedAt: now},
	}

	for i := 0; i < 5; i++ {
		// 两条与候选键的相似度相同（0.8），应稳定选中置信度更高的一条
		if idx := findSimilarMemoryIndex(mems, "喜欢喝咖啡呢", "preference"); idx != 2 {
			t.Fatalf("应选置信度更高的那条, idx=%d", idx)
		}
	}
}
