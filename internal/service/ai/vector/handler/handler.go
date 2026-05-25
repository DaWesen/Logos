package handler

import (
	"time"

	"Logos/internal/service/ai/vector/model"
	"Logos/internal/service/ai/vector/service"
	"Logos/pkg/logger"
	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/vector"
	"context"
)

// VectorServiceImpl implements the VectorService interface.
type VectorServiceImpl struct {
	pb.UnimplementedVectorServiceServer
	VectorService service.VectorService
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

func convertModelVectorToProtoVector(vec *model.Vector) *pb.Vector {
	if vec == nil {
		return nil
	}

	return &pb.Vector{
		Id:        vec.ID,
		Values:    vec.Values,
		Metadata:  vec.Metadata,
		CreatedAt: vec.CreatedAt.Unix(),
	}
}

func convertModelCollectionToProtoCollection(collection *model.VectorCollection) *pb.VectorCollection {
	if collection == nil {
		return nil
	}

	return &pb.VectorCollection{
		Id:               collection.ID,
		Name:             collection.Name,
		ModelType:        pb.VectorModelType(collection.ModelType),
		IndexType:        pb.IndexType(collection.IndexType),
		Dimension:        int32(collection.Dimension),
		Parameters:       collection.Parameters,
		Size:             collection.Size,
		VlmModel:         collection.VLM.Model,
		VlmBaseUrl:       collection.VLM.BaseURL,
		VlmApiKey:        collection.VLM.APIKey,
		LlmModel:         collection.LLM.Model,
		LlmBaseUrl:       collection.LLM.BaseURL,
		LlmApiKey:        collection.LLM.APIKey,
		AsrModel:         collection.ASR.Model,
		AsrBaseUrl:       collection.ASR.BaseURL,
		AsrApiKey:        collection.ASR.APIKey,
		EmbeddingModel:   collection.Embedding.Model,
		EmbeddingBaseUrl: collection.Embedding.BaseURL,
		EmbeddingApiKey:  collection.Embedding.APIKey,
		CreatedAt:        collection.CreatedAt.Unix(),
		UpdatedAt:        collection.UpdatedAt.Unix(),
	}
}

func convertModelSearchResultToProtoSearchResult(item *model.SearchResultItem) *pb.SearchResultItem {
	if item == nil {
		return nil
	}

	return &pb.SearchResultItem{
		VectorId: item.VectorID,
		Score:    item.Score,
		Metadata: item.Metadata,
	}
}

// CreateCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) CreateCollection(ctx context.Context, req *pb.CreateCollectionReq) (*pb.CollectionResp, error) {
	resp := &pb.CollectionResp{}

	collection := &model.VectorCollection{
		Name:       req.Name,
		ModelType:  model.VectorModelType(req.ModelType),
		IndexType:  model.IndexType(req.IndexType),
		Dimension:  int(req.Dimension),
		Parameters: req.Parameters,
		VLM: model.ModelConfig{
			Model:   req.VlmModel,
			BaseURL: req.VlmBaseUrl,
			APIKey:  req.VlmApiKey,
		},
		LLM: model.ModelConfig{
			Model:   req.LlmModel,
			BaseURL: req.LlmBaseUrl,
			APIKey:  req.LlmApiKey,
		},
		ASR: model.ModelConfig{
			Model:   req.AsrModel,
			BaseURL: req.AsrBaseUrl,
			APIKey:  req.AsrApiKey,
		},
		Embedding: model.ModelConfig{
			Model:   req.EmbeddingModel,
			BaseURL: req.EmbeddingBaseUrl,
			APIKey:  req.EmbeddingApiKey,
		},
	}

	createdCollection, err := s.VectorService.CreateCollection(ctx, collection)
	if err != nil {
		logger.Error("CreateCollection failed", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Collection = convertModelCollectionToProtoCollection(createdCollection)

	return resp, nil
}

// UpdateCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) UpdateCollection(ctx context.Context, req *pb.UpdateCollectionReq) (*pb.CollectionResp, error) {
	resp := &pb.CollectionResp{}

	collection := &model.VectorCollection{
		ID: req.Id,
	}

	if req.Name != nil {
		collection.Name = *req.Name
	}
	if req.Parameters != nil {
		collection.Parameters = req.Parameters
		if model, ok := req.Parameters["__embedding_model"]; ok {
			collection.Embedding.Model = model
			delete(collection.Parameters, "__embedding_model")
		}
		if baseURL, ok := req.Parameters["__embedding_base_url"]; ok {
			collection.Embedding.BaseURL = baseURL
			delete(collection.Parameters, "__embedding_base_url")
		}
		if apiKey, ok := req.Parameters["__embedding_api_key"]; ok {
			collection.Embedding.APIKey = apiKey
			delete(collection.Parameters, "__embedding_api_key")
		}
	}

	updatedCollection, err := s.VectorService.UpdateCollection(ctx, collection)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Collection = convertModelCollectionToProtoCollection(updatedCollection)

	return resp, nil
}

// DeleteCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) DeleteCollection(ctx context.Context, req *pb.DeleteCollectionReq) (*pbCommon.BaseResp, error) {
	err := s.VectorService.DeleteCollection(ctx, req.Id)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

func (s *VectorServiceImpl) ListVectors(ctx context.Context, req *pb.ListVectorsReq) (*pb.ListVectorsResp, error) {
	logger.Info("列出向量预览请求",
		logger.StringField("collection_id", req.CollectionId),
		logger.IntField("page", int(req.Page)),
		logger.IntField("page_size", int(req.PageSize)))

	items, total, err := s.VectorService.ListVectors(ctx, req.CollectionId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("列出向量预览失败", logger.ErrorField(err))
		return &pb.ListVectorsResp{
			BaseResp: buildErrorBaseResp(err.Error()),
		}, nil
	}

	pbItems := make([]*pb.VectorPreviewItem, 0, len(items))
	for _, item := range items {
		pbItems = append(pbItems, &pb.VectorPreviewItem{
			Id:       item.ID,
			Content:  item.Content,
			Metadata: item.Metadata,
		})
	}

	return &pb.ListVectorsResp{
		BaseResp: buildSuccessBaseResp(),
		Vectors:  pbItems,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}, nil
}

// GetCollection implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) GetCollection(ctx context.Context, req *pb.GetByIdReq) (*pb.CollectionResp, error) {
	resp := &pb.CollectionResp{}

	collection, err := s.VectorService.GetCollection(ctx, req.Id)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Collection = convertModelCollectionToProtoCollection(collection)

	return resp, nil
}

