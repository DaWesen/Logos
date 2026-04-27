package handler

import (
	"context"
	"net/http"
	"strconv"

	"Logos/internal/service/platform/gateway/model"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/user"

	"github.com/gin-gonic/gin"
)

func (h *Handler) Login(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.Login(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp,
	})
}

func (h *Handler) Register(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	var req pb.RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.Register(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp,
	})
}

func (h *Handler) GetUser(c *gin.Context) {
	id := c.Param("id")
	userID, _ := strconv.ParseInt(id, 10, 64)
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	req := &pb.UserInfoReq{UserId: userID}
	resp, err := h.UserClient.GetUserInfo(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp.User,
	})
}

func (h *Handler) GetUserByUsername(c *gin.Context) {
	username := c.Param("username")
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	req := &pb.UserInfoByUsernameReq{Username: username}
	resp, err := h.UserClient.GetUserInfoByUsername(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp.User,
	})
}

func (h *Handler) BatchGetUsers(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	var req pb.BatchUserInfoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.BatchGetUserInfo(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp.Users,
	})
}

func (h *Handler) UpdateUser(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	var req pb.UpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.UpdateUser(context.Background(), &req)
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

func (h *Handler) UpdateAvatar(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	var req pb.UpdateAvatarReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.UpdateAvatar(context.Background(), &req)
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

func (h *Handler) CheckUsername(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	var req pb.CheckUsernameReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.CheckUsername(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"available": resp.Available,
	}})
}

func (h *Handler) SearchUsers(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	var req pb.SearchUsersReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.SearchUsers(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Users})
}

func (h *Handler) GetUserStats(c *gin.Context) {
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "�û������ݲ�����"))
		return
	}
	req := &pb.UserStatsReq{UserId: userID}
	resp, err := h.UserClient.GetUserStats(context.Background(), req)
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
