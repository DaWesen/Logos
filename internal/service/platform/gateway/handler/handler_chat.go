package handler

import (
	"context"
	"net/http"

	"Logos/internal/service/platform/gateway/model"

	pb "Logos/proto_gen/chat"

	"github.com/gin-gonic/gin"
)

func (h *Handler) SendChatMessage(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.SendMessage(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) SearchChatMessages(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.SearchMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.SearchMessages(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetChatHistory(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.GetMessageHistoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.GetMessageHistory(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) MarkChatMessagesRead(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.MarkMessagesReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.MarkMessagesRead(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) WithdrawChatMessage(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.WithdrawMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.WithdrawMessage(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) EditChatMessage(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.EditMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.EditMessage(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) CreateChatGroup(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.CreateGroup(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) InviteGroupMember(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.InviteGroupMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.InviteGroupMember(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) KickGroupMember(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.KickGroupMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.KickGroupMember(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) MuteGroupMember(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.MuteGroupMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.MuteGroupMember(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) TransferGroupOwner(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.TransferGroupOwnerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.TransferGroupOwner(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) UpdateGroupAnnouncement(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.UpdateGroupAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.UpdateGroupAnnouncement(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) SetGroupAdmin(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.SetGroupAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.SetGroupAdmin(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetGroupMembers(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.GetGroupMembersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.GetGroupMembers(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) JoinChatGroup(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.JoinGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.JoinGroup(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) LeaveChatGroup(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.LeaveGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.LeaveGroup(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetChatGroup(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.GetGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.GetGroup(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}
