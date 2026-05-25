package dao

import (
	"context"
	"fmt"
	"strings"
	"time"

	"Logos/internal/service/ai/vector/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/vector"

	"github.com/cloudwego/eino/components/embedding"
	"go.uber.org/zap/zapcore"
	"gorm.io/gorm"
)

// logTiming 记录耗时日志
func logTiming(step string, start time.Time, fields ...zapcore.Field) {
	fields = append(fields, logger.Float64Field("cost_ms", float64(time.Since(start).Milliseconds())))
	logger.Info("[向量] "+step, fields...)
}

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

	ListVectors(ctx context.Context, collectionID string, page, pageSize int) ([]*model.VectorPreviewItem, int64, error)
}

type vectorRepository struct {
	db         *gorm.DB
	milvus     *vector.MilvusManager
	einoClient *eino.EinoManager
}

func NewVectorRepository(db *gorm.DB, milvus *vector.MilvusManager, einoClient *eino.EinoManager) VectorRepository {
	return &vectorRepository{
		db:         db,
		milvus:     milvus,
		einoClient: einoClient,
	}
}

func (r *vectorRepository) milvusUnavailableError(op string) error {
	return fmt.Errorf("Milvus未连接，无法执行操作: %s", op)
}

func (r *vectorRepository) CreateCollection(ctx context.Context, collection *model.VectorCollection) error {
	if r.milvus == nil {
		return r.milvusUnavailableError("CreateCollection")
	}
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

	if r.db != nil {
		rec := collectionToRecord(collection)
		if createErr := r.db.WithContext(ctx).Create(rec).Error; createErr != nil {
			logger.Warn("保存集合元信息到数据库失败", logger.ErrorField(createErr))
		}
	}

	logger.Info("创建向量集合成功",
		logger.StringField("id", collection.ID),
		logger.StringField("name", collection.Name))

	return nil
}

func (r *vectorRepository) UpdateCollection(ctx context.Context, collection *model.VectorCollection) error {
	if r.milvus == nil {
		return r.milvusUnavailableError("UpdateCollection")
	}
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
			logger.StringField("index_type", indexType),
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
	if r.milvus == nil {
		return r.milvusUnavailableError("DeleteCollection")
	}
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

	if r.db != nil {
		if delErr := r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.VectorCollectionRecord{}).Error; delErr != nil {
			logger.Warn("删除集合数据库记录失败", logger.StringField("id", id), logger.ErrorField(delErr))
		}
	}

	logger.Info("删除向量集合成功",
		logger.StringField("id", id))

	return nil
}

func (r *vectorRepository) GetCollection(ctx context.Context, id string) (*model.VectorCollection, error) {
	if r.db != nil {
		var rec model.VectorCollectionRecord
		if err := r.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error; err == nil {
			coll := recordToModel(&rec)
			collectionName := "vector_" + id
			if r.milvus != nil {
				if stats, statErr := r.milvus.GetCollectionStats(ctx, collectionName); statErr == nil {
					if rowCount, ok := stats["row_count"]; ok {
						if s, err := parseStatInt(rowCount); err == nil {
							coll.Size = s
						}
					}
				}
			}
			return coll, nil
		}
	}
	if r.milvus == nil {
		return nil, r.milvusUnavailableError("GetCollection")
	}
	logger.Info("获取向量集合", logger.StringField("id", id))
	collectionName := "vector_" + id
	has, err := r.milvus.HasCollection(ctx, collectionName)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, nil
	}
	collection := &model.VectorCollection{ID: id, Name: collectionName}
	if stats, statErr := r.milvus.GetCollectionStats(ctx, collectionName); statErr == nil {
		if rowCount, ok := stats["row_count"]; ok {
			if s, err := parseStatInt(rowCount); err == nil {
				collection.Size = s
			}
		}
	}
	return collection, nil
}

