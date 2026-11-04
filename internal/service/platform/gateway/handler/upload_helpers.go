package handler

import (
	"net/http"

	"Logos/internal/service/platform/gateway/model"
	"Logos/pkg/uploadguard"

	"github.com/gin-gonic/gin"
)

// readImageUpload 统一处理头像类图片上传：
// 限制整个 multipart 请求与单文件大小，读取有上限，内容嗅探必须是图片。
// 扩展名不采用用户文件名，而是由嗅探结果决定，避免把 HTML 改名成 .jpg 后按图片类型存储。
func readImageUpload(c *gin.Context, field string) ([]byte, string, string, bool) {
	if !uploadguard.LimitBody(c.Writer, c.Request, uploadguard.MaxImageBytes+uploadguard.RequestOverhead) {
		c.JSON(http.StatusRequestEntityTooLarge, model.BadRequest("图片过大（最大 5MB）"))
		return nil, "", "", false
	}

	header, err := c.FormFile(field)
	if err != nil {
		if uploadguard.IsTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, model.BadRequest("图片过大（最大 5MB）"))
			return nil, "", "", false
		}
		c.JSON(http.StatusBadRequest, model.BadRequest("请上传图片文件"))
		return nil, "", "", false
	}
	if header.Size > uploadguard.MaxImageBytes {
		c.JSON(http.StatusRequestEntityTooLarge, model.BadRequest("图片过大（最大 5MB）"))
		return nil, "", "", false
	}

	file, err := header.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError("打开图片失败"))
		return nil, "", "", false
	}
	defer file.Close()

	data, err := uploadguard.ReadAll(file, header.Size, uploadguard.MaxImageBytes)
	if err != nil {
		if uploadguard.IsTooLarge(err) {
			c.JSON(http.StatusRequestEntityTooLarge, model.BadRequest("图片过大（最大 5MB）"))
			return nil, "", "", false
		}
		c.JSON(http.StatusInternalServerError, model.InternalError("读取图片失败"))
		return nil, "", "", false
	}

	contentType, err := uploadguard.ImageContentType(data)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest("仅支持 JPG/PNG/GIF/WebP 图片"))
		return nil, "", "", false
	}

	return data, imageExt(contentType), contentType, true
}

func imageExt(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}
