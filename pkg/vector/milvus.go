package vector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"Logos/config"
	"Logos/pkg/logger"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

type MilvusManager struct {
	client *milvusclient.Client
}

var (
	milvusInstance *MilvusManager
	milvusOnce     sync.Once
)

func isCollectionNotFound(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "collection not found") ||
		strings.Contains(errStr, "can't find collection") ||
		strings.Contains(errStr, "CollectionNotFound")
}

func InitMilvus() (*MilvusManager, error) {
	var err error
	milvusOnce.Do(func() {
		cfg := config.GetConfig()
		milvusInstance, err = NewMilvusManager(cfg.Milvus.Host, cfg.Milvus.Port)
	})
	return milvusInstance, err
}

func NewMilvusManager(host string, port int) (*MilvusManager, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	var c *milvusclient.Client
	var err error

	for i := 0; i < 60; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		c, err = milvusclient.New(ctx, &milvusclient.ClientConfig{
			Address: addr,
		})
		cancel()

		if err == nil {
			logger.Info("连接Milvus成功",
				logger.StringField("address", addr))
			return &MilvusManager{
				client: c,
			}, nil
		}

		logger.Warn("连接Milvus失败，重试中...",
			logger.StringField("address", addr),
			logger.IntField("attempt", i+1),
			logger.ErrorField(err))
		time.Sleep(3 * time.Second)
	}

	logger.Error("连接Milvus失败，已达最大重试次数",
		logger.StringField("address", addr),
		logger.ErrorField(err))
	return nil, fmt.Errorf("failed to connect milvus after 60 attempts: %w", err)
}

func (m *MilvusManager) Close() error {
	return m.client.Close(context.Background())
}

func (m *MilvusManager) CreateCollection(ctx context.Context, collectionName string, dim int) error {
	logger.Info("创建Milvus集合",
		logger.StringField("collection", collectionName),
		logger.IntField("dimension", dim))

	schema := entity.NewSchema().
		WithName(collectionName).
		WithDescription("Vector collection for " + collectionName).
		WithField(entity.NewField().
			WithName("id").
			WithDataType(entity.FieldTypeVarChar).
			WithMaxLength(256).
			WithIsPrimaryKey(true)).
		WithField(entity.NewField().
			WithName("vector").
			WithDataType(entity.FieldTypeFloatVector).
			WithDim(int64(dim))).
		WithField(entity.NewField().
			WithName("content").
			WithDataType(entity.FieldTypeVarChar).
			WithMaxLength(65535))

	err := m.client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(collectionName, schema))
	if err != nil {
		logger.Error("创建Milvus集合失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("创建Milvus集合成功",
		logger.StringField("collection", collectionName))

	return nil
}

type VectorPreview struct {
	ID      string
	Content string
}

