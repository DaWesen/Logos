package handler

import (
	"encoding/json"
	"time"

	"Logos/internal/service/ai/search/model"
	"Logos/internal/service/ai/search/service"
	"Logos/pkg/logger"
	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/search"
	"context"
)

// SearchServiceImpl implements the SearchService interface.
type SearchServiceImpl struct {
	pb.UnimplementedSearchServiceServer
	SearchService service.SearchService
}

func buildSuccessBaseResp() *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    0,
		StatusMessage: "success",
		ServiceTime:   time.Now().Unix(),
	}
}

func buildErrorBaseResp(message string) *pbCommon.BaseResp {
	return &pbCommon.BaseResp{
		StatusCode:    1,
		StatusMessage: message,
		ServiceTime:   time.Now().Unix(),
	}
}

func convertModelDocumentToProtoDocument(doc *model.SearchDocument) *pb.IndexDocument {
	if doc == nil {
		return nil
	}

	result := &pb.IndexDocument{
		Id:        doc.ID,
		Type:      pb.IndexType(doc.Type),
		Title:     doc.Title,
		Content:   doc.Content,
		Metadata:  doc.Metadata,
		CreatedAt: doc.CreatedAt.Unix(),
		UpdatedAt: doc.UpdatedAt.Unix(),
	}

	if doc.Fields != nil {
		fields := make(map[string][]byte)
		for k, v := range doc.Fields {
			if jsonBytes, err := json.Marshal(v); err == nil {
				fields[k] = jsonBytes
			}
		}
		if len(fields) > 0 {
			result.Fields = fields
		}
	}

	return result
}

func convertModelResultItemToProtoResultItem(item *model.SearchResultItem) *pb.SearchResultItem {
	if item == nil {
		return nil
	}

	result := &pb.SearchResultItem{
		Id:       item.ID,
		Type:     item.Type,
		Title:    item.Title,
		Content:  item.Content,
		Score:    item.Score,
		Metadata: item.Metadata,
	}

	if item.Fields != nil {
		fields := make(map[string][]byte)
		for k, v := range item.Fields {
			if jsonBytes, err := json.Marshal(v); err == nil {
				fields[k] = jsonBytes
			}
		}
		if len(fields) > 0 {
			result.Fields = fields
		}
	}

	return result
}

func convertModelStatsToProtoStats(stat *model.IndexStats) *pb.IndexStats {
	if stat == nil {
		return nil
	}

	return &pb.IndexStats{
		Type:          pb.IndexType(stat.Type),
		DocumentCount: stat.DocumentCount,
		SizeInBytes:   stat.SizeInBytes,
		LastUpdated:   stat.LastUpdated,
	}
}

