package service

import (
	"context"
	"errors"
	"math"
	"testing"

	botmodel "Logos/internal/service/ai/bot/model"
)

func TestCosineSimilarity_IdenticalVectors(t *testing.T) {
	a := []float32{1, 2, 3}
	if got := cosineSimilarity(a, a); math.Abs(got-1) > 1e-6 {
		t.Errorf("identical vectors should have similarity 1, got %f", got)
	}
}

func TestCosineSimilarity_ScaleInvariant(t *testing.T) {
	a := []float32{0.1, 0.2, 0.3}
	b := []float32{10, 20, 30} // 同方向、不同模长
	if got := cosineSimilarity(a, b); math.Abs(got-1) > 1e-5 {
		t.Errorf("cosine similarity should be scale-invariant, got %f", got)
	}
}

func TestCosineSimilarity_OrthogonalVectors(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	if got := cosineSimilarity(a, b); math.Abs(got) > 1e-6 {
		t.Errorf("orthogonal vectors should have similarity 0, got %f", got)
	}
}

func TestCosineSimilarity_OppositeVectors(t *testing.T) {
	a := []float32{1, 1}
	b := []float32{-1, -1}
	if got := cosineSimilarity(a, b); math.Abs(got+1) > 1e-6 {
		t.Errorf("opposite vectors should have similarity -1, got %f", got)
	}
}

func TestCosineSimilarity_InvalidInputs(t *testing.T) {
	if got := cosineSimilarity(nil, nil); got != 0 {
		t.Errorf("nil vectors should return 0, got %f", got)
	}
	if got := cosineSimilarity([]float32{1, 2}, []float32{1}); got != 0 {
		t.Errorf("length-mismatched vectors should return 0, got %f", got)
	}
	if got := cosineSimilarity([]float32{0, 0}, []float32{0, 0}); got != 0 {
		t.Errorf("zero vectors should return 0, got %f", got)
	}
}

func TestSemanticCache_NilReceiverIsNoop(t *testing.T) {
	// SemanticCache 为 nil（如 cfg 缺失时）时 Lookup/Store 必须直通不 panic
	var c *SemanticCache
	if _, ok := c.Lookup(t.Context(), nil, "u1", "query"); ok {
		t.Error("nil cache should always miss")
	}
	c.Store(t.Context(), nil, "u1", "query", "answer") // 不应 panic
}

func TestSemanticCache_EmbedFailureMeansMiss(t *testing.T) {
	// embedding 不可用时缓存直通（无 Redis 依赖）
	c := NewSemanticCache("127.0.0.1:1", "", 0, func(ctx context.Context, bot *botmodel.Bot, text string) ([]float32, error) {
		return nil, errors.New("embedding unavailable")
	})
	if _, ok := c.Lookup(t.Context(), nil, "u1", "你好"); ok {
		t.Error("embed failure should result in cache miss")
	}
}

func TestToFloat32(t *testing.T) {
	got := toFloat32([]float64{1.5, 2.5})
	if len(got) != 2 || got[0] != 1.5 || got[1] != 2.5 {
		t.Errorf("toFloat32 conversion wrong: %v", got)
	}
}

func TestEstimateTokenCount(t *testing.T) {
	tests := []struct {
		name string
		text string
		min  int // 下界（含 +50 基础开销）
		max  int // 上界
	}{
		{"纯英文400字符", repeatRunes('a', 400), 100 + 50, 110 + 50},
		{"纯中文100字", repeatRunes('字', 100), 55 + 50, 65 + 50},
		{"空文本", "", 50, 51},
	}
	for _, tt := range tests {
		got := estimateTokenCount(tt.text)
		if got < tt.min || got > tt.max {
			t.Errorf("%s: estimateTokenCount = %d, want [%d, %d]", tt.name, got, tt.min, tt.max)
		}
	}

	// 修复前 bug：纯英文被全部按中文系数(0.6)计费，高估 ~2.4 倍
	ascii := repeatRunes('a', 400)
	if got := estimateTokenCount(ascii); got > 160 {
		t.Errorf("pure ASCII should be ~0.25 token/char, got %d for 400 chars", got)
	}
}

func repeatRunes(r rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}
