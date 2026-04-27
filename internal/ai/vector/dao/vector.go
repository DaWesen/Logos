package dao

import (
	"context"
	"fmt"
	"time"

	"Logos/internal/ai/vector/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/vector"
)

type VectorRepository interface {
	CreateCollection(ctx context.Context, collection *model.VectorCollection) error
	UpdateCollection(ctx context.Context, collection *model.VectorCollection) error
	DeleteCollection(ctx context.Context, id string) error
	GetCollection(ctx context.Context, id string) (*model.VectorCollection, error)
	ListCollections(ctx context.Context) ([]*model.VectorCollection, error)

	Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error)
	BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]*model.Vector, error)

	Search(ctx context.Context, collectionID string, queryVector []float64, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error)
	TextSearch(ctx context.Context, collectionID string, text string, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error)

	DeleteVector(ctx context.Context, collectionID string, vectorID string) error
	BatchDeleteVector(ctx context.Context, collectionID string, vectorIDs []string) error
}

type vectorRepository struct {
	milvus     *vector.MilvusManager
	einoClient *eino.EinoManager
}

func NewVectorRepository(milvus *vector.MilvusManager, einoClient *eino.EinoManager) VectorRepository {
	return &vectorRepository{
		milvus:     milvus,
		einoClient: einoClient,
	}
}

