package handler

import (
	common "Noah/kitex_gen/common"
	vector "Noah/kitex_gen/vector"
	"context"
)

// VectorServiceImpl implements the last service interface defined in the IDL.
type VectorServiceImpl struct{}

// CreateCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) CreateCollection(ctx context.Context, req *vector.CreateCollectionReq) (resp *vector.CollectionResp, err error) {
	// TODO: Your code here...
	return
}

// UpdateCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) UpdateCollection(ctx context.Context, req *vector.UpdateCollectionReq) (resp *vector.CollectionResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) DeleteCollection(ctx context.Context, req *vector.DeleteCollectionReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) GetCollection(ctx context.Context, id string) (resp *vector.CollectionResp, err error) {
	// TODO: Your code here...
	return
}

// ListCollections implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) ListCollections(ctx context.Context) (resp *vector.BatchCollectionResp, err error) {
	// TODO: Your code here...
	return
}

// Vectorize implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) Vectorize(ctx context.Context, req *vector.VectorizeReq) (resp *vector.VectorizeResp, err error) {
	// TODO: Your code here...
	return
}

// BatchVectorize implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) BatchVectorize(ctx context.Context, req *vector.BatchVectorizeReq) (resp *vector.BatchVectorizeResp, err error) {
	// TODO: Your code here...
	return
}

// Search implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) Search(ctx context.Context, req *vector.SearchReq) (resp *vector.SearchResp, err error) {
	// TODO: Your code here...
	return
}

// TextSearch implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) TextSearch(ctx context.Context, req *vector.TextSearchReq) (resp *vector.SearchResp, err error) {
	// TODO: Your code here...
	return
}

// DeleteVector implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) DeleteVector(ctx context.Context, req *vector.DeleteVectorReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// BatchDeleteVector implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) BatchDeleteVector(ctx context.Context, req *vector.BatchDeleteVectorReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}
