package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func avatarRequest(t *testing.T, filename string, data []byte) *gin.Context {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("avatar", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/avatar", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	return c
}

// TestReadImageUpload_AcceptsRealPNG 真实 PNG 内容应通过，扩展名由内容决定。
func TestReadImageUpload_AcceptsRealPNG(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	c := avatarRequest(t, "avatar.exe", png)

	data, ext, contentType, ok := readImageUpload(c, "avatar")
	if !ok {
		t.Fatalf("PNG 应通过，status=%d", c.Writer.Status())
	}
	if ext != ".png" || contentType != "image/png" {
		t.Fatalf("ext=%q contentType=%q", ext, contentType)
	}
	if !bytes.Equal(data, png) {
		t.Fatal("读取内容不一致")
	}
}

// TestReadImageUpload_RejectsHTMLDisguisedAsImage 只看扩展名会被绕过，
// 内容嗅探必须拒绝 HTML 伪装成 .jpg 的上传。
func TestReadImageUpload_RejectsHTMLDisguisedAsImage(t *testing.T) {
	c := avatarRequest(t, "avatar.jpg", []byte("<!doctype html><html><body>x</body></html>"))

	if _, _, _, ok := readImageUpload(c, "avatar"); ok {
		t.Fatal("HTML 伪装图片应被拒绝")
	}
	if c.Writer.Status() != http.StatusBadRequest {
		t.Fatalf("status = %d，期望 400", c.Writer.Status())
	}
}
