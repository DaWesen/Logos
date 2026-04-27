package handler

import (
	"context"
	"net/http"
	"strconv"

	"Logos/internal/platform/gateway/model"

	pb "Logos/proto_gen/message"
	pbCommon "Logos/proto_gen/common"

	"github.com/gin-gonic/gin"
)

func (h *Handler) SendMessage(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.SendMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.SendMessage(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: map[string]string{"message_id": resp.MessageId}})
}

func (h *Handler) BatchSendMessage(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.BatchSendMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.BatchSendMessage(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Responses})
}

func (h *Handler) Subscribe(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.SubscribeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.Subscribe(context.Background(), &req)
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

func (h *Handler) ConsumeMessages(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.ConsumeMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.ConsumeMessages(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Messages})
}

func (h *Handler) AcknowledgeMessage(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.AcknowledgeMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.AcknowledgeMessage(context.Background(), &req)
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

func (h *Handler) BatchAcknowledgeMessages(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.BatchAcknowledgeMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.BatchAcknowledgeMessages(context.Background(), &req)
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

func (h *Handler) GetMessageStats(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	resp, err := h.MessageClient.GetMessageStats(context.Background(), &pb.EmptyReq{})
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

func (h *Handler) CreateTopic(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.GetByTopicReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.CreateTopic(context.Background(), &req)
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

func (h *Handler) DeleteTopic(c *gin.Context) {
	topicID, _ := strconv.ParseInt(c.Param("topic"), 10, 64)
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	req := &pb.GetByTopicReq{Topic: pb.Topic(topicID)}
	resp, err := h.MessageClient.DeleteTopic(context.Background(), req)
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

func (h *Handler) ClearMessages(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "消息服务暂不可用"))
		return
	}
	var req pb.GetByTopicReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.ClearMessages(context.Background(), &req)
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
