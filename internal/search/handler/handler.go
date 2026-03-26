package handler

import (
				common "Noah/kitex_gen/common"
				search "Noah/kitex_gen/search"
			"context"
)

// SearchServiceImpl implements the last service interface defined in the IDL.
type SearchServiceImpl struct{}

// Search implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) Search(ctx context.Context , req *search.SearchReq ) (resp *search.SearchResp, err error) {
	// TODO: Your code here...
	return
}


// AddDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) AddDocument(ctx context.Context , req *search.AddDocumentReq ) (resp *search.DocumentResp, err error) {
	// TODO: Your code here...
	return
}


// UpdateDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) UpdateDocument(ctx context.Context , req *search.UpdateDocumentReq ) (resp *search.DocumentResp, err error) {
	// TODO: Your code here...
	return
}


// DeleteDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) DeleteDocument(ctx context.Context , req *search.DeleteDocumentReq ) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}


// GetDocument implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) GetDocument(ctx context.Context , id string ) (resp *search.DocumentResp, err error) {
	// TODO: Your code here...
	return
}


// BatchAddDocuments implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) BatchAddDocuments(ctx context.Context , req *search.BatchAddDocumentReq ) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}


// BatchDeleteDocuments implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) BatchDeleteDocuments(ctx context.Context , req *search.BatchDeleteDocumentReq ) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}


// CreateIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) CreateIndex(ctx context.Context, indexType search.IndexType) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}


// DeleteIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) DeleteIndex(ctx context.Context, indexType search.IndexType) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}


// RefreshIndex implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) RefreshIndex(ctx context.Context, indexType search.IndexType) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}


// GetIndexStats implements the SearchServiceImpl interface.
func (s *SearchServiceImpl) GetIndexStats(ctx context.Context  ) (resp *search.IndexStatsResp, err error) {
	// TODO: Your code here...
	return
}