func (r *vectorRepository) ListCollections(ctx context.Context) ([]*model.VectorCollection, error) {
	if r.db != nil {
		var records []model.VectorCollectionRecord
		if err := r.db.WithContext(ctx).Find(&records).Error; err == nil && len(records) > 0 {
			var collections []*model.VectorCollection
			for i := range records {
				coll := recordToModel(&records[i])
				collectionName := "vector_" + coll.ID
				if r.milvus != nil {
					if stats, statErr := r.milvus.GetCollectionStats(ctx, collectionName); statErr == nil {
						if rowCount, ok := stats["row_count"]; ok {
							if s, err := parseStatInt(rowCount); err == nil {
								coll.Size = s
							}
						}
					}
				}
				collections = append(collections, coll)
			}
			logger.Info("列出向量集合完成", logger.IntField("count", len(collections)))
			return collections, nil
		}
	}
	if r.milvus == nil {
		logger.Warn("Milvus未连接，返回空集合列表")
		return []*model.VectorCollection{}, nil
	}
	logger.Info("列出所有向量集合")
	collectionNames, err := r.milvus.ListCollections(ctx)
	if err != nil {
		logger.Error("列出Milvus集合失败", logger.ErrorField(err))
		return nil, err
	}
	var collections []*model.VectorCollection
	for _, name := range collectionNames {
		if !strings.HasPrefix(name, "vector_") {
			continue
		}
		id := strings.TrimPrefix(name, "vector_")
		collection := &model.VectorCollection{ID: id, Name: name}
		if stats, statErr := r.milvus.GetCollectionStats(ctx, name); statErr == nil {
			if rowCount, ok := stats["row_count"]; ok {
				if s, err := parseStatInt(rowCount); err == nil {
					collection.Size = s
				}
			}
		}
		collections = append(collections, collection)
	}
	logger.Info("列出向量集合完成", logger.IntField("count", len(collections)))
	return collections, nil
}

func parseStatInt(val interface{}) (int64, error) {
	switch v := val.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case string:
		var i int64
		_, err := fmt.Sscanf(v, "%d", &i)
		return i, err
	default:
		return 0, fmt.Errorf("unexpected type %T", val)
	}
}

