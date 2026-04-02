package es

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"Noah/config"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

var (
	esClient *elasticsearch.Client
	esOnce   sync.Once
)

type SearchQuery struct {
	Query  map[string]interface{}   `json:"query"`
	From   int                      `json:"from"`
	Size   int                      `json:"size"`
	Sort   []map[string]interface{} `json:"sort,omitempty"`
	Source []string                 `json:"_source,omitempty"`
}

type SearchResult struct {
	Took int64 `json:"took"`
	Hits struct {
		Total struct {
			Value int64 `json:"value"`
		} `json:"total"`
		Hits []struct {
			Index  string          `json:"_index"`
			ID     string          `json:"_id"`
			Score  float64         `json:"_score"`
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

type ESManager struct {
	client *elasticsearch.Client
}

func InitElasticsearch() (*elasticsearch.Client, error) {
	var err error
	esOnce.Do(func() {
		cfg := config.GetConfig()
		esConfig := cfg.Elasticsearch

		esCfg := elasticsearch.Config{
			Addresses: []string{esConfig.URL},
		}

		if esConfig.Username != "" && esConfig.Password != "" {
			esCfg.Username = esConfig.Username
			esCfg.Password = esConfig.Password
		}

		esClient, err = elasticsearch.NewClient(esCfg)
		if err != nil {
			err = fmt.Errorf("failed to create elasticsearch client: %w", err)
			return
		}

		res, pingErr := esClient.Ping()
		if pingErr != nil {
			err = fmt.Errorf("failed to ping elasticsearch: %w", pingErr)
			return
		}
		defer res.Body.Close()

		if res.IsError() {
			err = fmt.Errorf("elasticsearch ping failed: %s", res.Status())
			return
		}
	})

	return esClient, err
}

func NewESManager(client *elasticsearch.Client) *ESManager {
	return &ESManager{
		client: client,
	}
}

func (e *ESManager) AddDocument(index string, id string, doc interface{}) error {
	docJSON, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	req := esapi.IndexRequest{
		Index:      index,
		DocumentID: id,
		Body:       bytes.NewReader(docJSON),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to add document: %s", res.Status())
	}

	return nil
}

func (e *ESManager) UpdateDocument(index string, id string, doc interface{}) error {
	updateDoc := map[string]interface{}{
		"doc": doc,
	}

	docJSON, err := json.Marshal(updateDoc)
	if err != nil {
		return err
	}

	req := esapi.UpdateRequest{
		Index:      index,
		DocumentID: id,
		Body:       bytes.NewReader(docJSON),
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to update document: %s", res.Status())
	}

	return nil
}

func (e *ESManager) DeleteDocument(index string, id string) error {
	req := esapi.DeleteRequest{
		Index:      index,
		DocumentID: id,
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to delete document: %s", res.Status())
	}

	return nil
}

func (e *ESManager) GetDocument(index string, id string) (json.RawMessage, error) {
	req := esapi.GetRequest{
		Index:      index,
		DocumentID: id,
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("failed to get document: %s", res.Status())
	}

	var result struct {
		Source json.RawMessage `json:"_source"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Source, nil
}

func (e *ESManager) Search(index string, query SearchQuery, result interface{}) error {
	queryJSON, err := json.Marshal(query)
	if err != nil {
		return err
	}

	req := esapi.SearchRequest{
		Index: []string{index},
		Body:  bytes.NewReader(queryJSON),
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to search: %s", res.Status())
	}

	if err := json.NewDecoder(res.Body).Decode(result); err != nil {
		return err
	}

	return nil
}

func (e *ESManager) CreateIndex(index string, mappings interface{}) error {
	mappingsJSON, err := json.Marshal(mappings)
	if err != nil {
		return err
	}

	req := esapi.IndicesCreateRequest{
		Index: index,
		Body:  bytes.NewReader(mappingsJSON),
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to create index: %s", res.Status())
	}

	return nil
}

func (e *ESManager) DeleteIndex(index string) error {
	req := esapi.IndicesDeleteRequest{
		Index: []string{index},
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to delete index: %s", res.Status())
	}

	return nil
}

func (e *ESManager) RefreshIndex(index string) error {
	req := esapi.IndicesRefreshRequest{
		Index: []string{index},
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to refresh index: %s", res.Status())
	}

	return nil
}

// IndexStats 索引统计信息
type IndexStats struct {
	DocumentCount int64  `json:"document_count"`
	SizeInBytes   int64  `json:"size_in_bytes"`
	LastUpdated   string `json:"last_updated"`
}

// GetIndexStats 获取索引统计信息
func (e *ESManager) GetIndexStats(index string) (*IndexStats, error) {
	req := esapi.IndicesStatsRequest{
		Index:  []string{index},
		Metric: []string{"docs", "store"},
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("failed to get index stats: %s", res.Status())
	}

	var result map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return nil, err
	}

	stats := &IndexStats{}

	// 解析文档数量
	if indices, ok := result["indices"].(map[string]interface{}); ok {
		if indexData, ok := indices[index].(map[string]interface{}); ok {
			if total, ok := indexData["total"].(map[string]interface{}); ok {
				// 文档数量
				if docs, ok := total["docs"].(map[string]interface{}); ok {
					if count, ok := docs["count"].(float64); ok {
						stats.DocumentCount = int64(count)
					}
				}
				// 索引大小
				if store, ok := total["store"].(map[string]interface{}); ok {
					if size, ok := store["size_in_bytes"].(float64); ok {
						stats.SizeInBytes = int64(size)
					}
				}
			}
		}
	}

	return stats, nil
}
