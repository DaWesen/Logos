package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/platform/monitoring/dao"
	"Logos/internal/platform/monitoring/model"
)

type MonitoringService interface {
	RecordMetric(ctx context.Context, serviceName string, metricType int32, value float64, unit string, tags map[string]string) error
	BatchRecordMetrics(ctx context.Context, metrics []struct {
		ServiceName string
		Type        int32
		Value       float64
		Unit        string
		Tags        map[string]string
	}) error
	QueryMetrics(ctx context.Context, serviceName string, metricType int32, startTime, endTime int64, tags map[string]string) ([]*model.Metric, int64, error)

	RecordLog(ctx context.Context, serviceName, level, message string, fields map[string]string) error
	BatchRecordLogs(ctx context.Context, logs []struct {
		ServiceName string
		Level       string
		Message     string
		Fields      map[string]string
	}) error
	QueryLogs(ctx context.Context, serviceName, level, query string, startTime, endTime int64, page, pageSize int32) ([]*model.LogEntry, int64, error)

	QueryAlerts(ctx context.Context, serviceName *string, level *int32, resolved *bool, startTime, endTime int64) ([]*model.Alert, int64, error)
	ResolveAlert(ctx context.Context, alertID string) error

	UpdateServiceStatus(ctx context.Context, serviceName, status string, errorMessage *string, metadata map[string]string) error
	GetServiceStatus(ctx context.Context, serviceName string) (*model.ServiceStatus, error)
	ListServiceStatuses(ctx context.Context) ([]*model.ServiceStatus, error)
}

type monitoringServiceImpl struct {
	repo dao.MonitoringRepository
}

func NewMonitoringService(repo dao.MonitoringRepository) MonitoringService {
	return &monitoringServiceImpl{repo: repo}
}

func (s *monitoringServiceImpl) RecordMetric(ctx context.Context, serviceName string, metricType int32, value float64, unit string, tags map[string]string) error {
	m := &model.Metric{
		ID:          uuid.New().String(),
		ServiceName: serviceName,
		Type:        int(metricType),
		Value:       value,
		Unit:        unit,
		Tags:        dao.MarshalTags(tags),
		Timestamp:   time.Now().UnixMilli(),
	}

	if err := s.repo.SaveMetric(ctx, m); err != nil {
		return fmt.Errorf("保存指标失败: %w", err)
	}
	s.checkThreshold(ctx, m)
	return nil
}

func (s *monitoringServiceImpl) BatchRecordMetrics(ctx context.Context, metrics []struct {
	ServiceName string
	Type        int32
	Value       float64
	Unit        string
	Tags        map[string]string
}) error {
	var ms []*model.Metric
	now := time.Now().UnixMilli()
	for _, m := range metrics {
		ms = append(ms, &model.Metric{
			ID:          uuid.New().String(),
			ServiceName: m.ServiceName,
			Type:        int(m.Type),
			Value:       m.Value,
			Unit:        m.Unit,
			Tags:        dao.MarshalTags(m.Tags),
			Timestamp:   now,
		})
	}
	return s.repo.BatchSaveMetrics(ctx, ms)
}

func (s *monitoringServiceImpl) QueryMetrics(ctx context.Context, serviceName string, metricType int32, startTime, endTime int64, tags map[string]string) ([]*model.Metric, int64, error) {
	return s.repo.QueryMetrics(ctx, serviceName, int(metricType), startTime, endTime, tags, 1, 100)
}

func (s *monitoringServiceImpl) RecordLog(ctx context.Context, serviceName, level, message string, fields map[string]string) error {
	l := &model.LogEntry{
		ID:          uuid.New().String(),
		ServiceName: serviceName,
		Level:       level,
		Message:     message,
		Fields:      dao.MarshalTags(fields),
		Timestamp:   time.Now().UnixMilli(),
	}
	return s.repo.SaveLog(ctx, l)
}

