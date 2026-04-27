package handler

import (
	"context"
	"time"

	"Logos/internal/ai/recommend/model"
	"Logos/internal/ai/recommend/service"
	pb "Logos/proto_gen/recommend"
	pbCommon "Logos/proto_gen/common"
	"Logos/pkg/logger"
)

type RecommendationServiceImpl struct {
	pb.UnimplementedRecommendationServiceServer
	RecommendService service.RecommendService
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

func convertModelRecommendationItemToProto(item *model.RecommendationItem) *pb.RecommendationItem {
	result := &pb.RecommendationItem{
		Id:          item.ID,
		Type:        item.Type,
		Title:       item.Title,
		Description: item.Description,
		Score:       item.Score,
		EntityId:    item.EntityID,
		CreatedAt:   item.CreatedAt,
	}
	if item.ImageURL != nil {
		result.ImageUrl = item.ImageURL
	}
	return result
}

func convertModelHistoryItemToProto(item *model.RecommendationHistory) *pb.HistoryItem {
	return &pb.HistoryItem{
		Id:        item.ID,
		ItemId:    item.ItemID,
		ItemType:  item.ItemType,
		Title:     item.Title,
		Action:    item.Action,
		Timestamp: item.Timestamp,
	}
}

// GetRecommendations implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) GetRecommendations(ctx context.Context, req *pb.RecommendationReq) (*pb.RecommendationResp, error) {
	resp := &pb.RecommendationResp{}

	var recommendType string
	if req.Type != nil {
		recommendType = *req.Type
	}

	limit := 10
	if req.Limit != nil {
		limit = int(*req.Limit)
	}

	var reqContext map[string]string
	if req.Context != nil {
		reqContext = req.Context
	}

	items, total, err := s.RecommendService.GetRecommendations(ctx, req.UserId, recommendType, limit, reqContext)
	if err != nil {
		logger.Error("获取推荐失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Items = make([]*pb.RecommendationItem, 0, len(items))
	for _, item := range items {
		resp.Items = append(resp.Items, convertModelRecommendationItemToProto(item))
	}
	resp.Total = total

	return resp, nil
}

// GetRelatedRecommendations implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) GetRelatedRecommendations(ctx context.Context, req *pb.RelatedRecommendationReq) (*pb.RecommendationResp, error) {
	resp := &pb.RecommendationResp{}

	var recommendType string
	if req.Type != nil {
		recommendType = *req.Type
	}

	limit := 10
	if req.Limit != nil {
		limit = int(*req.Limit)
	}

	items, total, err := s.RecommendService.GetRelatedRecommendations(ctx, req.EntityId, recommendType, limit)
	if err != nil {
		logger.Error("获取相关推荐失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Items = make([]*pb.RecommendationItem, 0, len(items))
	for _, item := range items {
		resp.Items = append(resp.Items, convertModelRecommendationItemToProto(item))
	}
	resp.Total = total

	return resp, nil
}

// SubmitFeedback implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) SubmitFeedback(ctx context.Context, req *pb.FeedbackReq) (*pbCommon.BaseResp, error) {
	err := s.RecommendService.SubmitFeedback(ctx, req.ItemId, req.UserId, req.Action, req.Timestamp)
	if err != nil {
		logger.Error("提交推荐反馈失败", logger.ErrorField(err))
		return buildErrorBaseResp(err.Error()), nil
	}

	return buildSuccessBaseResp(), nil
}

// GetRecommendationHistory implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) GetRecommendationHistory(ctx context.Context, req *pb.HistoryReq) (*pb.HistoryResp, error) {
	resp := &pb.HistoryResp{}

	histories, total, err := s.RecommendService.GetRecommendationHistory(ctx, req.UserId, int(req.Page), int(req.PageSize))
	if err != nil {
		logger.Error("获取推荐历史失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Items = make([]*pb.HistoryItem, 0, len(histories))
	for _, item := range histories {
		resp.Items = append(resp.Items, convertModelHistoryItemToProto(item))
	}
	resp.Total = total

	return resp, nil
}

// BatchGetRecommendations implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) BatchGetRecommendations(ctx context.Context, req *pb.BatchRecommendationReq) (*pb.BatchRecommendationResp, error) {
	resp := &pb.BatchRecommendationResp{}

	var recommendType string
	if req.Type != nil {
		recommendType = *req.Type
	}

	limit := 10
	if req.Limit != nil {
		limit = int(*req.Limit)
	}

	recommendations, err := s.RecommendService.BatchGetRecommendations(ctx, req.UserIds, recommendType, limit)
	if err != nil {
		logger.Error("批量获取推荐失败", logger.ErrorField(err))
		resp.BaseResp = buildErrorBaseResp(err.Error())
		return resp, nil
	}

	resp.BaseResp = buildSuccessBaseResp()
	resp.Recommendations = make(map[int64]*pb.RecommendationList)
	for userID, items := range recommendations {
		protoItems := make([]*pb.RecommendationItem, 0, len(items))
		for _, item := range items {
			protoItems = append(protoItems, convertModelRecommendationItemToProto(item))
		}
		resp.Recommendations[userID] = &pb.RecommendationList{Items: protoItems}
	}

	return resp, nil
}
