package handler

import (
	"context"
	"encoding/json"

	"Logos/internal/service/platform/monitoring/service"
	pb "Logos/proto_gen/monitoring"
	pbCommon "Logos/proto_gen/common"
)

type MonitoringServiceImpl struct {
	pb.UnimplementedMonitoringServiceServer
	MonitoringService service.MonitoringService
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// RecordMetric implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) RecordMetric(ctx context.Context, req *pb.RecordMetricReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if recErr := s.MonitoringService.RecordMetric(ctx, req.ServiceName, int32(req.Type), req.Value, req.Unit, req.Tags); recErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = recErr.Error()
	}
	return resp, nil
}

// BatchRecordMetric implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) BatchRecordMetric(ctx context.Context, req *pb.BatchRecordMetricReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	inputs := make([]struct {
		ServiceName string
		Type        int32
		Value       float64
		Unit        string
		Tags        map[string]string
	}, len(req.Metrics))

	for i, m := range req.Metrics {
		inputs[i] = struct {
			ServiceName string
			Type        int32
			Value       float64
			Unit        string
			Tags        map[string]string
		}{
			ServiceName: m.ServiceName,
			Type:        int32(m.Type),
			Value:       m.Value,
			Unit:        m.Unit,
			Tags:        m.Tags,
		}
	}

	if batchErr := s.MonitoringService.BatchRecordMetrics(ctx, inputs); batchErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = batchErr.Error()
	}
	return resp, nil
}

// QueryMetrics implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) QueryMetrics(ctx context.Context, req *pb.QueryMetricReq) (*pb.MetricResp, error) {
	resp := &pb.MetricResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	metrics, total, queryErr := s.MonitoringService.QueryMetrics(ctx,
		req.ServiceName, int32(req.Type), req.StartTime, req.EndTime, req.Tags)
	if queryErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = queryErr.Error()
		return resp, nil
	}

	for _, m := range metrics {
		tags := make(map[string]string)
		json.Unmarshal([]byte(m.Tags), &tags)

		resp.Metrics = append(resp.Metrics, &pb.Metric{
			Id:          m.ID,
			ServiceName: m.ServiceName,
			Type:        pb.MetricType(m.Type),
			Value:       m.Value,
			Unit:        m.Unit,
			Timestamp:   m.Timestamp,
			Tags:        tags,
		})
	}
	resp.Total = total
	return resp, nil
}

// RecordLog implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) RecordLog(ctx context.Context, req *pb.RecordLogReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if recErr := s.MonitoringService.RecordLog(ctx, req.ServiceName, req.Level, req.Message, req.Fields); recErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = recErr.Error()
	}
	return resp, nil
}

// BatchRecordLog implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) BatchRecordLog(ctx context.Context, req *pb.BatchRecordLogReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	inputs := make([]struct {
		ServiceName string
		Level       string
		Message     string
		Fields      map[string]string
	}, len(req.Logs))

	for i, l := range req.Logs {
		inputs[i] = struct {
			ServiceName string
			Level       string
			Message     string
			Fields      map[string]string
		}{
			ServiceName: l.ServiceName,
			Level:       l.Level,
			Message:     l.Message,
			Fields:      l.Fields,
		}
	}

	if batchErr := s.MonitoringService.BatchRecordLogs(ctx, inputs); batchErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = batchErr.Error()
	}
	return resp, nil
}

// QueryLogs implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) QueryLogs(ctx context.Context, req *pb.QueryLogReq) (*pb.LogResp, error) {
	resp := &pb.LogResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	logs, total, queryErr := s.MonitoringService.QueryLogs(ctx,
		derefString(req.ServiceName), derefString(req.Level), derefString(req.Query), req.StartTime, req.EndTime, req.Page, req.PageSize)
	if queryErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = queryErr.Error()
		return resp, nil
	}

	for _, l := range logs {
		fields := make(map[string]string)
		json.Unmarshal([]byte(l.Fields), &fields)

		resp.Logs = append(resp.Logs, &pb.Log{
			Id:          l.ID,
			ServiceName: l.ServiceName,
			Level:       l.Level,
			Message:     l.Message,
			Timestamp:   l.Timestamp,
			Fields:      fields,
		})
	}
	resp.Total = total
	return resp, nil
}

