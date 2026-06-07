package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"Logos/internal/service/ai/vector/dao"
	"Logos/internal/service/ai/vector/model"
	"Logos/pkg/auth"
	"Logos/pkg/logger"
)

var (
	ErrCollectionNotFound = errors.New("collection not found")
	ErrInvalidDimension   = errors.New("invalid dimension")
	ErrInternalServer     = errors.New("internal server error")
)

type VectorService interface {
	// 集合管理
	CreateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error)
	UpdateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error)
	DeleteCollection(ctx context.Context, id string) error
	GetCollection(ctx context.Context, id string) (*model.VectorCollection, error)
	ListCollections(ctx context.Context) ([]*model.VectorCollection, error)

	// 向量化
	Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error)
	BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]*model.Vector, error)

	// 相似度搜索
	Search(ctx context.Context, collectionID string, queryVector []float64, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error)
	TextSearch(ctx context.Context, collectionID string, text string, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error)

	// 向量管理
	DeleteVector(ctx context.Context, collectionID string, vectorID string) error
	BatchDeleteVector(ctx context.Context, collectionID string, vectorIDs []string) error

	ListVectors(ctx context.Context, collectionID string, page, pageSize int) ([]*model.VectorPreviewItem, int64, error)
}

type vectorServiceImpl struct {
	repo dao.VectorRepository
}

func NewVectorService(repo dao.VectorRepository) VectorService {
	return &vectorServiceImpl{
		repo: repo,
	}
}

func getUserIDFromContext(ctx context.Context) string {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return ""
	}
	return userID
}

