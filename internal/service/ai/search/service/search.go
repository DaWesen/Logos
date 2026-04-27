package service

import (
	"context"
	"errors"

	"Logos/internal/service/ai/search/dao"
	"Logos/internal/service/ai/search/model"
	"Logos/pkg/logger"
)

var (
	ErrDocumentNotFound = errors.New("Document not found")
	ErrInternalServer   = errors.New("Internal server error")
)

type SearchService interface {
	// ����
	Search(ctx context.Context, query string, indexType model.IndexType, page, pageSize int, conditions []model.SearchCondition, sorts []model.SortCondition, filters map[string]string) ([]*model.SearchResultItem, int64, error)

	// �ĵ�����
	AddDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error)
	UpdateDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error)
	DeleteDocument(ctx context.Context, id string) error
	GetDocument(ctx context.Context, id string) (*model.SearchDocument, error)

	// ��������
	BatchAddDocuments(ctx context.Context, docs []*model.SearchDocument) error
	BatchDeleteDocuments(ctx context.Context, ids []string) error

	// ��������
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
	logger.Info("��������",
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
		logger.Error("����ʧ��",
			logger.StringField("query", query),
			logger.ErrorField(err))
		return nil, 0, ErrInternalServer
	}

	logger.Info("�����ɹ�",
		logger.StringField("query", query),
		logger.IntField("count", len(results)),
		logger.Int64Field("total", total))

	return results, total, nil
}

func (s *searchServiceImpl) AddDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error) {
	logger.Info("�����ĵ�����",
		logger.IntField("type", int(doc.Type)),
		logger.StringField("title", doc.Title))

	if doc.Title == "" {
		return nil, errors.New("���ⲻ��Ϊ��")
	}

	if err := s.repo.AddDocument(ctx, doc); err != nil {
		logger.Error("�����ĵ�ʧ��",
			logger.StringField("title", doc.Title),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�����ĵ��ɹ�",
		logger.StringField("id", doc.ID),
		logger.StringField("title", doc.Title))

	return doc, nil
}

func (s *searchServiceImpl) UpdateDocument(ctx context.Context, doc *model.SearchDocument) (*model.SearchDocument, error) {
	logger.Info("�����ĵ�����",
		logger.StringField("id", doc.ID))

	if doc.ID == "" {
		return nil, errors.New("�ĵ�ID����Ϊ��")
	}

	existing, err := s.repo.GetDocument(ctx, doc.ID)
	if err != nil {
		logger.Error("��ѯ�ĵ�ʧ��",
			logger.StringField("id", doc.ID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if existing == nil {
		return nil, ErrDocumentNotFound
	}

	if err := s.repo.UpdateDocument(ctx, doc); err != nil {
		logger.Error("�����ĵ�ʧ��",
			logger.StringField("id", doc.ID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�����ĵ��ɹ�",
		logger.StringField("id", doc.ID))

	return doc, nil
}

func (s *searchServiceImpl) DeleteDocument(ctx context.Context, id string) error {
	logger.Info("ɾ���ĵ�����",
		logger.StringField("id", id))

	if id == "" {
		return errors.New("�ĵ�ID����Ϊ��")
	}

	if err := s.repo.DeleteDocument(ctx, id); err != nil {
		logger.Error("ɾ���ĵ�ʧ��",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("ɾ���ĵ��ɹ�",
		logger.StringField("id", id))

	return nil
}

func (s *searchServiceImpl) GetDocument(ctx context.Context, id string) (*model.SearchDocument, error) {
	logger.Info("��ȡ�ĵ�����",
		logger.StringField("id", id))

	if id == "" {
		return nil, errors.New("�ĵ�ID����Ϊ��")
	}

	doc, err := s.repo.GetDocument(ctx, id)
	if err != nil {
		logger.Error("��ȡ�ĵ�ʧ��",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	logger.Info("��ȡ�ĵ��ɹ�",
		logger.StringField("id", id))

	return doc, nil
}

func (s *searchServiceImpl) BatchAddDocuments(ctx context.Context, docs []*model.SearchDocument) error {
	logger.Info("���������ĵ�����",
		logger.IntField("count", len(docs)))

	if len(docs) == 0 {
		return nil
	}

	if err := s.repo.BatchAddDocuments(ctx, docs); err != nil {
		logger.Error("���������ĵ�ʧ��",
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("���������ĵ��ɹ�",
		logger.IntField("count", len(docs)))

	return nil
}

func (s *searchServiceImpl) BatchDeleteDocuments(ctx context.Context, ids []string) error {
	logger.Info("����ɾ���ĵ�����",
		logger.IntField("count", len(ids)))

	if len(ids) == 0 {
		return nil
	}

	if err := s.repo.BatchDeleteDocuments(ctx, ids); err != nil {
		logger.Error("����ɾ���ĵ�ʧ��",
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("����ɾ���ĵ��ɹ�",
		logger.IntField("count", len(ids)))

	return nil
}

func (s *searchServiceImpl) CreateIndex(ctx context.Context, indexType model.IndexType) error {
	logger.Info("������������",
		logger.IntField("type", int(indexType)))

	if err := s.repo.CreateIndex(ctx, indexType); err != nil {
		logger.Error("��������ʧ��",
			logger.IntField("type", int(indexType)),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("���������ɹ�",
		logger.IntField("type", int(indexType)))

	return nil
}

func (s *searchServiceImpl) DeleteIndex(ctx context.Context, indexType model.IndexType) error {
	logger.Info("ɾ����������",
		logger.IntField("type", int(indexType)))

	if err := s.repo.DeleteIndex(ctx, indexType); err != nil {
		logger.Error("ɾ������ʧ��",
			logger.IntField("type", int(indexType)),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("ɾ�������ɹ�",
		logger.IntField("type", int(indexType)))

	return nil
}

func (s *searchServiceImpl) RefreshIndex(ctx context.Context, indexType model.IndexType) error {
	logger.Info("ˢ����������",
		logger.IntField("type", int(indexType)))

	if err := s.repo.RefreshIndex(ctx, indexType); err != nil {
		logger.Error("ˢ������ʧ��",
			logger.IntField("type", int(indexType)),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("ˢ�������ɹ�",
		logger.IntField("type", int(indexType)))

	return nil
}

func (s *searchServiceImpl) GetIndexStats(ctx context.Context) ([]*model.IndexStats, error) {
	logger.Info("��ȡ����ͳ������")

	stats, err := s.repo.GetIndexStats(ctx)
	if err != nil {
		logger.Error("��ȡ����ͳ��ʧ��",
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("��ȡ����ͳ�Ƴɹ�")

	return stats, nil
}
