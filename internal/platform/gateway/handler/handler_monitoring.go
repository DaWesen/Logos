package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"Logos/internal/platform/gateway/model"

	pb "Logos/proto_gen/monitoring"
	pbCommon "Logos/proto_gen/common"

	"github.com/gin-gonic/gin"
)

func (h *Handler) RecordMetric(c *gin.Context) {
	if h.MonitoringClient == nil {
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
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
		c.JSON(http.StatusServiceUnavailable, model.Error(503, "监控服务暂不可用"))
		return
	}
	resp, err := h.MonitoringClient.ListServiceStatus(context.Background(), &pb.EmptyReq{})
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
	c.JSON(statusCode, model.Response{Code: statusCode, Message: "success", Data: []any{}})
}

func mustAtoi(s string) int32       { v, _ := strconv.Atoi(s); return int32(v) }
func mustAtoi64(s string) int64     { v, _ := strconv.ParseInt(s, 10, 64); return v }

func _() { _ = (*pbCommon.BaseResp)(nil) }
