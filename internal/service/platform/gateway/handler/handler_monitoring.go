package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"Logos/internal/service/platform/gateway/model"

	pbCommon "Logos/proto_gen/common"
	pb "Logos/proto_gen/monitoring"

	"github.com/gin-gonic/gin"
)

func (h *Handler) RecordMetric(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "internal server error"))
		return
	}
	var req pb.RecordMetricReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MonitoringClient.RecordMetric(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) BatchRecordMetric(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.BatchRecordMetricReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MonitoringClient.BatchRecordMetric(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) QueryMetrics(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	req := &pb.QueryMetricReq{
		ServiceName: c.Query("service_name"),
		Type:        pb.MetricType(mustAtoi(c.DefaultQuery("type", "1"))),
		StartTime:   mustAtoi64(c.DefaultQuery("start_time", "0")),
		EndTime:     mustAtoi64(c.DefaultQuery("end_time", "0")),
	}
	resp, err := h.MonitoringClient.QueryMetrics(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Metrics})
}

func (h *Handler) RecordLog(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.RecordLogReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MonitoringClient.RecordLog(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) BatchRecordLog(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.BatchRecordLogReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MonitoringClient.BatchRecordLog(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) QueryLogs(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	req := &pb.QueryLogReq{
		Page:      int32(page),
		PageSize:  int32(pageSize),
		StartTime: mustAtoi64(c.DefaultQuery("start_time", "0")),
		EndTime:   mustAtoi64(c.DefaultQuery("end_time", "0")),
	}
	if serviceName := c.Query("service_name"); serviceName != "" {
		req.ServiceName = &serviceName
	}
	if level := c.Query("level"); level != "" {
		req.Level = &level
	}
	if query := c.Query("query"); query != "" {
		req.Query = &query
	}

	resp, err := h.MonitoringClient.QueryLogs(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Logs})
}

func (h *Handler) QueryAlerts(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	req := &pb.QueryAlertReq{
		StartTime: mustAtoi64(c.DefaultQuery("start_time", "0")),
		EndTime:   mustAtoi64(c.DefaultQuery("end_time", "0")),
	}
	if serviceName := c.Query("service_name"); serviceName != "" {
		req.ServiceName = &serviceName
	}
	if levelStr := c.Query("level"); levelStr != "" {
		level := pb.AlertLevel(mustAtoi(levelStr))
		req.Level = &level
	}
	if c.Query("resolved") == "true" {
		resolved := true
		req.Resolved = &resolved
	} else if c.Query("resolved") == "false" {
		resolved := false
		req.Resolved = &resolved
	}

	resp, err := h.MonitoringClient.QueryAlerts(context.Background(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp.BaseResp != nil {
		statusCode = int(resp.BaseResp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp.BaseResp), Data: resp.Alerts})
}

func (h *Handler) ResolveAlert(c *gin.Context) {
	alertId := c.Param("alertId")
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	resp, err := h.MonitoringClient.ResolveAlert(context.Background(), &pb.GetByAlertIdReq{AlertId: alertId})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp), Data: map[string]string{"resolved_id": alertId}})
}

func (h *Handler) UpdateServiceStatus(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	var req pb.ServiceStatus
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.BadRequest(err.Error()))
		return
	}
	resp, err := h.MonitoringClient.UpdateServiceStatus(context.Background(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: getBaseRespMessage(resp)})
}

func (h *Handler) GetServiceStatus(c *gin.Context) {
	serviceName := c.Query("service_name")
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "operation"))
		return
	}
	resp, err := h.MonitoringClient.GetServiceStatus(context.Background(), &pb.GetByServiceNameReq{ServiceName: serviceName})
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.InternalError(err.Error()))
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	if resp != nil && resp.StatusCode != 0 {
		c.JSON(statusCode, model.Response{Code: statusCode, Message: resp.StatusMessage})
		return
	}
	var data any
	json.Unmarshal([]byte(getBaseRespMessage(resp)), &data)
	if m, ok := data.(map[string]any); ok {
		c.JSON(statusCode, model.Response{Code: statusCode, Message: "success", Data: m["data"]})
		return
	}
	c.JSON(statusCode, model.Response{Code: statusCode, Message: "success", Data: nil})
}

