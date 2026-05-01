package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"Logos/internal/models/asr"
	"Logos/internal/models/video"
	"Logos/internal/models/vlm"
	"Logos/internal/service/ai/process/dao"
	"Logos/internal/service/ai/process/model"
	"Logos/internal/service/ai/process/parser"
)

type Config struct {
	ProcessPort      int
	VectorCollection string
}

type VectorModel interface {
	GetID() string
}

type ExtractionService interface {
	ExtractFromText(ctx context.Context, text string, taskType int32, parameters map[string]string) (entities []map[string]interface{}, relations []map[string]interface{}, triples []map[string]interface{}, summary *string, keyphrases []string, err error)
}

type VectorService interface {
	Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (VectorModel, error)
	BatchVectorize(ctx context.Context, texts []string, collectionID string, metadataList []map[string]string) ([]VectorModel, error)
}

type KnowledgeService interface {
	AddEntity(ctx context.Context, entityType, name string, properties map[string]string, description *string) (VectorModel, error)
	AddRelation(ctx context.Context, relationType, sourceId, targetId string, properties map[string]string, description *string) (VectorModel, error)
}

type ProcessService struct {
	repo              dao.ProcessRepository
	parserManager     *parser.ParserManager
	extractionService ExtractionService
	vectorService     VectorService
	knowledgeService  KnowledgeService
	config            *Config
}

func NewProcessService(
	repo dao.ProcessRepository,
	extractionService ExtractionService,
	vectorService VectorService,
	knowledgeService KnowledgeService,
	vlmModel vlm.VLM,
	asrModel asr.ASR,
	videoExtractor video.Extractor,
	config *Config,
) ProcessService {
	parserManager := parser.NewParserManager(vlmModel, asrModel, videoExtractor)

	return ProcessService{
		repo:              repo,
		parserManager:     parserManager,
		extractionService: extractionService,
		vectorService:     vectorService,
		knowledgeService:  knowledgeService,
		config:            config,
	}
}

