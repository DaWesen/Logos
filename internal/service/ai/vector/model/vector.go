package model

import (
	"time"
)

// 向量模型类型
type VectorModelType int

const (
	BERT         VectorModelType = 1
	Word2Vec     VectorModelType = 2
	GloVe        VectorModelType = 3
	FastText     VectorModelType = 4
	SentenceBERT VectorModelType = 5
	Custom       VectorModelType = 6
)

// 向量索引类型
type IndexType int

const (
	FLAT     IndexType = 1
	IVF_FLAT IndexType = 2
	IVF_PQ   IndexType = 3
	HNSW     IndexType = 4
)

// 向量
type Vector struct {
	ID        string            `json:"id"`
	Values    []float64         `json:"values"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt time.Time         `json:"created_at"`
}

// 向量集合
type VectorCollection struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	ModelType  VectorModelType   `json:"model_type"`
	IndexType  IndexType         `json:"index_type"`
	Dimension  int               `json:"dimension"`
	Parameters map[string]string `json:"parameters"`
	Size       int64             `json:"size"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// 搜索结果项
type SearchResultItem struct {
	VectorID string            `json:"vector_id"`
	Score    float64           `json:"score"`
	Metadata map[string]string `json:"metadata"`
	Content  string            `json:"content"` // RAG 搜索时的内容
}

// GetID 实现 GetID 方法
func (v *Vector) GetID() string {
	return v.ID
}

// SearchResultItem 的 Getter 方法
func (s *SearchResultItem) GetID() string {
	return s.VectorID
}

func (s *SearchResultItem) GetVectorID() string {
	return s.VectorID
}

func (s *SearchResultItem) GetScore() float64 {
	return s.Score
}

func (s *SearchResultItem) GetMetadata() map[string]string {
	return s.Metadata
}

func (s *SearchResultItem) GetContent() string {
	return s.Content
}
