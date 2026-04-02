package model

import (
	"time"
)

// 搜索索引类型
type IndexType int

const (
	ENTITY    IndexType = 1
	RELATION  IndexType = 2
	QUESTION  IndexType = 3
	ANSWER    IndexType = 4
	DOCUMENT  IndexType = 5
	USER      IndexType = 6
)

// 搜索文档
type SearchDocument struct {
	ID        string            `json:"id"`
	Type      IndexType         `json:"type"`
	Title     string            `json:"title"`
	Content   string            `json:"content"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// 搜索结果项
type SearchResultItem struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	Score    float64           `json:"score"`
	Metadata map[string]string `json:"metadata"`
}

// 索引统计
type IndexStats struct {
	Type          IndexType `json:"type"`
	DocumentCount int64     `json:"document_count"`
	SizeInBytes   int64     `json:"size_in_bytes"`
	LastUpdated   string    `json:"last_updated"`
}

// 搜索条件
type SearchCondition struct {
	Field    string `json:"field"`
	Value    string `json:"value"`
	Operator string `json:"operator"`
}

// 排序条件
type SortCondition struct {
	Field string `json:"field"`
	Order string `json:"order"`
}
