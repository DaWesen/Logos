package uploadguard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

var (
	// ErrTooLarge 表示文件或请求体超过上限。
	ErrTooLarge = errors.New("upload exceeds size limit")
	// ErrTooManyFiles 表示批量上传的文件数超过上限。
	ErrTooManyFiles = errors.New("too many files")
	// ErrUnsupportedType 表示文件内容不在允许的类型内。
	ErrUnsupportedType = errors.New("unsupported file type")
)

const (
	// MaxImageBytes 头像等图片上传上限。
	MaxImageBytes int64 = 5 << 20
	// MaxDocumentBytes 文档/通用文件上传上限。
	MaxDocumentBytes int64 = 100 << 20
	// MaxChatMediaBytes 聊天媒体上限（与原产品限制保持一致）。
	MaxChatMediaBytes int64 = 500 << 20
	// MaxBatchFiles 单次批量上传的文件数上限。
	MaxBatchFiles = 10
	// RequestOverhead multipart 边界、字段名等额外开销的预留。
	RequestOverhead int64 = 1 << 20
)

// LimitBody 在解析 multipart 之前限制整个请求体。
//
// BodyLimit 中间件跳过 multipart 是为了不误伤上传，但这不等于上传可以无上限：
// 必须由上传接口自己按业务设定总请求上限。Content-Length 已知时直接拒绝，
// 未知时用 MaxBytesReader 在读取阶段兜底。
func LimitBody(w http.ResponseWriter, r *http.Request, maxBytes int64) bool {
	if maxBytes <= 0 {
		return true
	}
	if r.ContentLength > maxBytes {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	return true
}

// ReadAll 读取文件并保证不超过 max 字节。
//
// declared（multipart 的 header.Size）只用于提前失败，不能作为唯一依据：
// 真正的上限必须落在读取阶段，否则伪造/缺失 Content-Length 时仍会读满内存。
func ReadAll(r io.Reader, declared, max int64) ([]byte, error) {
	if max <= 0 {
		max = MaxDocumentBytes
	}
	if declared > max {
		return nil, fmt.Errorf("%w: declared %d > %d", ErrTooLarge, declared, max)
	}

	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: read more than %d bytes", ErrTooLarge, max)
	}
	return data, nil
}

// Copy 把文件流写入目标（临时文件/MinIO）并保证不超过 max 字节。
func Copy(dst io.Writer, src io.Reader, max int64) (int64, error) {
	if max <= 0 {
		max = MaxDocumentBytes
	}
	n, err := io.Copy(dst, io.LimitReader(src, max+1))
	if err != nil {
		return n, err
	}
	if n > max {
		return n, fmt.Errorf("%w: wrote more than %d bytes", ErrTooLarge, max)
	}
	return n, nil
}

// SanitizeExt 只保留字母数字扩展名，避免用户文件名把奇怪字符带进对象键。
func SanitizeExt(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) < 2 || len(ext) > 11 {
		return ""
	}
	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return ext
}

// ImageContentType 用内容嗅探判断图片类型。
// 只看扩展名或客户端 Content-Type 不够：把 HTML 改名成 .jpg 一样能骗过字符串检查。
func ImageContentType(data []byte) (string, error) {
	ct := http.DetectContentType(data)
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return ct, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedType, ct)
	}
}

// IsTooLarge 判断错误是否来自大小限制（包括 MaxBytesReader）。
func IsTooLarge(err error) bool {
	if err == nil {
		return false
	}
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr) || errors.Is(err, ErrTooLarge)
}
