package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"Logos/config"
	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/logger"

	pb "Logos/proto_gen/user"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) Login(c *gin.Context) {
	logger.Info("Login request received")
	if h.UserClient == nil {
		logger.Error("UserClient is nil - user service is not connected")
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务暂时不可用，请稍后重试"))
		return
	}

	var req pb.LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error("Failed to bind JSON", logger.ErrorField(err))
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	logger.Info("Calling UserClient.Login", logger.StringField("username", req.Username))

	// 添加更长的超时时间
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	resp, err := h.UserClient.Login(ctx, &req)
	if err != nil {
		logger.Error("UserClient.Login failed",
			logger.ErrorField(err),
			logger.StringField("username", req.Username),
		)
		c.JSON(http.StatusInternalServerError, model.InternalError("登录失败："+err.Error()))
		return
	}

	logger.Info("UserClient.Login success",
		logger.IntField("status_code", int(resp.BaseResp.StatusCode)),
		logger.StringField("message", resp.BaseResp.StatusMessage),
	)

	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp,
	})
}

func (h *Handler) Register(c *gin.Context) {
	logger.Info("Register request received")
	if h.UserClient == nil {
		logger.Error("UserClient is nil - user service is not connected")
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务暂时不可用，请稍后重试"))
		return
	}

	// 先读取原始请求，查看前端传来什么
	var rawReq map[string]interface{}
	if err := c.ShouldBindJSON(&rawReq); err != nil {
		logger.Error("Failed to bind JSON", logger.ErrorField(err))
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	// 构造 gRPC 请求
	var req pb.RegisterReq
	req.Username = getString(rawReq, "username", "")
	req.Password = getString(rawReq, "password", "")

	// 处理可选字段
	if email, ok := rawReq["email"].(string); ok && email != "" {
		req.Email = &email
	}
	if phone, ok := rawReq["phone"].(string); ok && phone != "" {
		req.Phone = &phone
	}

	logger.Info("Calling UserClient.Register",
		logger.StringField("username", req.Username),
	)

	// 添加更长的超时时间
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	resp, err := h.UserClient.Register(ctx, &req)
	if err != nil {
		logger.Error("UserClient.Register failed",
			logger.ErrorField(err),
			logger.StringField("username", req.Username),
		)
		c.JSON(http.StatusInternalServerError, model.InternalError("注册失败："+err.Error()))
		return
	}

	logger.Info("UserClient.Register success",
		logger.IntField("status_code", int(resp.BaseResp.StatusCode)),
		logger.StringField("message", resp.BaseResp.StatusMessage),
	)

	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp,
	})
}

func getString(m map[string]interface{}, key, defaultValue string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return defaultValue
}

func (h *Handler) GetUser(c *gin.Context) {
	id := c.Param("id")
	userID, _ := strconv.ParseInt(id, 10, 64)
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}
	req := &pb.UserInfoReq{UserId: userID}
	resp, err := h.UserClient.GetUserInfo(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp.User,
	})
}

func (h *Handler) GetUserByUsername(c *gin.Context) {
	username := c.Param("username")
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}
	req := &pb.UserInfoByUsernameReq{Username: username}
	resp, err := h.UserClient.GetUserInfoByUsername(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp.User,
	})
}

func (h *Handler) BatchGetUsers(c *gin.Context) {
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}
	var req pb.BatchUserInfoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.UserClient.BatchGetUserInfo(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data:    resp.Users,
	})
}

