package dao

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"Logos/internal/service/ai/vector/model"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/pagination"
	"Logos/pkg/vector"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/milvus-io/milvus/client/v2/entity"
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
	DeleteCollection(ctx context.Context, id string, userID string) error
	GetCollection(ctx context.Context, id string, userID string) (*model.VectorCollection, error)
	ListCollections(ctx context.Context, userID string) ([]*model.VectorCollection, error)

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

// collectionMetadataRollbackTimeout 是创建集合失败后补偿删除 Milvus 集合的
// 独立预算。请求 ctx 可能已经取消（写库失败常常正是因为超时），补偿必须
// 用 WithoutCancel 派生，同时保留一个上限，避免回滚本身把请求挂住。
const collectionMetadataRollbackTimeout = 10 * time.Second

func milvusIndexType(indexType model.IndexType) string {
	switch indexType {
	case model.IVF_FLAT:
		return "IVF_FLAT"
	case model.HNSW:
		return "HNSW"
	default:
		return "FLAT"
	}
}

// collectionDimension 从 Milvus schema 中读出向量维度。
// 解析失败必须报错：返回 0 会让调用方误以为「集合没有维度」，
// 从而跳过所有维度一致性校验。
func collectionDimension(coll *entity.Collection) (int, error) {
	if coll == nil || coll.Schema == nil {
		return 0, nil
	}
	for _, field := range coll.Schema.Fields {
		if field == nil || field.Name != "vector" {
			continue
		}
		dimStr, ok := field.TypeParams["dim"]
		if !ok || dimStr == "" {
			return 0, errors.New("向量字段缺少 dim 参数")
		}
		dim, err := strconv.Atoi(dimStr)
		if err != nil {
			return 0, fmt.Errorf("向量维度 %q 不是合法整数: %w", dimStr, err)
		}
		if dim <= 0 {
			return 0, fmt.Errorf("向量维度必须为正数，实际为 %d", dim)
		}
		return dim, nil
	}
	return 0, nil
}

