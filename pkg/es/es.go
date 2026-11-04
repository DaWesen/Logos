package es

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"Logos/config"
	"Logos/pkg/lazyinit"
	"Logos/pkg/obs"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

var (
	// 初始化失败不能被永久缓存：否则第二次调用返回 (nil, nil)，
	// 调用方以为成功，随后在 nil client 上 panic。
	esResource = lazyinit.New(lazyinit.Options{
		RetryInterval: 5 * time.Second,
	}, initElasticsearchClient)

	// esInitTimeout 限制启动 Ping 的耗时。go-elasticsearch 默认没有请求超时，
	// ES 地址可路由但不应答时，初始化会一直挂住，lazyinit 的互斥锁也会跟着卡死。
	esInitTimeout = 10 * time.Second
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
	return esResource.Get()
}

func initElasticsearchClient() (*elasticsearch.Client, error) {
	return initElasticsearchClientWith(config.GetConfig().Elasticsearch, esInitTimeout)
}

func initElasticsearchClientWith(esConfig config.Elasticsearch, timeout time.Duration) (*elasticsearch.Client, error) {
	if timeout <= 0 {
		timeout = esInitTimeout
	}

	esCfg := elasticsearch.Config{
		Addresses: []string{esConfig.URL},
	}

	if esConfig.Username != "" && esConfig.Password != "" {
		esCfg.Username = esConfig.Username
		esCfg.Password = esConfig.Password
	}

	client, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create elasticsearch client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	res, err := client.Ping(client.Ping.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to ping elasticsearch: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch ping failed: %s", res.Status())
	}
	return client, nil
}

func NewESManager(client *elasticsearch.Client) *ESManager {
	return &ESManager{
		client: client,
	}
}

// Health 是一次轻量集群连通性检查。
//
// 用 Ping 而不是查询某个业务索引：探针要回答的是「ES 还能不能服务」，
// 不应该因为某个索引尚未创建、或某次 reindex 进行中就被判为不健康。
func (m *ESManager) Health(ctx context.Context) error {
	if m == nil || m.client == nil {
		return fmt.Errorf("elasticsearch: client unavailable")
	}
	res, err := m.client.Ping(m.client.Ping.WithContext(ctxOrBackground(ctx)))
	if err != nil {
		return fmt.Errorf("elasticsearch: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch: %s", res.Status())
	}
	return nil
}

// ctxOrBackground 把 nil ctx 兜底成 Background，避免调用方传 nil 时 panic。
func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// esResult 把一次 ES 调用的错误归类成低基数标签。
//
// 取消和失败必须分开：ctx 透传后，客户端断连会稳定产生 canceled，
// 若与 failed 混在一起，真实故障率会被正常流量稀释，告警也就失去意义。
// 只有在确实出错时才看 ctx.Err()，避免「请求成功但 deadline 刚好到期」
// 把一次成功调用误标成取消。
func esResult(ctx context.Context, err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		ctx.Err() != nil {
		return "canceled"
	}
	return "failed"
}

// withMetrics 是所有 ES 调用的唯一出口：统一透传 ctx、统一计时、统一打点。
//
// 放在这一层而不是各业务 service 里，是为了保证「只要走了 ES，就一定有指标」；
// 否则新增一个调用点忘记埋点，检索链路的可观测性就会出现盲区。
func (e *ESManager) withMetrics(ctx context.Context, index, operation string, fn func(context.Context) error) error {
	ctx = ctxOrBackground(ctx)
	start := time.Now()
	err := fn(ctx)
	obs.RecordESRequest(ctx, index, operation, esResult(ctx, err), time.Since(start).Seconds())
	return err
}

func (e *ESManager) AddDocument(index string, id string, doc interface{}) error {
	return e.AddDocumentWithContext(context.Background(), index, id, doc)
}

// AddDocumentWithContext 与 AddDocument 相同，但把调用方 ctx 透传给 ES 客户端。
// 请求被取消/超时时，ES 请求会一起中止，不会在客户端断开后继续占着连接。
func (e *ESManager) AddDocumentWithContext(ctx context.Context, index string, id string, doc interface{}) error {
	return e.withMetrics(ctx, index, "add", func(ctx context.Context) error {
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

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to add document: %s", res.Status())
		}

		return nil
	})
}

func (e *ESManager) UpdateDocument(index string, id string, doc interface{}) error {
	return e.UpdateDocumentWithContext(context.Background(), index, id, doc)
}

func (e *ESManager) UpdateDocumentWithContext(ctx context.Context, index string, id string, doc interface{}) error {
	return e.withMetrics(ctx, index, "update", func(ctx context.Context) error {
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

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to update document: %s", res.Status())
		}

		return nil
	})
}