func (h *Handler) UpdateUser(c *gin.Context) {
	if h.UserClient == nil {
		logger.Error("UserClient is nil")
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}

	// 优先从认证信息获取用户 ID
	var userID int64
	if uid, exists := c.Get("user_id"); exists {
		if idStr, ok := uid.(string); ok {
			if parsed, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				userID = parsed
			}
		}
	}

	var req pb.UpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error("Failed to bind JSON", logger.ErrorField(err))
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	// 如果请求里有 user_id，用请求的，否则用认证的
	if req.UserId == 0 && userID > 0 {
		req.UserId = userID
	}

	logger.Info("Updating user", logger.Int64Field("user_id", req.UserId))

	if req.UserId == 0 {
		logger.Error("User ID is 0, cannot update")
		c.JSON(http.StatusBadRequest, model.BadRequest("请提供用户 ID"))
		return
	}

	resp, err := h.UserClient.UpdateUser(c.Request.Context(), &req)
	if err != nil {
		logger.Error("Update user failed", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	logger.Info("Update user success", logger.Int64Field("user_id", req.UserId))

	statusCode := 200
	if resp != nil {
		statusCode = mapStatusCode(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) UpdateAvatar(c *gin.Context) {
	if h.UserClient == nil {
		logger.Error("UserClient is nil")
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}
	if h.MinioManager == nil {
		logger.Error("MinioManager is nil")
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "存储服务不可用"))
		return
	}

	header, err := c.FormFile("avatar")
	if err != nil {
		logger.Error("Failed to get avatar file", logger.ErrorField(err))
		c.JSON(http.StatusBadRequest, model.BadRequest("请上传头像文件"))
		return
	}

	file, err := header.Open()
	if err != nil {
		logger.Error("Failed to open avatar file", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError("打开文件失败"))
		return
	}
	defer file.Close()

	fileData := make([]byte, header.Size)
	_, err = file.Read(fileData)
	if err != nil {
		logger.Error("Failed to read avatar file", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError("读取文件失败"))
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}
	objectKey := fmt.Sprintf("avatars/%s/%s%s", time.Now().Format("2006/01/02"), uuid.New().String(), ext)

	cfg := config.GetConfig()
	bucket := cfg.Minio.Bucket
	if bucket == "" {
		bucket = "logos"
	}

	err = h.MinioManager.UploadFile(bucket, objectKey, bytes.NewReader(fileData), header.Size, header.Header.Get("Content-Type"))
	if err != nil {
		logger.Error("上传头像失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError("上传失败"))
		return
	}

	proxyURL := "/api/v1/file/minio/" + objectKey

	userID := int64(0)
	if uid, exists := c.Get("user_id"); exists {
		if idStr, ok := uid.(string); ok {
			if parsedID, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				userID = parsedID
			}
		}
	}

	logger.Info("Updating avatar", logger.Int64Field("user_id", userID), logger.StringField("url", proxyURL))

	req := &pb.UpdateAvatarReq{
		UserId:     userID,
		AvatarData: []byte(proxyURL),
	}

	resp, err := h.UserClient.UpdateAvatar(c.Request.Context(), req)
	if err != nil {
		logger.Error("Update avatar failed", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	logger.Info("Update avatar success", logger.Int64Field("user_id", userID), logger.StringField("url", proxyURL))

	statusCode := 200
	if resp != nil {
		statusCode = mapStatusCode(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp),
		Data: gin.H{
			"url": proxyURL,
		},
	})
}

func (h *Handler) CheckUsername(c *gin.Context) {
	if h.UserClient == nil {
		logger.Error("UserClient is nil")
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}
	username := c.Query("username")
	if username == "" {
		// 支持 POST JSON 方式
		var req pb.CheckUsernameReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
			return
		}
		username = req.Username
	}

	if username == "" {
		c.JSON(http.StatusBadRequest, model.BadRequest("请提供用户名"))
		return
	}

	logger.Info("Checking username", logger.StringField("username", username))

	req := &pb.CheckUsernameReq{Username: username}
	resp, err := h.UserClient.CheckUsername(c.Request.Context(), req)
	if err != nil {
		logger.Error("Check username failed", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: gin.H{
		"available": resp.Available,
	}})
}

func (h *Handler) SearchUsers(c *gin.Context) {
	if h.UserClient == nil {
		logger.Error("UserClient is nil")
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}

	var req pb.SearchUsersReq
	req.Keyword = c.Query("keyword")
	pageStr := c.Query("page")
	pageSizeStr := c.Query("page_size")

	if req.Keyword == "" {
		// 支持 POST JSON 方式
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
			return
		}
	} else {
		// 使用 GET 查询参数
		if pageStr != "" {
			if page, err := strconv.Atoi(pageStr); err == nil {
				req.Page = int32(page)
			}
		}
		if pageSizeStr != "" {
			if pageSize, err := strconv.Atoi(pageSizeStr); err == nil {
				req.PageSize = int32(pageSize)
			}
		}
		// 默认值
		if req.Page <= 0 {
			req.Page = 1
		}
		if req.PageSize <= 0 {
			req.PageSize = 20
		}
	}

	logger.Info("Searching users", logger.StringField("keyword", req.Keyword))

	resp, err := h.UserClient.SearchUsers(c.Request.Context(), &req)
	if err != nil {
		logger.Error("Search users failed", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{
		Code:    statusCode,
		Message: getBaseRespMessage(resp.BaseResp),
		Data: gin.H{
			"users": resp.Users,
			"total": resp.Total,
		},
	})
}

func (h *Handler) GetUserStats(c *gin.Context) {
	userID, _ := strconv.ParseInt(c.DefaultQuery("user_id", "0"), 10, 64)
	if h.UserClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "用户服务不可用"))
		return
	}
	req := &pb.UserStatsReq{UserId: userID}
	resp, err := h.UserClient.GetUserStats(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := mapBaseRespStatusCode(resp.BaseResp)
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Stats})
}