// collectionNeedsReindex 判断本次更新是否真的改变了索引。
// 名称、模型配置的变更不应该触发 DropIndex/CreateIndex。
func collectionNeedsReindex(existing *model.VectorCollectionRecord, coll *model.VectorCollection) bool {
	return int(coll.IndexType) != existing.IndexType ||
		coll.Dimension != existing.Dimension ||
		!maps.Equal(coll.Parameters, existing.Parameters)
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

	indexType := milvusIndexType(collection.IndexType)

	// 创建时就把索引参数写进 Milvus：否则库里的 parameters 只是装饰，
	// 后续更新又会拿这份参数去重建索引，两边永远对不上。
	err = r.milvus.CreateIndex(ctx, collectionName, "vector", indexType, collection.Parameters)
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
			// 元数据写不进去，Milvus 里就留下一个没有归属、列表也查不到的
			// 孤儿集合：用户重试会撞上同名集合，管理员也无从定位。
			// 刚创建的集合必然是空的，补偿删除比留下不可见状态更安全。
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), collectionMetadataRollbackTimeout)
			defer cancel()
			rollbackErr := r.milvus.DropCollection(rollbackCtx, collectionName)
			if rollbackErr != nil {
				logger.Error("保存集合元信息失败，回滚 Milvus 集合也失败",
					logger.StringField("id", collection.ID),
					logger.ErrorField(createErr),
					logger.ErrorField(rollbackErr))
				return fmt.Errorf("保存集合元信息失败: %w（回滚 Milvus 集合失败: %v，需人工清理 %s）",
					createErr, rollbackErr, collectionName)
			}
			logger.Warn("保存集合元信息失败，已回滚 Milvus 集合",
				logger.StringField("id", collection.ID),
				logger.ErrorField(createErr))
			return fmt.Errorf("保存集合元信息失败: %w", createErr)
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
	if r.db == nil {
		// 更新接口的价值在于「改完能读回来」。没有数据库就只能改 Milvus
		// 索引，用户的名称/模型配置依旧会丢，所以直接拒绝。
		return errors.New("数据库未初始化，无法持久化集合更新")
	}
	logger.Info("更新向量集合",
		logger.StringField("id", collection.ID),
		logger.StringField("name", collection.Name))

	collectionName := "vector_" + collection.ID

	// 先确认元信息存在，再动 Milvus。反过来的话，索引已经重建完才发现
	// 没有可写的记录，就留下了一个不可回滚的半成品。
	var existing model.VectorCollectionRecord
	lookupErr := r.db.WithContext(ctx).Where("id = ?", collection.ID).First(&existing).Error
	if lookupErr != nil {
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("集合元信息不存在，无法更新: %s", collection.ID)
		}
		return fmt.Errorf("查询集合元信息失败: %w", lookupErr)
	}

	// 未传的字段沿用库里的值。这里逐字段合并，而不是「三项全空才沿用」：
	// 后者会让「只改 BaseURL」把模型和密钥一并清空。
	if collection.IndexType == 0 {
		collection.IndexType = model.IndexType(existing.IndexType)
	}
	if collection.Parameters == nil {
		collection.Parameters = existing.Parameters
	}
	if collection.Dimension <= 0 {
		collection.Dimension = existing.Dimension
	}

	coll, err := r.milvus.DescribeCollection(ctx, collectionName)
	if err != nil {
		logger.Error("获取集合信息失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return fmt.Errorf("获取集合信息失败: %w", err)
	}

	currentDim, err := collectionDimension(coll)
	if err != nil {
		logger.Error("解析集合维度失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return fmt.Errorf("解析集合维度失败: %w", err)
	}

	if currentDim > 0 {
		if collection.Dimension <= 0 {
			// 元数据缺失时以 Milvus 实际维度为准，顺手把库补全。
			collection.Dimension = currentDim
		} else if collection.Dimension != currentDim {
			return fmt.Errorf("集合 %s 的实际向量维度为 %d，与请求的 %d 不一致：更新接口不能修改维度，请新建集合并重新写入",
				collection.ID, currentDim, collection.Dimension)
		}
	}

	// 只有索引相关字段变化时才 DropIndex/CreateIndex/LoadCollection。
	// 改个名字就重建索引，会让集合在整个重建期间无法搜索。
	indexType := milvusIndexType(collection.IndexType)
	if collectionNeedsReindex(&existing, collection) {
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
	} else {
		logger.Info("索引配置未变化，跳过索引重建",
			logger.StringField("id", collection.ID))
	}

	if err := r.saveCollectionMetadata(ctx, collection); err != nil {
		logger.Error("保存集合元信息失败",
			logger.StringField("id", collection.ID),
			logger.ErrorField(err))
		return err
	}

	logger.Info("更新向量集合成功",
		logger.StringField("id", collection.ID),
		logger.StringField("index_type", indexType))

	return nil
}

// saveCollectionMetadata 只写回本接口允许修改的字段。
// user_id / size / created_at 不在 Select 里，避免一次重命名把归属和
// 统计信息一起覆盖掉。
func (r *vectorRepository) saveCollectionMetadata(ctx context.Context, collection *model.VectorCollection) error {
	rec := collectionToRecord(collection)
	result := r.db.WithContext(ctx).
		Model(&model.VectorCollectionRecord{}).
		Where("id = ?", collection.ID).
		Select(
			"Name", "ModelType", "IndexType", "Dimension", "Parameters",
			"VlmModel", "VlmBaseURL", "VlmApiKey",
			"LlmModel", "LlmBaseURL", "LlmApiKey",
			"AsrModel", "AsrBaseURL", "AsrApiKey",
			"EmbeddingModel", "EmbeddingBaseURL", "EmbeddingApiKey",
		).
		Updates(rec)
	if result.Error != nil {
		return fmt.Errorf("保存集合元信息失败: %w", result.Error)
	}
	return nil
}

func (r *vectorRepository) DeleteCollection(ctx context.Context, id string, userID string) error {
	if r.milvus == nil {
		return r.milvusUnavailableError("DeleteCollection")
	}

	// 校验所有权
	if userID != "" && r.db != nil {
		var count int64
		if countErr := r.db.WithContext(ctx).
			Model(&model.VectorCollectionRecord{}).
			Where("id = ? AND user_id = ?", id, userID).
			Count(&count).Error; countErr != nil {
			// 计数失败时 count 恒为 0，旧实现会把它报成「不存在或无权操作」：
			// 用户以为集合没了，运维也看不到数据库故障。归属校验失败必须
			// 单独成一种事实，且仍然拒绝删除（fail closed）。
			logger.Error("校验集合归属失败",
				logger.StringField("id", id),
				logger.ErrorField(countErr))
			return fmt.Errorf("校验集合归属失败: %w", countErr)
		}
		if count == 0 {
			return fmt.Errorf("集合不存在或无权操作")
		}
	}

	logger.Info("删除向量集合",
		logger.StringField("id", id))

	collectionName := "vector_" + id

	// 先探测再删除，让「删除」可重试：如果上一次已经删掉 Milvus 集合、
	// 只在清理元数据时失败，重试不能因为集合不存在而永远卡住。
	has, err := r.milvus.HasCollection(ctx, collectionName)
	if err != nil {
		logger.Error("检查Milvus集合是否存在失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return err
	}
	if has {
		if dropErr := r.milvus.DropCollection(ctx, collectionName); dropErr != nil {
			logger.Error("删除Milvus集合失败",
				logger.StringField("id", id),
				logger.ErrorField(dropErr))
			return dropErr
		}
	}

	if r.db != nil {
		delQuery := r.db.WithContext(ctx).Where("id = ?", id)
		if userID != "" {
			delQuery = delQuery.Where("user_id = ?", userID)
		}
		if delErr := delQuery.Delete(&model.VectorCollectionRecord{}).Error; delErr != nil {
			// 数据已经删了、记录还在：读接口会返回一个指向空集合的幽灵配置。
			// 上抛错误让调用方重试，重试会因为 has=false 直接走到这里补删记录。
			logger.Error("删除集合数据库记录失败", logger.StringField("id", id), logger.ErrorField(delErr))
			return fmt.Errorf("Milvus 集合已删除，但清理集合元信息失败（可重试）: %w", delErr)
		}
	}

	logger.Info("删除向量集合成功",
		logger.StringField("id", id))

	return nil
}

func (r *vectorRepository) GetCollection(ctx context.Context, id string, userID string) (*model.VectorCollection, error) {
	if r.db != nil {
		var rec model.VectorCollectionRecord
		query := r.db.WithContext(ctx).Where("id = ?", id)
		if userID != "" {
			query = query.Where("user_id = ?", userID)
		}
		lookupErr := query.First(&rec).Error
		if lookupErr == nil {
			coll := recordToModel(&rec)
			coll.Size = r.collectionSize(ctx, id)
			return coll, nil
		}
		// 数据库故障不能降级成「集合不存在」：调用方会据此提示用户
		// 重新创建集合，而内部调用会退回全局 Embedding，产生与集合
		// 维度不一致的向量。只有明确的 Not Found 才允许继续探测。
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			logger.Error("查询集合元信息失败",
				logger.StringField("id", id),
				logger.ErrorField(lookupErr))
			return nil, fmt.Errorf("查询集合元信息失败: %w", lookupErr)
		}
		// 带归属校验的查询在缺少元数据时无法判断所有者。此时若回退到
		// Milvus 探测，返回的集合没有任何 user_id，等于在数据库抖动
		// 期间放开跨用户访问。宁可回答「不存在」。
		if userID != "" {
			return nil, nil
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
	collection.Size = r.collectionSize(ctx, id)
	return collection, nil
}

func (r *vectorRepository) ListCollections(ctx context.Context, userID string) ([]*model.VectorCollection, error) {
	if r.db != nil {
		var records []model.VectorCollectionRecord
		query := r.db.WithContext(ctx).Model(&model.VectorCollectionRecord{})
		if userID != "" {
			query = query.Where("user_id = ?", userID)
		}
		err := query.Find(&records).Error
		if err == nil {
			var collections []*model.VectorCollection
			for i := range records {
				coll := recordToModel(&records[i])
				coll.Size = r.collectionSize(ctx, coll.ID)
				collections = append(collections, coll)
			}
			logger.Info("列出向量集合完成", logger.IntField("count", len(collections)), logger.StringField("user_id", userID))
			return collections, nil
		}

		// 带归属的列表绝不能回退到 Milvus：探测结果只有 ID 和名称，没有
		// user_id。数据库故障时回退，等于把全库集合返回给任意登录用户。
		if userID != "" {
			logger.Error("查询集合列表失败，拒绝回退到无归属信息的 Milvus 探测",
				logger.StringField("user_id", userID),
				logger.ErrorField(err))
			return nil, fmt.Errorf("查询集合列表失败: %w", err)
		}

		// 内部调用（userID 为空）才允许回退，回退结果仅用于运维视角。
		logger.Warn("数据库查询集合列表失败，尝试Milvus回退", logger.ErrorField(err))
	}
	if r.milvus == nil {
		// 两套存储都读不到时，空列表会被调用方当成「该用户没有任何集合」，
		// 与「服务不可用」混为一谈。与 TextSearch 保持一致：报错，不返回空。
		return nil, r.milvusUnavailableError("ListCollections")
	}
	logger.Info("列出所有向量集合(Milvus回退)")
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
		collection.Size = r.collectionSize(ctx, id)
		collections = append(collections, collection)
	}
	logger.Info("列出向量集合完成", logger.IntField("count", len(collections)))
	return collections, nil
}

// collectionSize 读取集合行数，仅用于展示。
//
// 统计失败不影响集合本身可用，所以不向上抛错；但必须留下带集合名的日志。
// 旧实现把「stats 调用失败」「结果缺 row_count」「row_count 解析失败」三层
// 全部写成 `if err == nil` 静默跳过，前端只能看到「0 条向量」，无法区分
// 「集合真的是空的」和「统计根本读不出来」。
func (r *vectorRepository) collectionSize(ctx context.Context, collectionID string) int64 {
	if r.milvus == nil {
		return 0
	}

	collectionName := "vector_" + collectionID
	stats, err := r.milvus.GetCollectionStats(ctx, collectionName)
	if err != nil {
		logger.Warn("读取集合统计失败，按 0 条展示",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return 0
	}

	size, err := parseCollectionSize(stats)
	if err != nil {
		logger.Warn("解析集合统计失败，按 0 条展示",
			logger.StringField("collection", collectionName),
			logger.ErrorField(err))
		return 0
	}
	return size
}

// parseCollectionSize 从统计结果里取出 row_count。
// 缺字段与格式错误都要报出来，不能把「没有统计」当成「0 条」。
func parseCollectionSize(stats map[string]string) (int64, error) {
	raw, ok := stats["row_count"]
	if !ok {
		return 0, errors.New("统计结果缺少 row_count 字段")
	}
	size, err := parseStatInt(raw)
	if err != nil {
		return 0, fmt.Errorf("解析 row_count=%q 失败: %w", raw, err)
	}
	if size < 0 {
		return 0, fmt.Errorf("row_count 不能为负数: %d", size)
	}
	return size, nil
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
		// 不能用 fmt.Sscanf：它会接受 "12abc" 这样的前缀并静默返回 12，
		// 把「数据格式不对」变成「恰好看起来合理的数字」。ParseInt 要求
		// 整个字符串都是数字，非法输入会明确报错。
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, err
		}
		return i, nil
	default:
		return 0, fmt.Errorf("unexpected type %T", val)
	}
}

func (r *vectorRepository) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error) {
	logger.Info("向量化",
		logger.StringField("text", text),
		logger.StringField("collection_id", collectionID))

	var embeddings []float32

	collection, err := r.GetCollection(ctx, collectionID, "")
	if err != nil {
		return nil, fmt.Errorf("查询集合 %s 失败: %w", collectionID, err)
	}
	if collection == nil {
		return nil, fmt.Errorf("集合 %s 不存在，请先创建集合", collectionID)
	}
	customEmbedder, embedderErr := collectionEmbedder(collection)
	if embedderErr != nil {
		return nil, embedderErr
	}

	if customEmbedder != nil {
		// 集合显式配置了嵌入模型时，失败不能回退到全局模型：
		// 不同模型即使维度相同，向量也不在同一语义空间；维度不同
		// 还会触发下面的重建逻辑，把集合里的历史向量一起删掉。
		vecs, embedErr := customEmbedder.EmbedStrings(ctx, []string{text})
		if embedErr != nil {
			return nil, fmt.Errorf("集合 %s 的嵌入模型调用失败: %w", collectionID, embedErr)
		}
		normalized, normErr := toFloat32Vectors(vecs, 1)
		if normErr != nil {
			return nil, fmt.Errorf("集合 %s 的嵌入结果非法: %w", collectionID, normErr)
		}
		embeddings = normalized[0]
	} else {
		if r.einoClient == nil || !r.einoClient.HasEmbedder() {
			logger.Error("无可用的Embedder，无法生成向量")
			return nil, fmt.Errorf("无可用的Embedder，无法生成向量")
		}
		vec, embedErr := r.einoClient.EmbedText(ctx, text)
		if embedErr != nil {
			logger.Error("全局嵌入失败", logger.ErrorField(embedErr))
			return nil, fmt.Errorf("嵌入失败，无法生成向量: %w", embedErr)
		}
		if len(vec) == 0 {
			return nil, fmt.Errorf("全局嵌入返回空向量，无法写入集合 %s", collectionID)
		}
		embeddings = float64ToFloat32(vec)
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
			return nil, fmt.Errorf("检查 Milvus 集合 %s 是否存在失败: %w", collectionName, hasErr)
		}
		if has {
			existingDim, dimErr := r.milvus.GetCollectionDimension(ctx, collectionName)
			if dimErr != nil {
				return nil, fmt.Errorf("查询集合 %s 的向量维度失败: %w", collectionName, dimErr)
			}
			if existingDim != len(embeddings) {
				// 维度不一致时旧实现直接 DropCollection 再重建，
				// 一次上游抖动就会静默删掉整个集合的历史向量。
				// 重建必须由运维显式发起。
				return nil, fmt.Errorf("集合 %s 的向量维度为 %d，与当前嵌入模型输出的 %d 不一致，请显式重建集合后再写入", collectionName, existingDim, len(embeddings))
			}
		}
		if !has {
			dim := len(embeddings)
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

	if len(texts) == 0 {
		return nil, fmt.Errorf("批量向量化的文本列表为空")
	}

	vectors := make([]*model.Vector, 0, len(texts))
	var allEmbeddings [][]float32
	var contents []string

	collection, err := r.GetCollection(ctx, collectionID, "")
	if err != nil {
		return nil, fmt.Errorf("查询集合 %s 失败: %w", collectionID, err)
	}
	if collection == nil {
		return nil, fmt.Errorf("集合 %s 不存在，请先创建集合", collectionID)
	}
	customEmbedder, embedderErr := collectionEmbedder(collection)
	if embedderErr != nil {
		return nil, embedderErr
	}

	if customEmbedder != nil {
		vecs, embedErr := customEmbedder.EmbedStrings(ctx, texts)
		if embedErr != nil {
			return nil, fmt.Errorf("集合 %s 的嵌入模型调用失败: %w", collectionID, embedErr)
		}
		normalized, normErr := toFloat32Vectors(vecs, len(texts))
		if normErr != nil {
			return nil, fmt.Errorf("集合 %s 的批量嵌入结果非法: %w", collectionID, normErr)
		}
		allEmbeddings = normalized
	} else {
		if r.einoClient == nil || !r.einoClient.HasEmbedder() {
			logger.Error("无可用的Embedder，无法生成向量")
			return nil, fmt.Errorf("无可用的Embedder，无法生成向量")
		}
		batchEmbeddings, batchErr := r.einoClient.BatchEmbedText(ctx, texts)
		if batchErr != nil {
			logger.Error("全局批量嵌入失败", logger.ErrorField(batchErr))
			return nil, fmt.Errorf("批量嵌入失败，无法生成向量: %w", batchErr)
		}
		normalized, normErr := toFloat32Vectors(batchEmbeddings, len(texts))
		if normErr != nil {
			return nil, fmt.Errorf("全局批量嵌入结果非法: %w", normErr)
		}
		allEmbeddings = normalized
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
			return nil, fmt.Errorf("检查 Milvus 集合 %s 是否存在失败: %w", collectionName, hasErr)
		}
		if has {
			newDim := len(allEmbeddings[0])
			existingDim, dimErr := r.milvus.GetCollectionDimension(ctx, collectionName)
			if dimErr != nil {
				return nil, fmt.Errorf("查询集合 %s 的向量维度失败: %w", collectionName, dimErr)
			}
			if existingDim != newDim {
				// 与单条写入保持一致：维度不一致说明集合与当前嵌入模型
				// 已经不匹配，删集合会把历史向量一起丢掉。重建必须由
				// 运维显式发起，这里只拒绝写入。
				return nil, fmt.Errorf("集合 %s 的向量维度为 %d，与当前嵌入模型输出的 %d 不一致，请显式重建集合后再写入", collectionName, existingDim, newDim)
			}
		}
		if !has {
			dim := len(allEmbeddings[0])
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
		// 空结果是「确实没搜到」这一业务事实，不能拿来表示「向量库不可用」。
		// 调用方（RAG、推荐）会把空结果当成「没有资料」继续降级，
		// 真正的配置/依赖故障就此消失。
		return nil, r.milvusUnavailableError("Search")
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
		return nil, r.milvusUnavailableError("TextSearch")
	}
	logger.Info("文本搜索",
		logger.StringField("collection_id", collectionID),
		logger.StringField("text", text),
		logger.IntField("top_k", topK))

	var embeddings []float32

	t0 := time.Now()
	collection, err := r.GetCollection(ctx, collectionID, "")
	logTiming("GetCollection", t0, logger.StringField("collection_id", collectionID))
	if err != nil {
		return nil, fmt.Errorf("查询集合 %s 失败: %w", collectionID, err)
	}
	if collection == nil {
		return nil, fmt.Errorf("集合 %s 不存在，请先创建集合", collectionID)
	}

	customEmbedder, embedderErr := collectionEmbedder(collection)
	if embedderErr != nil {
		return nil, embedderErr
	}

	if customEmbedder != nil {
		t2 := time.Now()
		vecs, embedErr := customEmbedder.EmbedStrings(ctx, []string{text})
		logTiming("自定义Embedding API调用", t2, logger.StringField("model", collection.Embedding.Model))
		if embedErr != nil {
			return nil, fmt.Errorf("集合 %s 的嵌入模型调用失败: %w", collectionID, embedErr)
		}
		normalized, normErr := toFloat32Vectors(vecs, 1)
		if normErr != nil {
			return nil, fmt.Errorf("集合 %s 的嵌入结果非法: %w", collectionID, normErr)
		}
		embeddings = normalized[0]
	} else {
		if r.einoClient == nil || !r.einoClient.HasEmbedder() {
			return nil, fmt.Errorf("无可用的Embedder，无法执行文本搜索")
		}
		t3 := time.Now()
		vec, embedErr := r.einoClient.EmbedText(ctx, text)
		logTiming("全局Embedding API调用", t3)
		if embedErr != nil {
			logger.Error("全局嵌入失败", logger.ErrorField(embedErr))
			return nil, fmt.Errorf("全局嵌入失败: %w", embedErr)
		}
		if len(vec) == 0 {
			return nil, fmt.Errorf("全局嵌入返回空向量，无法搜索集合 %s", collectionID)
		}
		embeddings = float64ToFloat32(vec)
	}

	t4 := time.Now()
	collectionName := "vector_" + collectionID

	has, hasErr := r.milvus.HasCollection(ctx, collectionName)
	if hasErr != nil {
		return nil, fmt.Errorf("检查 Milvus 集合 %s 是否存在失败: %w", collectionName, hasErr)
	}
	if !has {
		return nil, fmt.Errorf("集合 %s 在向量库中不存在，请先写入向量", collectionName)
	}
	existingDim, dimErr := r.milvus.GetCollectionDimension(ctx, collectionName)
	if dimErr != nil {
		return nil, fmt.Errorf("查询集合 %s 的向量维度失败: %w", collectionName, dimErr)
	}
	if existingDim != len(embeddings) {
		// 维度不一致时搜索结果没有意义，返回空列表会让调用方
		// 误以为「集合里确实没有相关内容」，必须把配置问题暴露出来。
		return nil, fmt.Errorf("集合 %s 的向量维度为 %d，与当前嵌入模型输出的 %d 不一致，请检查集合的嵌入模型配置", collectionName, existingDim, len(embeddings))
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

// collectionEmbedder 只在集合显式配置了嵌入模型时创建动态 Embedder。
// 只配置了一半（例如有模型名却没有密钥）说明集合配置已经损坏，
// 此时回退全局模型会把两种语义空间的向量混进同一个集合，必须报错。
func collectionEmbedder(collection *model.VectorCollection) (embedding.Embedder, error) {
	if collection == nil {
		return nil, nil
	}
	config := collection.Embedding
	if config.Model == "" && config.APIKey == "" {
		return nil, nil
	}
	if config.Model == "" || config.APIKey == "" {
		return nil, fmt.Errorf("集合 %s 的嵌入模型配置不完整: model=%q, api_key_configured=%t", collection.ID, config.Model, config.APIKey != "")
	}
	embedder, err := eino.NewDynamicEmbedder(config.APIKey, config.Model, config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("集合 %s 的嵌入模型 %s 初始化失败: %w", collection.ID, config.Model, err)
	}
	return embedder, nil
}

// toFloat32Vectors 校验嵌入结果的条数、维度并转成 float32。
// 条数不符会让第 i 条文本拿到别的文本的向量；空向量会让 Milvus
// 建出维度为 0 的集合，这两种情况都必须在写入前失败。
func toFloat32Vectors(vecs [][]float64, want int) ([][]float32, error) {
	if len(vecs) != want {
		return nil, fmt.Errorf("嵌入结果条数为 %d，与输入文本条数 %d 不一致", len(vecs), want)
	}
	if want == 0 {
		return [][]float32{}, nil
	}
	dim := len(vecs[0])
	if dim == 0 {
		return nil, fmt.Errorf("嵌入返回空向量")
	}
	result := make([][]float32, 0, len(vecs))
	for i, vec := range vecs {
		if len(vec) != dim {
			return nil, fmt.Errorf("第 %d 条嵌入向量维度为 %d，与首条 %d 不一致", i, len(vec), dim)
		}
		result = append(result, float64ToFloat32(vec))
	}
	return result, nil
}

func (r *vectorRepository) ListVectors(ctx context.Context, collectionID string, page, pageSize int) ([]*model.VectorPreviewItem, int64, error) {
	if r.milvus == nil {
		logger.Error("ListVectors: milvus manager is nil")
		return nil, 0, r.milvusUnavailableError("ListVectors")
	}

	offset, pageSize := pagination.Offset(page, pageSize)

	collectionName := "vector_" + collectionID

	logger.Info("ListVectors DAO: calling QueryVectors",
		logger.StringField("collection_id", collectionID),
		logger.StringField("collection_name", collectionName),
		logger.IntField("offset", offset),
		logger.IntField("pageSize", pageSize))

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
		ID:         rec.ID,
		Name:       rec.Name,
		ModelType:  model.VectorModelType(rec.ModelType),
		IndexType:  model.IndexType(rec.IndexType),
		Dimension:  rec.Dimension,
		Parameters: rec.Parameters,
		Size:       rec.Size,
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
		UserID:    rec.UserID,
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
		Parameters:       coll.Parameters,
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
		UserID:           coll.UserID,
		CreatedAt:        coll.CreatedAt,
		UpdatedAt:        coll.UpdatedAt,
	}
}