func (r *vectorRepository) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error) {
	logger.Info("向量化",
		logger.StringField("text", text),
		logger.StringField("collection_id", collectionID))

	var embeddings []float32

	collection, _ := r.GetCollection(ctx, collectionID)
	if collection == nil {
		return nil, fmt.Errorf("集合 %s 不存在，请先创建集合", collectionID)
	}
	var useCustomEmbedder embedding.Embedder = nil
	if collection != nil && collection.Embedding.Model != "" && collection.Embedding.APIKey != "" {
		var createErr error
		useCustomEmbedder, createErr = eino.NewDynamicEmbedder(
			collection.Embedding.APIKey,
			collection.Embedding.Model,
			collection.Embedding.BaseURL,
		)
		if createErr != nil {
			logger.Warn("创建动态Embedder失败，使用全局配置", logger.ErrorField(createErr))
			useCustomEmbedder = nil
		}
	}

	if useCustomEmbedder != nil {
		vec, embedErr := useCustomEmbedder.EmbedStrings(ctx, []string{text})
		if embedErr != nil {
			logger.Warn("自定义嵌入失败，回退到全局Embedding", logger.ErrorField(embedErr))
			useCustomEmbedder = nil
		} else if len(vec) > 0 {
			embeddings = float64ToFloat32(vec[0])
		} else {
			logger.Warn("自定义嵌入返回空向量，回退到全局Embedding")
			useCustomEmbedder = nil
		}
	}

	if useCustomEmbedder == nil {
		if r.einoClient != nil && r.einoClient.HasEmbedder() {
			vec, embedErr := r.einoClient.EmbedText(ctx, text)
			if embedErr != nil {
				logger.Error("全局嵌入失败", logger.ErrorField(embedErr))
				return nil, fmt.Errorf("嵌入失败，无法生成向量: %w", embedErr)
			}
			embeddings = float64ToFloat32(vec)
		} else {
			logger.Error("无可用的Embedder，无法生成向量")
			return nil, fmt.Errorf("无可用的Embedder，无法生成向量")
		}
	}

	vectorID := fmt.Sprintf("%d", time.Now().UnixNano())

	if metadata == nil {
		metadata = map[string]string{}
	}
	if _, hasContent := metadata["content"]; !hasContent {
		metadata["content"] = text
	}

	if r.milvus != nil {
		collectionName := "vector_" + collectionID
		has, hasErr := r.milvus.HasCollection(ctx, collectionName)
		if hasErr != nil {
			logger.Warn("检查Milvus集合是否存在失败", logger.StringField("collection", collectionName), logger.ErrorField(hasErr))
		}
		if has {
			existingDim, dimErr := r.milvus.GetCollectionDimension(ctx, collectionName)
			if dimErr == nil && existingDim != len(embeddings) {
				logger.Warn("集合维度不匹配，需要重建集合",
					logger.StringField("collection", collectionName),
					logger.IntField("existing_dim", existingDim),
					logger.IntField("new_dim", len(embeddings)))
				if dropErr := r.milvus.DropCollection(ctx, collectionName); dropErr != nil {
					logger.Error("删除旧集合失败", logger.StringField("collection", collectionName), logger.ErrorField(dropErr))
				} else {
					logger.Info("旧集合已删除，准备重建", logger.StringField("collection", collectionName))
					has = false
				}
			}
		}
		if !has {
			dim := len(embeddings)
			if dim == 0 {
				dim = 768
			}
			logger.Info("Milvus集合不存在，自动创建", logger.StringField("collection", collectionName), logger.IntField("dimension", dim))
			if createErr := r.milvus.CreateCollection(ctx, collectionName, dim); createErr != nil {
				logger.Error("自动创建Milvus集合失败", logger.StringField("collection", collectionName), logger.ErrorField(createErr))
				return nil, fmt.Errorf("自动创建Milvus集合失败: %w", createErr)
			}
			if idxErr := r.milvus.CreateIndex(ctx, collectionName, "vector", "FLAT", nil); idxErr != nil {
				logger.Warn("自动创建索引失败", logger.StringField("collection", collectionName), logger.ErrorField(idxErr))
			}
			if loadErr := r.milvus.LoadCollection(ctx, collectionName, 1); loadErr != nil {
				logger.Warn("自动加载集合失败", logger.StringField("collection", collectionName), logger.ErrorField(loadErr))
			}
		}
		insertErr := r.milvus.Insert(ctx, collectionName, []string{vectorID}, [][]float32{embeddings}, metadata["content"])
		if insertErr != nil {
			logger.Error("插入向量到Milvus失败", logger.StringField("collection_id", collectionID), logger.ErrorField(insertErr))
			return nil, fmt.Errorf("插入向量到Milvus失败: %w", insertErr)
		}
	} else {
		logger.Warn("Milvus未连接，向量仅返回不存储")
	}

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
	var contents []string

	collection, _ := r.GetCollection(ctx, collectionID)
	if collection == nil {
		return nil, fmt.Errorf("集合 %s 不存在，请先创建集合", collectionID)
	}
	var useCustomEmbedder embedding.Embedder = nil
	if collection != nil && collection.Embedding.Model != "" && collection.Embedding.APIKey != "" {
		var createErr error
		useCustomEmbedder, createErr = eino.NewDynamicEmbedder(
			collection.Embedding.APIKey,
			collection.Embedding.Model,
			collection.Embedding.BaseURL,
		)
		if createErr != nil {
			logger.Warn("创建动态Embedder失败，使用全局配置", logger.ErrorField(createErr))
			useCustomEmbedder = nil
		}
	}

	if useCustomEmbedder != nil {
		vecs, embedErr := useCustomEmbedder.EmbedStrings(ctx, texts)
		if embedErr != nil {
			logger.Warn("自定义批量嵌入失败，回退到全局Embedding", logger.ErrorField(embedErr))
			useCustomEmbedder = nil
		} else if len(vecs) > 0 {
			allEmbeddings = make([][]float32, len(vecs))
			for i, vec := range vecs {
				allEmbeddings[i] = float64ToFloat32(vec)
			}
		} else {
			logger.Warn("自定义批量嵌入返回空，回退到全局Embedding")
			useCustomEmbedder = nil
		}
	}

	if useCustomEmbedder == nil {
		if r.einoClient != nil && r.einoClient.HasEmbedder() {
			batchEmbeddings, batchErr := r.einoClient.BatchEmbedText(ctx, texts)
			if batchErr != nil {
				logger.Error("全局批量嵌入失败", logger.ErrorField(batchErr))
				return nil, fmt.Errorf("批量嵌入失败，无法生成向量: %w", batchErr)
			}
			allEmbeddings = make([][]float32, len(batchEmbeddings))
			for i, emb := range batchEmbeddings {
				allEmbeddings[i] = float64ToFloat32(emb)
			}
		} else {
			logger.Error("无可用的Embedder，无法生成向量")
			return nil, fmt.Errorf("无可用的Embedder，无法生成向量")
		}
	}

	ids := make([]string, len(texts))
	for i := range texts {
		metadata := map[string]string{}
		if i < len(metadataList) {
			for k, v := range metadataList[i] {
				metadata[k] = v
			}
		}
		if _, hasContent := metadata["content"]; !hasContent {
			metadata["content"] = texts[i]
		}
		contents = append(contents, metadata["content"])
		ids[i] = fmt.Sprintf("%d_%d", time.Now().UnixNano(), i)
		vectors = append(vectors, &model.Vector{
			ID:        ids[i],
			Values:    float32ToFloat64(allEmbeddings[i]),
			Metadata:  metadata,
			CreatedAt: time.Now(),
		})
	}

	if r.milvus != nil {
		collectionName := "vector_" + collectionID
		has, hasErr := r.milvus.HasCollection(ctx, collectionName)
		if hasErr != nil {
			logger.Warn("检查Milvus集合是否存在失败", logger.StringField("collection", collectionName), logger.ErrorField(hasErr))
		}
		if has {
			newDim := 0
			if len(allEmbeddings) > 0 {
				newDim = len(allEmbeddings[0])
			}
			existingDim, dimErr := r.milvus.GetCollectionDimension(ctx, collectionName)
			if dimErr == nil && existingDim != newDim {
				logger.Warn("集合维度不匹配，需要重建集合",
					logger.StringField("collection", collectionName),
					logger.IntField("existing_dim", existingDim),
					logger.IntField("new_dim", newDim))
				if dropErr := r.milvus.DropCollection(ctx, collectionName); dropErr != nil {
					logger.Error("删除旧集合失败", logger.StringField("collection", collectionName), logger.ErrorField(dropErr))
				} else {
					logger.Info("旧集合已删除，准备重建", logger.StringField("collection", collectionName))
					has = false
				}
			}
		}
		if !has {
			dim := 768
			if len(allEmbeddings) > 0 && len(allEmbeddings[0]) > 0 {
				dim = len(allEmbeddings[0])
			}
			logger.Info("Milvus集合不存在，自动创建", logger.StringField("collection", collectionName), logger.IntField("dimension", dim))
			if createErr := r.milvus.CreateCollection(ctx, collectionName, dim); createErr != nil {
				logger.Error("自动创建Milvus集合失败", logger.StringField("collection", collectionName), logger.ErrorField(createErr))
				return nil, fmt.Errorf("自动创建Milvus集合失败: %w", createErr)
			}
			if idxErr := r.milvus.CreateIndex(ctx, collectionName, "vector", "FLAT", nil); idxErr != nil {
				logger.Warn("自动创建索引失败", logger.StringField("collection", collectionName), logger.ErrorField(idxErr))
			}
			if loadErr := r.milvus.LoadCollection(ctx, collectionName, 1); loadErr != nil {
				logger.Warn("自动加载集合失败", logger.StringField("collection", collectionName), logger.ErrorField(loadErr))
			}
		}
		insertErr := r.milvus.Insert(ctx, collectionName, ids, allEmbeddings, contents...)
		if insertErr != nil {
			logger.Error("批量插入向量到Milvus失败", logger.StringField("collection_id", collectionID), logger.ErrorField(insertErr))
			return nil, fmt.Errorf("批量插入向量到Milvus失败: %w", insertErr)
		}
	} else {
		logger.Warn("Milvus未连接，批量向量仅返回不存储")
	}

	logger.Info("批量向量化成功",
		logger.IntField("count", len(vectors)))

	return vectors, nil
}

