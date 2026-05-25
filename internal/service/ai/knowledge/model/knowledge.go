package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type Entity struct {
	ID           string         `gorm:"primaryKey;size:36;comment:实体ID" json:"id"`
	Type         string         `gorm:"index;size:50;not null;comment:实体类型" json:"type"`
	Name         string         `gorm:"index;size:255;not null;comment:实体名称" json:"name"`
	CollectionID string         `gorm:"index;size:36;comment:所属集合ID" json:"collectionId"`
	Color        string         `gorm:"size:7;comment:显示颜色" json:"color,omitempty"`
	Properties   JSONMap        `gorm:"type:jsonb;comment:实体属性" json:"properties"`
	Description  *string        `gorm:"type:text;comment:实体描述" json:"description,omitempty"`
	CreatedAt    time.Time      `gorm:"autoCreateTime;index;comment:创建时间" json:"createdAt"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

type Relation struct {
	ID           string         `gorm:"primaryKey;size:36;comment:关系ID" json:"id"`
	Type         string         `gorm:"index;size:50;not null;comment:关系类型" json:"type"`
	SourceID     string         `gorm:"index;size:36;not null;comment:源实体ID" json:"sourceId"`
	TargetID     string         `gorm:"index;size:36;not null;comment:目标实体ID" json:"targetId"`
	CollectionID string         `gorm:"index;size:36;comment:所属集合ID" json:"collectionId"`
	Properties   JSONMap        `gorm:"type:jsonb;comment:关系属性" json:"properties"`
	Description  *string        `gorm:"type:text;comment:关系描述" json:"description,omitempty"`
	CreatedAt    time.Time      `gorm:"autoCreateTime;index;comment:创建时间" json:"createdAt"`
	UpdatedAt    time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

type GraphStats struct {
	EntityCount       int64            `json:"entityCount"`
	RelationCount     int64            `json:"relationCount"`
	EntityTypeCount   map[string]int64 `json:"entityTypeCount"`
	RelationTypeCount map[string]int64 `json:"relationTypeCount"`
}

type Subgraph struct {
	Nodes     []*Entity   `json:"nodes"`
	Edges     []*Relation `json:"edges"`
	NodeCount int         `json:"nodeCount"`
	EdgeCount int         `json:"edgeCount"`
}

type EntityPath struct {
	Entities []*Entity `json:"entities"`
	Edges    []*Relation `json:"edges"`
	Length   int       `json:"length"`
}

type JSONMap map[string]string

func (j JSONMap) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, j)
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Entity{}, &Relation{})
}

// GetID 方法
func (e *Entity) GetID() string {
	return e.ID
}

// GetID 方法
func (r *Relation) GetID() string {
	return r.ID
}
