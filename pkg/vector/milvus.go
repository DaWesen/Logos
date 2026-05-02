package vector

import (
	"context"
	"fmt"
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

func InitMilvus() (*MilvusManager, error) {
	var err error
	milvusOnce.Do(func() {
		cfg := config.GetConfig()
		milvusInstance, err = NewMilvusManager(cfg.Milvus.Host, cfg.Milvus.Port)
	})
	return milvusInstance, err
}

func NewMilvusManager(host string, port int) (*MilvusManager, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	addr := fmt.Sprintf("%s:%d", host, port)
	c, err := milvusclient.New(ctx, &milvusclient.ClientConfig{
		Address: addr,
	})
	if err != nil {
		logger.Error("连接Milvus失败",
			logger.StringField("address", addr),
			logger.ErrorField(err))
		return nil, err
	}

	logger.Info("连接Milvus成功",
		logger.StringField("address", addr))

	return &MilvusManager{
		client: c,
	}, nil
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
			WithDim(int64(dim)))

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

func (m *MilvusManager) Insert(ctx context.Context, collectionName string, ids []string, vectors [][]float32) error {
	logger.Info("插入向量到Milvus",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(ids)))

	idCol := column.NewColumnVarChar("id", ids)
	vecCol := column.NewColumnFloatVector("vector", len(vectors[0]), vectors)

	_, err := m.client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(collectionName, idCol, vecCol))
	if err != nil {
		logger.Error("插入向量到Milvus失败",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return err
	}

	logger.Info("插入向量到Milvus成功",
		logger.StringField("collection", collectionName),
		logger.IntField("count", len(ids)))

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
	ID    string
	Score float32
}

func (m *MilvusManager) Search(ctx context.Context, collectionName string, queryVector []float32, topK int, metricType string) ([]*SearchResult, error) {
	logger.Warn("Milvus向量搜索功能待实现")
	return []*SearchResult{}, nil
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

func (m *MilvusManager) LoadCollection(ctx context.Context, collectionName string, replicaNum int) error {
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