func (r *vectorRepository) Search(ctx context.Context, collectionID string, queryVector []float64, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	if r.milvus == nil {
		logger.Warn("Milvus未连接，搜索返回空结果")
		return []*model.SearchResultItem{}, nil
	}
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
		metadata := map[string]string{}
		if result.Content != "" {
			metadata["content"] = result.Content
		}
		searchResults = append(searchResults, &model.SearchResultItem{
			VectorID: result.ID,
			Score:    float64(result.Score),
			Metadata: metadata,
		})
	}

	logger.Info("向量搜索成功",
		logger.IntField("count", len(searchResults)))

	return searchResults, nil
}

func (r *vectorRepository) TextSearch(ctx context.Context, collectionID string, text string, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	startAll := time.Now()
	if r.milvus == nil {
		logger.Warn("Milvus未连接，文本搜索返回空结果")
		return []*model.SearchResultItem{}, nil
	}
	logger.Info("文本搜索",
		logger.StringField("collection_id", collectionID),
		logger.StringField("text", text),
		logger.IntField("top_k", topK))

	var embeddings []float32

	t0 := time.Now()
	collection, _ := r.GetCollection(ctx, collectionID)
	logTiming("GetCollection", t0, logger.StringField("collection_id", collectionID))

	var useCustomEmbedder embedding.Embedder = nil
	if collection != nil && collection.Embedding.Model != "" && collection.Embedding.APIKey != "" {
		logger.Info("使用集合自定义Embedding",
			logger.StringField("model", collection.Embedding.Model),
			logger.StringField("base_url", collection.Embedding.BaseURL))
		t1 := time.Now()
		var createErr error
		useCustomEmbedder, createErr = eino.NewDynamicEmbedder(
			collection.Embedding.APIKey,
			collection.Embedding.Model,
			collection.Embedding.BaseURL,
		)
		logTiming("创建动态Embedder", t1, logger.StringField("model", collection.Embedding.Model))
		if createErr != nil {
			logger.Warn("创建动态Embedder失败，使用全局配置", logger.ErrorField(createErr))
			useCustomEmbedder = nil
		}
	}

	if useCustomEmbedder != nil {
		t2 := time.Now()
		vec, embedErr := useCustomEmbedder.EmbedStrings(ctx, []string{text})
		logTiming("自定义Embedding API调用", t2, logger.StringField("model", collection.Embedding.Model))
		if embedErr != nil {
			logger.Warn("自定义嵌入失败，回退到全局Embedding", logger.ErrorField(embedErr))
			useCustomEmbedder = nil
		} else if len(vec) > 0 {
			embeddings = float64ToFloat32(vec[0])
		} else {
			logger.Warn("自定义嵌入返回空向量，回退到全局Embedding")
			useCustomEmbedder = nil
		}
	}

	if useCustomEmbedder == nil {
		if r.einoClient != nil && r.einoClient.HasEmbedder() {
			t3 := time.Now()
			vec, embedErr := r.einoClient.EmbedText(ctx, text)
			logTiming("全局Embedding API调用", t3)
			if embedErr != nil {
				logger.Error("全局嵌入失败",
					logger.ErrorField(embedErr))
				return []*model.SearchResultItem{}, embedErr
			}
			embeddings = float64ToFloat32(vec)
		} else {
			logger.Warn("Eino未初始化或无Embedder，无法执行文本搜索")
			return []*model.SearchResultItem{}, nil
		}
	}

	t4 := time.Now()
	collectionName := "vector_" + collectionID

	if len(embeddings) > 0 {
		existingDim, dimErr := r.milvus.GetCollectionDimension(ctx, collectionName)
		if dimErr == nil && existingDim != len(embeddings) {
			logger.Warn("搜索向量维度与集合不匹配，跳过向量搜索",
				logger.StringField("collection", collectionName),
				logger.IntField("collection_dim", existingDim),
				logger.IntField("query_dim", len(embeddings)))
			return []*model.SearchResultItem{}, nil
		}
	}

	results, err := r.milvus.Search(ctx, collectionName, embeddings, topK, "IP")
	logTiming("Milvus搜索", t4, logger.StringField("collection", collectionName))
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
		metadata := map[string]string{}
		if result.Content != "" {
			metadata["content"] = result.Content
		}
		searchResults = append(searchResults, &model.SearchResultItem{
			VectorID: result.ID,
			Score:    float64(result.Score),
			Metadata: metadata,
		})
	}

	logTiming("TextSearch总计", startAll,
		logger.StringField("collection_id", collectionID),
		logger.IntField("results", len(searchResults)))

	return searchResults, nil
}