func (h *Handler) ListServiceStatuses(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusOK, model.Response{Code: 200, Message: "success", Data: h.probeLocalServices()})
		return
	}
	resp, err := h.MonitoringClient.ListServiceStatus(context.Background(), &pb.EmptyReq{})
	if err != nil {
		c.JSON(http.StatusOK, model.Response{Code: 200, Message: "success", Data: h.probeLocalServices()})
		return
	}
	statusCode := 200
	if resp != nil {
		statusCode = int(resp.StatusCode)
	}
	if resp != nil && resp.StatusCode != 0 {
		c.JSON(statusCode, model.Response{Code: statusCode, Message: resp.StatusMessage})
		return
	}
	var data any
	json.Unmarshal([]byte(getBaseRespMessage(resp)), &data)
	if m, ok := data.(map[string]any); ok {
		if arr, ok := m["data"].([]any); ok && len(arr) > 0 {
			c.JSON(statusCode, model.Response{Code: statusCode, Message: "success", Data: h.mergeWithProbe(arr)})
			return
		}
	}
	c.JSON(http.StatusOK, model.Response{Code: 200, Message: "success", Data: h.probeLocalServices()})
}

func (h *Handler) ListServiceInfo(c *gin.Context) {
	type ServiceInfo struct {
		Name     string `json:"name"`
		Port     int    `json:"port"`
		Address  string `json:"address"`
		EtcdName string `json:"etcd_name"`
	}
	services := []ServiceInfo{
		{Name: "logos.gateway", Port: h.Cfg.Ports.Gateway, EtcdName: "logos.gateway"},
		{Name: "logos.user", Port: h.Cfg.Ports.User, EtcdName: "logos.user"},
		{Name: "logos.monitoring", Port: h.Cfg.Ports.Monitoring, EtcdName: "logos.monitoring"},
		{Name: "logos.billing", Port: h.Cfg.Ports.Billing, EtcdName: "logos.billing"},
		{Name: "logos.im", Port: h.Cfg.Ports.IM, EtcdName: "logos.im"},
		{Name: "logos.chat", Port: h.Cfg.Ports.Chat, EtcdName: "logos.chat"},
		{Name: "logos.contact", Port: h.Cfg.Ports.Contact, EtcdName: "logos.contact"},
		{Name: "logos.message", Port: h.Cfg.Ports.Message, EtcdName: "logos.message"},
		{Name: "logos.bot", Port: h.Cfg.Ports.Bot, EtcdName: "logos.bot"},
		{Name: "logos.vector", Port: h.Cfg.Ports.Vector, EtcdName: "logos.vector"},
		{Name: "logos.summary", Port: h.Cfg.Ports.Summary, EtcdName: "logos.summary"},
		{Name: "logos.moderation", Port: h.Cfg.Ports.Moderation, EtcdName: "logos.moderation"},
		{Name: "logos.mcp", Port: h.Cfg.Ports.MCP, EtcdName: "logos.mcp"},
		{Name: "logos.knowledge", Port: h.Cfg.Ports.Knowledge, EtcdName: "logos.knowledge"},
		{Name: "logos.search", Port: h.Cfg.Ports.Search, EtcdName: "logos.search"},
		{Name: "logos.extraction", Port: h.Cfg.Ports.Extraction, EtcdName: "logos.extraction"},
		{Name: "logos.question", Port: h.Cfg.Ports.Question, EtcdName: "logos.question"},
		{Name: "logos.recommend", Port: h.Cfg.Ports.Recommend, EtcdName: "logos.recommend"},
		{Name: "logos.collection", Port: h.Cfg.Ports.Collection, EtcdName: "logos.collection"},
	}
	for i := range services {
		if services[i].Port > 0 {
			services[i].Address = fmt.Sprintf("127.0.0.1:%d", services[i].Port)
		}
	}
	c.JSON(http.StatusOK, model.Response{Code: 200, Message: "success", Data: services})
}