// Search implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) Search(ctx context.Context, req *pb.SearchReq) (*pb.SearchResp, error) {
	resp := &pb.SearchResp{}

	conditions := make([]model.SearchCondition, 0)
	if req.Conditions != nil {
		for _, cond := range req.Conditions {
			conditions = append(conditions, model.SearchCondition{
				Field:    cond.Field,
				Value:    cond.Value,
				Operator: cond.Operator,
			})
		}
	}

	sorts := make([]model.SortCondition, 0)
	if req.Sorts != nil {
		for _, sort := range req.Sorts {
			sorts = append(sorts, model.SortCondition{
				Field: sort.Field,
				Order: sort.Order,
			})
		}
	}

	results, total, err := s.SearchService.Search(
		ctx,
		req.Query,
		model.IndexType(req.IndexType),
		int(req.Page),
		int(req.PageSize),
		conditions,
		sorts,
		req.Filters,
	)

	if err != nil {
		logger.Error("Search failed", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		resp.Results = make([]*pb.SearchResultItem, 0)
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Results = make([]*pb.SearchResultItem, 0, len(results))
	for _, item := range results {
		resp.Results = append(resp.Results, convertModelResultItemToProtoResultItem(item))
	}
	resp.Total = total

	return resp, nil
}

// AddDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) AddDocument(ctx context.Context, req *pb.AddDocumentReq) (*pb.DocumentResp, error) {
	resp := &pb.DocumentResp{}

	doc := &model.SearchDocument{
		Type:     model.IndexType(req.Type),
		Title:    req.Title,
		Content:  req.Content,
		Metadata: req.Metadata,
		Fields:   make(map[string]interface{}),
	}

	if req.Fields != nil {
		for k, v := range req.Fields {
			var val interface{}
			if err := json.Unmarshal(v, &val); err == nil {
				doc.Fields[k] = val
			}
		}
	}

	createdDoc, err := s.SearchService.AddDocument(ctx, doc)
	if err != nil {
		logger.Error("�����ĵ�ʧ��", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Document = convertModelDocumentToProtoDocument(createdDoc)

	return resp, nil
}

// UpdateDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) UpdateDocument(ctx context.Context, req *pb.UpdateDocumentReq) (*pb.DocumentResp, error) {
	resp := &pb.DocumentResp{}

	doc := &model.SearchDocument{
		ID: req.Id,
	}

	if req.Title != nil {
		doc.Title = *req.Title
	}
	if req.Content != nil {
		doc.Content = *req.Content
	}
	if req.Metadata != nil {
		doc.Metadata = req.Metadata
	}

	if req.Fields != nil {
		doc.Fields = make(map[string]interface{})
		for k, v := range req.Fields {
			var val interface{}
			if err := json.Unmarshal(v, &val); err == nil {
				doc.Fields[k] = val
			}
		}
	}

	updatedDoc, err := s.SearchService.UpdateDocument(ctx, doc)
	if err != nil {
		logger.Error("�����ĵ�ʧ��", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Document = convertModelDocumentToProtoDocument(updatedDoc)

	return resp, nil
}

// DeleteDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) DeleteDocument(ctx context.Context, req *pb.DeleteDocumentReq) (*pbCommon.BaseResp, error) {
	err := s.SearchService.DeleteDocument(ctx, req.Id)
	if err != nil {
		logger.Error("ɾ���ĵ�ʧ��", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// GetDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) GetDocument(ctx context.Context, req *pb.GetByIdReq) (*pb.DocumentResp, error) {
	resp := &pb.DocumentResp{}

	doc, err := s.SearchService.GetDocument(ctx, req.Id)
	if err != nil {
		logger.Error("��ȡ�ĵ�ʧ��", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Document = convertModelDocumentToProtoDocument(doc)

	return resp, nil
}

// BatchAddDocuments implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) BatchAddDocuments(ctx context.Context, req *pb.BatchAddDocumentReq) (*pbCommon.BaseResp, error) {
	docs := make([]*model.SearchDocument, 0, len(req.Documents))
	for _, docReq := range req.Documents {
		docs = append(docs, &model.SearchDocument{
			Type:     model.IndexType(docReq.Type),
			Title:    docReq.Title,
			Content:  docReq.Content,
			Metadata: docReq.Metadata,
		})
	}

	err := s.SearchService.BatchAddDocuments(ctx, docs)
	if err != nil {
		logger.Error("���������ĵ�ʧ��", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// BatchDeleteDocuments implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) BatchDeleteDocuments(ctx context.Context, req *pb.BatchDeleteDocumentReq) (*pbCommon.BaseResp, error) {
	err := s.SearchService.BatchDeleteDocuments(ctx, req.Ids)
	if err != nil {
		logger.Error("����ɾ���ĵ�ʧ��", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// CreateIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) CreateIndex(ctx context.Context, req *pb.GetByIndexTypeReq) (*pbCommon.BaseResp, error) {
	err := s.SearchService.CreateIndex(ctx, model.IndexType(req.Type))
	if err != nil {
		logger.Error("��������ʧ��", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// DeleteIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) DeleteIndex(ctx context.Context, req *pb.GetByIndexTypeReq) (*pbCommon.BaseResp, error) {
	err := s.SearchService.DeleteIndex(ctx, model.IndexType(req.Type))
	if err != nil {
		logger.Error("ɾ������ʧ��", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// RefreshIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) RefreshIndex(ctx context.Context, req *pb.GetByIndexTypeReq) (*pbCommon.BaseResp, error) {
	err := s.SearchService.RefreshIndex(ctx, model.IndexType(req.Type))
	if err != nil {
		logger.Error("ˢ������ʧ��", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// GetIndexStats implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) GetIndexStats(ctx context.Context, req *pb.EmptyReq) (*pb.IndexStatsResp, error) {
	resp := &pb.IndexStatsResp{}

	stats, err := s.SearchService.GetIndexStats(ctx)
	if err != nil {
		logger.Error("��ȡ����ͳ��ʧ��", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Stats = make([]*pb.IndexStats, 0, len(stats))
	for _, stat := range stats {
		resp.Stats = append(resp.Stats, convertModelStatsToProtoStats(stat))
	}

	return resp, nil
}
