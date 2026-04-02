package service

import (
	"context"
	"errors"

	"Noah/internal/search/dao"
	"Noah/internal/search/model"
	"Noah/pkg/logger"
)

var (
	ErrDocumentNotFound = errors.New("文档不存在")
	ErrInternalServer   = errors.New("服务器内部错误")
)

type SearchService interface {
	// 搜索
	Search(ctx context.Context, query string, indexType model.IndexType, page, pageSize int, conditions []model.SearchCondition, sorts []model.SortCondition, filters map[string]string) ([]*model.SearchResultItem, int64, error)

	// 文档管理
	AddDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error)
	UpdateDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error)
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

type searchServiceImpl struct {
	repo dao.SearchRepository
}

func NewSearchService(repo dao.SearchRepository) SearchService {
	return &searchServiceImpl{
		repo: repo,
	}
}

func (s *searchServiceImpl) Search(ctx context.Context, query string, indexType model.IndexType, page, pageSize int, conditions []model.SearchCondition, sorts []model.SortCondition, filters map[string]string) ([]*model.SearchResultItem, int64, error) {
	logger.Info("搜索请求",
		logger.StringField("query", query),
		logger.IntField("index_type", int(indexType)),
		logger.IntField("page", page),
		logger.IntField("page_size", pageSize))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	results, total, err := s.repo.Search(ctx, query, indexType, page, pageSize, conditions, sorts, filters)
	if err != nil {
		logger.Error("搜索失败",
			logger.StringField("query", query),
			logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	logger.Info("搜索成功",
		logger.StringField("query", query),
		logger.IntField("count", len(results)),
		logger.Int64Field("total", total))

	return results, total, nil
}

func (s *searchServiceImpl) AddDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error) {
	logger.Info("添加文档请求",
		logger.IntField("type", int(doc.Type)),
		logger.StringField("title", doc.Title))

	if doc.Title == "" {
		return nil, errors.New("标题不能为空")
	}

	if err := s.repo.AddDocument(ctx, doc); err != nil {
		logger.Error("添加文档失败",
			logger.StringField("title", doc.Title),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("添加文档成功",
		logger.StringField("id", doc.ID),
		logger.StringField("title", doc.Title))

	return doc, nil
}

func (s *searchServiceImpl) UpdateDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error) {
	logger.Info("更新文档请求",
		logger.StringField("id", doc.ID))

	if doc.ID == "" {
		return nil, errors.New("文档ID不能为空")
	}

	existing, err := s.repo.GetDocument(ctx, doc.ID)
	if err != nil {
		logger.Error("查询文档失败",
			logger.StringField("id", doc.ID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if existing == nil {
		return nil, ErrDocumentNotFound
	}

	if err := s.repo.UpdateDocument(ctx, doc); err != nil {
		logger.Error("更新文档失败",
			logger.StringField("id", doc.ID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("更新文档成功",
		logger.StringField("id", doc.ID))

	return doc, nil
}

func (s *searchServiceImpl) DeleteDocument(ctx context.Context, id string) error {
	logger.Info("删除文档请求",
		logger.StringField("id", id))

	if id == "" {
		return errors.New("文档ID不能为空")
	}

	if err := s.repo.DeleteDocument(ctx, id); err != nil {
		logger.Error("删除文档失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("删除文档成功",
		logger.StringField("id", id))

	return nil
}

func (s *searchServiceImpl) GetDocument(ctx context.Context, id string) (*model.SearchDocument, error) {
	logger.Info("获取文档请求",
		logger.StringField("id", id))

	if id == "" {
		return nil, errors.New("文档ID不能为空")
	}

	doc, err := s.repo.GetDocument(ctx, id)
	if err != nil {
		logger.Error("获取文档失败",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	logger.Info("获取文档成功",
		logger.StringField("id", id))

	return doc, nil
}

func (s *searchServiceImpl) BatchAddDocuments(ctx context.Context, docs []*model.SearchDocument) error {
	logger.Info("批量添加文档请求",
		logger.IntField("count", len(docs)))

	if len(docs) == 0 {
		return nil
	}

	if err := s.repo.BatchAddDocuments(ctx, docs); err != nil {
		logger.Error("批量添加文档失败",
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("批量添加文档成功",
		logger.IntField("count", len(docs)))

	return nil
}

func (s *searchServiceImpl) BatchDeleteDocuments(ctx context.Context, ids []string) error {
	logger.Info("批量删除文档请求",
		logger.IntField("count", len(ids)))

	if len(ids) == 0 {
		return nil
	}

	if err := s.repo.BatchDeleteDocuments(ctx, ids); err != nil {
		logger.Error("批量删除文档失败",
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("批量删除文档成功",
		logger.IntField("count", len(ids)))

	return nil
}

func (s *searchServiceImpl) CreateIndex(ctx context.Context, indexType model.IndexType) error {
	logger.Info("创建索引请求",
		logger.IntField("type", int(indexType)))

	if err := s.repo.CreateIndex(ctx, indexType); err != nil {
		logger.Error("创建索引失败",
			logger.IntField("type", int(indexType)),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("创建索引成功",
		logger.IntField("type", int(indexType)))

	return nil
}

func (s *searchServiceImpl) DeleteIndex(ctx context.Context, indexType model.IndexType) error {
	logger.Info("删除索引请求",
		logger.IntField("type", int(indexType)))

	if err := s.repo.DeleteIndex(ctx, indexType); err != nil {
		logger.Error("删除索引失败",
			logger.IntField("type", int(indexType)),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("删除索引成功",
		logger.IntField("type", int(indexType)))

	return nil
}

func (s *searchServiceImpl) RefreshIndex(ctx context.Context, indexType model.IndexType) error {
	logger.Info("刷新索引请求",
		logger.IntField("type", int(indexType)))

	if err := s.repo.RefreshIndex(ctx, indexType); err != nil {
		logger.Error("刷新索引失败",
			logger.IntField("type", int(indexType)),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("刷新索引成功",
		logger.IntField("type", int(indexType)))

	return nil
}

func (s *searchServiceImpl) GetIndexStats(ctx context.Context) ([]*model.IndexStats, error) {
	logger.Info("获取索引统计请求")

	stats, err := s.repo.GetIndexStats(ctx)
	if err != nil {
		logger.Error("获取索引统计失败",
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("获取索引统计成功")

	return stats, nil
}
