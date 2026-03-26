package handler

import (
	common "Noah/kitex_gen/common"
	recommend "Noah/kitex_gen/recommend"
	"context"
)

// RecommendationServiceImpl implements the last service interface defined in the IDL.
type RecommendationServiceImpl struct{}

// GetRecommendations implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) GetRecommendations(ctx context.Context, req *recommend.RecommendationReq) (resp *recommend.RecommendationResp, err error) {
	// TODO: Your code here...
	return
}

// GetRelatedRecommendations implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) GetRelatedRecommendations(ctx context.Context, req *recommend.RelatedRecommendationReq) (resp *recommend.RecommendationResp, err error) {
	// TODO: Your code here...
	return
}

// SubmitFeedback implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) SubmitFeedback(ctx context.Context, req *recommend.FeedbackReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetRecommendationHistory implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) GetRecommendationHistory(ctx context.Context, req *recommend.HistoryReq) (resp *recommend.HistoryResp, err error) {
	// TODO: Your code here...
	return
}

// BatchGetRecommendations implements the RecommendationServiceImpl interface.
func (s *RecommendationServiceImpl) BatchGetRecommendations(ctx context.Context, req *recommend.BatchRecommendationReq) (resp *recommend.BatchRecommendationResp, err error) {
	// TODO: Your code here...
	return
}
