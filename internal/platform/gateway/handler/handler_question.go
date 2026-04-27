package handler

import (
	"context"
	"net/http"
	"strconv"

	"Logos/internal/platform/gateway/model"

	pb "Logos/proto_gen/question"
	pbCommon "Logos/proto_gen/common"

	"github.com/gin-gonic/gin"
)

func (h *Handler) AskQuestion(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "问答服务暂不可用"))
		return
	}
	var req pb.QuestionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.QuestionClient.AskQuestion(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"answer":     resp.Answer,
		"confidence": resp.Confidence,
		"sources":    resp.Sources,
		"questionId": resp.QuestionId,
		"timestamp":  resp.Timestamp,
	}})
}

func (h *Handler) BatchAskQuestions(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "问答服务暂不可用"))
		return
	}
	var req pb.BatchQuestionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.QuestionClient.BatchAskQuestions(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Answers})
}

func (h *Handler) GetHistory(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "问答服务暂不可用"))
		return
	}
	userID, _ := strconv.ParseInt(c.Query("user_id"), 10, 64)
	if userID == 0 {
		if uid, exists := c.Get("user_id"); exists {
			if uidStr, ok := uid.(string); ok {
				userID, _ = strconv.ParseInt(uidStr, 10, 64)
			}
		}
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	req := &pb.HistoryReq{
		UserId:   userID,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}
	resp, err := h.QuestionClient.GetHistory(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"records": resp.Records,
		"total":   resp.Total,
	}})
}

func (h *Handler) SubmitFeedback(c *gin.Context) {
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "问答服务暂不可用"))
		return
	}
	var req pb.FeedbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.QuestionClient.SubmitFeedback(context.Background(), &req)
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

func (h *Handler) GetRecommendedQuestions(c *gin.Context) {
	userID, _ := strconv.ParseInt(c.Param("userId"), 10, 64)
	if h.QuestionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "问答服务暂不可用"))
		return
	}
	resp, err := h.QuestionClient.GetRecommendedQuestions(context.Background(), &pb.GetRecommendedQuestionsReq{UserId: userID})
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

func _() { _ = (*pbCommon.BaseResp)(nil) }