func (e *ESManager) DeleteDocument(index string, id string) error {
	return e.DeleteDocumentWithContext(context.Background(), index, id)
}

func (e *ESManager) DeleteDocumentWithContext(ctx context.Context, index string, id string) error {
	return e.withMetrics(ctx, index, "delete", func(ctx context.Context) error {
		req := esapi.DeleteRequest{
			Index:      index,
			DocumentID: id,
			Refresh:    "true",
		}

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to delete document: %s", res.Status())
		}

		return nil
	})
}

func (e *ESManager) GetDocument(index string, id string) (json.RawMessage, error) {
	return e.GetDocumentWithContext(context.Background(), index, id)
}

func (e *ESManager) GetDocumentWithContext(ctx context.Context, index string, id string) (json.RawMessage, error) {
	var source json.RawMessage
	err := e.withMetrics(ctx, index, "get", func(ctx context.Context) error {
		req := esapi.GetRequest{
			Index:      index,
			DocumentID: id,
		}

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to get document: %s", res.Status())
		}

		var result struct {
			Source json.RawMessage `json:"_source"`
		}
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			return err
		}

		source = result.Source
		return nil
	})
	if err != nil {
		return nil, err
	}
	return source, nil
}

func (e *ESManager) Search(index string, query SearchQuery, result interface{}) error {
	return e.SearchWithContext(context.Background(), index, query, result)
}

// SearchWithContext 是检索链路的正确入口：语义/关键词双路检索都可能跑在
// 用户请求的 ctx 下，客户端断连后应立即中止 ES 查询，而不是查完再丢弃结果。
func (e *ESManager) SearchWithContext(ctx context.Context, index string, query SearchQuery, result interface{}) error {
	return e.withMetrics(ctx, index, "search", func(ctx context.Context) error {
		queryJSON, err := json.Marshal(query)
		if err != nil {
			return err
		}

		req := esapi.SearchRequest{
			Index: []string{index},
			Body:  bytes.NewReader(queryJSON),
		}

		res, err := req.Do(ctx, e.client)
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
	})
}

func (e *ESManager) CreateIndex(index string, mappings interface{}) error {
	return e.CreateIndexWithContext(context.Background(), index, mappings)
}

func (e *ESManager) CreateIndexWithContext(ctx context.Context, index string, mappings interface{}) error {
	return e.withMetrics(ctx, index, "create_index", func(ctx context.Context) error {
		mappingsJSON, err := json.Marshal(mappings)
		if err != nil {
			return err
		}

		req := esapi.IndicesCreateRequest{
			Index: index,
			Body:  bytes.NewReader(mappingsJSON),
		}

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to create index: %s", res.Status())
		}

		return nil
	})
}

func (e *ESManager) DeleteIndex(index string) error {
	return e.DeleteIndexWithContext(context.Background(), index)
}

func (e *ESManager) DeleteIndexWithContext(ctx context.Context, index string) error {
	return e.withMetrics(ctx, index, "delete_index", func(ctx context.Context) error {
		req := esapi.IndicesDeleteRequest{
			Index: []string{index},
		}

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to delete index: %s", res.Status())
		}

		return nil
	})
}

func (e *ESManager) RefreshIndex(index string) error {
	return e.RefreshIndexWithContext(context.Background(), index)
}

func (e *ESManager) RefreshIndexWithContext(ctx context.Context, index string) error {
	return e.withMetrics(ctx, index, "refresh", func(ctx context.Context) error {
		req := esapi.IndicesRefreshRequest{
			Index: []string{index},
		}

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to refresh index: %s", res.Status())
		}

		return nil
	})
}

// IndexStats 索引统计信息
type IndexStats struct {
	DocumentCount int64  `json:"document_count"`
	SizeInBytes   int64  `json:"size_in_bytes"`
	LastUpdated   string `json:"last_updated"`
}

// GetIndexStats 获取索引统计信息
func (e *ESManager) GetIndexStats(index string) (*IndexStats, error) {
	return e.GetIndexStatsWithContext(context.Background(), index)
}

func (e *ESManager) GetIndexStatsWithContext(ctx context.Context, index string) (*IndexStats, error) {
	var stats *IndexStats
	err := e.withMetrics(ctx, index, "stats", func(ctx context.Context) error {
		req := esapi.IndicesStatsRequest{
			Index:  []string{index},
			Metric: []string{"docs", "store"},
		}

		res, err := req.Do(ctx, e.client)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.IsError() {
			return fmt.Errorf("failed to get index stats: %s", res.Status())
		}

		var result map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
			return err
		}

		stats = &IndexStats{}

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

		return nil
	})
	if err != nil {
		return nil, err
	}
	return stats, nil
}