func (m *MilvusManager) QueryVectors(ctx context.Context, collectionName string, offset, limit int) (result []*VectorPreview, total int64, err error) {
	// 防止 Milvus SDK 内部 panic
	defer func() {
		if r := recover(); r != nil {
			logger.Error("QueryVectors panic recovered", logger.AnyField("panic", r))
			err = fmt.Errorf("milvus internal error: %v", r)
		}
	}()

	if m == nil || m.client == nil {
		return nil, 0, fmt.Errorf("milvus client not initialized")
	}

	logger.Info("查询Milvus向量",
		logger.StringField("collection", collectionName),
		logger.IntField("offset", offset),
		logger.IntField("limit", limit))

	loadCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	loadErr := m.LoadCollection(loadCtx, collectionName, 1)
	cancel()
	if loadErr != nil {
		logger.Warn("加载Milvus集合到内存失败，尝试继续查询",
			logger.StringField("collection", collectionName),
			logger.ErrorField(loadErr))
	}

	option := milvusclient.NewQueryOption(collectionName).
		WithFilter("id != ''").
		WithOutputFields("id", "content").
		WithLimit(limit)
	if offset > 0 {
		option = option.WithOffset(offset)
	}

	resultSet, err := m.client.Query(ctx, option)
	if err != nil {
		logger.Warn("QueryVectors第一次查询失败，尝试不带filter查询",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))

		option2 := milvusclient.NewQueryOption(collectionName).
			WithOutputFields("id", "content").
			WithLimit(limit)
		if offset > 0 {
			option2 = option2.WithOffset(offset)
		}

		resultSet, err = m.client.Query(ctx, option2)
		if err != nil {
			logger.Error("查询Milvus向量失败",
				logger.StringField("collection", collectionName),
				logger.IntField("limit", limit),
				logger.ErrorField(err))
			return nil, 0, err
		}
	}

	var totalCount int64

	stats, statsErr := m.client.GetCollectionStats(ctx, milvusclient.NewGetCollectionStatsOption(collectionName))
	if statsErr == nil {
		if rowCountStr, ok := stats["row_count"]; ok {
			if parsed, parseErr := fmt.Sscanf(rowCountStr, "%d", &totalCount); parseErr != nil || parsed != 1 {
				logger.Warn("解析row_count失败", logger.StringField("value", rowCountStr))
				totalCount = int64(resultSet.ResultCount)
			}
		}
	}

	logger.Info("QueryVectors结果",
		logger.StringField("collection", collectionName),
		logger.IntField("result_count", resultSet.ResultCount),
		logger.Int64Field("total_from_stats", totalCount))

	vectors := make([]*VectorPreview, 0)
	if resultSet.ResultCount == 0 {
		return vectors, totalCount, nil
	}

	idCol := resultSet.GetColumn("id")
	contentCol := resultSet.GetColumn("content")

	if idCol == nil {
		logger.Warn("Query结果中没有id列", logger.StringField("collection", collectionName))
	}
	if contentCol == nil {
		logger.Warn("Query结果中没有content列", logger.StringField("collection", collectionName))
	}

	for i := 0; i < resultSet.ResultCount; i++ {
		idStr := ""
		if idCol != nil {
			if col, ok := idCol.(*column.ColumnVarChar); ok {
				if v, getErr := col.Get(i); getErr == nil {
					if s, typeOk := v.(string); typeOk {
						idStr = s
					}
				}
			}
		}
		contentStr := ""
		if contentCol != nil {
			if col, ok := contentCol.(*column.ColumnVarChar); ok {
				if v, getErr := col.Get(i); getErr == nil {
					if s, typeOk := v.(string); typeOk {
						contentStr = s
					}
				}
			}
		}
		vectors = append(vectors, &VectorPreview{
			ID:      idStr,
			Content: contentStr,
		})
	}

	logger.Info("查询Milvus向量成功",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(vectors)))

	return vectors, totalCount, nil
}

