package model

import (
	"time"

	"gorm.io/gorm"
)

type VectorCollectionRecord struct {
	ID               string         `gorm:"primaryKey;size:64;comment:集合ID" json:"id"`
	Name             string         `gorm:"size:255;not null;comment:集合名称" json:"name"`
	ModelType        int            `gorm:"default:4;comment:模型类型" json:"model_type"`
	IndexType        int            `gorm:"default:3;comment:索引类型" json:"index_type"`
	Dimension        int            `gorm:"default:768;comment:向量维度" json:"dimension"`
	Size             int64          `gorm:"default:0;comment:向量数量" json:"size"`
	VlmModel         string         `gorm:"size:255;comment:VLM模型名称" json:"vlm_model"`
	VlmBaseURL       string         `gorm:"size:512;comment:VLM模型API基础URL" json:"vlm_base_url"`
	VlmApiKey        string         `gorm:"size:255;comment:VLM模型API密钥" json:"vlm_api_key"`
	LlmModel         string         `gorm:"size:255;comment:LLM模型名称" json:"llm_model"`
	LlmBaseURL       string         `gorm:"size:512;comment:LLM模型API基础URL" json:"llm_base_url"`
	LlmApiKey        string         `gorm:"size:255;comment:LLM模型API密钥" json:"llm_api_key"`
	AsrModel         string         `gorm:"size:255;comment:ASR模型名称" json:"asr_model"`
	AsrBaseURL       string         `gorm:"size:512;comment:ASR模型API基础URL" json:"asr_base_url"`
	AsrApiKey        string         `gorm:"size:255;comment:ASR模型API密钥" json:"asr_api_key"`
	EmbeddingModel   string         `gorm:"size:255;comment:嵌入模型名称" json:"embedding_model"`
	EmbeddingBaseURL string         `gorm:"size:512;comment:嵌入模型API基础URL" json:"embedding_base_url"`
	EmbeddingApiKey  string         `gorm:"size:255;comment:嵌入模型API密钥" json:"embedding_api_key"`
	UserID           string         `gorm:"index;size:64;comment:所属用户ID" json:"user_id"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
}

func (VectorCollectionRecord) TableName() string {
	return "vector_collections"
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&VectorCollectionRecord{})
}
