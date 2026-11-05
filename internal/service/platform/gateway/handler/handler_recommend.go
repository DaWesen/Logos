package handler

import (
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/pagination"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/recommend"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetRecommendations(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	// 推荐结果同样按用户画像生成，?user_id= 只能等于登录用户，否则 403。
	userID, ok := h.resolveTargetUserID(c)
	if !ok {
		return
	}
	limit, ok := parseLimit(c, "limit", 20, pagination.MaxPageSize)
	if !ok {
		return
	}

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

	resp, err := h.RecommendClient.GetRecommendations(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"items": resp.Items,
		"total": resp.Total,
	}})
}

func (h *Handler) GetRelatedRecommendations(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	entityID := c.Param("entityId")
	limit, ok := parseLimit(c, "limit", 20, pagination.MaxPageSize)
	if !ok {
		return
	}

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

	resp, err := h.RecommendClient.GetRelatedRecommendations(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"items": resp.Items,
		"total": resp.Total,
	}})
}

func (h *Handler) SubmitRecommendFeedback(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.FeedbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.RecommendClient.SubmitFeedback(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) GetRecommendationHistory(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	userID, ok := h.resolveTargetUserID(c)
	if !ok {
		return
	}
	page, pageSize, ok := parsePagination(c, "page", "page_size", pagination.DefaultPageSize)
	if !ok {
		return
	}

	req := &pb.HistoryReq{
		UserId:   userID,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}
	resp, err := h.RecommendClient.GetRecommendationHistory(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"items": resp.Items,
		"total": resp.Total,
	}})
}

func (h *Handler) BatchGetRecommendations(c *gin.Context) {
	if h.RecommendClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.BatchRecommendationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.RecommendClient.BatchGetRecommendations(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Recommendations})
}

func _() { _ = (*pbCommon.BaseResp)(nil) }
