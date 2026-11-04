package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"Logos/pkg/logger"

	"github.com/gin-gonic/gin"
)

// multipartUploadRoutes 是确实需要接收文件的入口。
// 只按 Content-Type 跳过限制是不安全的：攻击者可以给任意 JSON 接口
// 伪造一个 multipart 头，从而绕过 2MB 上限。这里用「路由白名单」兜底。
var multipartUploadRoutes = map[string]struct{}{
	"/api/v1/file/upload":          {},
	"/api/v1/file/upload/multiple": {},
	"/api/v1/users/avatar":         {},
	"/api/v1/chat/upload":          {},
	"/api/v1/chat/group/avatar":    {},
}

// DefaultJSONBodyLimit 是 JSON 请求体的默认上限（2MB）。
//
// 为什么需要：gin 的 ShouldBindJSON 会把 Body 全量读进内存，没有上限时一个
// 100MB 的 JSON 就能让单个请求占住大量内存；并发几十个就足以把网关打挂。
// 2MB 对普通业务接口足够（批量参数通常几十 KB），文件上传走 multipart，
// 不经过这条限制。
const DefaultJSONBodyLimit int64 = 2 << 20

// BodyLimit 限制非 multipart 请求体的大小。
//
// 两道防线：
//  1. Content-Length 已知时直接拒绝，不读 Body；
//  2. 分块传输（chunked，没有 Content-Length）用 http.MaxBytesReader 兜底，
//     读取超过上限时返回错误而不是无限分配内存。
//
// 必须放在限流之后：被限流挡掉的请求不应该再产生一条「超大请求」日志。
func BodyLimit(limit int64) gin.HandlerFunc {
	if limit <= 0 {
		limit = DefaultJSONBodyLimit
	}
	return func(c *gin.Context) {
		if isMultipartUpload(c) {
			c.Next()
			return
		}

		if c.Request.ContentLength > limit {
			rejectOversizedBody(c, limit)
			return
		}

		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

func isMultipart(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data")
}

func isMultipartUpload(c *gin.Context) bool {
	if !isMultipart(c.Request) {
		return false
	}
	path := c.FullPath()
	if _, ok := multipartUploadRoutes[path]; ok {
		return true
	}
	// process 走 catch-all 反代，文档上传也在其中；只放行这个前缀。
	return strings.HasPrefix(path, "/api/v1/process/")
}

func rejectOversizedBody(c *gin.Context, limit int64) {
	logger.Warn("请求体超过上限",
		logger.StringField("path", c.FullPath()),
		logger.StringField("client_ip", c.ClientIP()),
		logger.StringField("content_length", strconv.FormatInt(c.Request.ContentLength, 10)),
		logger.StringField("limit", strconv.FormatInt(limit, 10)))
	c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
		"code":    http.StatusRequestEntityTooLarge,
		"message": "请求体过大",
	})
}
