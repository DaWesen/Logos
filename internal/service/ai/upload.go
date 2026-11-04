package ai

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/storage"
	"Logos/pkg/uploadguard"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	oneDayInSeconds  = 86400
	thirtyDaysInDays = 30
	oneYearInDays    = 365
)

// FileUploadHandler 文件上传处理器
type FileUploadHandler struct {
	minioManager *storage.MinioManager
	bucket       string
}

// NewFileUploadHandler 创建文件上传处理器
func NewFileUploadHandler(minioManager *storage.MinioManager, bucket string) *FileUploadHandler {
	return &FileUploadHandler{
		minioManager: minioManager,
		bucket:       bucket,
	}
}

// UploadFile 上传文件接口
func (h *FileUploadHandler) UploadFile(c *gin.Context) {
	if !uploadguard.LimitBody(c.Writer, c.Request, uploadguard.MaxDocumentBytes+uploadguard.RequestOverhead) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 413, "message": "请求体过大"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		if uploadguard.IsTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 413, "message": "文件过大"})
			return
		}
		logger.Error("Failed to get file", logger.ErrorField(err))
		c.JSON(400, gin.H{
			"code":    400,
			"message": "Failed to get file",
		})
		return
	}
	defer file.Close()

	if header.Size > uploadguard.MaxDocumentBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 413, "message": "文件过大（最大 100MB）"})
		return
	}

	fileType := detectFileType(header.Filename, header.Header.Get("Content-Type"))
	fileExt := uploadguard.SanitizeExt(header.Filename)
	newFileName := fmt.Sprintf("%s%s", uuid.New().String(), fileExt)
	now := time.Now()
	storagePath := fmt.Sprintf("%d/%d/%s", now.Year(), now.Month(), newFileName)

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = getMimeType(fileType)
	}

	// 直接流式上传：文件大小已由 multipart 解析确认，不需要先整份读进内存再写一遍。
	// ctx 必须透传：调用方断开或网关超时后，MinIO 上传不应继续占着连接与临时文件。
	if err := h.minioManager.UploadFileContext(c.Request.Context(), h.bucket, storagePath, file, header.Size, mimeType); err != nil {
		logger.Error("Failed to upload file", logger.ErrorField(err))
		c.JSON(500, gin.H{
			"code":    500,
			"message": "Failed to upload file",
		})
		return
	}

	url, err := h.minioManager.GetFileURLContext(c.Request.Context(), h.bucket, storagePath, oneDayInSeconds*oneYearInDays)
	if err != nil {
		url = ""
		logger.Warn("Failed to get file URL", logger.ErrorField(err))
	}

	logger.Info("File uploaded",
		logger.StringField("file_name", newFileName),
		logger.StringField("file_type", fileType),
		logger.Int64Field("size", header.Size))

	c.JSON(200, gin.H{
		"code":    200,
		"message": "Success",
		"data": gin.H{
			"file_id":      newFileName,
			"file_name":    header.Filename,
			"file_type":    fileType,
			"size":         header.Size,
			"storage_path": storagePath,
			"url":          url,
		},
	})
}