func (r *vectorRepository) CreateCollection(ctx context.Context, collection *model.VectorCollection) error {
	logger.Info("创建向量集合",
		logger.StringField("id", collection.ID),
		logger.StringField("name", collection.Name))

	collectionName := "vector_" + collection.ID
	err := r.milvus.CreateCollection(ctx, collectionName, collection.Dimension)
	if err != nil {
		logger.Error("创建Milvus集合失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return err
	}

	indexType := "FLAT"
	switch collection.IndexType {
	case model.IVF_FLAT:
		indexType = "IVF_FLAT"
	case model.HNSW:
		indexType = "HNSW"
	}

	err = r.milvus.CreateIndex(ctx, collectionName, "vector", indexType, nil)
	if err != nil {
		logger.Error("创建Milvus索引失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return err
	}

	err = r.milvus.LoadCollection(ctx, collectionName, 1)
	if err != nil {
		logger.Error("加载Milvus集合失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("创建向量集合成功",
		logger.StringField("id", collection.ID),
		logger.StringField("name", collection.Name))

	return nil
}

func (r *vectorRepository) UpdateCollection(ctx context.Context, collection *model.VectorCollection) error {
	logger.Info("更新向量集合",
		logger.StringField("id", collection.ID),
		logger.StringField("name", collection.Name))

	collectionName := "vector_" + collection.ID

	coll, err := r.milvus.DescribeCollection(ctx, collectionName)
	if err != nil {
		logger.Error("获取集合信息失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return fmt.Errorf("获取集合信息失败: %w", err)
	}

	currentDim := 0
	for _, field := range coll.Schema.Fields {
		if field.Name == "vector" && field.TypeParams != nil {
			if dimStr, ok := field.TypeParams["dim"]; ok {
				fmt.Sscanf(dimStr, "%d", &currentDim)
			}
		}
	}

	if currentDim > 0 && currentDim != collection.Dimension {
		logger.Warn("维度变更需要重建集合（危险操作），当前仅支持索引类型更新",
			logger.StringField("id", collection.ID),
			logger.IntField("old_dim", currentDim),
			logger.IntField("new_dim", collection.Dimension))
	}

	indexType := "FLAT"
	switch collection.IndexType {
	case model.IVF_FLAT:
		indexType = "IVF_FLAT"
	case model.HNSW:
		indexType = "HNSW"
	}

	err = r.milvus.DropIndex(ctx, collectionName, "vector")
	if err != nil {
		logger.Error("删除旧索引失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return fmt.Errorf("删除旧索引失败: %w", err)
	}

	err = r.milvus.CreateIndex(ctx, collectionName, "vector", indexType, collection.Parameters)
	if err != nil {
		logger.Error("创建新索引失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return fmt.Errorf("创建新索引失败: %w", err)
	}

	err = r.milvus.LoadCollection(ctx, collectionName, 1)
	if err != nil {
		logger.Error("重新加载集合失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return fmt.Errorf("重新加载集合失败: %w", err)
	}

	logger.Info("更新向量集合成功",
		logger.StringField("id", collection.ID),
		logger.StringField("index_type", indexType))

	return nil
}

func (r *vectorRepository) DeleteCollection(ctx context.Context, id string) error {
	logger.Info("删除向量集合",
		logger.StringField("id", id))

	collectionName := "vector_" + id
	err := r.milvus.DropCollection(ctx, collectionName)
	if err != nil {
		logger.Error("删除Milvus集合失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return err
	}

	logger.Info("删除向量集合成功",
		logger.StringField("id", id))

	return nil
}

func (r *vectorRepository) GetCollection(ctx context.Context, id string) (*model.VectorCollection, error) {
	logger.Info("获取向量集合",
		logger.StringField("id", id))

	collectionName := "vector_" + id
	has, err := r.milvus.HasCollection(ctx, collectionName)
	if err != nil {
		logger.Error("检查Milvus集合是否存在失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return nil, err
	}

	if !has {
		return nil, nil
	}

	stats, err := r.milvus.GetCollectionStats(ctx, collectionName)
	if err != nil {
		logger.Error("获取Milvus集合统计失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return nil, err
	}

	collection := &model.VectorCollection{
		ID: id,
	}

	if size, ok := stats["row_count"]; ok {
		collection.Size = int64(len(size))
	}

	return collection, nil
}

func (r *vectorRepository) ListCollections(ctx context.Context) ([]*model.VectorCollection, error) {
	logger.Info("列出所有向量集合")

	logger.Warn("列出向量集合功能待实现")
	return []*model.VectorCollection{}, nil
}

func (r *vectorRepository) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error) {
	logger.Info("向量化",
		logger.StringField("text", text),
		logger.StringField("collection_id", collectionID))

	var embeddings []float32

	if r.einoClient != nil && r.einoClient.HasEmbedder() {
		vec, embedErr := r.einoClient.EmbedText(ctx, text)
		if embedErr != nil {
			logger.Error("EINO嵌入失败，返回空向量",
				logger.ErrorField(embedErr))
			embeddings = make([]float32, 768)
		} else {
			embeddings = float64ToFloat32(vec)
		}
	} else {
		logger.Warn("Eino未初始化或无Embedder，返回空向量")
		embeddings = make([]float32, 768)
	}

	vectorID := fmt.Sprintf("%d", time.Now().UnixNano())

	collectionName := "vector_" + collectionID
	_ = r.milvus.Insert(ctx, collectionName, []string{vectorID}, [][]float32{embeddings})

	vector := &model.Vector{
		ID:        vectorID,
		Values:    float32ToFloat64(embeddings),
		Metadata:  metadata,
		CreatedAt: time.Now(),
	}

	logger.Info("向量化成功",
		logger.StringField("vector_id", vectorID),
		logger.IntField("dimension", len(embeddings)))

	return vector, nil
}

func (r *vectorRepository) BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]*model.Vector, error) {
	logger.Info("批量向量化",
		logger.IntField("count", len(texts)),
		logger.StringField("collection_id", collectionID))

	vectors := make([]*model.Vector, 0, len(texts))
	var allEmbeddings [][]float32

	if r.einoClient != nil && r.einoClient.HasEmbedder() {
		batchEmbeddings, batchErr := r.einoClient.BatchEmbedText(ctx, texts)
		if batchErr != nil {
			logger.Error("EINO批量嵌入失败",
				logger.ErrorField(batchErr))
			allEmbeddings = make([][]float32, len(texts))
			for i := range texts {
				allEmbeddings[i] = make([]float32, 768)
			}
		} else {
			allEmbeddings = make([][]float32, len(batchEmbeddings))
			for i, emb := range batchEmbeddings {
				allEmbeddings[i] = float64ToFloat32(emb)
			}
		}
	} else {
		logger.Warn("Eino未初始化或无Embedder，返回空向量")
		for range texts {
			allEmbeddings = append(allEmbeddings, make([]float32, 768))
		}
	}

	ids := make([]string, len(texts))
	for i := range texts {
		metadata := map[string]string{}
		if i < len(metadataList) {
			metadata = metadataList[i]
		}
		ids[i] = fmt.Sprintf("%d_%d", time.Now().UnixNano(), i)
		vectors = append(vectors, &model.Vector{
			ID:        ids[i],
			Values:    float32ToFloat64(allEmbeddings[i]),
			Metadata:  metadata,
			CreatedAt: time.Now(),
		})
	}

	collectionName := "vector_" + collectionID
	_ = r.milvus.Insert(ctx, collectionName, ids, allEmbeddings)

	logger.Info("批量向量化成功",
		logger.IntField("count", len(vectors)))

	return vectors, nil
}

func (r *vectorRepository) Search(ctx context.Context, collectionID string, queryVector []float64, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	logger.Info("向量搜索",
		logger.StringField("collection_id", collectionID),
		logger.IntField("top_k", topK))

	collectionName := "vector_" + collectionID

	queryVec32 := make([]float32, len(queryVector))
	for i, v := range queryVector {
		queryVec32[i] = float32(v)
	}

	results, err := r.milvus.Search(ctx, collectionName, queryVec32, topK, "IP")
	if err != nil {
		logger.Error("Milvus向量搜索失败",
			logger.StringField("collection_id", collectionID),
			logger.ErrorField(err))
		return nil, err
	}

	var searchResults []*model.SearchResultItem
	for _, result := range results {
		if result.Score < float32(threshold) {
			continue
		}
		searchResults = append(searchResults, &model.SearchResultItem{
			VectorID: result.ID,
			Score:    float64(result.Score),
			Metadata: map[string]string{},
		})
	}

	logger.Info("向量搜索成功",
		logger.IntField("count", len(searchResults)))

	return searchResults, nil
}

func (r *vectorRepository) TextSearch(ctx context.Context, collectionID string, text string, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	logger.Info("文本搜索",
		logger.StringField("collection_id", collectionID),
		logger.StringField("text", text),
		logger.IntField("top_k", topK))

	var embeddings []float32
	if r.einoClient != nil && r.einoClient.HasEmbedder() {
		vec, embedErr := r.einoClient.EmbedText(ctx, text)
		if embedErr != nil {
			logger.Error("EINO嵌入失败",
				logger.ErrorField(embedErr))
			return []*model.SearchResultItem{}, embedErr
		}
		embeddings = float64ToFloat32(vec)
	} else {
		logger.Warn("Eino未初始化或无Embedder，无法执行文本搜索")
		return []*model.SearchResultItem{}, nil
	}

	collectionName := "vector_" + collectionID
	results, err := r.milvus.Search(ctx, collectionName, embeddings, topK, "IP")
	if err != nil {
		logger.Error("Milvus文本搜索失败",
			logger.StringField("collection_id", collectionID),
			logger.ErrorField(err))
		return nil, err
	}

	var searchResults []*model.SearchResultItem
	for _, result := range results {
		if result.Score < float32(threshold) {
			continue
		}
		searchResults = append(searchResults, &model.SearchResultItem{
			VectorID: result.ID,
			Score:    float64(result.Score),
			Metadata: map[string]string{},
		})
	}

	logger.Info("文本搜索成功",
		logger.IntField("count", len(searchResults)))

	return searchResults, nil
}

func (r *vectorRepository) DeleteVector(ctx context.Context, collectionID string, vectorID string) error {
	logger.Info("删除向量",
		logger.StringField("collection_id", collectionID),
		logger.StringField("vector_id", vectorID))

	collectionName := "vector_" + collectionID
	err := r.milvus.Delete(ctx, collectionName, []string{vectorID})
	if err != nil {
		logger.Error("从Milvus删除向量失败",
			logger.StringField("collection_id", collectionID),
			logger.StringField("vector_id", vectorID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("删除向量成功",
		logger.StringField("vector_id", vectorID))

	return nil
}

func (r *vectorRepository) BatchDeleteVector(ctx context.Context, collectionID string, vectorIDs []string) error {
	logger.Info("批量删除向量",
		logger.StringField("collection_id", collectionID),
		logger.IntField("count", len(vectorIDs)))

	collectionName := "vector_" + collectionID
	err := r.milvus.Delete(ctx, collectionName, vectorIDs)
	if err != nil {
		logger.Error("从Milvus批量删除向量失败",
			logger.StringField("collection_id", collectionID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("批量删除向量成功",
		logger.IntField("count", len(vectorIDs)))

	return nil
}

func float32ToFloat64(vec []float32) []float64 {
	result := make([]float64, len(vec))
	for i, v := range vec {
		result[i] = float64(v)
	}
	return result
}

func float64ToFloat32(vec []float64) []float32 {
	result := make([]float32, len(vec))
	for i, v := range vec {
		result[i] = float32(v)
	}
	return result
}
