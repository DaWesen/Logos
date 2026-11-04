package router

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// applyTrustedProxies 决定是否采信 X-Forwarded-For / X-Real-IP。
//
// gin 默认信任所有代理，`c.ClientIP()` 会直接返回客户端伪造的 X-Forwarded-For：
// 攻击者每个请求换一个假 IP，就能绕过按 IP 的限流，日志里的 client_ip 也不可信。
// 默认必须不信任；只有部署在已知入口（nginx/ingress）后面时，
// 才通过 TRUSTED_PROXIES 显式列出代理网段。
func applyTrustedProxies(r *gin.Engine) error {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if raw == "" {
		// nil = 不信任任何代理，ClientIP 只取 RemoteAddr。
		return r.SetTrustedProxies(nil)
	}

	proxies := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		proxy := strings.TrimSpace(item)
		if proxy == "" {
			continue
		}
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				return fmt.Errorf("invalid trusted proxy %q: %w", proxy, err)
			}
		}
		proxies = append(proxies, proxy)
	}
	if len(proxies) == 0 {
		return r.SetTrustedProxies(nil)
	}
	return r.SetTrustedProxies(proxies)
}
