package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"Logos/internal/service/ai/process/model"
	"Logos/internal/service/ai/process/service"
	"Logos/pkg/jwt"
	"Logos/pkg/logger"
	"Logos/pkg/pagination"
	"Logos/pkg/uploadguard"
)

type ProcessHandler struct {
	processService service.ProcessService
	jwtManager     *jwt.JWTManager
}

func NewProcessHandler(processService service.ProcessService) *ProcessHandler {
	return &ProcessHandler{
		processService: processService,
		jwtManager:     jwt.NewJWTManager(),
	}
}

func (h *ProcessHandler) RegisterRoutes(r *gin.Engine) {
	processGroup := r.Group("/api/ai/process")
	{
		processGroup.POST("/file", h.ProcessFile)
		processGroup.POST("/url", h.ProcessURL)
		processGroup.GET("/documents", h.ListDocuments)
		processGroup.GET("/documents/:id", h.GetDocument)
		processGroup.DELETE("/documents/:id", h.DeleteDocument)
		processGroup.POST("/documents/:id/reprocess", h.ReprocessDocument)
		processGroup.GET("/documents/:id/chunks", h.GetDocumentChunks)
	}
}

func (h *ProcessHandler) ProcessFile(c *gin.Context) {
	if !uploadguard.LimitBody(c.Writer, c.Request, uploadguard.MaxDocumentBytes+uploadguard.RequestOverhead) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件过大（最大 100MB）"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		if uploadguard.IsTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件过大（最大 100MB）"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传文件"})
		return
	}
	if file.Size > uploadguard.MaxDocumentBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件过大（最大 100MB）"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取文件失败"})
		return
	}
	defer src.Close()

	tmpFile, err := os.CreateTemp("", "logos-upload-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建临时文件失败"})
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := uploadguard.Copy(tmpFile, src, uploadguard.MaxDocumentBytes); err != nil {
		tmpFile.Close()
		if uploadguard.IsTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件过大（最大 100MB）"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败"})
		return
	}
	tmpFile.Close()

	fileURL := c.PostForm("url")
	if fileURL == "" {
		fileURL = "upload://" + file.Filename
	}

	collectionID := c.PostForm("collection_id")

	doc, err := h.processService.ProcessFileFromPath(c.Request.Context(), file.Filename, tmpPath, file.Size, fileURL, collectionID)
	if err != nil {
		logger.Error("处理文件失败", logger.StringField("filename", file.Filename), logger.ErrorField(err))
		writeProcessError(c, err, "处理文件失败")
		return
	}

	dto := h.documentDTOAfterMutation(c, doc, "处理文件")

	c.JSON(http.StatusOK, gin.H{"success": true, "data": dto})
}

type ProcessURLRequest struct {
	URL          string `json:"url" binding:"required"`
	CollectionID string `json:"collection_id"`
}

func (h *ProcessHandler) ProcessURL(c *gin.Context) {
	var req ProcessURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	doc, err := h.processService.ProcessURL(c.Request.Context(), req.URL, req.CollectionID)
	if err != nil {
		logger.Error("处理URL失败", logger.StringField("url", req.URL), logger.ErrorField(err))
		writeProcessError(c, err, "处理URL失败")
		return
	}

	dto := h.documentDTOAfterMutation(c, doc, "处理URL")

	c.JSON(http.StatusOK, gin.H{"success": true, "data": dto})
}

// documentDTOAfterMutation 用于「写操作已经提交，随后补充读模型」的场景。
// 此时为了一个可选的块数量返回 500 会让客户端重试并重复创建文档，因此降级返回
// 基础信息；但读取失败必须记 Error，不能把 0 当成真实块数悄悄返回。
func (h *ProcessHandler) documentDTOAfterMutation(c *gin.Context, doc *model.Document, operation string) *model.DocumentDTO {
	dto, err := h.processService.GetDocumentDTO(c.Request.Context(), doc.ID)
	if err == nil && dto != nil {
		return dto
	}
	if err != nil {
		logger.Error(operation+"：文档已写入，但读取详情失败，返回基础信息",
			logger.StringField("doc_id", doc.ID),
			logger.ErrorField(err))
	} else {
		logger.Warn(operation+"：文档已写入，但详情查询为空，返回基础信息",
			logger.StringField("doc_id", doc.ID))
	}
	return model.ToDocumentDTO(doc, 0)
}

