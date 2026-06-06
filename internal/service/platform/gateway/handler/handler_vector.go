package handler

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"runtime/debug"
	"strconv"

	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/logger"

	pb "Logos/proto_gen/vector"

	"github.com/gin-gonic/gin"
)

// safeBaseResp 安全获取 BaseResp 的状态码和消息
// 注意：Go中nil指针传入interface参数时，interface不为nil（有类型但值为nil），
// 所以需要用reflect检查，否则baseResp==nil判断会失效
func safeBaseResp(baseResp interface {
	GetStatusCode() int32
	GetStatusMessage() string
}) (int, string) {
	if baseResp == nil {
		return http.StatusInternalServerError, "internal error"
	}
	// 处理nil指针被包装为非nil interface的情况
	// protobuf生成的GetStatusCode/GetStatusMessage是nil-safe的，
	// 但逻辑上BaseResp为nil应视为错误
	if v := reflect.ValueOf(baseResp); v.Kind() == reflect.Ptr && v.IsNil() {
		return http.StatusInternalServerError, "internal error"
	}
	return mapStatusCode(baseResp.GetStatusCode()), baseResp.GetStatusMessage()
}

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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Collection})
}

func (h *Handler) ListCollections(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	resp, err := h.VectorClient.ListCollections(context.Background(), &pb.EmptyReq{})
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Collections})
}

func (h *Handler) GetCollection(c *gin.Context) {
	id := c.Param("id")
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	resp, err := h.VectorClient.GetCollection(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Collection})
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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Collection})
}

func (h *Handler) DeleteCollection(c *gin.Context) {
	if h.VectorClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "vector service unavailable"))
		return
	}
	var req pb.DeleteCollectionReq
	req.Id = c.Param("id")
	resp, err := h.VectorClient.DeleteCollection(context.Background(), &req)
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Vector})
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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Vectors})
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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Results})
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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode, msg := safeBaseResp(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: msg, Data: resp.Results})
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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = mapStatusCode(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) ListVectors(c *gin.Context) {
	// 防止 panic 导致整个服务崩溃，并记录堆栈
	defer func() {
		if r := recover(); r != nil {
			logger.Error("ListVectors handler panic recovered",
				logger.AnyField("panic", r),
				logger.StringField("stack", string(debug.Stack())))
			c.JSON(http.StatusOK, model.Response{
				Code:    http.StatusInternalServerError,
				Message: fmt.Sprintf("internal error: %v", r),
			})
		}
	}()

	if h.VectorClient == nil {
		logger.Warn("ListVectors: VectorClient is nil")
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

	logger.Info("ListVectors: calling gRPC",
		logger.StringField("collection_id", collectionID),
		logger.IntField("page", int(page)),
		logger.IntField("page_size", int(pageSize)))

	resp, err := h.VectorClient.ListVectors(context.Background(), &pb.ListVectorsReq{
		CollectionId: collectionID,
		Page:         page,
		PageSize:     pageSize,
	})

	logger.Info("ListVectors: gRPC call completed",
		logger.ErrorField(err),
		logger.BoolField("resp_nil", resp == nil))

	if err != nil || resp == nil {
		c.JSON(http.StatusOK, model.Response{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("vector service error: %v", err),
		})
		return
	}

	logger.Info("ListVectors: checking BaseResp",
		logger.BoolField("base_resp_nil", resp.BaseResp == nil))

	statusCode, msg := safeBaseResp(resp.BaseResp)

	logger.Info("ListVectors: building response",
		logger.IntField("status_code", statusCode),
		logger.StringField("msg", msg),
		logger.IntField("vectors_count", len(resp.Vectors)),
		logger.Int64Field("total", resp.Total))

	c.JSON(http.StatusOK, model.Response{
		Code:    statusCode,
		Message: msg,
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
	if err != nil || resp == nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(fmt.Sprintf("vector service error: %v", err)))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = mapStatusCode(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}
