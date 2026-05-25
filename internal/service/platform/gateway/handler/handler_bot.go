package handler

import (
	"context"
	"net/http"
	"strconv"
	"Logos/internal/service/platform/gateway/model"

	pb "Logos/proto_gen/bot"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *Handler) CreateBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.CreateBotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	// 从 gin.Context 获取 userID
	if userID, exists := c.Get("user_id"); exists {
		req.UserId, _ = userID.(string)
	}

	resp, err := h.BotClient.CreateBot(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) UpdateBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.UpdateBotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	req.BotId = c.Param("id")

	if userID, exists := c.Get("user_id"); exists {
		req.UserId, _ = userID.(string)
	}

	resp, err := h.BotClient.UpdateBot(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) DeleteBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	id := c.Param("id")
	req := &pb.DeleteBotRequest{BotId: id}
	resp, err := h.BotClient.DeleteBot(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
	})
}

func (h *Handler) GetBot(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	id := c.Param("id")
	req := &pb.GetBotRequest{BotId: id}
	resp, err := h.BotClient.GetBot(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Data,
	})
}

func (h *Handler) ListBots(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	var botType pb.BotType
	if t := c.Query("type"); t == "builtin" {
		botType = pb.BotType_BOT_TYPE_BUILTIN
	} else if t == "custom" {
		botType = pb.BotType_BOT_TYPE_CUSTOM
	} else {
		botType = pb.BotType_BOT_TYPE_UNSPECIFIED
	}

	var status pb.BotStatus
	if s := c.Query("status"); s == "active" {
		status = pb.BotStatus_BOT_STATUS_ACTIVE
	} else if s == "inactive" {
		status = pb.BotStatus_BOT_STATUS_INACTIVE
	} else if s == "deleted" {
		status = pb.BotStatus_BOT_STATUS_DELETED
	} else {
		status = pb.BotStatus_BOT_STATUS_UNSPECIFIED
	}

	req := &pb.ListBotsRequest{
		Type:     botType,
		Status:   status,
		Page:     int32(page),
		PageSize: int32(pageSize),
	}

	// 从 gin.Context 获取 userID
	if userID, exists := c.Get("user_id"); exists {
		req.UserId, _ = userID.(string)
	}

	resp, err := h.BotClient.ListBots(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data: gin.H{
			"bots":  resp.Bots,
			"total": resp.Total,
		},
	})
}

func (h *Handler) SendBotMessage(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.SendBotMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	if userID, exists := c.Get("user_id"); exists {
		req.UserId, _ = userID.(string)
	}

	var header metadata.MD
	resp, err := h.BotClient.SendBotMessage(context.Background(), &req, grpc.Header(&header))
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	data := gin.H{
		"content": resp.Content,
		"done":    resp.Done,
		"chat_id": resp.ChatId,
	}

	if costVals := header.Get("x-cost"); len(costVals) > 0 {
		if cost, parseErr := strconv.ParseFloat(costVals[0], 64); parseErr == nil {
			data["cost"] = cost
		}
	}
	if tokenVals := header.Get("x-tokens"); len(tokenVals) > 0 {
		if tokens, parseErr := strconv.ParseInt(tokenVals[0], 10, 64); parseErr == nil {
			data["tokens"] = tokens
		}
	}

	c.JSON(http.StatusOK, model.Response{
		Code:    200,
		Message: "success",
		Data:    data,
	})
}

func (h *Handler) GetBotHistory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	botID := c.Query("bot_id")
	chatID := c.Query("chat_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))

	req := &pb.GetBotHistoryRequest{
		BotId:  botID,
		ChatId: chatID,
		Limit:  int32(limit),
	}

	if beforeTime := c.Query("before_time"); beforeTime != "" {
		if t, err := strconv.ParseInt(beforeTime, 10, 64); err == nil {
			ts := &timestamppb.Timestamp{Seconds: t}
			req.BeforeTime = ts
		}
	}

	resp, err := h.BotClient.GetBotHistory(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data: gin.H{
			"messages": resp.Messages,
			"hasMore":  resp.HasMore,
		},
	})
}

func (h *Handler) GetUserMemory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	botID := c.Query("bot_id")
	userID, _ := c.Get("user_id")

	req := &pb.GetUserMemoryRequest{
		UserId: userID.(string),
		BotId:  botID,
	}

	resp, err := h.BotClient.GetUserMemory(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
		Data:    resp.Memories,
	})
}

func (h *Handler) SetUserMemory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.SetUserMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	if userID, exists := c.Get("user_id"); exists {
		req.UserId, _ = userID.(string)
	}

	resp, err := h.BotClient.SetUserMemory(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
	})
}

func (h *Handler) DeleteUserMemory(c *gin.Context) {
	if h.BotClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	botID := c.Query("bot_id")
	key := c.Query("key")
	userID, _ := c.Get("user_id")

	req := &pb.DeleteUserMemoryRequest{
		UserId: userID.(string),
		BotId:  botID,
		Key:    key,
	}

	resp, err := h.BotClient.DeleteUserMemory(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.Code != 0 {
		statusCode = int(resp.Code)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: resp.Message,
	})
}