func (m *MilvusManager) DropCollection(ctx context.Context, collectionName string) error {
	logger.Info("删除Milvus集合",
		logger.StringField("collection", collectionName))

	err := m.client.DropCollection(ctx, milvusclient.NewDropCollectionOption(collectionName))
	if err != nil {
		logger.Error("删除Milvus集合失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("删除Milvus集合成功",
		logger.StringField("collection", collectionName))

	return nil
}

func (m *MilvusManager) HasCollection(ctx context.Context, collectionName string) (bool, error) {
	has, err := m.client.HasCollection(ctx, milvusclient.NewHasCollectionOption(collectionName))
	if err != nil {
		logger.Error("检查Milvus集合是否存在失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return false, err
	}
	return has, nil
}

func (m *MilvusManager) Insert(ctx context.Context, collectionName string, ids []string, vectors [][]float32, contents ...string) error {
	logger.Info("插入向量到Milvus",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(ids)))

	var cols []column.Column

	idCol := column.NewColumnVarChar("id", ids)
	cols = append(cols, idCol)

	vecCol := column.NewColumnFloatVector("vector", len(vectors[0]), vectors)
	cols = append(cols, vecCol)

	if len(contents) > 0 {
		contentList := make([]string, len(ids))
		for i := range ids {
			if i < len(contents) {
				contentList[i] = contents[i]
			} else {
				contentList[i] = ""
			}
		}
		contentCol := column.NewColumnVarChar("content", contentList)
		cols = append(cols, contentCol)
	}

	_, err := m.client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(collectionName, cols...))
	if err != nil {
		logger.Error("插入向量到Milvus失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("插入向量到Milvus成功",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(ids)))

	flushCtx, flushCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer flushCancel()
	flushTask, flushErr := m.client.Flush(flushCtx, milvusclient.NewFlushOption(collectionName))
	if flushErr != nil {
		logger.Warn("Flush Milvus失败（数据仍可搜索，但前端计数可能不准确）",
			logger.StringField("collection", collectionName),
			logger.ErrorField(flushErr))
		return nil
	}
	if awaitErr := flushTask.Await(flushCtx); awaitErr != nil {
		logger.Warn("等待Flush完成失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(awaitErr))
	}

	logger.Info("Milvus Flush完成",
		logger.StringField("collection", collectionName))
	return nil
}

func (m *MilvusManager) Delete(ctx context.Context, collectionName string, ids []string) error {
	logger.Info("从Milvus删除向量",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(ids)))

	_, err := m.client.Delete(ctx, milvusclient.NewDeleteOption(collectionName).WithStringIDs("id", ids))
	if err != nil {
		logger.Error("从Milvus删除向量失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("从Milvus删除向量成功",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(ids)))

	return nil
}

type SearchResult struct {
	ID      string
	Score   float32
	Content string
}

func (m *MilvusManager) Search(ctx context.Context, collectionName string, queryVector []float32, topK int, metricType string) ([]*SearchResult, error) {
	logger.Info("Milvus向量搜索",
		logger.StringField("collection", collectionName),
		logger.IntField("topK", topK))

	if metricType == "" {
		metricType = "IP"
	}

	option := milvusclient.NewSearchOption(
		collectionName,
		topK,
		[]entity.Vector{entity.FloatVector(queryVector)},
	).WithANNSField("vector").
		WithOutputFields("id", "content")

	resultSets, err := m.client.Search(ctx, option)
	if err != nil {
		logger.Error("执行搜索失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return nil, err
	}

	results := make([]*SearchResult, 0)
	for _, rs := range resultSets {
		idCol := rs.IDs
		contentCol := rs.GetColumn("content")
		for i := 0; i < rs.ResultCount; i++ {
			idStr := ""
			if idCol != nil {
				switch col := idCol.(type) {
				case *column.ColumnVarChar:
					if v, err := col.Get(i); err == nil {
						if s, ok := v.(string); ok {
							idStr = s
						}
					}
				}
			}
			contentStr := ""
			if contentCol != nil {
				switch col := contentCol.(type) {
				case *column.ColumnVarChar:
					if v, err := col.Get(i); err == nil {
						if s, ok := v.(string); ok {
							contentStr = s
						}
					}
				}
			}
			score := float32(0)
			if i < len(rs.Scores) {
				score = rs.Scores[i]
			}
			results = append(results, &SearchResult{
				ID:      idStr,
				Score:   score,
				Content: contentStr,
			})
		}
	}

	logger.Info("Milvus向量搜索成功",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(results)))

	return results, nil
}

func (m *MilvusManager) CreateIndex(ctx context.Context, collectionName string, fieldName string, indexType string, params map[string]string) error {
	logger.Info("创建Milvus索引",
		logger.StringField("collection", collectionName),
		logger.StringField("field", fieldName),
		logger.StringField("index_type", indexType))

	var idx index.Index
	switch indexType {
	case "HNSW":
		idx = index.NewHNSWIndex(entity.L2, 16, 8)
	case "IVF_FLAT":
		idx = index.NewIvfFlatIndex(entity.L2, 128)
	default:
		idx = index.NewFlatIndex(entity.L2)
	}

	task, err := m.client.CreateIndex(ctx, milvusclient.NewCreateIndexOption(collectionName, fieldName, idx))
	if err != nil {
		logger.Error("创建Milvus索引失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	err = task.Await(ctx)
	if err != nil {
		logger.Error("等待索引创建完成失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("创建Milvus索引成功",
		logger.StringField("collection", collectionName),
		logger.StringField("field", fieldName))

	return nil
}

func (m *MilvusManager) LoadCollection(ctx context.Context, collectionName string, replicaNum int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("LoadCollection panic recovered", logger.AnyField("panic", r))
			err = fmt.Errorf("milvus internal error: %v", r)
		}
	}()

	if m == nil || m.client == nil {
		return fmt.Errorf("milvus client not initialized")
	}

	logger.Info("加载Milvus集合到内存",
		logger.StringField("collection", collectionName))

	task, err := m.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(collectionName).WithReplica(replicaNum))
	if err != nil {
		logger.Error("加载Milvus集合失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	err = task.Await(ctx)
	if err != nil {
		logger.Error("等待加载完成失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("加载Milvus集合成功",
		logger.StringField("collection", collectionName))

	return nil
}

func (m *MilvusManager) ReleaseCollection(ctx context.Context, collectionName string) error {
	logger.Info("释放Milvus集合",
		logger.StringField("collection", collectionName))

	err := m.client.ReleaseCollection(ctx, milvusclient.NewReleaseCollectionOption(collectionName))
	if err != nil {
		logger.Error("释放Milvus集合失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("释放Milvus集合成功",
		logger.StringField("collection", collectionName))

	return nil
}

func (m *MilvusManager) GetCollectionStats(ctx context.Context, collectionName string) (map[string]string, error) {
	logger.Info("获取Milvus集合统计",
		logger.StringField("collection", collectionName))

	stats, err := m.client.GetCollectionStats(ctx, milvusclient.NewGetCollectionStatsOption(collectionName))
	if err != nil {
		logger.Error("获取Milvus集合统计失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return nil, err
	}

	return stats, nil
}

func (m *MilvusManager) ListCollections(ctx context.Context) ([]string, error) {
	logger.Info("列出所有Milvus集合")

	collectionNames, err := m.client.ListCollections(ctx, milvusclient.NewListCollectionOption())
	if err != nil {
		logger.Error("列出Milvus集合失败", logger.ErrorField(err))
		return nil, err
	}

	logger.Info("列出Milvus集合完成", logger.IntField("count", len(collectionNames)))
	return collectionNames, nil
}

func (m *MilvusManager) DescribeCollection(ctx context.Context, collectionName string) (*entity.Collection, error) {
	logger.Info("描述Milvus集合",
		logger.StringField("collection", collectionName))

	coll, err := m.client.DescribeCollection(ctx, milvusclient.NewDescribeCollectionOption(collectionName))
	if err != nil {
		logger.Error("描述Milvus集合失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return nil, err
	}

	return coll, nil
}

func (m *MilvusManager) GetCollectionDimension(ctx context.Context, collectionName string) (int, error) {
	coll, err := m.DescribeCollection(ctx, collectionName)
	if err != nil {
		return 0, err
	}
	for _, field := range coll.Schema.Fields {
		if field.Name == "vector" && field.TypeParams != nil {
			if dimStr, ok := field.TypeParams["dim"]; ok {
				var dim int
				if _, scanErr := fmt.Sscanf(dimStr, "%d", &dim); scanErr == nil {
					return dim, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("vector field dimension not found in collection %s", collectionName)
}

func (m *MilvusManager) DropIndex(ctx context.Context, collectionName string, fieldName string) error {
	logger.Info("删除Milvus索引",
		logger.StringField("collection", collectionName),
		logger.StringField("field", fieldName))

	err := m.client.DropIndex(ctx, milvusclient.NewDropIndexOption(collectionName, fieldName))
	if err != nil {
		logger.Error("删除Milvus索引失败",
			logger.StringField("collection", collectionName),
			logger.StringField("field", fieldName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("删除Milvus索引成功",
		logger.StringField("collection", collectionName),
		logger.StringField("field", fieldName))

	return nil
}
