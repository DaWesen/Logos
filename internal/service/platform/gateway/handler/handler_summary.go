package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	pb "Logos/proto_gen/summary"

	"github.com/gin-gonic/gin"
)

func (h *Handler) SummarizeMessages(c *gin.Context) {
	if h.SummaryClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "摘要服务不可用"))
		return
	}
	var req pb.SummarizeMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SummaryClient.SummarizeMessages(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"summary":          resp.Summary,
		"todos":            resp.Todos,
		"reply_candidates": resp.ReplyCandidates,
		"key_points":       resp.KeyPoints,
		"participants":     resp.Participants,
	}})
}

func (h *Handler) GenerateReplyCandidates(c *gin.Context) {
	if h.SummaryClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "摘要服务不可用"))
		return
	}
	var req pb.GenerateReplyCandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SummaryClient.GenerateReplyCandidates(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Candidates})
}

func (h *Handler) ExtractTodos(c *gin.Context) {
	if h.SummaryClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "摘要服务不可用"))
		return
	}
	var req pb.ExtractTodosRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SummaryClient.ExtractTodos(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Todos})
}

func toHTTPCode(code int32) int {
	if code == 0 {
		return http.StatusOK
	}
	return int(code)
}
