package dao

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/ai/search/model"
	"Logos/pkg/es"
	"Logos/pkg/logger"
)

type SearchRepository interface {
	// 搜索
	Search(ctx context.Context, query string, indexType model.IndexType, page, pageSize int, conditions []model.SearchCondition, sorts []model.SortCondition, filters map[string]string) ([]*model.SearchResultItem, int64, error)

	// 文档管理
	AddDocument(ctx context.Context, doc *model.SearchDocument) error
	UpdateDocument(ctx context.Context, doc *model.SearchDocument) error
	DeleteDocument(ctx context.Context, id string) error
	GetDocument(ctx context.Context, id string) (*model.SearchDocument, error)

	// 批量操作
	BatchAddDocuments(ctx context.Context, docs []*model.SearchDocument) error
	BatchDeleteDocuments(ctx context.Context, ids []string) error

	// 索引管理
	CreateIndex(ctx context.Context, indexType model.IndexType) error
	DeleteIndex(ctx context.Context, indexType model.IndexType) error
	RefreshIndex(ctx context.Context, indexType model.IndexType) error
	GetIndexStats(ctx context.Context) ([]*model.IndexStats, error)
}

type searchRepository struct {
	esManager *es.ESManager
}

func NewSearchRepository(esManager *es.ESManager) SearchRepository {
	return &searchRepository{
		esManager: esManager,
	}
}

func (r *searchRepository) getIndexName(indexType model.IndexType) string {
	switch indexType {
	case model.ENTITY:
		return "entities"
	case model.RELATION:
		return "relations"
	case model.QUESTION:
		return "questions"
	case model.ANSWER:
		return "answers"
	case model.DOCUMENT:
		return "documents"
	case model.USER:
		return "users"
	default:
		return "documents"
	}
}

