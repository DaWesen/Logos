package dao

import (
	"strings"
	"testing"

	"Logos/internal/service/ai/vector/model"
)

func TestCollectionEmbedder(t *testing.T) {
	tests := []struct {
		name        string
		collection  *model.VectorCollection
		wantNil     bool
		wantErrText string
	}{
		{
			name:       "集合为空",
			collection: nil,
			wantNil:    true,
		},
		{
			name:       "未配置嵌入模型时使用全局模型",
			collection: &model.VectorCollection{ID: "col_1"},
			wantNil:    true,
		},
		{
			name: "只有模型没有密钥",
			collection: &model.VectorCollection{
				ID:        "col_1",
				Embedding: model.ModelConfig{Model: "text-embedding-3-small"},
			},
			wantErrText: "配置不完整",
		},
		{
			name: "只有密钥没有模型",
			collection: &model.VectorCollection{
				ID:        "col_1",
				Embedding: model.ModelConfig{APIKey: "sk-test"},
			},
			wantErrText: "配置不完整",
		},
		{
			name: "模型与密钥齐全",
			collection: &model.VectorCollection{
				ID: "col_1",
				Embedding: model.ModelConfig{
					Model:   "text-embedding-3-small",
					BaseURL: "https://example.invalid/v1",
					APIKey:  "sk-test",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedder, err := collectionEmbedder(tt.collection)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("应返回错误，实际得到 embedder=%v", embedder)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("错误信息应包含 %q，实际 %v", tt.wantErrText, err)
				}
				if embedder != nil {
					t.Fatalf("配置非法时不应返回 embedder")
				}
				return
			}
			if err != nil {
				t.Fatalf("不应返回错误，实际 %v", err)
			}
			if tt.wantNil && embedder != nil {
				t.Fatalf("应回退全局模型，实际返回了自定义 embedder")
			}
			if !tt.wantNil && embedder == nil {
				t.Fatalf("应创建自定义 embedder，实际为 nil")
			}
		})
	}
}

func TestToFloat32Vectors(t *testing.T) {
	tests := []struct {
		name        string
		vecs        [][]float64
		want        int
		wantErrText string
		wantDim     int
	}{
		{
			name:    "正常转换",
			vecs:    [][]float64{{0.5, 0.25}, {1, 0}},
			want:    2,
			wantDim: 2,
		},
		{
			name:    "空批次",
			vecs:    [][]float64{},
			want:    0,
			wantDim: 0,
		},
		{
			name:        "条数少于输入",
			vecs:        [][]float64{{0.5}},
			want:        2,
			wantErrText: "条数",
		},
		{
			name:        "返回空向量",
			vecs:        [][]float64{{}},
			want:        1,
			wantErrText: "空向量",
		},
		{
			name:        "维度不一致",
			vecs:        [][]float64{{0.5, 0.25}, {0.5}},
			want:        2,
			wantErrText: "维度",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toFloat32Vectors(tt.vecs, tt.want)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("应返回错误，实际得到 %v", got)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("错误信息应包含 %q，实际 %v", tt.wantErrText, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应返回错误，实际 %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("向量条数应为 %d，实际 %d", tt.want, len(got))
			}
			for i, vec := range got {
				if len(vec) != tt.wantDim {
					t.Fatalf("第 %d 条向量维度应为 %d，实际 %d", i, tt.wantDim, len(vec))
				}
			}
		})
	}
}

func TestToFloat32VectorsPreservesValues(t *testing.T) {
	got, err := toFloat32Vectors([][]float64{{0.5, -0.25}}, 1)
	if err != nil {
		t.Fatalf("不应返回错误，实际 %v", err)
	}
	if got[0][0] != 0.5 || got[0][1] != -0.25 {
		t.Fatalf("数值转换结果不正确: %v", got[0])
	}
}
