package dao

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"Logos/internal/service/platform/monitoring/model"

	"gorm.io/gorm"
)

type MonitoringRepository interface {
	SaveMetric(ctx context.Context, m *model.Metric) error
	BatchSaveMetrics(ctx context.Context, metrics []*model.Metric) error
	QueryMetrics(ctx context.Context, serviceName string, metricType int, startTime, endTime int64, tags map[string]string, page, pageSize int) ([]*model.Metric, int64, error)

	SaveLog(ctx context.Context, l *model.LogEntry) error
	BatchSaveLogs(ctx context.Context, logs []*model.LogEntry) error
	QueryLogs(ctx context.Context, serviceName, level, query string, startTime, endTime int64, page, pageSize int) ([]*model.LogEntry, int64, error)

	CreateAlert(ctx context.Context, a *model.Alert) error
	QueryAlerts(ctx context.Context, serviceName string, level *int, resolved *bool, startTime, endTime int64) ([]*model.Alert, int64, error)
	ResolveAlert(ctx context.Context, id string) error

	UpsertServiceStatus(ctx context.Context, s *model.ServiceStatus) error
	GetServiceStatus(ctx context.Context, serviceName string) (*model.ServiceStatus, error)
	ListServiceStatuses(ctx context.Context) ([]*model.ServiceStatus, error)
}

type monitoringRepository struct {
	db *gorm.DB
}

func NewMonitoringRepository(db *gorm.DB) MonitoringRepository {
	return &monitoringRepository{db: db}
}

func (r *monitoringRepository) SaveMetric(ctx context.Context, m *model.Metric) error {
	return r.db.WithContext(ctx).Create(m).Error
}

func (r *monitoringRepository) BatchSaveMetrics(ctx context.Context, metrics []*model.Metric) error {
	if len(metrics) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&metrics).Error
}

func (r *monitoringRepository) QueryMetrics(ctx context.Context, serviceName string, metricType int, startTime, endTime int64, tags map[string]string, page, pageSize int) ([]*model.Metric, int64, error) {
	var results []*model.Metric
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Metric{})
	if serviceName != "" {
		query = query.Where("service_name = ?", serviceName)
	}
	if metricType > 0 {
		query = query.Where("type = ?", metricType)
	}
	if startTime > 0 {
		query = query.Where("timestamp >= ?", startTime)
	}
	if endTime > 0 {
		query = query.Where("timestamp <= ?", endTime)
	}

	query.Count(&total)

	offset := (page - 1) * pageSize
	err := query.Order("timestamp desc").Offset(offset).Limit(pageSize).Find(&results).Error
	return results, total, err
}

func (r *monitoringRepository) SaveLog(ctx context.Context, l *model.LogEntry) error {
	return r.db.WithContext(ctx).Create(l).Error
}

func (r *monitoringRepository) BatchSaveLogs(ctx context.Context, logs []*model.LogEntry) error {
	if len(logs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&logs).Error
}

func (r *monitoringRepository) QueryLogs(ctx context.Context, serviceName, level, query string, startTime, endTime int64, page, pageSize int) ([]*model.LogEntry, int64, error) {
	var results []*model.LogEntry
	var total int64

	q := r.db.WithContext(ctx).Model(&model.LogEntry{})
	if serviceName != "" {
		q = q.Where("service_name = ?", serviceName)
	}
	if level != "" {
		q = q.Where("level = ?", level)
	}
	if query != "" {
		q = q.Where("message ILIKE ?", "%"+query+"%")
	}
	if startTime > 0 {
		q = q.Where("timestamp >= ?", startTime)
	}
	if endTime > 0 {
		q = q.Where("timestamp <= ?", endTime)
	}

	q.Count(&total)

	offset := (page - 1) * pageSize
	err := q.Order("timestamp desc").Offset(offset).Limit(pageSize).Find(&results).Error
	return results, total, err
}

func (r *monitoringRepository) CreateAlert(ctx context.Context, a *model.Alert) error {
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *monitoringRepository) QueryAlerts(ctx context.Context, serviceName string, level *int, resolved *bool, startTime, endTime int64) ([]*model.Alert, int64, error) {
	var results []*model.Alert
	var total int64

	q := r.db.WithContext(ctx).Model(&model.Alert{})
	if serviceName != "" {
		q = q.Where("service_name = ?", serviceName)
	}
	if level != nil {
		q = q.Where("level = ?", *level)
	}
	if resolved != nil {
		q = q.Where("resolved = ?", *resolved)
	}
	if startTime > 0 {
		q = q.Where("timestamp >= ?", startTime)
	}
	if endTime > 0 {
		q = q.Where("timestamp <= ?", endTime)
	}

	q.Count(&total)
	err := q.Order("timestamp desc").Find(&results).Error
	return results, total, err
}

func (r *monitoringRepository) ResolveAlert(ctx context.Context, id string) error {
	now := fmt.Sprintf("%d", time.Now().Unix())
	return r.db.WithContext(ctx).Model(&model.Alert{}).Where("id = ?", id).
		Updates(map[string]interface{}{"resolved": true, "resolution_time": now}).Error
}

func (r *monitoringRepository) UpsertServiceStatus(ctx context.Context, s *model.ServiceStatus) error {
	return r.db.WithContext(ctx).Where("service_name = ?", s.ServiceName).
		Assign(s).
		FirstOrCreate(s).Error
}

func (r *monitoringRepository) GetServiceStatus(ctx context.Context, serviceName string) (*model.ServiceStatus, error) {
	var s model.ServiceStatus
	err := r.db.WithContext(ctx).Where("service_name = ?", serviceName).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &s, err
}

func (r *monitoringRepository) ListServiceStatuses(ctx context.Context) ([]*model.ServiceStatus, error) {
	var list []*model.ServiceStatus
	err := r.db.WithContext(ctx).Order("service_name asc").Find(&list).Error
	return list, err
}

func MarshalTags(m map[string]string) string {
	if m == nil {
		return "{}"
	}
	data, _ := json.Marshal(m)
	return string(data)
}

func UnmarshalTags(s string) map[string]string {
	if s == "" || s == "{}" {
		return make(map[string]string)
	}
	var m map[string]string
	json.Unmarshal([]byte(s), &m)
	return m
}
