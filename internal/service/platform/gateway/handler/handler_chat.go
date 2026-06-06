package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"Logos/config"
	"Logos/internal/service/messaging/types"
	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/logger"

	pb "Logos/proto_gen/chat"
	pbModeration "Logos/proto_gen/moderation"
	pbUser "Logos/proto_gen/user"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func checkChatCode(c *gin.Context, code int32, message string) bool {
	if code != 0 && code != 200 {
		c.JSON(mapStatusCode(code), model.Response{Code: int(code), Message: message})
		return false
	}
	return true
}

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
	resp, err := h.ChatClient.SendMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) UploadChatMedia(c *gin.Context) {
	if h.MinioManager == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "storage unavailable"))
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		logger.Error("UploadChatMedia: FormFile解析失败", logger.ErrorField(err), logger.StringField("content_type", c.GetHeader("Content-Type")))
		c.JSON(http.StatusBadRequest, model.BadRequest("file required: "+err.Error()))
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError("read file failed"))
		return
	}

	if len(fileData) > 500*1024*1024 {
		c.JSON(http.StatusBadRequest, model.BadRequest("file too large (max 500MB)"))
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	msgType := detectMediaType(ext)
	objectKey := fmt.Sprintf("chat-media/%s/%s%s", time.Now().Format("2006/01/02"), uuid.New().String(), ext)

	cfg := config.GetConfig()
	bucket := cfg.Minio.Bucket
	if bucket == "" {
		bucket = "logos"
	}

	err = h.MinioManager.UploadFile(bucket, objectKey, bytes.NewReader(fileData), int64(len(fileData)), header.Header.Get("Content-Type"))
	if err != nil {
		logger.Error("上传聊天媒体失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError("upload failed"))
		return
	}

	proxyURL := "/api/v1/file/minio/" + objectKey

	mediaMeta := buildMediaMeta(msgType, ext, int64(len(fileData)), header.Filename)
	metaBytes, _ := json.Marshal(mediaMeta)

	c.JSON(http.StatusOK, model.Response{
		Code:    0,
		Message: "success",
		Data: gin.H{
			"url":          proxyURL,
			"message_type": msgType,
			"media_meta":   json.RawMessage(metaBytes),
			"object_key":   objectKey,
		},
	})
}

func (h *Handler) SendMediaMessage(c *gin.Context) {
	var req struct {
		ChatID      string          `json:"chat_id" binding:"required"`
		ChatType    int             `json:"chat_type"`
		MessageType int             `json:"message_type" binding:"required"`
		MediaURL    string          `json:"media_url" binding:"required"`
		MediaMeta   json.RawMessage `json:"media_meta"`
		Content     string          `json:"content"`
		ReplyTo     string          `json:"reply_to,omitempty"`
		MentionIDs  []string        `json:"mention_user_ids,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	if req.ChatType == 0 {
		req.ChatType = 1
	}

	userID, _ := c.Get("user_id")
	senderID, _ := userID.(string)

	logger.Info("SendMediaMessage", logger.StringField("chat_id", req.ChatID), logger.StringField("media_url", req.MediaURL), logger.IntField("message_type", req.MessageType))

	messageID := uuid.New().String()
	event := types.NewMediaMessageEvent(
		messageID,
		req.ChatID,
		types.ChatType(req.ChatType),
		senderID,
		types.MessageType(req.MessageType),
		req.Content,
		req.MediaURL,
		req.MediaMeta,
		nil,
		req.ReplyTo,
		req.MentionIDs,
		[]string{},
	)

	eventBus := types.GetEventBus()
	if err := eventBus.PublishMessageEvent(c.Request.Context(), event); err != nil {
		logger.Error("发送媒体消息事件失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError("send failed"))
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Code:    0,
		Message: "success",
		Data: gin.H{
			"message_id": messageID,
		},
	})
}

func detectMediaType(ext string) int {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".tiff", ".svg":
		return int(types.MessageTypeImage)
	case ".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma":
		return int(types.MessageTypeVoice)
	case ".mp4", ".webm", ".avi", ".mov", ".mkv", ".flv", ".wmv":
		return int(types.MessageTypeVideo)
	default:
		return int(types.MessageTypeFile)
	}
}

func buildMediaMeta(msgType int, ext string, size int64, filename string) map[string]interface{} {
	base := map[string]interface{}{
		"size":      size,
		"mime_type": mimeByExt(ext),
	}
	switch msgType {
	case int(types.MessageTypeImage):
		base["thumbnail"] = ""
	case int(types.MessageTypeVoice):
		base["duration"] = 0
	case int(types.MessageTypeVideo):
		base["duration"] = 0
		base["thumbnail"] = ""
	default:
		base["filename"] = filename
	}
	return base
}

func mimeByExt(ext string) string {
	mimes := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
		".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml",
		".mp3": "audio/mpeg", ".wav": "audio/wav", ".flac": "audio/flac",
		".aac": "audio/aac", ".ogg": "audio/ogg", ".m4a": "audio/mp4",
		".mp4": "video/mp4", ".webm": "video/webm", ".avi": "video/x-msvideo",
		".mov": "video/quicktime", ".mkv": "video/x-matroska",
		".pdf": "application/pdf", ".doc": "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".ppt": "application/vnd.ms-powerpoint", ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".txt": "text/plain", ".md": "text/markdown", ".zip": "application/zip",
	}
	if m, ok := mimes[ext]; ok {
		return m
	}
	return "application/octet-stream"
}

func (h *Handler) TranslateChatMessage(c *gin.Context) {
	if h.ModerationClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "翻译服务不可用"))
		return
	}

	var req struct {
		Content    string `json:"content"`
		SourceLang string `json:"source_lang"`
		TargetLang string `json:"target_lang" binding:"required"`
		MessageID  string `json:"message_id,omitempty"`
		ModelConfig struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			ApiKey   string `json:"api_key"`
			BaseUrl  string `json:"base_url"`
		} `json:"model_config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}

	if req.SourceLang == "" {
		req.SourceLang = "auto"
	}

	translateReq := &pbModeration.TranslateRequest{
		Content:    req.Content,
		SourceLang: req.SourceLang,
		TargetLang: req.TargetLang,
		ContentId:  req.MessageID,
	}
	if req.ModelConfig.ApiKey != "" {
		translateReq.ModelConfig = &pbModeration.ModelConfig{
			Provider: req.ModelConfig.Provider,
			Model:    req.ModelConfig.Model,
			ApiKey:   req.ModelConfig.ApiKey,
			BaseUrl:  req.ModelConfig.BaseUrl,
		}
	}

	translated, err := h.ModerationClient.Translate(c.Request.Context(), translateReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError("翻译失败"))
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Code:    0,
		Message: "success",
		Data: gin.H{
			"translated_content": translated.GetTranslatedContent(),
			"source_lang":        translated.GetSourceLang(),
			"target_lang":        translated.GetTargetLang(),
		},
	})
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
	resp, err := h.ChatClient.SearchMessages(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetChatHistory(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}

	chatID := c.Query("chat_id")
	if chatID == "" {
		c.JSON(http.StatusBadRequest, model.BadRequest("chat_id is required"))
		return
	}

	chatType := int32(0)
	if ct := c.Query("chat_type"); ct != "" {
		if v, err := strconv.Atoi(ct); err == nil {
			chatType = int32(v)
		}
	}
	limit := int32(50)
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil {
			limit = int32(v)
		}
	}

	resp, err := h.ChatClient.GetMessageHistory(c.Request.Context(), &pb.GetMessageHistoryRequest{
		ChatId:   chatID,
		ChatType: pb.ChatType(chatType),
		Limit:    limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	if req.ChatType == pb.ChatType_CHAT_TYPE_UNSPECIFIED {
		req.ChatType = pb.ChatType_CHAT_TYPE_PRIVATE
	}
	resp, err := h.ChatClient.MarkMessagesRead(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.WithdrawMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.EditMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.CreateGroup(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	data := buildGroupResponse(resp.GetData())
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: data})
}

func (h *Handler) GetConversationList(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	resp, err := h.ChatClient.GetConversationList(c.Request.Context(), &pb.GetConversationListRequest{
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}

	// 添加详细日志
	logger.Info("[Gateway] GetConversationList response",
		logger.IntField("conversations_count", len(resp.Conversations)))
	for i, conv := range resp.Conversations {
		logger.Info("[Gateway] Conversation item",
			logger.IntField("index", i),
			logger.StringField("chat_id", conv.ChatId),
			logger.StringField("name", conv.Name),
			logger.BoolField("is_friend", conv.IsFriend),
			logger.BoolField("is_blocked", conv.IsBlocked))
	}

	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetUnreadCount(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	chatIDs := c.QueryArray("chat_id")

	resp, err := h.ChatClient.GetUnreadCount(c.Request.Context(), &pb.GetUnreadCountRequest{
		ChatIds: chatIDs,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) ForwardMessage(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.ForwardMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.ForwardMessage(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.InviteGroupMember(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.KickGroupMember(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.MuteGroupMember(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.TransferGroupOwner(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.UpdateGroupAnnouncement(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) UpdateGroupAvatar(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "聊天服务不可用"))
		return
	}
	if h.MinioManager == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "存储服务不可用"))
		return
	}

	groupID := c.PostForm("group_id")
	if groupID == "" {
		groupID = c.Query("group_id")
	}
	if groupID == "" {
		c.JSON(http.StatusBadRequest, model.BadRequest("请提供群组ID"))
		return
	}

	header, err := c.FormFile("avatar")
	if err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest("请上传头像文件"))
		return
	}

	file, err := header.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError("打开文件失败"))
		return
	}
	defer file.Close()

	fileData := make([]byte, header.Size)
	if _, err := file.Read(fileData); err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError("读取文件失败"))
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}
	objectKey := fmt.Sprintf("group-avatars/%s/%s%s", time.Now().Format("2006/01/02"), uuid.New().String(), ext)

	cfg := config.GetConfig()
	bucket := cfg.Minio.Bucket
	if bucket == "" {
		bucket = "logos"
	}

	if err := h.MinioManager.UploadFile(bucket, objectKey, bytes.NewReader(fileData), header.Size, header.Header.Get("Content-Type")); err != nil {
		logger.Error("上传群头像失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError("上传失败"))
		return
	}

	proxyURL := "/api/v1/file/minio/" + objectKey

	req := &pb.UpdateGroupAvatarRequest{
		GroupId: groupID,
		Avatar:  proxyURL,
	}

	resp, err := h.ChatClient.UpdateGroupAvatar(c.Request.Context(), req)
	if err != nil {
		logger.Error("更新群头像失败", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Code:    http.StatusOK,
		Message: "success",
		Data: gin.H{
			"url": proxyURL,
		},
	})
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
	resp, err := h.ChatClient.SetGroupAdmin(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetGroupMembers(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	groupID := c.Query("group_id")
	if groupID == "" {
		c.JSON(http.StatusBadRequest, model.BadRequest("group_id is required"))
		return
	}
	page := int32(1)
	if p := c.Query("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			page = int32(v)
		}
	}
	pageSize := int32(20)
	if ps := c.Query("page_size"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil {
			pageSize = int32(v)
		}
	}
	req := &pb.GetGroupMembersRequest{
		GroupId:  groupID,
		Page:     page,
		PageSize: pageSize,
	}
	resp, err := h.ChatClient.GetGroupMembers(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}

	// 使用字符串作为 key 来存储用户信息
	userMap := map[string]*userInfo{}
	if h.UserClient != nil {
		var userIDs []int64
		// 存储 string -> int64 的映射，以便后面查找
		stringToInt64 := map[string]int64{}
		for _, m := range resp.GetMembers() {
			if uid, err := strconv.ParseInt(m.GetUserId(), 10, 64); err == nil {
				userIDs = append(userIDs, uid)
				stringToInt64[m.GetUserId()] = uid
			}
		}
		if len(userIDs) > 0 {
			userResp, err := h.UserClient.BatchGetUserInfo(c.Request.Context(), &pbUser.BatchUserInfoReq{UserIds: userIDs})
			if err == nil && userResp != nil {
				for k, v := range userResp.GetUsers() {
					info := &userInfo{Username: v.GetUsername()}
					if v.Avatar != nil {
						info.Avatar = *v.Avatar
					}
					// 将 int64 的 key 转换为 string 来匹配
					userMap[strconv.FormatInt(k, 10)] = info
				}
			}
		}
	}

	members := buildMembersResponse(resp.GetMembers(), userMap)
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: gin.H{
		"members": members,
		"total":   resp.GetTotal(),
	}})
}