// QueryAlerts implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) QueryAlerts(ctx context.Context, req *pb.QueryAlertReq) (*pb.AlertResp, error) {
	resp := &pb.AlertResp{
		BaseResp: &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"},
	}

	var serviceName *string
	if req.ServiceName != nil && len(*req.ServiceName) > 0 {
		serviceName = req.ServiceName
	}

	var lvl *int32
	if req.Level != nil {
		l := int32(*req.Level)
		lvl = &l
	}

	alerts, total, queryErr := s.MonitoringService.QueryAlerts(ctx, serviceName, lvl, req.Resolved, req.StartTime, req.EndTime)
	if queryErr != nil {
		resp.BaseResp.StatusCode = 500
		resp.BaseResp.StatusMessage = queryErr.Error()
		return resp, nil
	}

	for _, a := range alerts {
		resp.Alerts = append(resp.Alerts, &pb.Alert{
			Id:             a.ID,
			ServiceName:    a.ServiceName,
			Level:          pb.AlertLevel(a.Level),
			Message:        a.Message,
			MetricValue:    a.MetricValue,
			Threshold:      a.Threshold,
			Timestamp:      a.Timestamp,
			Resolved:       a.Resolved,
			ResolutionTime: a.ResolutionTime,
		})
	}
	resp.Total = total
	return resp, nil
}

// ResolveAlert implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) ResolveAlert(ctx context.Context, req *pb.GetByAlertIdReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}
	if resolveErr := s.MonitoringService.ResolveAlert(ctx, req.AlertId); resolveErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = resolveErr.Error()
	}
	return resp, nil
}

// UpdateServiceStatus implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) UpdateServiceStatus(ctx context.Context, status *pb.ServiceStatus) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	metadata := make(map[string]string)
	if status.Metadata != nil {
		metadata = status.Metadata
	}

	if updateErr := s.MonitoringService.UpdateServiceStatus(ctx, status.ServiceName, status.Status, status.ErrorMessage, metadata); updateErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = updateErr.Error()
	}
	return resp, nil
}

// GetServiceStatus implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) GetServiceStatus(ctx context.Context, req *pb.GetByServiceNameReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	ss, getErr := s.MonitoringService.GetServiceStatus(ctx, req.ServiceName)
	if getErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = getErr.Error()
	} else if ss == nil {
		resp.StatusCode = 404
		resp.StatusMessage = "{\"data\":null}"
	} else {
		data, _ := json.Marshal(map[string]any{
			"service_name":    ss.ServiceName,
			"status":         ss.Status,
			"last_check_time": ss.LastCheckTime,
			"error_message":  ss.ErrorMessage,
			"metadata":       ss.Metadata,
			"updatedAt":      ss.UpdatedAt,
		})
		resp.StatusMessage = "{\"data\":" + string(data) + "}"
	}
	return resp, nil
}

// ListServiceStatus implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) ListServiceStatus(ctx context.Context, req *pb.EmptyReq) (*pbCommon.BaseResp, error) {
	resp := &pbCommon.BaseResp{StatusCode: 0, StatusMessage: "success"}

	list, listErr := s.MonitoringService.ListServiceStatuses(ctx)
	if listErr != nil {
		resp.StatusCode = 500
		resp.StatusMessage = listErr.Error()
		return resp, nil
	}

	items := make([]map[string]any, 0, len(list))
	for _, ss := range list {
		items = append(items, map[string]any{
			"service_name":    ss.ServiceName,
			"status":         ss.Status,
			"last_check_time": ss.LastCheckTime,
			"error_message":  ss.ErrorMessage,
			"metadata":       ss.Metadata,
			"updatedAt":      ss.UpdatedAt,
		})
	}
	data, _ := json.Marshal(items)
	resp.StatusMessage = "{\"data\":" + string(data) + "}"
	return resp, nil
}