func (s *monitoringServiceImpl) BatchRecordLogs(ctx context.Context, logs []struct {
	ServiceName string
	Level       string
	Message     string
	Fields      map[string]string
}) error {
	var ls []*model.LogEntry
	now := time.Now().UnixMilli()
	for _, l := range logs {
		ls = append(ls, &model.LogEntry{
			ID:          uuid.New().String(),
			ServiceName: l.ServiceName,
			Level:       l.Level,
			Message:     l.Message,
			Fields:      dao.MarshalTags(l.Fields),
			Timestamp:   now,
		})
	}
	return s.repo.BatchSaveLogs(ctx, ls)
}

func (s *monitoringServiceImpl) QueryLogs(ctx context.Context, serviceName, level, query string, startTime, endTime int64, page, pageSize int32) ([]*model.LogEntry, int64, error) {
	p, ps := int(page), int(pageSize)
	if p < 1 {
		p = 1
	}
	if ps < 1 || ps > 100 {
		ps = 20
	}
	return s.repo.QueryLogs(ctx, serviceName, level, query, startTime, endTime, p, ps)
}

func (s *monitoringServiceImpl) QueryAlerts(ctx context.Context, serviceName *string, level *int32, resolved *bool, startTime, endTime int64) ([]*model.Alert, int64, error) {
	var sn string
	if serviceName != nil {
		sn = *serviceName
	}
	var lvl *int
	if level != nil {
		l := int(*level)
		lvl = &l
	}
	return s.repo.QueryAlerts(ctx, sn, lvl, resolved, startTime, endTime)
}

func (s *monitoringServiceImpl) ResolveAlert(ctx context.Context, alertID string) error {
	return s.repo.ResolveAlert(ctx, alertID)
}

func (s *monitoringServiceImpl) UpdateServiceStatus(ctx context.Context, serviceName, status string, errorMessage *string, metadata map[string]string) error {
	ss := &model.ServiceStatus{
		ID:            uuid.New().String(),
		ServiceName:   serviceName,
		Status:        status,
		LastCheckTime: time.Now().UnixMilli(),
		ErrorMessage:  errorMessage,
		Metadata:      dao.MarshalTags(metadata),
	}
	return s.repo.UpsertServiceStatus(ctx, ss)
}

func (s *monitoringServiceImpl) GetServiceStatus(ctx context.Context, serviceName string) (*model.ServiceStatus, error) {
	return s.repo.GetServiceStatus(ctx, serviceName)
}

func (s *monitoringServiceImpl) ListServiceStatuses(ctx context.Context) ([]*model.ServiceStatus, error) {
	return s.repo.ListServiceStatuses(ctx)
}

func (s *monitoringServiceImpl) checkThreshold(ctx context.Context, m *model.Metric) {
	var threshold float64
	var level int
	var alertMsg string

	switch m.Type {
	case 6:
		threshold = 10.0
		level = 3
		alertMsg = fmt.Sprintf("错误率 %.2f%% 超过阈值 %.0f%%", m.Value, threshold)
	case 7:
		threshold = 5000.0
		level = 2
		alertMsg = fmt.Sprintf("延迟 %.2fms 超过阈值 %.0fms", m.Value, threshold)
	case 8:
		threshold = 100.0
		level = 2
		alertMsg = fmt.Sprintf("吞吐量 %.2f 低于阈值 %.0f", m.Value, threshold)
	default:
		return
	}

	shouldAlert := false
	switch m.Type {
	case 6, 7:
		shouldAlert = m.Value > threshold
	case 8:
		shouldAlert = m.Value < threshold
	}

	if shouldAlert {
		s.repo.CreateAlert(ctx, &model.Alert{
			ID:          uuid.New().String(),
			ServiceName: m.ServiceName,
			Level:       level,
			Message:     alertMsg,
			MetricValue: m.Value,
			Threshold:   threshold,
			Timestamp:   m.Timestamp,
		})
	}
}
