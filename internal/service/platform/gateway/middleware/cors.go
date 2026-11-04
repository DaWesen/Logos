package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS 默认允许任意来源，但不允许携带凭据。
//
// 规范上 `Access-Control-Allow-Origin: *` 与 `Allow-Credentials: true` 不能同时使用：
// 浏览器会直接拒绝，很多实现却把这两个一起写，属于「看起来支持跨域登录，实际不生效」。
// 本项目鉴权走 Authorization 头，不需要 cookie；如果部署确实需要凭据，
// 用 CORS_ALLOWED_ORIGINS 显式列出来源，中间件会反射 Origin 并开启 credentials。
func CORS() gin.HandlerFunc {
	allowed := parseAllowedOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if len(allowed) == 0 {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if _, ok := allowed[origin]; origin != "" && ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Vary", "Origin")
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Request-ID, Sec-WebSocket-Protocol")
		c.Header("Access-Control-Expose-Headers", "Content-Length, X-Request-ID")

		c.Next()
	}
}

// Preflight 在限流之后短路 OPTIONS。
//
// 拆成两个中间件的原因：CORS 头必须在最前面设置，这样即使请求被限流返回 429，
// 浏览器也能读到 Access-Control-Allow-Origin 并看到真实状态码；
// 但预检本身不能提前短路，否则就绕过了限流。
func Preflight() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func parseAllowedOrigins(raw string) map[string]struct{} {
	origins := make(map[string]struct{})
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(item)
		if origin == "" {
			continue
		}
		origins[origin] = struct{}{}
	}
	return origins
}
