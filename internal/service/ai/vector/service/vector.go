package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"Logos/internal/service/ai/vector/dao"
	"Logos/internal/service/ai/vector/model"
	"Logos/pkg/logger"
)

var (
	ErrCollectionNotFound = errors.New("collection not found")
	ErrInvalidDimension   = errors.New("invalid dimension")
	ErrInternalServer     = errors.New("internal server error")
)

type VectorService interface {
	// ���Ϲ���
	CreateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error)
	UpdateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error)
	DeleteCollection(ctx context.Context, id string) error
	GetCollection(ctx context.Context, id string) (*model.VectorCollection, error)
	ListCollections(ctx context.Context) ([]*model.VectorCollection, error)

	// ������
	Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error)
	BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]*model.Vector, error)

	// ����������
	Search(ctx context.Context, collectionID string, queryVector []float64, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error)
	TextSearch(ctx context.Context, collectionID string, text string, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error)

	// ��������
	DeleteVector(ctx context.Context, collectionID string, vectorID string) error
	BatchDeleteVector(ctx context.Context, collectionID string, vectorIDs []string) error
}

type vectorServiceImpl struct {
	repo dao.VectorRepository
}

func NewVectorService(repo dao.VectorRepository) VectorService {
	return &vectorServiceImpl{
		repo: repo,
	}
}

