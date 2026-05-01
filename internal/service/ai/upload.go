package ai

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"Logos/pkg/logger"
	"Logos/pkg/storage"

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
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		logger.Error("Failed to get file", logger.ErrorField(err))
		c.JSON(400, gin.H{
			"code":    400,
			"message": "Failed to get file",
		})
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		logger.Error("Failed to read file", logger.ErrorField(err))
		c.JSON(500, gin.H{
			"code":    500,
			"message": "Failed to read file",
		})
		return
	}

	fileType := detectFileType(header.Filename, header.Header.Get("Content-Type"))
	fileExt := filepath.Ext(header.Filename)
	newFileName := fmt.Sprintf("%s%s", uuid.New().String(), fileExt)
	now := time.Now()
	storagePath := fmt.Sprintf("%d/%d/%s", now.Year(), now.Month(), newFileName)

	reader := bytes.NewReader(fileData)
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = getMimeType(fileType)
	}

	if err := h.minioManager.UploadFile(h.bucket, storagePath, reader, int64(len(fileData)), mimeType); err != nil {
		logger.Error("Failed to upload file", logger.ErrorField(err))
		c.JSON(500, gin.H{
			"code":    500,
			"message": "Failed to upload file",
		})
		return
	}

	url, err := h.minioManager.GetFileURL(h.bucket, storagePath, oneDayInSeconds*oneYearInDays)
	if err != nil {
		url = ""
		logger.Warn("Failed to get file URL", logger.ErrorField(err))
	}

	logger.Info("File uploaded",
		logger.StringField("file_name", newFileName),
		logger.StringField("file_type", fileType),
		logger.IntField("size", len(fileData)))

	c.JSON(200, gin.H{
		"code":    200,
		"message": "Success",
		"data": gin.H{
			"file_id":      newFileName,
			"file_name":    header.Filename,
			"file_type":    fileType,
			"size":         len(fileData),
			"storage_path": storagePath,
			"url":          url,
		},
	})
}

// UploadMultipleFiles 批量上传文件
func (h *FileUploadHandler) UploadMultipleFiles(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
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

	var results []gin.H
	now := time.Now()

	for _, fileHeader := range files {
		file, err := fileHeader.Open()
		if err != nil {
			logger.Error("Failed to open file", logger.StringField("filename", fileHeader.Filename), logger.ErrorField(err))
			continue
		}

		fileData, err := io.ReadAll(file)
		file.Close()

		if err != nil {
			continue
		}

		fileType := detectFileType(fileHeader.Filename, fileHeader.Header.Get("Content-Type"))
		fileExt := filepath.Ext(fileHeader.Filename)
		newFileName := fmt.Sprintf("%s%s", uuid.New().String(), fileExt)
		storagePath := fmt.Sprintf("%d/%d/%s", now.Year(), now.Month(), newFileName)

		reader := bytes.NewReader(fileData)
		mimeType := fileHeader.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = getMimeType(fileType)
		}

		if err := h.minioManager.UploadFile(h.bucket, storagePath, reader, int64(len(fileData)), mimeType); err != nil {
			continue
		}

		url, _ := h.minioManager.GetFileURL(h.bucket, storagePath, oneDayInSeconds*oneYearInDays)

		results = append(results, gin.H{
			"file_name":    fileHeader.Filename,
			"file_id":      newFileName,
			"file_type":    fileType,
			"size":         len(fileData),
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

	if err := h.minioManager.DeleteFile(h.bucket, storagePath); err != nil {
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

	url, err := h.minioManager.GetFileURL(h.bucket, storagePath, oneDayInSeconds*thirtyDaysInDays)
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