// ListCollections implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) ListCollections(ctx context.Context, req *pb.EmptyReq) (*pb.BatchCollectionResp, error) {
	resp := &pb.BatchCollectionResp{}

	collections, err := s.VectorService.ListCollections(ctx)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Collections = make([]*pb.VectorCollection, 0, len(collections))
	for _, collection := range collections {
		resp.Collections = append(resp.Collections, convertModelCollectionToProtoCollection(collection))
	}

	return resp, nil
}

// Vectorize implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) Vectorize(ctx context.Context, req *pb.VectorizeReq) (*pb.VectorizeResp, error) {
	logger.Info("收到 Vectorize 请求",
		logger.StringField("collection_id", req.CollectionId),
		logger.StringField("text", req.Text))
	resp := &pb.VectorizeResp{}

	vec, err := s.VectorService.Vectorize(ctx, req.Text, req.CollectionId, req.Metadata)
	if err != nil {
		logger.Error("Vectorize 失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Vector = convertModelVectorToProtoVector(vec)

	logger.Info("Vectorize 完成", logger.StringField("vector_id", vec.ID))
	return resp, nil
}

// BatchVectorize implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) BatchVectorize(ctx context.Context, req *pb.BatchVectorizeReq) (*pb.BatchVectorizeResp, error) {
	resp := &pb.BatchVectorizeResp{}

	var metadataList []map[string]string
	for _, mw := range req.MetadataList {
		metadataList = append(metadataList, mw.Data)
	}

	vectors, err := s.VectorService.BatchVectorize(ctx, req.Texts, req.CollectionId, metadataList)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Vectors = make([]*pb.Vector, 0, len(vectors))
	for _, vec := range vectors {
		resp.Vectors = append(resp.Vectors, convertModelVectorToProtoVector(vec))
	}

	return resp, nil
}

// Search implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) Search(ctx context.Context, req *pb.SearchReq) (*pb.SearchResp, error) {
	resp := &pb.SearchResp{}

	threshold := 0.0
	if req.Threshold != nil {
		threshold = *req.Threshold
	}

	filter := make(map[string]string)
	if req.Filter != nil {
		filter = req.Filter
	}

	results, err := s.VectorService.Search(ctx, req.CollectionId, req.QueryVector, int(req.TopK), threshold, filter)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		resp.Results = make([]*pb.SearchResultItem, 0)
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Results = make([]*pb.SearchResultItem, 0, len(results))
	for _, result := range results {
		resp.Results = append(resp.Results, convertModelSearchResultToProtoSearchResult(result))
	}

	return resp, nil
}

// TextSearch implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) TextSearch(ctx context.Context, req *pb.TextSearchReq) (*pb.SearchResp, error) {
	logger.Info("gRPC TextSearch 到达",
		logger.StringField("collection_id", req.CollectionId),
		logger.StringField("text", req.Text),
		logger.IntField("top_k", int(req.TopK)))
	resp := &pb.SearchResp{}

	threshold := 0.0
	if req.Threshold != nil {
		threshold = *req.Threshold
	}

	filter := make(map[string]string)
	if req.Filter != nil {
		filter = req.Filter
	}

	results, err := s.VectorService.TextSearch(ctx, req.CollectionId, req.Text, int(req.TopK), threshold, filter)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Results = make([]*pb.SearchResultItem, 0, len(results))
	for _, result := range results {
		resp.Results = append(resp.Results, convertModelSearchResultToProtoSearchResult(result))
	}

	return resp, nil
}

// DeleteVector implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) DeleteVector(ctx context.Context, req *pb.DeleteVectorReq) (*pbCommon.BaseResp, error) {
	err := s.VectorService.DeleteVector(ctx, req.CollectionId, req.VectorId)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// BatchDeleteVector implements the VectorServiceImpl interface.
func (s *VectorServiceImpl) BatchDeleteVector(ctx context.Context, req *pb.BatchDeleteVectorReq) (*pbCommon.BaseResp, error) {
	err := s.VectorService.BatchDeleteVector(ctx, req.CollectionId, req.VectorIds)
	if err != nil {
		logger.Error("operation", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}