func (s *vectorServiceImpl) CreateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error) {
	logger.Info("����������������",
		logger.StringField("name", req.Name),
		logger.IntField("dimension", req.Dimension))

	if req.Name == "" {
		return nil, errors.New("�������Ʋ���Ϊ��")
	}
	if req.Dimension <= 0 {
		return nil, ErrInvalidDimension
	}

	if req.ID == "" {
		req.ID = fmt.Sprintf("col_%d", time.Now().UnixNano())
	}
	req.CreatedAt = time.Now()
	req.UpdatedAt = time.Now()
	req.Size = 0

	if err := s.repo.CreateCollection(ctx, req); err != nil {
		logger.Error("������������ʧ��",
			logger.StringField("name", req.Name),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�����������ϳɹ�",
		logger.StringField("id", req.ID),
		logger.StringField("name", req.Name))

	return req, nil
}

func (s *vectorServiceImpl) UpdateCollection(ctx context.Context, req *model.VectorCollection) (*model.VectorCollection, error) {
	logger.Info("����������������",
		logger.StringField("id", req.ID))

	if req.ID == "" {
		return nil, errors.New("����ID����Ϊ��")
	}

	existing, err := s.repo.GetCollection(ctx, req.ID)
	if err != nil {
		logger.Error("��ѯ��������ʧ��",
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
	req.CreatedAt = existing.CreatedAt
	req.Size = existing.Size

	if err := s.repo.UpdateCollection(ctx, req); err != nil {
		logger.Error("������������ʧ��",
			logger.StringField("id", req.ID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�����������ϳɹ�",
		logger.StringField("id", req.ID))

	return req, nil
}

func (s *vectorServiceImpl) DeleteCollection(ctx context.Context, id string) error {
	logger.Info("ɾ��������������",
		logger.StringField("id", id))

	if id == "" {
		return errors.New("����ID����Ϊ��")
	}

	if err := s.repo.DeleteCollection(ctx, id); err != nil {
		logger.Error("ɾ����������ʧ��",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("ɾ���������ϳɹ�",
		logger.StringField("id", id))

	return nil
}

func (s *vectorServiceImpl) GetCollection(ctx context.Context, id string) (*model.VectorCollection, error) {
	logger.Info("��ȡ������������",
		logger.StringField("id", id))

	if id == "" {
		return nil, errors.New("����ID����Ϊ��")
	}

	collection, err := s.repo.GetCollection(ctx, id)
	if err != nil {
		logger.Error("��ȡ��������ʧ��",
			logger.StringField("id", id),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}
	if collection == nil {
		return nil, ErrCollectionNotFound
	}

	logger.Info("��ȡ�������ϳɹ�",
		logger.StringField("id", id))

	return collection, nil
}

func (s *vectorServiceImpl) ListCollections(ctx context.Context) ([]*model.VectorCollection, error) {
	logger.Info("�г�����������������")

	collections, err := s.repo.ListCollections(ctx)
	if err != nil {
		logger.Error("�г���������ʧ��",
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�г��������ϳɹ�",
		logger.IntField("count", len(collections)))

	return collections, nil
}

func (s *vectorServiceImpl) Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (*model.Vector, error) {
	logger.Info("����������",
		logger.StringField("text", text),
		logger.StringField("collection_id", collectionID))

	if text == "" {
		return nil, errors.New("�ı�����Ϊ��")
	}
	if collectionID == "" {
		return nil, errors.New("����ID����Ϊ��")
	}

	vector, err := s.repo.Vectorize(ctx, text, collectionID, metadata)
	if err != nil {
		logger.Error("������ʧ��",
			logger.StringField("text", text),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�������ɹ�",
		logger.StringField("vector_id", vector.ID))

	return vector, nil
}

func (s *vectorServiceImpl) BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]*model.Vector, error) {
	logger.Info("��������������",
		logger.IntField("count", len(texts)),
		logger.StringField("collection_id", collectionID))

	if len(texts) == 0 {
		return nil, errors.New("�ı��б�����Ϊ��")
	}
	if collectionID == "" {
		return nil, errors.New("����ID����Ϊ��")
	}

	vectors, err := s.repo.BatchVectorize(ctx, texts, collectionID, metadataList)
	if err != nil {
		logger.Error("����������ʧ��",
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�����������ɹ�",
		logger.IntField("count", len(vectors)))

	return vectors, nil
}

func (s *vectorServiceImpl) Search(ctx context.Context, collectionID string, queryVector []float64, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	logger.Info("������������",
		logger.StringField("collection_id", collectionID),
		logger.IntField("top_k", topK))

	if collectionID == "" {
		return nil, errors.New("����ID����Ϊ��")
	}
	if len(queryVector) == 0 {
		return nil, errors.New("��ѯ��������Ϊ��")
	}
	if topK <= 0 {
		topK = 10
	}

	results, err := s.repo.Search(ctx, collectionID, queryVector, topK, threshold, filter)
	if err != nil {
		logger.Error("��������ʧ��",
			logger.StringField("collection_id", collectionID),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("���������ɹ�",
		logger.IntField("count", len(results)))

	return results, nil
}

func (s *vectorServiceImpl) TextSearch(ctx context.Context, collectionID string, text string, topK int, threshold float64, filter map[string]string) ([]*model.SearchResultItem, error) {
	logger.Info("�ı���������",
		logger.StringField("collection_id", collectionID),
		logger.StringField("text", text),
		logger.IntField("top_k", topK))

	if collectionID == "" {
		return nil, errors.New("����ID����Ϊ��")
	}
	if text == "" {
		return nil, errors.New("�ı�����Ϊ��")
	}
	if topK <= 0 {
		topK = 10
	}

	results, err := s.repo.TextSearch(ctx, collectionID, text, topK, threshold, filter)
	if err != nil {
		logger.Error("�ı�����ʧ��",
			logger.StringField("collection_id", collectionID),
			logger.StringField("text", text),
			logger.ErrorField(err))
		return nil, ErrInternalServer
	}

	logger.Info("�ı������ɹ�",
		logger.IntField("count", len(results)))

	return results, nil
}

func (s *vectorServiceImpl) DeleteVector(ctx context.Context, collectionID string, vectorID string) error {
	logger.Info("ɾ����������",
		logger.StringField("collection_id", collectionID),
		logger.StringField("vector_id", vectorID))

	if collectionID == "" {
		return errors.New("����ID����Ϊ��")
	}
	if vectorID == "" {
		return errors.New("����ID����Ϊ��")
	}

	if err := s.repo.DeleteVector(ctx, collectionID, vectorID); err != nil {
		logger.Error("ɾ������ʧ��",
			logger.StringField("collection_id", collectionID),
			logger.StringField("vector_id", vectorID),
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("ɾ�������ɹ�",
		logger.StringField("vector_id", vectorID))

	return nil
}

func (s *vectorServiceImpl) BatchDeleteVector(ctx context.Context, collectionID string, vectorIDs []string) error {
	logger.Info("����ɾ����������",
		logger.StringField("collection_id", collectionID),
		logger.IntField("count", len(vectorIDs)))

	if collectionID == "" {
		return errors.New("����ID����Ϊ��")
	}
	if len(vectorIDs) == 0 {
		return nil
	}

	if err := s.repo.BatchDeleteVector(ctx, collectionID, vectorIDs); err != nil {
		logger.Error("����ɾ������ʧ��",
			logger.ErrorField(err))
		return ErrInternalServer
	}

	logger.Info("����ɾ�������ɹ�",
		logger.IntField("count", len(vectorIDs)))

	return nil
}