func (r *searchRepository) Search(ctx context.Context, query string, indexType model.IndexType, page, pageSize int, conditions []model.SearchCondition, sorts []model.SortCondition, filters map[string]string) ([]*model.SearchResultItem, int64, error) {
	if r.esManager == nil {
		return nil, 0, fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexName := r.getIndexName(indexType)

	esQuery := map[string]interface{}{
		"multi_match": map[string]interface{}{
			"query":    query,
			"fields":   []string{"title", "content"},
			"type":     "best_fields",
			"operator": "and",
		},
	}

	searchQuery := es.SearchQuery{
		Query: esQuery,
		From:  (page - 1) * pageSize,
		Size:  pageSize,
	}

	if len(sorts) > 0 {
		esSorts := make([]map[string]interface{}, 0, len(sorts))
		for _, sort := range sorts {
			esSorts = append(esSorts, map[string]interface{}{
				sort.Field: map[string]interface{}{
					"order": sort.Order,
				},
			})
		}
		searchQuery.Sort = esSorts
	}

	var result es.SearchResult
	if err := r.esManager.Search(indexName, searchQuery, &result); err != nil {
		logger.Error("搜索失败",
			logger.StringField("index", indexName),
			logger.StringField("query", query),
			logger.ErrorField(err))
		return nil, 0, err
	}

	items := make([]*model.SearchResultItem, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var doc map[string]interface{}
		if err := json.Unmarshal(hit.Source, &doc); err != nil {
			continue
		}

		item := &model.SearchResultItem{
			ID:     hit.ID,
			Score:  hit.Score,
			Fields: make(map[string]interface{}),
		}

		// 提取通用字段
		if title, ok := doc["title"].(string); ok {
			item.Title = title
		}
		if content, ok := doc["content"].(string); ok {
			item.Content = content
		}
		if metadata, ok := doc["metadata"].(map[string]interface{}); ok {
			item.Metadata = make(map[string]string)
			for k, v := range metadata {
				if strV, ok := v.(string); ok {
					item.Metadata[k] = strV
				}
			}
		}

		// 提取所有其他字段到 Fields
		commonFields := map[string]bool{
			"id":         true,
			"title":      true,
			"content":    true,
			"metadata":   true,
			"created_at": true,
			"updated_at": true,
		}
		for k, v := range doc {
			if !commonFields[k] {
				item.Fields[k] = v
			}
		}

		items = append(items, item)
	}

	return items, result.Hits.Total.Value, nil
}

func (r *searchRepository) AddDocument(ctx context.Context, doc *model.SearchDocument) error {
	if r.esManager == nil {
		return fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexName := r.getIndexName(doc.Type)
	if doc.ID == "" {
		doc.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = time.Now()
	}
	doc.UpdatedAt = time.Now()

	// 创建合并的文档结构
	mergedDoc := make(map[string]interface{})
	mergedDoc["id"] = doc.ID
	mergedDoc["type"] = doc.Type
	mergedDoc["title"] = doc.Title
	mergedDoc["content"] = doc.Content
	mergedDoc["metadata"] = doc.Metadata
	mergedDoc["created_at"] = doc.CreatedAt
	mergedDoc["updated_at"] = doc.UpdatedAt

	// 合并 Fields 到根级别
	for k, v := range doc.Fields {
		mergedDoc[k] = v
	}

	if err := r.esManager.AddDocument(indexName, doc.ID, mergedDoc); err != nil {
		logger.Error("添加文档失败",
			logger.StringField("id", doc.ID),
			logger.StringField("index", indexName),
			logger.ErrorField(err))
		return err
	}

	return nil
}

func (r *searchRepository) UpdateDocument(ctx context.Context, doc *model.SearchDocument) error {
	if r.esManager == nil {
		return fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexName := r.getIndexName(doc.Type)
	doc.UpdatedAt = time.Now()

	// 创建合并的文档结构
	mergedDoc := make(map[string]interface{})
	mergedDoc["id"] = doc.ID
	if doc.Title != "" {
		mergedDoc["title"] = doc.Title
	}
	if doc.Content != "" {
		mergedDoc["content"] = doc.Content
	}
	if doc.Metadata != nil {
		mergedDoc["metadata"] = doc.Metadata
	}
	mergedDoc["updated_at"] = doc.UpdatedAt

	// 合并 Fields 到根级别
	for k, v := range doc.Fields {
		mergedDoc[k] = v
	}

	if err := r.esManager.UpdateDocument(indexName, doc.ID, mergedDoc); err != nil {
		logger.Error("更新文档失败",
			logger.StringField("id", doc.ID),
			logger.StringField("index", indexName),
			logger.ErrorField(err))
		return err
	}

	return nil
}

func (r *searchRepository) DeleteDocument(ctx context.Context, id string) error {
	if r.esManager == nil {
		return fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexes := []string{"entities", "relations", "questions", "answers", "documents", "users"}

	for _, index := range indexes {
		err := r.esManager.DeleteDocument(index, id)
		if err == nil {
			return nil
		}
	}

	return fmt.Errorf("文档不存在: %s", id)
}

func (r *searchRepository) GetDocument(ctx context.Context, id string) (*model.SearchDocument, error) {
	if r.esManager == nil {
		return nil, fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexes := []string{"entities", "relations", "questions", "answers", "documents", "users"}

	for _, index := range indexes {
		source, err := r.esManager.GetDocument(index, id)
		if err == nil {
			var docMap map[string]interface{}
			if err := json.Unmarshal(source, &docMap); err != nil {
				continue
			}

			doc := &model.SearchDocument{
				Fields: make(map[string]interface{}),
			}

			// 提取通用字段
			if idVal, ok := docMap["id"].(string); ok {
				doc.ID = idVal
			}
			if typeVal, ok := docMap["type"].(float64); ok {
				doc.Type = model.IndexType(typeVal)
			}
			if title, ok := docMap["title"].(string); ok {
				doc.Title = title
			}
			if content, ok := docMap["content"].(string); ok {
				doc.Content = content
			}
			if metadata, ok := docMap["metadata"].(map[string]interface{}); ok {
				doc.Metadata = make(map[string]string)
				for k, v := range metadata {
					if strV, ok := v.(string); ok {
						doc.Metadata[k] = strV
					}
				}
			}
			if createdAt, ok := docMap["created_at"].(string); ok {
				if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
					doc.CreatedAt = t
				}
			}
			if updatedAt, ok := docMap["updated_at"].(string); ok {
				if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
					doc.UpdatedAt = t
				}
			}

			// 提取所有其他字段到 Fields
			commonFields := map[string]bool{
				"id":         true,
				"type":       true,
				"title":      true,
				"content":    true,
				"metadata":   true,
				"created_at": true,
				"updated_at": true,
			}
			for k, v := range docMap {
				if !commonFields[k] {
					doc.Fields[k] = v
				}
			}

			return doc, nil
		}
	}

	return nil, nil
}

func (r *searchRepository) BatchAddDocuments(ctx context.Context, docs []*model.SearchDocument) error {
	for _, doc := range docs {
		if err := r.AddDocument(ctx, doc); err != nil {
			logger.Warn("批量添加文档失败",
				logger.StringField("id", doc.ID),
				logger.ErrorField(err))
		}
	}
	return nil
}

func (r *searchRepository) BatchDeleteDocuments(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := r.DeleteDocument(ctx, id); err != nil {
			logger.Warn("批量删除文档失败",
				logger.StringField("id", id),
				logger.ErrorField(err))
		}
	}
	return nil
}

func (r *searchRepository) CreateIndex(ctx context.Context, indexType model.IndexType) error {
	if r.esManager == nil {
		return fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexName := r.getIndexName(indexType)

	var mappings model.IndexMapping
	switch indexType {
	case model.ENTITY:
		mappings = model.GenerateEntityMapping()
	case model.RELATION:
		mappings = model.GenerateRelationMapping()
	case model.QUESTION:
		mappings = model.GenerateQuestionMapping()
	case model.ANSWER:
		mappings = model.GenerateAnswerMapping()
	case model.DOCUMENT:
		mappings = model.GenerateDocumentMapping()
	case model.USER:
		mappings = model.GenerateUserMapping()
	default:
		mappings = model.GenerateDocumentMapping()
	}

	if err := r.esManager.CreateIndex(indexName, mappings); err != nil {
		logger.Error("创建索引失败",
			logger.StringField("index", indexName),
			logger.ErrorField(err))
		return err
	}

	return nil
}

func (r *searchRepository) DeleteIndex(ctx context.Context, indexType model.IndexType) error {
	if r.esManager == nil {
		return fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexName := r.getIndexName(indexType)

	if err := r.esManager.DeleteIndex(indexName); err != nil {
		logger.Error("删除索引失败",
			logger.StringField("index", indexName),
			logger.ErrorField(err))
		return err
	}

	return nil
}

func (r *searchRepository) RefreshIndex(ctx context.Context, indexType model.IndexType) error {
	if r.esManager == nil {
		return fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexName := r.getIndexName(indexType)

	if err := r.esManager.RefreshIndex(indexName); err != nil {
		logger.Error("刷新索引失败",
			logger.StringField("index", indexName),
			logger.ErrorField(err))
		return err
	}

	return nil
}

func (r *searchRepository) GetIndexStats(ctx context.Context) ([]*model.IndexStats, error) {
	if r.esManager == nil {
		return nil, fmt.Errorf("Elasticsearch未初始化，搜索服务暂不可用")
	}

	indexes := []struct {
		Type model.IndexType
		Name string
	}{
		{model.ENTITY, "entities"},
		{model.RELATION, "relations"},
		{model.QUESTION, "questions"},
		{model.ANSWER, "answers"},
		{model.DOCUMENT, "documents"},
		{model.USER, "users"},
	}

	stats := make([]*model.IndexStats, 0, len(indexes))
	for _, index := range indexes {
		stat := &model.IndexStats{
			Type: index.Type,
		}

		esStats, err := r.esManager.GetIndexStats(index.Name)
		if err != nil {
			logger.Warn("获取索引统计失败",
				logger.StringField("index", index.Name),
				logger.ErrorField(err))
			stats = append(stats, stat)
			continue
		}

		stat.DocumentCount = esStats.DocumentCount
		stat.SizeInBytes = esStats.SizeInBytes
		stat.LastUpdated = esStats.LastUpdated

		stats = append(stats, stat)
	}

	return stats, nil
}