// UploadMultipleFiles 批量上传文件
func (h *FileUploadHandler) UploadMultipleFiles(c *gin.Context) {
	// 批量上传按「总请求」限一次，避免 10 个 100MB 文件把内存/磁盘打满。
	if !uploadguard.LimitBody(c.Writer, c.Request, 2*uploadguard.MaxDocumentBytes+uploadguard.RequestOverhead) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 413, "message": "请求体过大"})
		return
	}

	form, err := c.MultipartForm()
	if err != nil {
		if uploadguard.IsTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": 413, "message": "请求体过大"})
			return
		}
		c.JSON(400, gin.H{
			"code":    400,
			"message": "Failed to parse form",
		})
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(400, gin.H{
			"code":    400,
			"message": "No files uploaded",
		})
		return
	}
	if len(files) > uploadguard.MaxBatchFiles {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": fmt.Sprintf("一次最多上传 %d 个文件", uploadguard.MaxBatchFiles)})
		return
	}

	var results []gin.H
	now := time.Now()

	for _, fileHeader := range files {
		if fileHeader.Size > uploadguard.MaxDocumentBytes {
			logger.Warn("批量上传跳过超大文件",
				logger.StringField("filename", fileHeader.Filename),
				logger.Int64Field("size", fileHeader.Size))
			continue
		}

		file, err := fileHeader.Open()
		if err != nil {
			logger.Error("Failed to open file", logger.StringField("filename", fileHeader.Filename), logger.ErrorField(err))
			continue
		}

		fileType := detectFileType(fileHeader.Filename, fileHeader.Header.Get("Content-Type"))
		fileExt := uploadguard.SanitizeExt(fileHeader.Filename)
		newFileName := fmt.Sprintf("%s%s", uuid.New().String(), fileExt)
		storagePath := fmt.Sprintf("%d/%d/%s", now.Year(), now.Month(), newFileName)

		mimeType := fileHeader.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = getMimeType(fileType)
		}

		if err := h.minioManager.UploadFileContext(c.Request.Context(), h.bucket, storagePath, file, fileHeader.Size, mimeType); err != nil {
			file.Close()
			continue
		}
		file.Close()

		url, _ := h.minioManager.GetFileURLContext(c.Request.Context(), h.bucket, storagePath, oneDayInSeconds*oneYearInDays)

		results = append(results, gin.H{
			"file_name":    fileHeader.Filename,
			"file_id":      newFileName,
			"file_type":    fileType,
			"size":         fileHeader.Size,
			"storage_path": storagePath,
			"url":          url,
		})
	}

	c.JSON(200, gin.H{
		"code":    200,
		"message": "Success",
		"data": gin.H{
			"total": len(files),
			"files": results,
		},
	})
}

// DeleteFile 删除文件
func (h *FileUploadHandler) DeleteFile(c *gin.Context) {
	storagePath := c.Query("storage_path")
	if storagePath == "" {
		c.JSON(400, gin.H{
			"code":    400,
			"message": "Storage path is required",
		})
		return
	}
	if err := ValidateObjectKey(storagePath); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	if err := h.minioManager.DeleteFileContext(c.Request.Context(), h.bucket, storagePath); err != nil {
		logger.Error("Failed to delete file", logger.ErrorField(err))
		c.JSON(500, gin.H{
			"code":    500,
			"message": "Failed to delete file",
		})
		return
	}

	c.JSON(200, gin.H{
		"code":    200,
		"message": "Deleted",
	})
}

// GetFileURL 获取文件 URL
func (h *FileUploadHandler) GetFileURL(c *gin.Context) {
	storagePath := c.Query("storage_path")
	if storagePath == "" {
		c.JSON(400, gin.H{
			"code":    400,
			"message": "Storage path is required",
		})
		return
	}
	if err := ValidateObjectKey(storagePath); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	url, err := h.minioManager.GetFileURLContext(c.Request.Context(), h.bucket, storagePath, oneDayInSeconds*thirtyDaysInDays)
	if err != nil {
		logger.Error("Failed to get file URL", logger.ErrorField(err))
		c.JSON(500, gin.H{
			"code":    500,
			"message": "Failed to get file URL",
		})
		return
	}

	c.JSON(200, gin.H{
		"code": 200,
		"url":  url,
	})
}

func detectFileType(filename string, mimeType string) string {
	ext := strings.ToLower(filepath.Ext(filename))

	switch {
	case ext == ".pdf" || strings.Contains(mimeType, "pdf"):
		return "document-pdf"
	case ext == ".doc" || ext == ".docx":
		return "document-word"
	case ext == ".ppt" || ext == ".pptx":
		return "document-ppt"
	case ext == ".xls" || ext == ".xlsx":
		return "document-excel"
	case ext == ".txt" || ext == ".md" || ext == ".csv":
		return "document-text"
	case ext == ".jpg" || ext == ".jpeg" || strings.Contains(mimeType, "jpeg"):
		return "image-jpeg"
	case ext == ".png" || strings.Contains(mimeType, "png"):
		return "image-png"
	case ext == ".gif" || strings.Contains(mimeType, "gif"):
		return "image-gif"
	case ext == ".webp":
		return "image-webp"
	case ext == ".mp3" || strings.Contains(mimeType, "mpeg"):
		return "audio-mp3"
	case ext == ".wav" || strings.Contains(mimeType, "wav"):
		return "audio-wav"
	case ext == ".flac":
		return "audio-flac"
	case ext == ".mp4" || strings.Contains(mimeType, "mp4"):
		return "video-mp4"
	case ext == ".webm":
		return "video-webm"
	case ext == ".avi":
		return "video-avi"
	default:
		return "file-other"
	}
}

