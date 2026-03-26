package handler

import (
	common "Noah/kitex_gen/common"
	monitoring "Noah/kitex_gen/monitoring"
	"context"
)

// MonitoringServiceImpl implements the last service interface defined in the IDL.
type MonitoringServiceImpl struct{}

// RecordMetric implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) RecordMetric(ctx context.Context, req *monitoring.RecordMetricReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// BatchRecordMetric implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) BatchRecordMetric(ctx context.Context, req *monitoring.BatchRecordMetricReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// QueryMetrics implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) QueryMetrics(ctx context.Context, req *monitoring.QueryMetricReq) (resp *monitoring.MetricResp, err error) {
	// TODO: Your code here...
	return
}

// RecordLog implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) RecordLog(ctx context.Context, req *monitoring.RecordLogReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// BatchRecordLog implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) BatchRecordLog(ctx context.Context, req *monitoring.BatchRecordLogReq) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// QueryLogs implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) QueryLogs(ctx context.Context, req *monitoring.QueryLogReq) (resp *monitoring.LogResp, err error) {
	// TODO: Your code here...
	return
}

// QueryAlerts implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) QueryAlerts(ctx context.Context, req *monitoring.QueryAlertReq) (resp *monitoring.AlertResp, err error) {
	// TODO: Your code here...
	return
}

// ResolveAlert implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) ResolveAlert(ctx context.Context, alertId string) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// UpdateServiceStatus implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) UpdateServiceStatus(ctx context.Context, status *monitoring.ServiceStatus) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// GetServiceStatus implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) GetServiceStatus(ctx context.Context, serviceName string) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}

// ListServiceStatus implements the MonitoringServiceImpl interface.
func (s *MonitoringServiceImpl) ListServiceStatus(ctx context.Context) (resp *common.BaseResp, err error) {
	// TODO: Your code here...
	return
}
