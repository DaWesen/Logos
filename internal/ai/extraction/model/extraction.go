package model

import (
	"time"

	"gorm.io/gorm"
)

type ExtractionTask struct {
	ID          string     `gorm:"primaryKey;size:64;comment:任务ID"`
	Type        int        `gorm:"not null;comment:任务类型 1-实体识别 2-关系抽取 3-三元组抽取 4-摘要 5-关键短语"`
	DataID      string     `gorm:"size:128;index;not null;comment:数据ID"`
	DataType    string     `gorm:"size:32;not null;comment:数据类型"`
	Status      int        `gorm:"not null;default:1;comment:状态 1-待执行 2-运行中 3-成功 4-失败 5-已取消"`
	Parameters  string     `gorm:"type:text;comment:参数JSON"`
	ScheduledAt *time.Time `gorm:"comment:计划执行时间"`
	StartedAt   *time.Time `gorm:"comment:实际开始时间"`
	EndedAt     *time.Time `gorm:"comment:结束时间"`
	CreatedAt   time.Time  `gorm:"autoCreateTime;comment:创建时间"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime;comment:更新时间"`
}

func (ExtractionTask) TableName() string {
	return "extraction_tasks"
}

func (t *ExtractionTask) GetID() string           { return t.ID }
func (t *ExtractionTask) GetType() int            { return t.Type }
func (t *ExtractionTask) GetDataID() string       { return t.DataID }
func (t *ExtractionTask) GetDataType() string     { return t.DataType }
func (t *ExtractionTask) GetStatus() int          { return t.Status }
func (t *ExtractionTask) GetParameters() string   { return t.Parameters }
func (t *ExtractionTask) GetCreatedAt() time.Time { return t.CreatedAt }
func (t *ExtractionTask) GetUpdatedAt() time.Time { return t.UpdatedAt }

type ExtractionResult struct {
	ID         string    `gorm:"primaryKey;size:64;comment:结果ID"`
	TaskID     string    `gorm:"size:64;index;not null;comment:关联任务ID"`
	Status     int       `gorm:"not null;default:1;comment:状态"`
	Entities   string    `gorm:"type:text;comment:实体JSON数组"`
	Relations  string    `gorm:"type:text;comment:关系JSON数组"`
	Triples    string    `gorm:"type:text;comment:三元组JSON数组"`
	Summary    *string   `gorm:"type:text;comment:摘要文本"`
	Keyphrases *string   `gorm:"type:text;comment:关键短语JSON数组"`
	ErrorMsg   *string   `gorm:"type:text;comment:错误信息"`
	StartTime  int64     `gorm:"not null;comment:开始时间戳"`
	EndTime    int64     `gorm:"not null;comment:结束时间戳"`
	CreatedAt  time.Time `gorm:"autoCreateTime;comment:创建时间"`
}

func (ExtractionResult) TableName() string {
	return "extraction_results"
}

func (r *ExtractionResult) GetID() string          { return r.ID }
func (r *ExtractionResult) GetTaskID() string      { return r.TaskID }
func (r *ExtractionResult) GetStatus() int         { return r.Status }
func (r *ExtractionResult) GetEntities() string    { return r.Entities }
func (r *ExtractionResult) GetRelations() string   { return r.Relations }
func (r *ExtractionResult) GetTriples() string     { return r.Triples }
func (r *ExtractionResult) GetSummary() *string    { return r.Summary }
func (r *ExtractionResult) GetKeyphrases() *string { return r.Keyphrases }
func (r *ExtractionResult) GetErrorMsg() *string   { return r.ErrorMsg }
func (r *ExtractionResult) GetStartTime() int64    { return r.StartTime }
func (r *ExtractionResult) GetEndTime() int64      { return r.EndTime }

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&ExtractionTask{}, &ExtractionResult{})
}
