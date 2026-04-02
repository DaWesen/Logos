package handler

import (
	"time"

	"Noah/internal/search/model"
	"Noah/internal/search/service"
	common "Noah/kitex_gen/common"
	search "Noah/kitex_gen/search"
	"Noah/pkg/logger"
	"context"
)

// SearchServiceImpl implements the last service interface defined in the IDL.
type SearchServiceImpl struct {
	SearchService service.SearchService
}

func buildSuccessBaseResp() *common.BaseResp {
	return &common.BaseResp{
		StatusCode:    0,
		StatusMessage: "success",
		ServiceTime:   time.Now().Unix(),
	}
}

func buildErrorBaseResp(message string) *common.BaseResp {
	return &common.BaseResp{
		StatusCode:    1,
		StatusMessage: message,
		ServiceTime:   time.Now().Unix(),
	}
}

func convertModelDocumentToThriftDocument(doc *model.SearchDocument) *search.IndexDocument {
	if doc == nil {
		return nil
	}

	return &search.IndexDocument{
		Id:        doc.ID,
		Type:      search.IndexType(doc.Type),
		Title:     doc.Title,
		Content:   doc.Content,
		Metadata:  doc.Metadata,
		CreatedAt: doc.CreatedAt.Unix(),
		UpdatedAt: doc.UpdatedAt.Unix(),
	}
}

func convertModelResultItemToThriftResultItem(item *model.SearchResultItem) *search.SearchResultItem {
	if item == nil {
		return nil
	}

	return &search.SearchResultItem{
		Id:       item.ID,
		Type:     item.Type,
		Title:    item.Title,
		Content:  item.Content,
		Score:    item.Score,
		Metadata: item.Metadata,
	}
}

func convertModelStatsToThriftStats(stat *model.IndexStats) *search.IndexStats {
	if stat == nil {
		return nil
	}

	return &search.IndexStats{
		Type:          search.IndexType(stat.Type),
		DocumentCount: stat.DocumentCount,
		SizeInBytes:   stat.SizeInBytes,
		LastUpdated:   stat.LastUpdated,
	}
}

// Search implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) Search(ctx context.Context, req *search.SearchReq) (resp *search.SearchResp, err error) {
	resp = search.NewSearchResp()

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
		logger.Error("搜索失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Results = make([]*search.SearchResultItem, 0, len(results))
	for _, item := range results {
		resp.Results = append(resp.Results, convertModelResultItemToThriftResultItem(item))
	}
	resp.Total = total
	resp.SearchTime = time.Now().Unix() - resp.BaseResp.ServiceTime

	return resp, nil
}

// AddDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) AddDocument(ctx context.Context, req *search.AddDocumentReq) (resp *search.DocumentResp, err error) {
	resp = search.NewDocumentResp()

	doc := &model.SearchDocument{
		Type:     model.IndexType(req.Type),
		Title:    req.Title,
		Content:  req.Content,
		Metadata: req.Metadata,
	}

	createdDoc, err := s.SearchService.AddDocument(ctx, doc)
	if err != nil {
		logger.Error("添加文档失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Document = convertModelDocumentToThriftDocument(createdDoc)

	return resp, nil
}

// UpdateDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) UpdateDocument(ctx context.Context, req *search.UpdateDocumentReq) (resp *search.DocumentResp, err error) {
	resp = search.NewDocumentResp()

	doc := &model.SearchDocument{
		ID: req.Id,
	}

	if req.IsSetTitle() {
		doc.Title = *req.Title
	}
	if req.IsSetContent() {
		doc.Content = *req.Content
	}
	if req.IsSetMetadata() {
		doc.Metadata = req.Metadata
	}

	updatedDoc, err := s.SearchService.UpdateDocument(ctx, doc)
	if err != nil {
		logger.Error("更新文档失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Document = convertModelDocumentToThriftDocument(updatedDoc)

	return resp, nil
}

// DeleteDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) DeleteDocument(ctx context.Context, req *search.DeleteDocumentReq) (resp *common.BaseResp, err error) {
	err = s.SearchService.DeleteDocument(ctx, req.Id)
	if err != nil {
		logger.Error("删除文档失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// GetDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) GetDocument(ctx context.Context, id string) (resp *search.DocumentResp, err error) {
	resp = search.NewDocumentResp()

	doc, err := s.SearchService.GetDocument(ctx, id)
	if err != nil {
		logger.Error("获取文档失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Document = convertModelDocumentToThriftDocument(doc)

	return resp, nil
}

// BatchAddDocuments implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) BatchAddDocuments(ctx context.Context, req *search.BatchAddDocumentReq) (resp *common.BaseResp, err error) {
	docs := make([]*model.SearchDocument, 0, len(req.Documents))
	for _, docReq := range req.Documents {
		docs = append(docs, &model.SearchDocument{
			Type:     model.IndexType(docReq.Type),
			Title:    docReq.Title,
			Content:  docReq.Content,
			Metadata: docReq.Metadata,
		})
	}

	err = s.SearchService.BatchAddDocuments(ctx, docs)
	if err != nil {
		logger.Error("批量添加文档失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// BatchDeleteDocuments implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) BatchDeleteDocuments(ctx context.Context, req *search.BatchDeleteDocumentReq) (resp *common.BaseResp, err error) {
	err = s.SearchService.BatchDeleteDocuments(ctx, req.Ids)
	if err != nil {
		logger.Error("批量删除文档失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// CreateIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) CreateIndex(ctx context.Context, indexType search.IndexType) (resp *common.BaseResp, err error) {
	err = s.SearchService.CreateIndex(ctx, model.IndexType(indexType))
	if err != nil {
		logger.Error("创建索引失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// DeleteIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) DeleteIndex(ctx context.Context, indexType search.IndexType) (resp *common.BaseResp, err error) {
	err = s.SearchService.DeleteIndex(ctx, model.IndexType(indexType))
	if err != nil {
		logger.Error("删除索引失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// RefreshIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) RefreshIndex(ctx context.Context, indexType search.IndexType) (resp *common.BaseResp, err error) {
	err = s.SearchService.RefreshIndex(ctx, model.IndexType(indexType))
	if err != nil {
		logger.Error("刷新索引失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// GetIndexStats implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) GetIndexStats(ctx context.Context) (resp *search.IndexStatsResp, err error) {
	resp = search.NewIndexStatsResp()

	stats, err := s.SearchService.GetIndexStats(ctx)
	if err != nil {
		logger.Error("获取索引统计失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Stats = make([]*search.IndexStats, 0, len(stats))
	for _, stat := range stats {
		resp.Stats = append(resp.Stats, convertModelStatsToThriftStats(stat))
	}

	return resp, nil
}
