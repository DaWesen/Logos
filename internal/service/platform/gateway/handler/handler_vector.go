package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"Logos/internal/service/platform/gateway/model"

	pb "Logos/proto_gen/vector"

	"github.com/gin-gonic/gin"
)

func (h *Handler) CreateCollection(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.CreateCollectionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.CreateCollection(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Collection})
}

func (h *Handler) ListCollections(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	resp, err := h.VectorClient.ListCollections(context.Background(), &pb.EmptyReq{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Collections})
}

func (h *Handler) GetCollection(c *gin.Context) {
	id := c.Param("id")
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	resp, err := h.VectorClient.GetCollection(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Collection})
}

func (h *Handler) UpdateCollection(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.UpdateCollectionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.UpdateCollection(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Collection})
}

func (h *Handler) DeleteCollection(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.DeleteCollectionReq
	req.Id = c.Param("id")
	resp, err := h.VectorClient.DeleteCollection(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = mapStatusCode(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp), Data: map[string]string{"deleted_id": req.Id}})
}

func (h *Handler) Vectorize(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.VectorizeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.Vectorize(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Vector})
}

func (h *Handler) BatchVectorize(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.BatchVectorizeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.BatchVectorize(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Vectors})
}

func (h *Handler) VectorSearchByCollection(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.SearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.Search(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Results})
}

func (h *Handler) TextSearchByCollection(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.TextSearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.TextSearch(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Results})
}

func (h *Handler) DeleteVector(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.DeleteVectorReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.DeleteVector(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = mapStatusCode(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) ListVectors(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusServiceUnavailable,
			Message: "vector service unavailable",
		})
		return
	}
	collectionID := c.Param("id")
	if collectionID == "" {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusBadRequest,
			Message: "collection_id is required",
		})
		return
	}
	page := int32(1)
	pageSize := int32(20)
	if p, err := strconv.ParseInt(c.Query("page"), 10, 32); err == nil && p > 0 {
		page = int32(p)
	}
	if ps, err := strconv.ParseInt(c.Query("page_size"), 10, 32); err == nil && ps > 0 {
		pageSize = int32(ps)
	}
	resp, err := h.VectorClient.ListVectors(context.Background(), &pb.ListVectorsReq{
		CollectionId: collectionID,
		Page:         page,
		PageSize:     pageSize,
	})
	if err != nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("vector service error: %v", err),
		})
		return
	}
	statusCode := mapStatusCode(resp.BaseResp.GetStatusCode())
	c.JSON(http.StatusOK, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data: map[string]interface{}{
			"vectors":   resp.Vectors,
			"total":     resp.Total,
			"page":      resp.Page,
			"page_size": resp.PageSize,
		},
	})
}

func (h *Handler) BatchDeleteVector(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.BatchDeleteVectorReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.VectorClient.BatchDeleteVector(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = mapStatusCode(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}