func mustAtoi(s string) int32   { v, _ := strconv.Atoi(s); return int32(v) }
func mustAtoi64(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }

func (h *Handler) servicePortMap() map[string]int {
	return map[string]int{
		"logos.gateway":    h.Cfg.Ports.Gateway,
		"logos.user":       h.Cfg.Ports.User,
		"logos.monitoring": h.Cfg.Ports.Monitoring,
		"logos.billing":    h.Cfg.Ports.Billing,
		"logos.im":         h.Cfg.Ports.IM,
		"logos.chat":       h.Cfg.Ports.Chat,
		"logos.contact":    h.Cfg.Ports.Contact,
		"logos.message":    h.Cfg.Ports.Message,
		"logos.bot":        h.Cfg.Ports.Bot,
		"logos.vector":     h.Cfg.Ports.Vector,
		"logos.summary":    h.Cfg.Ports.Summary,
		"logos.moderation": h.Cfg.Ports.Moderation,
		"logos.mcp":        h.Cfg.Ports.MCP,
		"logos.knowledge":  h.Cfg.Ports.Knowledge,
		"logos.search":     h.Cfg.Ports.Search,
		"logos.extraction": h.Cfg.Ports.Extraction,
		"logos.question":   h.Cfg.Ports.Question,
		"logos.recommend":  h.Cfg.Ports.Recommend,
		"logos.collection": h.Cfg.Ports.Collection,
	}
}

func (h *Handler) mergeWithProbe(monitoringData []any) []any {
	portMap := h.servicePortMap()
	now := time.Now().UnixMilli()

	for _, item := range monitoringData {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		svcName, _ := entry["service_name"].(string)
		port, hasPort := portMap[svcName]
		if !hasPort || port <= 0 {
			continue
		}

		status, _ := entry["status"].(string)
		if status != "UP" {
			addr := fmt.Sprintf("127.0.0.1:%d", port)
			conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
			if err == nil {
				conn.Close()
				entry["status"] = "UP"
				delete(entry, "error_message")
				entry["last_check_time"] = now
			}
		}
	}

	return monitoringData
}

func (h *Handler) probeLocalServices() []any {
	type svcPort struct {
		name string
		port int
	}
	services := []svcPort{
		{"logos.gateway", h.Cfg.Ports.Gateway},
		{"logos.user", h.Cfg.Ports.User},
		{"logos.monitoring", h.Cfg.Ports.Monitoring},
		{"logos.billing", h.Cfg.Ports.Billing},
		{"logos.im", h.Cfg.Ports.IM},
		{"logos.chat", h.Cfg.Ports.Chat},
		{"logos.contact", h.Cfg.Ports.Contact},
		{"logos.message", h.Cfg.Ports.Message},
		{"logos.bot", h.Cfg.Ports.Bot},
		{"logos.vector", h.Cfg.Ports.Vector},
		{"logos.summary", h.Cfg.Ports.Summary},
		{"logos.moderation", h.Cfg.Ports.Moderation},
		{"logos.mcp", h.Cfg.Ports.MCP},
		{"logos.knowledge", h.Cfg.Ports.Knowledge},
		{"logos.search", h.Cfg.Ports.Search},
		{"logos.extraction", h.Cfg.Ports.Extraction},
		{"logos.question", h.Cfg.Ports.Question},
		{"logos.recommend", h.Cfg.Ports.Recommend},
		{"logos.collection", h.Cfg.Ports.Collection},
	}
	result := make([]any, 0, len(services))
	now := time.Now().UnixMilli()
	for _, s := range services {
		if s.port <= 0 {
			continue
		}
		addr := fmt.Sprintf("127.0.0.1:%d", s.port)
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			conn.Close()
			result = append(result, map[string]any{
				"service_name":    s.name,
				"status":          "UP",
				"last_check_time": now,
			})
		} else {
			result = append(result, map[string]any{
				"service_name":    s.name,
				"status":          "DOWN",
				"last_check_time": now,
				"error_message":   "No service instances found in etcd",
			})
		}
	}
	return result
}

func _() { _ = (*pbCommon.BaseResp)(nil) }