func (r *vectorRepository) DeleteVector(ctx context.Context, collectionID string, vectorID string) error {
	if r.milvus == nil {
		return r.milvusUnavailableError("DeleteVector")
	}
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
	if r.milvus == nil {
		return r.milvusUnavailableError("BatchDeleteVector")
	}
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

func (r *vectorRepository) ListVectors(ctx context.Context, collectionID string, page, pageSize int) ([]*model.VectorPreviewItem, int64, error) {
	if r.milvus == nil {
		return nil, 0, r.milvusUnavailableError("ListVectors")
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	collectionName := "vector_" + collectionID

	previews, total, err := r.milvus.QueryVectors(ctx, collectionName, offset, pageSize)
	if err != nil {
		logger.Error("查询向量预览失败",
			logger.StringField("collection_id", collectionID),
			logger.ErrorField(err))
		return nil, 0, err
	}

	items := make([]*model.VectorPreviewItem, 0, len(previews))
	for _, p := range previews {
		items = append(items, &model.VectorPreviewItem{
			ID:      p.ID,
			Content: p.Content,
		})
	}

	logger.Info("查询向量预览成功",
		logger.StringField("collection_id", collectionID),
		logger.IntField("count", len(items)),
		logger.Int64Field("total", total))

	return items, total, nil
}

func recordToModel(rec *model.VectorCollectionRecord) *model.VectorCollection {
	return &model.VectorCollection{
		ID:        rec.ID,
		Name:      rec.Name,
		ModelType: model.VectorModelType(rec.ModelType),
		IndexType: model.IndexType(rec.IndexType),
		Dimension: rec.Dimension,
		Size:      rec.Size,
		VLM: model.ModelConfig{
			Model:   rec.VlmModel,
			BaseURL: rec.VlmBaseURL,
			APIKey:  rec.VlmApiKey,
		},
		LLM: model.ModelConfig{
			Model:   rec.LlmModel,
			BaseURL: rec.LlmBaseURL,
			APIKey:  rec.LlmApiKey,
		},
		ASR: model.ModelConfig{
			Model:   rec.AsrModel,
			BaseURL: rec.AsrBaseURL,
			APIKey:  rec.AsrApiKey,
		},
		Embedding: model.ModelConfig{
			Model:   rec.EmbeddingModel,
			BaseURL: rec.EmbeddingBaseURL,
			APIKey:  rec.EmbeddingApiKey,
		},
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
}

func collectionToRecord(coll *model.VectorCollection) *model.VectorCollectionRecord {
	return &model.VectorCollectionRecord{
		ID:               coll.ID,
		Name:             coll.Name,
		ModelType:        int(coll.ModelType),
		IndexType:        int(coll.IndexType),
		Dimension:        coll.Dimension,
		Size:             coll.Size,
		VlmModel:         coll.VLM.Model,
		VlmBaseURL:       coll.VLM.BaseURL,
		VlmApiKey:        coll.VLM.APIKey,
		LlmModel:         coll.LLM.Model,
		LlmBaseURL:       coll.LLM.BaseURL,
		LlmApiKey:        coll.LLM.APIKey,
		AsrModel:         coll.ASR.Model,
		AsrBaseURL:       coll.ASR.BaseURL,
		AsrApiKey:        coll.ASR.APIKey,
		EmbeddingModel:   coll.Embedding.Model,
		EmbeddingBaseURL: coll.Embedding.BaseURL,
		EmbeddingApiKey:  coll.Embedding.APIKey,
		CreatedAt:        coll.CreatedAt,
		UpdatedAt:        coll.UpdatedAt,
	}
}