func (h *ProcessHandler) ListDocuments(c *gin.Context) {
	page, pageSize, status, ok := parseDocumentListQuery(c)
	if !ok {
		return
	}

	dtos, total, err := h.processService.ListDocumentDTOs(c.Request.Context(), status, page, pageSize)
	if err != nil {
		writeProcessError(c, err, "查询文档列表失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      dtos,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

const defaultDocumentPageSize = 20

// parseDocumentListQuery 解析文档列表的分页与状态过滤。
//
// 旧写法是 `page, _ := strconv.Atoi(...)`：`?page=abc` 变成 0，DAO 归一化后
// 静默返回第一页；`?status=abc` 的错误被 `if err == nil` 吃掉，status 保持
// nil —— 等于**取消过滤**，调用方以为筛了「失败文档」，实际拿到全部状态。
// `?status=99` 则完全没有白名单，被原样下传给 SQL。
//
// 这里把「非法输入」和「合法但需要归一化」分开：
//   - 非数字 / 非正数 / 未知状态 → 400，不猜测调用方意图
//   - page_size 超过上限 → 收敛到上限，并回显**实际生效**的值
//
// 回显很重要：旧实现里 DAO 把 page_size=1e9 截断成 100，响应体却仍写着
// 1e9，前端按响应算出的页数和服务端真实分页对不上。
func parseDocumentListQuery(c *gin.Context) (page, pageSize int, status *int, ok bool) {
	page, ok = parsePositiveIntQuery(c, "page", pagination.DefaultPage)
	if !ok {
		return 0, 0, nil, false
	}
	pageSize, ok = parsePositiveIntQuery(c, "page_size", defaultDocumentPageSize)
	if !ok {
		return 0, 0, nil, false
	}
	page, pageSize = pagination.NormalizeWithDefault(page, pageSize, defaultDocumentPageSize)

	raw := c.Query("status")
	if raw == "" {
		return page, pageSize, nil, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || !validDocumentStatus(parsed) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "status 必须是 1..4 之一（1=待处理 2=处理中 3=已完成 4=失败）",
		})
		return 0, 0, nil, false
	}
	status = &parsed
	return page, pageSize, status, true
}

func parsePositiveIntQuery(c *gin.Context, key string, defaultValue int) (int, bool) {
	raw := c.Query(key)
	if raw == "" {
		return defaultValue, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": key + " 必须是 >= 1 的整数"})
		return 0, false
	}
	return parsed, true
}

func validDocumentStatus(status int) bool {
	switch status {
	case model.DocumentStatusPending,
		model.DocumentStatusProcessing,
		model.DocumentStatusCompleted,
		model.DocumentStatusFailed:
		return true
	default:
		return false
	}
}

func (h *ProcessHandler) GetDocument(c *gin.Context) {
	id := c.Param("id")

	dto, err := h.processService.GetDocumentDTO(c.Request.Context(), id)
	if err != nil {
		writeProcessError(c, err, "查询文档失败")
		return
	}

	if dto == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": dto})
}

func (h *ProcessHandler) DeleteDocument(c *gin.Context) {
	id := c.Param("id")

	if err := h.processService.DeleteDocument(c.Request.Context(), id); err != nil {
		writeProcessError(c, err, "删除文档失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *ProcessHandler) ReprocessDocument(c *gin.Context) {
	id := c.Param("id")

	if err := h.processService.ProcessDocument(c.Request.Context(), id); err != nil {
		logger.Error("重新处理文档失败", logger.StringField("doc_id", id), logger.ErrorField(err))
		writeProcessError(c, err, "重新处理文档失败")
		return
	}

	dto, err := h.processService.GetDocumentDTO(c.Request.Context(), id)
	if err != nil {
		writeProcessError(c, err, "重新处理完成，但查询文档失败")
		return
	}
	if dto == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": dto})
}

func (h *ProcessHandler) GetDocumentChunks(c *gin.Context) {
	id := c.Param("id")

	chunks, err := h.processService.GetChunks(c.Request.Context(), id)
	if err != nil {
		writeProcessError(c, err, "查询文档块失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": chunks})
}

func (h *ProcessHandler) RegisterMiddlewares(r *gin.Engine) {
	r.Use(func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Next()
	})
	r.Use(requireAuth(h.jwtManager))
}

func writeProcessError(c *gin.Context, err error, fallback string) {
	switch {
	case errors.Is(err, service.ErrUnauthenticated):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权"})
	case errors.Is(err, service.ErrDocumentNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": fallback})
	}
}

func jsonResponse(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}