func (s *vectorServiceImpl) CreateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error) {
	logger.Info("创建向量集合请求",
		logger.StringField("name", req.Name),
		logger.IntField("dimension", req.Dimension))

	if req.Name == "" {
		return nil, errors.New("集合名称不能为空")
	}
	if req.Dimension <= 0 {
		return nil, ErrInvalidDimension
	}

	if req.ID == "" {
		req.ID = fmt.Sprintf("col_%d", time.Now().UnixNano())
	}
	req.UserID = getUserIDFromContext(ctx)
	req.CreatedAt = time.Now()
	req.UpdatedAt = time.Now()
	req.Size = 0

	if err := s.repo.CreateCollection(ctx, req); err != nil {
		logger.Error("创建向量集合失败",
			logger.StringField("name", req.Name),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("创建向量集合成功",
		logger.StringField("id", req.ID),
		logger.StringField("name", req.Name))

	return req, nil
}

func (s *vectorServiceImpl) UpdateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error) {
	logger.Info("更新向量集合请求",
		logger.StringField("id", req.ID))

	if req.ID == "" {
		return nil, errors.New("集合ID不能为空")
	}

	existing, err := s.repo.GetCollection(ctx, req.ID, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("查询向量集合失败",
			logger.StringField("id", req.ID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if existing == nil {
		return nil, ErrCollectionNotFound
	}

	req.UpdatedAt = time.Now()
	if req.Name == "" {
		req.Name = existing.Name
	}
	if req.Dimension <= 0 {
		req.Dimension = existing.Dimension
	}
	if req.ModelType == 0 {
		req.ModelType = existing.ModelType
	}
	if req.IndexType == 0 {
		req.IndexType = existing.IndexType
	}
	if req.VLM.Model == "" && req.VLM.BaseURL == "" && req.VLM.APIKey == "" {
		req.VLM = existing.VLM
	}
	if req.LLM.Model == "" && req.LLM.BaseURL == "" && req.LLM.APIKey == "" {
		req.LLM = existing.LLM
	}
	if req.ASR.Model == "" && req.ASR.BaseURL == "" && req.ASR.APIKey == "" {
		req.ASR = existing.ASR
	}
	if req.Embedding.Model == "" && req.Embedding.BaseURL == "" && req.Embedding.APIKey == "" {
		req.Embedding = existing.Embedding
	}
	if req.Parameters == nil {
		req.Parameters = existing.Parameters
	}
	req.CreatedAt = existing.CreatedAt
	req.Size = existing.Size

	if err := s.repo.UpdateCollection(ctx, req); err != nil {
		logger.Error("更新向量集合失败",
			logger.StringField("id", req.ID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("更新向量集合成功",
		logger.StringField("id", req.ID))

	return req, nil
}

func (s *vectorServiceImpl) DeleteCollection(ctx context.Context, id string) error {
	logger.Info("删除向量集合请求",
		logger.StringField("id", id))

	if id == "" {
		return errors.New("集合ID不能为空")
	}

	if err := s.repo.DeleteCollection(ctx, id, getUserIDFromContext(ctx)); err != nil {
		logger.Error("删除向量集合失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("删除向量集合成功",
		logger.StringField("id", id))

	return nil
}

func (s *vectorServiceImpl) ListVectors(ctx context.Context, collectionID string, page, pageSize int) ([]*model.VectorPreviewItem, int64, error) {
	logger.Info("列出向量预览请求",
		logger.StringField("collection_id", collectionID),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	if collectionID == "" {
		return nil, 0, errors.New("集合ID不能为空")
	}

	items, total, err := s.repo.ListVectors(ctx, collectionID, page, pageSize)
	if err != nil {
		logger.Error("列出向量预览失败",
			logger.StringField("collection_id", collectionID),
			logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	logger.Info("列出向量预览成功",
		logger.StringField("collection_id", collectionID),
		logger.IntField("count", len(items)),
		logger.Int64Field("total", total))

	return items, total, nil
}

func (s *vectorServiceImpl) GetCollection(ctx context.Context, id string) (*model.VectorCollection, error) {
	logger.Info("获取向量集合请求",
		logger.StringField("id", id))

	if id == "" {
		return nil, errors.New("集合ID不能为空")
	}

	collection, err := s.repo.GetCollection(ctx, id, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("获取向量集合失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if collection == nil {
		return nil, ErrCollectionNotFound
	}

	logger.Info("获取向量集合成功",
		logger.StringField("id", id))

	return collection, nil
}

func (s *vectorServiceImpl) ListCollections(ctx context.Context) ([]*model.VectorCollection, error) {
	logger.Info("列出向量集合请求")

	collections, err := s.repo.ListCollections(ctx, getUserIDFromContext(ctx))
	if err != nil {
		logger.Error("列出向量集合失败",
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("列出向量集合成功",
		logger.IntField("count", len(collections)))

	return collections, nil
}

func (s *vectorServiceImpl) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error) {
	logger.Info("文本向量化请求",
		logger.StringField("text", text),
		logger.StringField("collection_id", collectionID))

	if text == "" {
		return nil, errors.New("文本不能为空")
	}
	if collectionID == "" {
		return nil, errors.New("集合ID不能为空")
	}

	vector, err := s.repo.Vectorize(ctx, text, collectionID, metadata)
	if err != nil {
		logger.Error("文本向量化失败",
			logger.StringField("text", text),
			logger.ErrorField(err))
		return nil, fmt.Errorf("文本向量化失败: %w", err)
	}

	logger.Info("文本向量化成功",
		logger.StringField("vector_id", vector.ID))

	return vector, nil
}

func (s *vectorServiceImpl) BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]*model.Vector, error) {
	logger.Info("批量文本向量化请求",
		logger.IntField("count", len(texts)),
		logger.StringField("collection_id", collectionID))

	if len(texts) == 0 {
		return nil, errors.New("文本列表不能为空")
	}
	if collectionID == "" {
		return nil, errors.New("集合ID不能为空")
	}

	vectors, err := s.repo.BatchVectorize(ctx, texts, collectionID, metadataList)
	if err != nil {
		logger.Error("批量文本向量化失败",
			logger.ErrorField(err))
		return nil, fmt.Errorf("批量文本向量化失败: %w", err)
	}

	logger.Info("批量文本向量化成功",
		logger.IntField("count", len(vectors)))

	return vectors, nil
}

func (s *vectorServiceImpl) Search(ctx context.Context, collectionID string, queryVector []float64, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	logger.Info("向量搜索请求",
		logger.StringField("collection_id", collectionID),
		logger.IntField("top_k", topK))

	if collectionID == "" {
		return nil, errors.New("集合ID不能为空")
	}
	if len(queryVector) == 0 {
		return nil, errors.New("查询向量不能为空")
	}
	if topK <= 0 {
		topK = 10
	}

	results, err := s.repo.Search(ctx, collectionID, queryVector, topK, threshold, filter)
	if err != nil {
		logger.Error("向量搜索失败",
			logger.StringField("collection_id", collectionID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("向量搜索成功",
		logger.IntField("count", len(results)))

	return results, nil
}

func (s *vectorServiceImpl) TextSearch(ctx context.Context, collectionID string, text string, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	logger.Info("文本搜索请求",
		logger.StringField("collection_id", collectionID),
		logger.StringField("text", text),
		logger.IntField("top_k", topK))

	if collectionID == "" {
		return nil, errors.New("集合ID不能为空")
	}
	if text == "" {
		return nil, errors.New("文本不能为空")
	}
	if topK <= 0 {
		topK = 10
	}

	results, err := s.repo.TextSearch(ctx, collectionID, text, topK, threshold, filter)
	if err != nil {
		logger.Error("文本搜索失败",
			logger.StringField("collection_id", collectionID),
			logger.StringField("text", text),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("文本搜索成功",
		logger.IntField("count", len(results)))

	return results, nil
}

func (s *vectorServiceImpl) DeleteVector(ctx context.Context, collectionID string, vectorID string) error {
	logger.Info("删除向量请求",
		logger.StringField("collection_id", collectionID),
		logger.StringField("vector_id", vectorID))

	if collectionID == "" {
		return errors.New("集合ID不能为空")
	}
	if vectorID == "" {
		return errors.New("向量ID不能为空")
	}

	if err := s.repo.DeleteVector(ctx, collectionID, vectorID); err != nil {
		logger.Error("删除向量失败",
			logger.StringField("collection_id", collectionID),
			logger.StringField("vector_id", vectorID),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("删除向量成功",
		logger.StringField("vector_id", vectorID))

	return nil
}

func (s *vectorServiceImpl) BatchDeleteVector(ctx context.Context, collectionID string, vectorIDs []string) error {
	logger.Info("批量删除向量请求",
		logger.StringField("collection_id", collectionID),
		logger.IntField("count", len(vectorIDs)))

	if collectionID == "" {
		return errors.New("集合ID不能为空")
	}
	if len(vectorIDs) == 0 {
		return nil
	}

	if err := s.repo.BatchDeleteVector(ctx, collectionID, vectorIDs); err != nil {
		logger.Error("批量删除向量失败",
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("批量删除向量成功",
		logger.IntField("count", len(vectorIDs)))

	return nil
}
