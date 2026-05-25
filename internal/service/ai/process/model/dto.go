package model

import (
	"time"
)

type DocumentDTO struct {
	ID                 string                 `json:"id"`
	VectorCollectionID string                 `json:"vector_collection_id"`
	Name               string                 `json:"name"`
	SourceType         string                 `json:"source_type"`
	SourceURL          string                 `json:"source_url"`
	Status             string                 `json:"status"`
	ChunkCount         int                    `json:"chunk_count"`
	CreatedAt          string                 `json:"created_at"`
	UpdatedAt          string                 `json:"updated_at"`
	Content            string                 `json:"content,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
	ErrorMsg           *string                `json:"errorMsg,omitempty"`
}

func statusToString(status int) string {
	switch status {
	case DocumentStatusPending:
		return "pending"
	case DocumentStatusProcessing:
		return "processing"
	case DocumentStatusCompleted:
		return "completed"
	case DocumentStatusFailed:
		return "failed"
	default:
		return "pending"
	}
}

func ToDocumentDTO(doc *Document, chunkCount int) *DocumentDTO {
	if doc == nil {
		return nil
	}

	sourceType := doc.FileType
	if sourceType != "" && sourceType[0] == '.' {
		sourceType = sourceType[1:]
	}

	return &DocumentDTO{
		ID:                 doc.ID,
		VectorCollectionID: doc.VectorCollectionID,
		Name:               doc.FileName,
		SourceType:         sourceType,
		SourceURL:          doc.FileURL,
		Status:             statusToString(doc.Status),
		ChunkCount:         chunkCount,
		CreatedAt:          doc.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          doc.UpdatedAt.Format(time.RFC3339),
		Content:            doc.Content,
		Metadata:           doc.Metadata,
		ErrorMsg:           doc.ErrorMsg,
	}
}

func ToDocumentDTOList(docs []*Document, chunkCounts map[string]int) []*DocumentDTO {
	result := make([]*DocumentDTO, len(docs))
	for i, doc := range docs {
		count := 0
		if chunkCounts != nil {
			count = chunkCounts[doc.ID]
		}
		result[i] = ToDocumentDTO(doc, count)
	}
	return result
}
