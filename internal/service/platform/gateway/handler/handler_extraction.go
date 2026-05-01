package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/extraction"

	"github.com/gin-gonic/gin"
)

func (h *Handler) CreateTask(c *gin.Context) {
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.CreateExtractionTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ExtractionClient.CreateTask(context.Background(), &req)
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

func (h *Handler) ListTasks(c *gin.Context) {
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	resp, err := h.ExtractionClient.ListTasks(context.Background(), &pb.EmptyReq{})
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

func (h *Handler) GetTask(c *gin.Context) {
	id := c.Param("id")
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	resp, err := h.ExtractionClient.GetTask(context.Background(), &pb.GetByIdReq{Id: id})
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

func (h *Handler) UpdateTask(c *gin.Context) {
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	var req pb.UpdateExtractionTaskReq
	req.Id = c.Param("id")
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ExtractionClient.UpdateTask(context.Background(), &req)
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

func (h *Handler) DeleteTask(c *gin.Context) {
	id := c.Param("id")
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	resp, err := h.ExtractionClient.DeleteTask(context.Background(), &pb.GetByIdReq{Id: id})
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

func (h *Handler) ExecuteTask(c *gin.Context) {
	taskID := c.Param("id")
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	req := &pb.ExecuteExtractionTaskReq{TaskId: taskID}
	resp, err := h.ExtractionClient.ExecuteTask(context.Background(), req)
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

func (h *Handler) CancelTask(c *gin.Context) {
	taskID := c.Param("id")
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	resp, err := h.ExtractionClient.CancelTask(context.Background(), &pb.CancelByTaskIdReq{TaskId: taskID})
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

func (h *Handler) ExtractFromText(c *gin.Context) {
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	var req pb.TextExtractionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ExtractionClient.ExtractFromText(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"entities":  resp.Entities,
		"relations": resp.Relations,
		"triples":   resp.Triples,
	}})
}

func (h *Handler) GetResults(c *gin.Context) {
	taskID := c.Param("taskId")
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	resp, err := h.ExtractionClient.ListExtractionResults(context.Background(), &pb.GetByTaskIdReq{TaskId: taskID})
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

func (h *Handler) GetResult(c *gin.Context) {
	id := c.Param("id")
	if h.ExtractionClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "��ȡ�����ݲ�����"))
		return
	}
	resp, err := h.ExtractionClient.GetExtractionResult(context.Background(), &pb.GetByIdReq{Id: id})
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
