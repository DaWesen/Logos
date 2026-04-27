package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Metric struct {
	ID          string            `gorm:"primaryKey;size:64;comment:指标ID"`
	ServiceName string            `gorm:"size:128;index;not null;comment:服务名"`
	Type        int               `gorm:"not null;comment:类型 1-CPU 2-MEMORY 3-DISK 4-NETWORK 5-REQUEST 6-ERROR 7-LATENCY 8-THROUGHPUT"`
	Value       float64           `gorm:"not null;comment:值"`
	Unit        string            `gorm:"size:32;comment:单位"`
	Tags        string            `gorm:"type:text;comment:标签JSON"`
	Timestamp   int64             `gorm:"index;not null;comment:时间戳"`
	CreatedAt   time.Time         `gorm:"autoCreateTime;comment:创建时间"`
}

func (Metric) TableName() string {
	return "metrics"
}

func (m *Metric) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

type Alert struct {
	ID            string     `gorm:"primaryKey;size:64;comment:告警ID"`
	ServiceName   string     `gorm:"size:128;index;not null;comment:服务名"`
	Level         int        `gorm:"not null;comment:级别 1-INFO 2-WARNING 3-ERROR 4-CRITICAL"`
	Message       string     `gorm:"type:text;not null;comment:告警消息"`
	MetricName    string     `gorm:"size:128;comment:指标名"`
	MetricValue   float64    `gorm:"comment:指标值"`
	Threshold     float64    `gorm:"comment:阈值"`
	Timestamp     int64      `gorm:"index;not null;comment:时间戳"`
	Resolved      bool       `gorm:"not null;default:false;comment:是否已解决"`
	ResolutionTime *string   `gorm:"size:32;comment:解决时间"`
	CreatedAt     time.Time  `gorm:"autoCreateTime;comment:创建时间"`
}

func (Alert) TableName() string { return "alerts" }

func (a *Alert) BeforeCreate(tx *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.New().String()
	}
	return nil
}

type LogEntry struct {
	ID          string    `gorm:"primaryKey;size:64;comment:日志ID"`
	ServiceName string    `gorm:"size:128;index;not null;comment:服务名"`
	Level       string    `gorm:"size:16;index;not null;comment:日志级别"`
	Message     string    `gorm:"type:text;not null;comment:消息内容"`
	Fields      string    `gorm:"type:text;comment:字段JSON"`
	Timestamp   int64     `gorm:"index;not null;comment:时间戳"`
	CreatedAt   time.Time `gorm:"autoCreateTime;comment:创建时间"`
}

func (LogEntry) TableName() string { return "logs" }

func (l *LogEntry) BeforeCreate(tx *gorm.DB) error {
	if l.ID == "" {
		l.ID = uuid.New().String()
	}
	return nil
}

type ServiceStatus struct {
	ID            string    `gorm:"primaryKey;size:64;comment:状态ID"`
	ServiceName   string    `gorm:"size:128;uniqueIndex;not null;comment:服务名"`
	Status        string    `gorm:"size:16;not null;default:UP;comment:状态 UP/DOWN/DEGRADED"`
	LastCheckTime int64    `gorm:"not null;comment:最后检查时间"`
	ErrorMessage *string   `gorm:"type:text;comment:错误信息"`
	Metadata     string    `gorm:"type:text;comment:元数据JSON"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime;comment:更新时间"`
}

func (ServiceStatus) TableName() string { return "service_statuses" }

func (s *ServiceStatus) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	return nil
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Metric{}, &Alert{}, &LogEntry{}, &ServiceStatus{})
}
