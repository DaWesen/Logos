package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type Document struct {
	ID                 string         `gorm:"primaryKey;size:36;comment:文档ID" json:"id"`
	VectorCollectionID string         `gorm:"size:36;index;comment:向量集合ID" json:"vectorCollectionId"`
	FileName           string         `gorm:"size:255;not null;comment:文件名" json:"fileName"`
	FileType           string         `gorm:"size:50;index;comment:文件类型" json:"fileType"`
	FileSize           int64          `gorm:"comment:文件大小" json:"fileSize"`
	FileURL            string         `gorm:"type:text;comment:文件URL" json:"fileURL"`
	FileHash           string         `gorm:"size:64;index;comment:文件哈希" json:"fileHash"`
	Status             int            `gorm:"index;comment:状态" json:"status"`
	Content            string         `gorm:"type:text;comment:文件内容" json:"content"`
	Metadata           JSONMap        `gorm:"type:jsonb;comment:元数据" json:"metadata"`
	ErrorMsg           *string        `gorm:"type:text;comment:错误信息" json:"errorMsg"`
	ProcessedAt        *time.Time     `gorm:"comment:处理时间" json:"processedAt"`
	UserID             string         `gorm:"index;size:64;comment:所属用户ID" json:"user_id"`
	CreatedAt          time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt          time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt          gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

type DocumentChunk struct {
	ID         string         `gorm:"primaryKey;size:36;comment:块ID" json:"id"`
	DocumentID string         `gorm:"size:36;index;comment:文档ID" json:"documentId"`
	ChunkIndex int            `gorm:"index;comment:块索引" json:"chunkIndex"`
	ChunkType  string         `gorm:"size:50;index;comment:块类型" json:"chunkType"`
	Content    string         `gorm:"type:text;comment:块内容" json:"content"`
	VectorID   *string        `gorm:"size:36;index;comment:向量ID" json:"vectorId"`
	ParentID   *string        `gorm:"size:36;index;comment:父块ID" json:"parentId"`
	ImageInfo  string         `gorm:"type:text;comment:图像信息" json:"imageInfo"`
	IsEnabled  bool           `gorm:"default:true;comment:是否启用" json:"isEnabled"`
	CreatedAt  time.Time      `gorm:"autoCreateTime;comment:创建时间" json:"createdAt"`
	UpdatedAt  time.Time      `gorm:"autoUpdateTime;comment:更新时间" json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

type ImageInfo struct {
	URL         string `json:"url"`
	Caption     string `json:"caption"`
	OCRText     string `json:"ocrText"`
	OriginalURL string `json:"originalUrl"`
}

type JSONMap map[string]interface{}

func (j JSONMap) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

func (j *JSONMap) Scan(value interface{}) error {
	if value == nil {
		*j = make(map[string]interface{})
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		*j = make(map[string]interface{})
		return nil
	}
	return json.Unmarshal(bytes, j)
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(&Document{}, &DocumentChunk{})
}

const (
	DocumentStatusPending    = 1
	DocumentStatusProcessing = 2
	DocumentStatusCompleted  = 3
	DocumentStatusFailed     = 4
)

// Chunk types
const (
	ChunkTypeText         = "text"
	ChunkTypeImageOCR     = "image_ocr"
	ChunkTypeImageCaption = "image_caption"
	ChunkTypeAudio        = "audio_transcript"
	ChunkTypeVideo        = "video_transcript"
)

// File types
const (
	FileTypeTXT  = "txt"
	FileTypeMD   = "md"
	FileTypePDF  = "pdf"
	FileTypeDOC  = "doc"
	FileTypeDOCX = "docx"
	FileTypeXLS  = "xls"
	FileTypeXLSX = "xlsx"
	FileTypePPT  = "ppt"
	FileTypePPTX = "pptx"
	FileTypeCSV  = "csv"
	FileTypeHTML = "html"
	FileTypeJSON = "json"
	FileTypeJPG  = "jpg"
	FileTypeJPEG = "jpeg"
	FileTypePNG  = "png"
	FileTypeGIF  = "gif"
	FileTypeBMP  = "bmp"
	FileTypeWEBP = "webp"
	FileTypeMP3  = "mp3"
	FileTypeWAV  = "wav"
	FileTypeFLAC = "flac"
	FileTypeM4A  = "m4a"
	FileTypeMP4  = "mp4"
	FileTypeAVI  = "avi"
	FileTypeMOV  = "mov"
	FileTypeMKV  = "mkv"
)
