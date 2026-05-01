package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	pb "Logos/proto_gen/moderation"

	"github.com/gin-gonic/gin"
)

func (h *Handler) Translate(c *gin.Context) {
	if h.ModerationClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "审核服务不可用"))
		return
	}
	var req pb.TranslateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ModerationClient.Translate(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"translated_content": resp.TranslatedContent,
		"source_lang":        resp.SourceLang,
		"target_lang":        resp.TargetLang,
	}})
}

func (h *Handler) ModerateContent(c *gin.Context) {
	if h.ModerationClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "审核服务不可用"))
		return
	}
	var req pb.ModerateContentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ModerationClient.ModerateContent(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: resp.Data})
}

func (h *Handler) GetModerationRecords(c *gin.Context) {
	if h.ModerationClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "审核服务不可用"))
		return
	}
	var req pb.GetModerationRecordsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ModerationClient.GetModerationRecords(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(toHTTPCode(resp.Code), model.Response{Code: int(resp.Code), Message: resp.Message, Data: gin.H{
		"records": resp.Records,
		"total":   resp.Total,
	}})
}
