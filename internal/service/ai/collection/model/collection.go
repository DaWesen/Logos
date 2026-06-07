package model

import (
	"time"

	"gorm.io/gorm"
)

type DataSource struct {
	ID          string    `gorm:"primaryKey;size:64;comment:数据源ID"`
	Name        string    `gorm:"size:128;not null;comment:数据源名称"`
	Type        int       `gorm:"not null;comment:类型 1-内部系统 2-文档 3-API 4-数据库 5-网站"`
	URL         string    `gorm:"size:512;comment:连接地址"`
	Config      string    `gorm:"type:text;comment:配置JSON"`
	Description *string   `gorm:"type:text;comment:描述"`
	UserID      string    `gorm:"index;size:64;comment:所属用户ID" json:"user_id"`
	CreatedAt   time.Time `gorm:"autoCreateTime;comment:创建时间"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime;comment:更新时间"`
}

func (DataSource) TableName() string {
	return "data_sources"
}

func (d *DataSource) GetID() string           { return d.ID }
func (d *DataSource) GetName() string         { return d.Name }
func (d *DataSource) GetType() int            { return d.Type }
func (d *DataSource) GetURL() string          { return d.URL }
func (d *DataSource) GetConfig() string       { return d.Config }
func (d *DataSource) GetDescription() *string { return d.Description }
func (d *DataSource) GetCreatedAt() time.Time { return d.CreatedAt }
func (d *DataSource) GetUpdatedAt() time.Time { return d.UpdatedAt }

type CollectionTask struct {
	ID           string     `gorm:"primaryKey;size:64;comment:任务ID"`
	DataSourceID string     `gorm:"size:64;index;not null;comment:关联数据源ID"`
	Name         string     `gorm:"size:128;not null;comment:任务名称"`
	Format       int        `gorm:"not null;comment:数据格式 1-JSON 2-CSV 3-XML 4-TXT 5-PDF 6-WORD 7-EXCEL"`
	Status       string     `gorm:"size:32;not null;default:PENDING;comment:状态 PENDING/RUNNING/SUCCESS/FAILED/STOPPED"`
	Schedule     *string    `gorm:"size:64;comment:Cron表达式"`
	LastRunTime  *time.Time `gorm:"comment:上次执行时间"`
	NextRunTime  *time.Time `gorm:"comment:下次执行时间"`
	UserID       string     `gorm:"index;size:64;comment:所属用户ID" json:"user_id"`
	CreatedAt    time.Time  `gorm:"autoCreateTime;comment:创建时间"`
	UpdatedAt    time.Time  `gorm:"autoUpdateTime;comment:更新时间"`
}

func (CollectionTask) TableName() string {
	return "collection_tasks"
}

func (t *CollectionTask) GetID() string                              { return t.ID }
func (t *CollectionTask) GetDataSourceID() string                    { return t.DataSourceID }
func (t *CollectionTask) GetName() string                            { return t.Name }
func (t *CollectionTask) GetFormat() int                             { return t.Format }
func (t *CollectionTask) GetStatus() string                          { return t.Status }
func (t *CollectionTask) GetSchedule() *string                       { return t.Schedule }
func (t *CollectionTask) GetLastRunTime() interface{ IsZero() bool } { return t.LastRunTime }
func (t *CollectionTask) GetNextRunTime() interface{ IsZero() bool } { return t.NextRunTime }
func (t *CollectionTask) GetCreatedAt() time.Time                    { return t.CreatedAt }
func (t *CollectionTask) GetUpdatedAt() time.Time                    { return t.UpdatedAt }

type CollectionResult struct {
	ID             string    `gorm:"primaryKey;size:64;comment:结果ID"`
	TaskID         string    `gorm:"size:64;index;not null;comment:关联任务ID"`
	Status         string    `gorm:"size:32;not null;default:RUNNING;comment:状态"`
	CollectedCount int64     `gorm:"not null;default:0;comment:采集数量"`
	ProcessedCount int64     `gorm:"not null;default:0;comment:处理数量"`
	ErrorMsg       *string   `gorm:"type:text;comment:错误信息"`
	StartTime      int64     `gorm:"not null;comment:开始时间戳"`
	EndTime        int64     `gorm:"not null;comment:结束时间戳"`
	CreatedAt      time.Time `gorm:"autoCreateTime;comment:创建时间"`
}

func (CollectionResult) TableName() string {
	return "collection_results"
}

func (r *CollectionResult) GetID() string            { return r.ID }
func (r *CollectionResult) GetTaskID() string        { return r.TaskID }
func (r *CollectionResult) GetStatus() string        { return r.Status }
func (r *CollectionResult) GetCollectedCount() int64 { return r.CollectedCount }
func (r *CollectionResult) GetProcessedCount() int64 { return r.ProcessedCount }
func (r *CollectionResult) GetErrorMsg() *string     { return r.ErrorMsg }
func (r *CollectionResult) GetStartTime() int64      { return r.StartTime }
func (r *CollectionResult) GetEndTime() int64        { return r.EndTime }

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&DataSource{}, &CollectionTask{}, &CollectionResult{})
}