func (s *ProcessService) ProcessFile(ctx context.Context, filename string, fileData []byte, fileURL string) (*model.Document, error) {
	doc := &model.Document{
		ID:        generateID(),
		FileName:  filename,
		FileType:  filepath.Ext(filename),
		FileURL:   fileURL,
		FileSize:  int64(len(fileData)),
		Status:    model.DocumentStatusProcessing,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	reader := bytes.NewReader(fileData)
	content, metadata, err := s.parseFile(ctx, reader, filename, doc.FileType)
	if err != nil {
		doc.Status = model.DocumentStatusFailed
		errMsg := err.Error()
		doc.ErrorMsg = &errMsg
		s.repo.CreateDocument(ctx, doc)
		return nil, err
	}

	doc.Content = content
	doc.Metadata = metadata
	doc.Status = model.DocumentStatusCompleted
	now := time.Now()
	doc.ProcessedAt = &now

	err = s.repo.WithTransaction(ctx, func(txRepo dao.ProcessRepository) error {
		if err := txRepo.CreateDocument(ctx, doc); err != nil {
			return err
		}

		chunks := s.splitContentIntoChunks(doc, content, metadata)
		for _, chunk := range chunks {
			if err := txRepo.CreateChunk(ctx, chunk); err != nil {
				return err
			}
		}

		if err := s.processVectorization(ctx, chunks); err != nil {
			return err
		}

		if err := s.processKnowledgeExtraction(ctx, doc, content); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return doc, nil
}

func (s *ProcessService) ProcessURL(ctx context.Context, url string) (*model.Document, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	fileData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	filename := filepath.Base(url)
	return s.ProcessFile(ctx, filename, fileData, url)
}

func (s *ProcessService) parseFile(ctx context.Context, reader io.Reader, filename string, fileType string) (string, map[string]interface{}, error) {
	ext := filepath.Ext(filename)
	if ext != "" {
		ext = ext[1:]
	}

	if s.parserManager.HasParser(ext) {
		return s.parserManager.GetParser(ext).Parse(ctx, reader, filename)
	}

	if s.parserManager.HasParser(fileType) {
		return s.parserManager.GetParser(fileType).Parse(ctx, reader, filename)
	}

	return s.parserManager.GetParser("*").Parse(ctx, reader, filename)
}

func (s *ProcessService) ListDocuments(ctx context.Context, status *int, page, pageSize int) ([]*model.Document, int64, error) {
	return s.repo.ListDocuments(ctx, status, page, pageSize)
}

func (s *ProcessService) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	return s.repo.GetDocument(ctx, id)
}

func (s *ProcessService) GetChunks(ctx context.Context, docID string) ([]*model.DocumentChunk, error) {
	return s.repo.GetChunksByDocumentID(ctx, docID)
}

func (s *ProcessService) DeleteDocument(ctx context.Context, id string) error {
	return s.repo.DeleteDocument(ctx, id)
}

func (s *ProcessService) ProcessDocument(ctx context.Context, id string) error {
	return s.ReprocessDocument(ctx, id)
}

func (s *ProcessService) ReprocessDocument(ctx context.Context, id string) error {
	_, err := s.GetDocument(ctx, id)
	if err != nil {
		return err
	}

	return nil
}

func (s *ProcessService) GetServiceURL() string {
	return fmt.Sprintf("http://localhost:%d", s.config.ProcessPort)
}

func (s *ProcessService) splitContentIntoChunks(doc *model.Document, content string, metadata map[string]interface{}) []*model.DocumentChunk {
	var chunks []*model.DocumentChunk
	chunkIndex := 0

	chunkSize := 1000
	chunkOverlap := 100

	lines := strings.Split(content, "\n")
	var currentChunk strings.Builder

	for _, line := range lines {
		if currentChunk.Len()+len(line) > chunkSize && currentChunk.Len() > 0 {
			chunk := &model.DocumentChunk{
				ID:         generateID(),
				DocumentID: doc.ID,
				ChunkIndex: chunkIndex,
				ChunkType:  model.ChunkTypeText,
				Content:    currentChunk.String(),
				IsEnabled:  true,
			}
			chunks = append(chunks, chunk)
			chunkIndex++

			overlap := currentChunk.String()
			if len(overlap) > chunkOverlap {
				overlap = overlap[len(overlap)-chunkOverlap:]
			}
			currentChunk.Reset()
			currentChunk.WriteString(overlap)
		}

		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
	}

	if currentChunk.Len() > 0 {
		chunk := &model.DocumentChunk{
			ID:         generateID(),
			DocumentID: doc.ID,
			ChunkIndex: chunkIndex,
			ChunkType:  model.ChunkTypeText,
			Content:    currentChunk.String(),
			IsEnabled:  true,
		}
		chunks = append(chunks, chunk)
	}

	if imageChunks, ok := s.extractImageChunks(doc, metadata); ok {
		chunks = append(chunks, imageChunks...)
	}

	return chunks
}

func (s *ProcessService) extractImageChunks(doc *model.Document, metadata map[string]interface{}) ([]*model.DocumentChunk, bool) {
	var chunks []*model.DocumentChunk

	if imagesVal, ok := metadata["images"]; ok {
		if images, ok := imagesVal.([]map[string]interface{}); ok {
			for _, img := range images {
				if caption, ok := img["caption"].(string); ok && caption != "" {
					imageInfo := model.ImageInfo{
						URL:         getStringFromMap(img, "url"),
						Caption:     caption,
						OCRText:     getStringFromMap(img, "ocr"),
						OriginalURL: getStringFromMap(img, "original_url"),
					}
					infoJSON, _ := json.Marshal(imageInfo)
					chunk := &model.DocumentChunk{
						ID:         generateID(),
						DocumentID: doc.ID,
						ChunkIndex: len(chunks),
						ChunkType:  model.ChunkTypeImageCaption,
						Content:    caption,
						ImageInfo:  string(infoJSON),
						IsEnabled:  true,
					}
					chunks = append(chunks, chunk)
				}

				if ocr, ok := img["ocr"].(string); ok && ocr != "" {
					imageInfo := model.ImageInfo{
						URL:         getStringFromMap(img, "url"),
						Caption:     getStringFromMap(img, "caption"),
						OCRText:     ocr,
						OriginalURL: getStringFromMap(img, "original_url"),
					}
					infoJSON, _ := json.Marshal(imageInfo)
					chunk := &model.DocumentChunk{
						ID:         generateID(),
						DocumentID: doc.ID,
						ChunkIndex: len(chunks),
						ChunkType:  model.ChunkTypeImageOCR,
						Content:    ocr,
						ImageInfo:  string(infoJSON),
						IsEnabled:  true,
					}
					chunks = append(chunks, chunk)
				}
			}
		}
	}

	return chunks, len(chunks) > 0
}

func (s *ProcessService) processVectorization(ctx context.Context, chunks []*model.DocumentChunk) error {
	if s.vectorService == nil {
		return nil
	}

	collectionID := "documents"
	if s.config.VectorCollection != "" {
		collectionID = s.config.VectorCollection
	}

	for i, chunk := range chunks {
		metadata := map[string]string{
			"document_id": chunk.DocumentID,
			"chunk_id":    chunk.ID,
			"chunk_type":  chunk.ChunkType,
		}

		vector, err := s.vectorService.Vectorize(ctx, chunk.Content, collectionID, metadata)
		if err != nil {
			continue
		}

		if vector != nil {
			id := vector.GetID()
			chunks[i].VectorID = &id
		}
	}

	return nil
}

func (s *ProcessService) processKnowledgeExtraction(ctx context.Context, doc *model.Document, content string) error {
	if s.extractionService == nil || s.knowledgeService == nil {
		return nil
	}

	entities, _, triples, _, _, err := s.extractionService.ExtractFromText(
		ctx,
		content,
		3, // Triple extraction
		map[string]string{},
	)
	if err != nil {
		return err
	}

	for _, entity := range entities {
		entityType, _ := entity["type"].(string)
		name, _ := entity["text"].(string)
		props := make(map[string]string)
		for k, v := range entity {
			if strV, ok := v.(string); ok && k != "type" && k != "text" && k != "confidence" {
				props[k] = strV
			}
		}

		if entityType == "" {
			entityType = "ENTITY"
		}
		if name == "" {
			continue
		}

		desc := fmt.Sprintf("从文档 %s 提取", doc.FileName)
		s.knowledgeService.AddEntity(ctx, entityType, name, props, &desc)
	}

	for _, triple := range triples {
		subject, _ := triple["subject"].(string)
		predicate, _ := triple["predicate"].(string)
		obj, _ := triple["object"].(string)

		if subject == "" || obj == "" || predicate == "" {
			continue
		}

		srcEnt, _ := s.knowledgeService.AddEntity(ctx, "ENTITY", subject, nil, nil)
		tgtEnt, _ := s.knowledgeService.AddEntity(ctx, "ENTITY", obj, nil, nil)

		srcID := ""
		tgtID := ""
		if srcEnt != nil {
			srcID = srcEnt.GetID()
		}
		if tgtEnt != nil {
			tgtID = tgtEnt.GetID()
		}

		if srcID != "" && tgtID != "" {
			s.knowledgeService.AddRelation(ctx, predicate, srcID, tgtID, nil, nil)
		}
	}

	return nil
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if str, ok := v.(string); ok {
			return str
		}
	}
	return ""
}

func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