func getMimeType(fileType string) string {
	switch fileType {
	case "document-pdf":
		return "application/pdf"
	case "image-jpeg":
		return "image/jpeg"
	case "image-png":
		return "image/png"
	case "audio-mp3":
		return "audio/mpeg"
	case "video-mp4":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

func (h *FileUploadHandler) ProxyMinioFile(c *gin.Context) {
	objectKey := c.Param("path")
	if objectKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "path is required"})
		return
	}
	objectKey = strings.TrimPrefix(objectKey, "/")
	if err := ValidateObjectKey(objectKey); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": err.Error()})
		return
	}

	fileURL, err := h.minioManager.GetFileURLContext(c.Request.Context(), h.bucket, objectKey, oneDayInSeconds)
	if err != nil {
		logger.Error("Failed to get file URL for proxy", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to get file"})
		return
	}

	// 构建请求，转发 Range 头以支持音视频拖动进度条
	// 上游是 MinIO 预签名地址，但网络仍可能卡住；不能用一个没有超时的
	// http.DefaultClient 把网关的 goroutine 挂死。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		logger.Error("Failed to create request", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to create request"})
		return
	}
	if rangeHeader := c.GetHeader("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error("Failed to fetch file from minio", logger.ErrorField(err))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to fetch file"})
		return
	}
	defer resp.Body.Close()

	contentType, download := SafeProxyContentType(resp.Header.Get("Content-Type"))

	// 转发 Range 相关响应头
	headers := map[string]string{
		"Accept-Ranges": "bytes",
		// 浏览器不能因为 Content-Type 猜错就把响应当成 HTML/脚本执行。
		"X-Content-Type-Options": "nosniff",
	}
	// chunked 响应没有确定长度，写 -1 会生成非法头；交给 DataFromReader 处理。
	if resp.ContentLength >= 0 {
		headers["Content-Length"] = strconv.FormatInt(resp.ContentLength, 10)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		headers["Content-Range"] = cr
	}
	if download {
		// HTML/SVG 等可以在同源下执行的内容一律强制下载，避免存储型 XSS。
		headers["Content-Disposition"] = "attachment"
	}

	c.DataFromReader(resp.StatusCode, resp.ContentLength, contentType, resp.Body, headers)
}

// ValidateObjectKey 拒绝会让对象键语义变模糊的输入。
//
// MinIO 把对象键当字面量，但对象键最终会进入 URL 路径、日志和预签名链接；
// `..`、空段、反斜杠和控制字符在不同层（浏览器、代理、SDK）可能被归一化，
// 造成「签名的对象」和「实际访问的对象」不一致。上传端生成的都是 UUID 路径，
// 这里拒绝这些形态不会误伤正常文件。
func ValidateObjectKey(key string) error {
	if key == "" {
		return fmt.Errorf("对象键不能为空")
	}
	if len(key) > 1024 {
		return fmt.Errorf("对象键过长")
	}
	if strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("对象键格式非法")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("对象键包含控制字符")
		}
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("对象键包含非法路径段")
		}
	}
	return nil
}

// SafeProxyContentType 决定代理文件对外声明的类型。
//
// 上传时的 Content-Type 来自客户端，完全不可信：把 .html 声明成 image/png
// 仍会被浏览器按内容嗅探执行。这里只允许明确的媒体/文档类型内联展示，
// HTML、SVG、XML、脚本等一律降级为 application/octet-stream 并强制下载。
func SafeProxyContentType(raw string) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct == "" {
		return "application/octet-stream", true
	}

	if strings.HasPrefix(ct, "text/") || strings.HasPrefix(ct, "image/svg") ||
		strings.Contains(ct, "xml") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "xhtml") {
		return "application/octet-stream", true
	}

	switch ct {
	case "application/pdf",
		"application/zip",
		"application/octet-stream",
		"application/msword",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.ms-excel",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ct, false
	}

	if strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "audio/") ||
		strings.HasPrefix(ct, "video/") {
		return ct, false
	}
	return "application/octet-stream", true
}
