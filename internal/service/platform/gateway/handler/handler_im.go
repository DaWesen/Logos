package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/push"

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

func (h *Handler) HeartbeatIM(c *gin.Context) {
	if h.IMClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.HeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.IMClient.Heartbeat(context.Background(), &req)
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

func (h *Handler) RegisterPushToken(c *gin.Context) {
	var req struct {
		DeviceID string `json:"device_id" binding:"required"`
		Platform string `json:"platform" binding:"required"`
		Token    string `json:"token" binding:"required"`
		BundleID string `json:"bundle_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)

	pushMgr := push.GetPushManager()
	pushMgr.RegisterToken(&push.PushToken{
		UserID:   uid,
		DeviceID: req.DeviceID,
		Platform: push.Platform(req.Platform),
		Token:    req.Token,
		BundleID: req.BundleID,
	})

	c.JSON(http.StatusOK, model.Success("推送令牌注册成功"))
}

func (h *Handler) UnregisterPushToken(c *gin.Context) {
	var req struct {
		DeviceID string `json:"device_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	userID, _ := c.Get("user_id")
	uid, _ := userID.(string)

	pushMgr := push.GetPushManager()
	pushMgr.UnregisterToken(uid, req.DeviceID)

	c.JSON(http.StatusOK, model.Success("推送令牌注销成功"))
}
