package handler

import (
	"context"
	"net/http"
	"strconv"

	"Logos/internal/platform/gateway/model"

	pb "Logos/proto_gen/search"
	pbCommon "Logos/proto_gen/common"

	"github.com/gin-gonic/gin"
)

func (h *Handler) Search(c *gin.Context) {
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	var req pb.SearchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SearchClient.Search(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"results":    resp.Results,
		"total":      resp.Total,
		"searchTime": resp.SearchTime,
	}})
}

func (h *Handler) AddDocument(c *gin.Context) {
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	var req pb.AddDocumentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SearchClient.AddDocument(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Document})
}

func (h *Handler) UpdateDocument(c *gin.Context) {
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	var req pb.UpdateDocumentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SearchClient.UpdateDocument(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Document})
}

func (h *Handler) DeleteDocument(c *gin.Context) {
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	var req pb.DeleteDocumentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SearchClient.DeleteDocument(context.Background(), &req)
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

func (h *Handler) GetDocument(c *gin.Context) {
	id := c.Param("id")
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	resp, err := h.SearchClient.GetDocument(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Document})
}

func (h *Handler) BatchAddDocuments(c *gin.Context) {
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	var req pb.BatchAddDocumentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SearchClient.BatchAddDocuments(context.Background(), &req)
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

func (h *Handler) BatchDeleteDocuments(c *gin.Context) {
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	var req pb.BatchDeleteDocumentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.SearchClient.BatchDeleteDocuments(context.Background(), &req)
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

func (h *Handler) CreateIndex(c *gin.Context) {
	indexTypeStr := c.Param("type")
	indexType, _ := strconv.ParseInt(indexTypeStr, 10, 64)
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	resp, err := h.SearchClient.CreateIndex(context.Background(), &pb.GetByIndexTypeReq{Type: pb.IndexType(indexType)})
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

func (h *Handler) DeleteIndex(c *gin.Context) {
	indexTypeStr := c.Param("type")
	indexType, _ := strconv.ParseInt(indexTypeStr, 10, 64)
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	resp, err := h.SearchClient.DeleteIndex(context.Background(), &pb.GetByIndexTypeReq{Type: pb.IndexType(indexType)})
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

func (h *Handler) RefreshIndex(c *gin.Context) {
	indexTypeStr := c.Param("type")
	indexType, _ := strconv.ParseInt(indexTypeStr, 10, 64)
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	resp, err := h.SearchClient.RefreshIndex(context.Background(), &pb.GetByIndexTypeReq{Type: pb.IndexType(indexType)})
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

func (h *Handler) GetIndexStats(c *gin.Context) {
	if h.SearchClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "搜索服务暂不可用"))
		return
	}
	resp, err := h.SearchClient.GetIndexStats(context.Background(), &pb.EmptyReq{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Stats})
}

func _() { _ = (*pbCommon.BaseResp)(nil) }
