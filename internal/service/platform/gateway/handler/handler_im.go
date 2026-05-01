package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"

	pb "Logos/proto_gen/im"

	"github.com/gin-gonic/gin"
)

func (h *Handler) ConnectIM(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.ConnectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.Connect(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) DisconnectIM(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.DisconnectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.Disconnect(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetOnlineStatus(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.GetOnlineStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.GetOnlineStatus(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) SetOnlineStatus(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.SetOnlineStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.SetOnlineStatus(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) SendTypingStatus(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.SendTypingStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.SendTypingStatus(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) BroadcastIMMessage(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.BroadcastMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.BroadcastMessage(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) SyncOfflineMessages(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.SyncOfflineMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.SyncOfflineMessages(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) StreamMessages(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.ConnectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	stream, err := h.IMClient.StreamMessages(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: stream})
}
