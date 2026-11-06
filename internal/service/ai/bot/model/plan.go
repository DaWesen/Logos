package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// JSONBlob 任意 JSON 文档，供计划快照与步骤产物落库。
//
// 与 JSONMap 分开定义而不是复用 map[string]string：计划是嵌套结构，
// 强行拍平成字符串会在每次读取时都要再解析一次，且丢字段时无法定位。
type JSONBlob []byte

func (b JSONBlob) Value() (driver.Value, error) {
	if len(b) == 0 {
		return "null", nil
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("JSONBlob 不是合法 JSON")
	}
	return string(b), nil
}

func (b *JSONBlob) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		*b = nil
		return nil
	case []byte:
		*b = append((*b)[:0], v...)
		return nil
	case string:
		*b = append((*b)[:0], v...)
		return nil
	default:
		return fmt.Errorf("JSONBlob 不支持的类型 %T", value)
	}
}

// PlanRun 一次规划执行的快照。
//
// 运行记录是审计数据：不挂 DeletedAt，也不随会话删除级联清理。
// 排障时需要回答"当时模型看到了什么、执行到哪一步"，删掉就永远答不了。
type PlanRun struct {
	ID             string    `gorm:"primaryKey;size:36;comment:运行ID" json:"id"`
	BotID          string    `gorm:"index;size:36;comment:BotID" json:"botId"`
	ConversationID string    `gorm:"index;size:100;comment:对话ID" json:"conversationId"`
	UserID         string    `gorm:"index;size:36;comment:用户ID" json:"userId"`
	Goal           string    `gorm:"type:text;comment:原始目标（不可变）" json:"goal"`
	GoalHash       string    `gorm:"size:64;index;comment:目标哈希" json:"goalHash"`
	Status         string    `gorm:"size:20;index;comment:running/completed/degraded/failed" json:"status"`
	Revision       int       `gorm:"comment:重规划版本号" json:"revision"`
	PlanJSON       JSONBlob  `gorm:"type:jsonb;comment:当前计划快照" json:"planJson"`
	Error          string    `gorm:"type:text;comment:终止原因" json:"error"`
	CreatedAt      time.Time `gorm:"autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime" json:"updatedAt"`
}

func (r *PlanRun) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}

// PlanStepRecord 单步执行记录，同时承担审计与副作用重放。
//
// 每条记录代表一次 attempt：重试不覆盖旧记录，否则"重试了几次、
// 每次失败原因是什么"在事后完全不可见。
type PlanStepRecord struct {
	ID         string    `gorm:"primaryKey;size:36" json:"id"`
	RunID      string    `gorm:"index:idx_step_lookup,priority:1;size:36" json:"runId"`
	StepID     int       `gorm:"index:idx_step_lookup,priority:2" json:"stepId"`
	Revision   int       `gorm:"index:idx_step_lookup,priority:3" json:"revision"`
	Attempt    int       `gorm:"index:idx_step_lookup,priority:4" json:"attempt"`
	Status     string    `gorm:"size:20" json:"status"`
	Effect     string    `gorm:"size:20" json:"effect"`
	InputHash  string    `gorm:"size:64;index;comment:入参哈希，用于重放去重" json:"inputHash"`
	Output     string    `gorm:"type:text" json:"output"`
	Facts      JSONBlob  `gorm:"type:jsonb" json:"facts"`
	Violations JSONBlob  `gorm:"type:jsonb" json:"violations"`
	TokensIn   int       `json:"tokensIn"`
	TokensOut  int       `json:"tokensOut"`
	DurationMs int64     `json:"durationMs"`
	Error      string    `gorm:"type:text" json:"error"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

func (r *PlanStepRecord) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}
