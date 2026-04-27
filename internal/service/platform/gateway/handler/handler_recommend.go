package handler

import (
	"context"
	"net/http"
	"strconv"

	"Logos/internal/service/platform/gateway/model"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/recommend"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetRecommendations(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	req := &pb.RecommendationReq{
		UserId: userID,
	}
	if limit > 0 {
		limit32 := int32(limit)
		req.Limit = &limit32
	}
	if recType := c.Query("type"); recType != "" {
		req.Type = &recType
	}

	resp, err := h.RecommendClient.GetRecommendations(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"items": resp.Items,
		"total": resp.Total,
	}})
}

func (h *Handler) GetRelatedRecommendations(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�Ƽ������ݲ�����"))
		return
	}
	entityID := c.Param("entityId")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	req := &pb.RelatedRecommendationReq{
		EntityId: entityID,
	}
	if limit > 0 {
		limit32 := int32(limit)
		req.Limit = &limit32
	}
	if recType := c.Query("type"); recType != "" {
		req.Type = &recType
	}

	resp, err := h.RecommendClient.GetRelatedRecommendations(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"items": resp.Items,
		"total": resp.Total,
	}})
}

func (h *Handler) SubmitRecommendFeedback(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�Ƽ������ݲ�����"))
		return
	}
	var req pb.FeedbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.RecommendClient.SubmitFeedback(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) GetRecommendationHistory(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�Ƽ������ݲ�����"))
		return
	}
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	req := &pb.HistoryReq{
		UserId:   userID,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}
	resp, err := h.RecommendClient.GetRecommendationHistory(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"items": resp.Items,
		"total": resp.Total,
	}})
}

func (h *Handler) BatchGetRecommendations(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�Ƽ������ݲ�����"))
		return
	}
	var req pb.BatchRecommendationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.RecommendClient.BatchGetRecommendations(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Recommendations})
}

func _() { _ = (*pbCommon.BaseResp)(nil) }
