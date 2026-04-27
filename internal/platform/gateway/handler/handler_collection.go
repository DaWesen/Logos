package handler

import (
	"context"
	"net/http"

	"Logos/internal/platform/gateway/model"

	pb "Logos/proto_gen/collection"
	pbCommon "Logos/proto_gen/common"

	"github.com/gin-gonic/gin"
)

func (h *Handler) AddDataSource(c *gin.Context) {
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	var req pb.AddDataSourceReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.CollectionClient.AddDataSource(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.DataSource})
}

func (h *Handler) ListDataSources(c *gin.Context) {
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.ListDataSources(context.Background(), &pb.EmptyReq{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.DataSources})
}

func (h *Handler) GetDataSource(c *gin.Context) {
	id := c.Param("id")
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.GetDataSource(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.DataSource})
}

func (h *Handler) UpdateDataSource(c *gin.Context) {
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	var req pb.UpdateDataSourceReq
	req.Id = c.Param("id")
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.CollectionClient.UpdateDataSource(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.DataSource})
}

func (h *Handler) DeleteDataSource(c *gin.Context) {
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	var req pb.DeleteDataSourceReq
	req.Id = c.Param("id")
	resp, err := h.CollectionClient.DeleteDataSource(context.Background(), &req)
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

func (h *Handler) CreateCollectionTask(c *gin.Context) {
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	var req pb.CreateTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.CollectionClient.CreateTask(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Task})
}

func (h *Handler) ListCollectionTasks(c *gin.Context) {
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.ListTasks(context.Background(), &pb.EmptyReq{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Tasks})
}

func (h *Handler) GetCollectionTask(c *gin.Context) {
	id := c.Param("id")
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.GetTask(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Task})
}

func (h *Handler) UpdateCollectionTask(c *gin.Context) {
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	var req pb.UpdateTaskReq
	req.Id = c.Param("id")
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.CollectionClient.UpdateTask(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Task})
}

func (h *Handler) DeleteCollectionTask(c *gin.Context) {
	id := c.Param("id")
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.DeleteTask(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp), Data: map[string]string{"deleted_id": id}})
}

func (h *Handler) ExecuteCollectionTask(c *gin.Context) {
	taskID := c.Param("id")
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	req := &pb.ExecuteTaskReq{TaskId: taskID}
	resp, err := h.CollectionClient.ExecuteTask(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Result})
}

func (h *Handler) StopCollectionTask(c *gin.Context) {
	taskID := c.Param("id")
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.StopTask(context.Background(), &pb.StopByTaskIdReq{TaskId: taskID})
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

func (h *Handler) GetCollectionResults(c *gin.Context) {
	taskID := c.Param("taskId")
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.ListCollectionResults(context.Background(), &pb.GetByTaskIdReq{TaskId: taskID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Results})
}

func (h *Handler) GetCollectionResult(c *gin.Context) {
	id := c.Param("id")
	if h.CollectionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "采集服务暂不可用"))
		return
	}
	resp, err := h.CollectionClient.GetCollectionResult(context.Background(), &pb.GetByIdReq{Id: id})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Result})
}

func _() { _ = (*pbCommon.BaseResp)(nil) }
