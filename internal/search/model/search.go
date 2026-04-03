package model

import (
	"time"
)

// 搜索索引类型
type IndexType int

const (
	ENTITY   IndexType = 1
	RELATION IndexType = 2
	QUESTION IndexType = 3
	ANSWER   IndexType = 4
	DOCUMENT IndexType = 5
	USER     IndexType = 6
)

// 搜索文档
type SearchDocument struct {
	ID        string                 `json:"id"`
	Type      IndexType              `json:"type"`
	Title     string                 `json:"title"`
	Content   string                 `json:"content"`
	Metadata  map[string]string      `json:"metadata"`
	Fields    map[string]interface{} `json:"fields"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// 搜索结果项
type SearchResultItem struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Title    string                 `json:"title"`
	Content  string                 `json:"content"`
	Score    float64                `json:"score"`
	Metadata map[string]string      `json:"metadata"`
	Fields   map[string]interface{} `json:"fields"`
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

// IndexSettings 索引设置
type IndexSettings struct {
	NumberOfShards   int `json:"number_of_shards"`
	NumberOfReplicas int `json:"number_of_replicas"`
}

// PropertyMapping 属性映射
type PropertyMapping struct {
	Type     string                 `json:"type"`
	Analyzer string                 `json:"analyzer,omitempty"`
	Format   string                 `json:"format,omitempty"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
}

// PropertiesMapping 属性映射集合
type PropertiesMapping struct {
	Properties map[string]PropertyMapping `json:"properties"`
}

// IndexMapping 完整索引映射
type IndexMapping struct {
	Settings IndexSettings     `json:"settings"`
	Mappings PropertiesMapping `json:"mappings"`
}

// GenerateEntityMapping 生成实体索引映射
func GenerateEntityMapping() IndexMapping {
	return IndexMapping{
		Settings: IndexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
		},
		Mappings: PropertiesMapping{
			Properties: map[string]PropertyMapping{
				"id": {
					Type: "keyword",
				},
				"name": {
					Type:     "text",
					Analyzer: "ik_max_word",
					Fields: map[string]interface{}{
						"keyword": map[string]interface{}{
							"type":         "keyword",
							"ignore_above": 256,
						},
					},
				},
				"type": {
					Type: "keyword",
				},
				"description": {
					Type:     "text",
					Analyzer: "ik_max_word",
				},
				"aliases": {
					Type:     "text",
					Analyzer: "ik_max_word",
				},
				"attributes": {
					Type: "object",
				},
				"created_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
				"updated_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
			},
		},
	}
}

// GenerateRelationMapping 生成关系索引映射
func GenerateRelationMapping() IndexMapping {
	return IndexMapping{
		Settings: IndexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
		},
		Mappings: PropertiesMapping{
			Properties: map[string]PropertyMapping{
				"id": {
					Type: "keyword",
				},
				"source_id": {
					Type: "keyword",
				},
				"target_id": {
					Type: "keyword",
				},
				"type": {
					Type: "keyword",
				},
				"properties": {
					Type: "object",
				},
				"created_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
				"updated_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
			},
		},
	}
}

// GenerateQuestionMapping 生成问题索引映射
func GenerateQuestionMapping() IndexMapping {
	return IndexMapping{
		Settings: IndexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
		},
		Mappings: PropertiesMapping{
			Properties: map[string]PropertyMapping{
				"id": {
					Type: "keyword",
				},
				"user_id": {
					Type: "long",
				},
				"content": {
					Type:     "text",
					Analyzer: "ik_max_word",
				},
				"context": {
					Type: "object",
				},
				"created_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
			},
		},
	}
}

// GenerateAnswerMapping 生成答案索引映射
func GenerateAnswerMapping() IndexMapping {
	return IndexMapping{
		Settings: IndexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
		},
		Mappings: PropertiesMapping{
			Properties: map[string]PropertyMapping{
				"id": {
					Type: "keyword",
				},
				"question_id": {
					Type: "keyword",
				},
				"content": {
					Type:     "text",
					Analyzer: "ik_max_word",
				},
				"confidence": {
					Type: "double",
				},
				"sources": {
					Type: "keyword",
				},
				"created_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
			},
		},
	}
}

// GenerateDocumentMapping 生成文档索引映射
func GenerateDocumentMapping() IndexMapping {
	return IndexMapping{
		Settings: IndexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
		},
		Mappings: PropertiesMapping{
			Properties: map[string]PropertyMapping{
				"id": {
					Type: "keyword",
				},
				"title": {
					Type:     "text",
					Analyzer: "ik_max_word",
					Fields: map[string]interface{}{
						"keyword": map[string]interface{}{
							"type":         "keyword",
							"ignore_above": 256,
						},
					},
				},
				"content": {
					Type:     "text",
					Analyzer: "ik_max_word",
				},
				"type": {
					Type: "keyword",
				},
				"tags": {
					Type: "keyword",
				},
				"author": {
					Type: "keyword",
				},
				"created_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
				"updated_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
			},
		},
	}
}

// GenerateUserMapping 生成用户索引映射
func GenerateUserMapping() IndexMapping {
	return IndexMapping{
		Settings: IndexSettings{
			NumberOfShards:   1,
			NumberOfReplicas: 0,
		},
		Mappings: PropertiesMapping{
			Properties: map[string]PropertyMapping{
				"id": {
					Type: "long",
				},
				"username": {
					Type:     "text",
					Analyzer: "ik_max_word",
					Fields: map[string]interface{}{
						"keyword": map[string]interface{}{
							"type":         "keyword",
							"ignore_above": 256,
						},
					},
				},
				"avatar": {
					Type: "keyword",
				},
				"about": {
					Type:     "text",
					Analyzer: "ik_max_word",
				},
				"created_at": {
					Type:   "date",
					Format: "yyyy-MM-dd HH:mm:ss",
				},
			},
		},
	}
}
