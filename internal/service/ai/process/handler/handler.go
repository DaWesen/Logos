package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"Logos/internal/service/ai/process/model"
	"Logos/internal/service/ai/process/service"
	"Logos/pkg/logger"
)

type ProcessHandler struct {
	processService service.ProcessService
}

func NewProcessHandler(processService service.ProcessService) *ProcessHandler {
	return &ProcessHandler{
		processService: processService,
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
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请上传文件"})
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

	if _, err := io.Copy(tmpFile, src); err != nil {
		tmpFile.Close()
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "处理文件失败"})
		return
	}

	dto, _ := h.processService.GetDocumentDTO(c.Request.Context(), doc.ID)
	if dto == nil {
		dto = model.ToDocumentDTO(doc, 0)
	}

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "处理URL失败"})
		return
	}

	dto, _ := h.processService.GetDocumentDTO(c.Request.Context(), doc.ID)
	if dto == nil {
		dto = model.ToDocumentDTO(doc, 0)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": dto})
}

func (h *ProcessHandler) ListDocuments(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	statusStr := c.Query("status")

	var status *int
	if statusStr != "" {
		if s, err := strconv.Atoi(statusStr); err == nil {
			status = &s
		}
	}

	dtos, total, err := h.processService.ListDocumentDTOs(c.Request.Context(), status, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文档列表失败"})
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

func (h *ProcessHandler) GetDocument(c *gin.Context) {
	id := c.Param("id")

	dto, err := h.processService.GetDocumentDTO(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文档失败"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "删除文档失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *ProcessHandler) ReprocessDocument(c *gin.Context) {
	id := c.Param("id")

	if err := h.processService.ProcessDocument(c.Request.Context(), id); err != nil {
		logger.Error("重新处理文档失败", logger.StringField("doc_id", id), logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "重新处理文档失败"})
		return
	}

	dto, _ := h.processService.GetDocumentDTO(c.Request.Context(), id)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": dto})
}

func (h *ProcessHandler) GetDocumentChunks(c *gin.Context) {
	id := c.Param("id")

	chunks, err := h.processService.GetChunks(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询文档块失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": chunks})
}

func (h *ProcessHandler) RegisterMiddlewares(r *gin.Engine) {
	r.Use(func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Next()
	})
}

func jsonResponse(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}
