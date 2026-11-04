package health

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// HTTPHandler 把 Registry 暴露成两个语义不同的探针：
//
//	/livez  进程活着就 200，永远不查依赖；
//	/readyz 依赖全部可用才 200，否则 503。
//
// 混用会带来真实的运维事故：用 /readyz 当 liveness 探针，数据库一抖动
// 编排系统就会把所有副本重启；用 /livez 当 readiness 探针，进程活着但依赖挂了
// 仍然会接流量，用户看到的全是超时。
type Handler struct {
	registry *Registry
	service  string
}

// NewHTTPHandler 构造探针 handler。service 只用于响应体，便于确认探的是哪个服务。
func NewHTTPHandler(r *Registry, service string) *Handler {
	return &Handler{registry: r, service: service}
}

// Live 只回答进程是否存活。不查任何依赖，也不受 Registry 状态影响。
func (h *Handler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "alive",
		"service": h.service,
	})
}

// Ready 返回 200 或 503，并在响应体里列出每个依赖的状态。
//
// 失败原因要显式返回：只给一个 503 时，排查只能靠登录机器看日志；
// 把 failed 列表放进响应体，kubectl describe / curl 就能直接定位是哪一项。
func (h *Handler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultTimeout)
	defer cancel()

	res := h.registry.Ready(ctx)
	code := http.StatusOK
	status := "ready"
	if !res.Ready {
		code = http.StatusServiceUnavailable
		status = "not_ready"
	}
	c.JSON(code, gin.H{
		"status":     status,
		"service":    h.service,
		"ready":      res.Ready,
		"checks":     res.Checks,
		"failed":     res.Failed(),
		"degraded":   res.Degraded,
		"checked_at": res.CheckedAt.Format(time.RFC3339Nano),
		"cached":     res.Cached,
	})
}
