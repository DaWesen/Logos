package handler

import (
	"net/http"
	"strconv"

	"Logos/internal/service/platform/gateway/model"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/message"

	"github.com/gin-gonic/gin"
)

// parseTopicParam 解析路径里的消息主题。
//
// 旧实现 `topicID, _ := strconv.ParseInt(...)` 把解析失败吞成 0，
// 于是 /topics/abc 会被当成 DATA_COLLECTION 并真的删掉该主题的数据。
// 非法值和越界值必须在网关层拒绝，不能交给下游“猜”。
func parseTopicParam(raw string) (pb.Topic, bool) {
	id, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, false
	}
	if _, ok := pb.Topic_name[int32(id)]; !ok {
		return 0, false
	}
	return pb.Topic(id), true
}

func (h *Handler) SendMessage(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.SendMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.SendMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: map[string]string{"message_id": resp.MessageId}})
}

func (h *Handler) BatchSendMessage(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.BatchSendMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.BatchSendMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Responses})
}

func (h *Handler) Subscribe(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.SubscribeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.Subscribe(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) ConsumeMessages(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.ConsumeMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.ConsumeMessages(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Messages})
}

func (h *Handler) AcknowledgeMessage(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.AcknowledgeMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.AcknowledgeMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) BatchAcknowledgeMessages(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.BatchAcknowledgeMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.BatchAcknowledgeMessages(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) GetMessageStats(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	resp, err := h.MessageClient.GetMessageStats(c.Request.Context(), &pb.EmptyReq{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Stats})
}

func (h *Handler) CreateTopic(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.GetByTopicReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.CreateTopic(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) DeleteTopic(c *gin.Context) {
	topic, ok := parseTopicParam(c.Param("topic"))
	if !ok {
		c.JSON(http.StatusBadRequest, model.BadRequest("invalid topic"))
		return
	}
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	req := &pb.GetByTopicReq{Topic: topic}
	resp, err := h.MessageClient.DeleteTopic(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) ClearMessages(c *gin.Context) {
	if h.MessageClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.GetByTopicReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MessageClient.ClearMessages(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func _() { _ = (*pbCommon.BaseResp)(nil) }
