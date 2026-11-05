package service

import (
	"context"
	"strings"
	"testing"

	"Logos/internal/service/ai/vector/dao"
	"Logos/internal/service/ai/vector/model"
)

// stubVectorRepository 只实现更新用例需要的两个方法。
// 其余方法由内嵌接口提供：一旦用例走到不该走的分支会直接 panic。
type stubVectorRepository struct {
	dao.VectorRepository

	existing    *model.VectorCollection
	updated     *model.VectorCollection
	updateCalls int
}

func (s *stubVectorRepository) GetCollection(context.Context, string, string) (*model.VectorCollection, error) {
	return s.existing, nil
}

func (s *stubVectorRepository) UpdateCollection(_ context.Context, collection *model.VectorCollection) error {
	s.updateCalls++
	s.updated = collection
	return nil
}

func TestUpdateCollectionRejectsDimensionChange(t *testing.T) {
	repo := &stubVectorRepository{
		existing: &model.VectorCollection{ID: "col-1", Dimension: 1024},
	}
	svc := NewVectorService(repo)

	_, err := svc.UpdateCollection(context.Background(), &model.VectorCollection{
		ID:        "col-1",
		Dimension: 768,
	})
	if err == nil {
		t.Fatal("修改维度应被拒绝")
	}
	if !strings.Contains(err.Error(), "维度") {
		t.Fatalf("错误信息应说明维度不可修改，实际 %v", err)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("拒绝后不应调用仓储层，实际调用 %d 次", repo.updateCalls)
	}
}

func TestUpdateCollectionAcceptsUnchangedDimension(t *testing.T) {
	repo := &stubVectorRepository{
		existing: &model.VectorCollection{ID: "col-1", Dimension: 1024},
	}
	svc := NewVectorService(repo)

	if _, err := svc.UpdateCollection(context.Background(), &model.VectorCollection{
		ID:        "col-1",
		Dimension: 1024,
	}); err != nil {
		t.Fatalf("维度未变化时不应拒绝: %v", err)
	}
	if repo.updateCalls != 1 {
		t.Fatalf("仓储层应被调用 1 次，实际 %d 次", repo.updateCalls)
	}
}

func TestUpdateCollectionMergesModelConfigPerField(t *testing.T) {
	repo := &stubVectorRepository{
		existing: &model.VectorCollection{
			ID:        "col-1",
			Dimension: 1024,
			Embedding: model.ModelConfig{
				Model:   "bge-m3",
				BaseURL: "http://embedding.old",
				APIKey:  "sk-old",
			},
			Parameters: map[string]string{"M": "16"},
			UserID:     "user-1",
		},
	}
	svc := NewVectorService(repo)

	got, err := svc.UpdateCollection(context.Background(), &model.VectorCollection{
		ID: "col-1",
		Embedding: model.ModelConfig{
			BaseURL: "http://embedding.new",
		},
	})
	if err != nil {
		t.Fatalf("不应返回错误: %v", err)
	}
	if got.Embedding.Model != "bge-m3" || got.Embedding.APIKey != "sk-old" {
		t.Fatalf("只改 BaseURL 不应清空模型或密钥，实际 %+v", got.Embedding)
	}
	if got.Embedding.BaseURL != "http://embedding.new" {
		t.Fatalf("BaseURL 应被更新，实际 %s", got.Embedding.BaseURL)
	}
	if got.Parameters["M"] != "16" {
		t.Fatalf("未传参数时应沿用已有索引参数，实际 %v", got.Parameters)
	}
	if got.UserID != "user-1" {
		t.Fatalf("应沿用已有归属，实际 %q", got.UserID)
	}
}