type userInfo struct {
	Username string
	Avatar   string
}

func buildMembersResponse(protoMembers []*pb.GroupMember, userMap map[string]*userInfo) []gin.H {
	result := make([]gin.H, 0, len(protoMembers))
	for _, m := range protoMembers {
		if m == nil {
			continue
		}
		joinedAt := ""
		if m.GetJoinedAt() != nil {
			joinedAt = m.GetJoinedAt().AsTime().Format(time.RFC3339)
		}
		username := ""
		avatar := ""
		isBot := strings.HasPrefix(m.GetUserId(), "bot_")
		if info, ok := userMap[m.GetUserId()]; ok {
			username = info.Username
			avatar = info.Avatar
		}
		if isBot && username == "" {
			username = "Bot"
		}
		result = append(result, gin.H{
			"user_id":   m.GetUserId(),
			"username":  username,
			"avatar":    avatar,
			"role":      int32(m.GetRole()),
			"mute_type": int32(m.GetMuteType()),
			"joined_at": joinedAt,
			"is_bot":    isBot,
		})
	}
	return result
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
	resp, err := h.ChatClient.JoinGroup(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
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
	resp, err := h.ChatClient.LeaveGroup(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) GetChatGroup(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	groupID := c.Query("group_id")
	if groupID == "" {
		c.JSON(http.StatusBadRequest, model.BadRequest("group_id is required"))
		return
	}
	resp, err := h.ChatClient.GetGroup(c.Request.Context(), &pb.GetGroupRequest{GroupId: groupID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	data := buildGroupResponse(resp.GetData())
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: data})
}

func buildGroupResponse(g *pb.Group) gin.H {
	if g == nil {
		return nil
	}
	memberCount := len(g.GetMemberIds())
	createdAt := ""
	if g.GetCreatedAt() != nil {
		createdAt = g.GetCreatedAt().AsTime().Format(time.RFC3339)
	}
	updatedAt := ""
	if g.GetUpdatedAt() != nil {
		updatedAt = g.GetUpdatedAt().AsTime().Format(time.RFC3339)
	}
	return gin.H{
		"id":           g.GetId(),
		"name":         g.GetName(),
		"avatar":       g.GetAvatar(),
		"owner_id":     g.GetOwnerId(),
		"member_ids":   g.GetMemberIds(),
		"member_count": memberCount,
		"announcement": g.GetAnnouncement(),
		"metadata":     g.GetMetadata(),
		"created_at":   createdAt,
		"updated_at":   updatedAt,
	}
}

func (h *Handler) DeleteChat(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req pb.DeleteChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.DeleteChat(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}

func (h *Handler) DeleteChatHistory(c *gin.Context) {
	if h.ChatClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "service unavailable"))
		return
	}
	var req struct {
		ChatId string `json:"chat_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.ChatClient.DeleteChatHistory(c.Request.Context(), &pb.DeleteChatHistoryRequest{
		ChatId: req.ChatId,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	if !checkChatCode(c, resp.Code, resp.Message) {
		return
	}
	c.JSON(http.StatusOK, model.Response{Code: http.StatusOK, Message: "success", Data: resp})
}
