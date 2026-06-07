package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"Logos/internal/models/asr"
	"Logos/internal/models/video"
	"Logos/internal/models/vlm"
	knowledgeModel "Logos/internal/service/ai/knowledge/model"
	"Logos/internal/service/ai/process/dao"
	"Logos/internal/service/ai/process/model"
	"Logos/internal/service/ai/process/parser"
	"Logos/pkg/auth"
	"Logos/pkg/logger"

	"github.com/google/uuid"
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
	GetCollectionInfo(ctx context.Context, id string) (*CollectionInfo, error)
}

type CollectionInfo struct {
	ID         string
	VLMModel   string
	VLMBaseURL string
	VLMApiKey  string
	LLMModel   string
	LLMBaseURL string
	LLMApiKey  string
}

type KnowledgeService interface {
	AddEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*knowledgeModel.Entity, error)
	FindOrCreateEntity(ctx context.Context, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*knowledgeModel.Entity, error)
	UpdateEntity(ctx context.Context, id, entityType, name, collectionID string, properties map[string]string, description *string, color string) (*knowledgeModel.Entity, error)
	DeleteEntity(ctx context.Context, id string) error
	GetEntity(ctx context.Context, id string) (*knowledgeModel.Entity, error)
	QueryEntities(ctx context.Context, entityType, name, collectionID string, properties map[string]string, page, pageSize int) ([]*knowledgeModel.Entity, int64, error)
	SearchEntities(ctx context.Context, keyword string, entityType *string, collectionID string, page, pageSize int) ([]*knowledgeModel.Entity, int64, error)
	AddRelation(ctx context.Context, relationType, sourceId, targetId, collectionID string, properties map[string]string, description *string) (*knowledgeModel.Relation, error)
	UpdateRelation(ctx context.Context, id, relationType, sourceId, targetId string, properties map[string]string, description *string) (*knowledgeModel.Relation, error)
	DeleteRelation(ctx context.Context, id string) error
	GetRelation(ctx context.Context, id string) (*knowledgeModel.Relation, error)
	QueryRelations(ctx context.Context, relationType, sourceId, targetId, collectionID string, page, pageSize int) ([]*knowledgeModel.Relation, int64, error)
	GetGraphStats(ctx context.Context, collectionID string) (*knowledgeModel.GraphStats, error)
	GetRelatedEntities(ctx context.Context, entityId, relationType string) ([]*knowledgeModel.Entity, error)
	GetSubgraph(ctx context.Context, entityID string, depth int, collectionID string) (*knowledgeModel.Subgraph, error)
	GetEntityPaths(ctx context.Context, sourceID, targetID string, maxDepth int, collectionID string) ([]*knowledgeModel.EntityPath, error)
	ImportData(ctx context.Context, dataType string, data []string) error
	WithTransaction(ctx context.Context, fn func(txService KnowledgeService) error) error
}

type ProcessService struct {
	repo              dao.ProcessRepository
	parserManager     *parser.ParserManager
	extractionService ExtractionService
	vectorService     VectorService
	knowledgeService  KnowledgeService
	config            *Config
	vlmModel          vlm.VLM
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
		vlmModel:          vlmModel,
	}
}

func (s *ProcessService) ProcessFile(ctx context.Context, filename string, fileData []byte, fileURL string, collectionID string) (*model.Document, error) {
	doc := &model.Document{
		ID:                 generateID(),
		VectorCollectionID: collectionID,
		FileName:           filename,
		FileType:           filepath.Ext(filename),
		FileURL:            fileURL,
		FileSize:           int64(len(fileData)),
		Status:             model.DocumentStatusPending,
		UserID:             getUserIDFromContext(ctx),
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.repo.CreateDocument(ctx, doc, getUserIDFromContext(ctx)); err != nil {
		return nil, fmt.Errorf("创建文档记录失败: %w", err)
	}

	go s.processDocumentAsync(doc.ID, bytes.NewReader(fileData), int64(len(fileData)), filename)

	return doc, nil
}

func (s *ProcessService) ProcessFileFromPath(ctx context.Context, filename string, filePath string, fileSize int64, fileURL string, collectionID string) (*model.Document, error) {
	doc := &model.Document{
		ID:                 generateID(),
		VectorCollectionID: collectionID,
		FileName:           filename,
		FileType:           filepath.Ext(filename),
		FileURL:            fileURL,
		FileSize:           fileSize,
		Status:             model.DocumentStatusPending,
		UserID:             getUserIDFromContext(ctx),
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.repo.CreateDocument(ctx, doc, getUserIDFromContext(ctx)); err != nil {
		return nil, fmt.Errorf("创建文档记录失败: %w", err)
	}

	go s.processDocumentAsyncFromPath(doc.ID, filePath, fileSize, filename)

	return doc, nil
}

func (s *ProcessService) ProcessURL(ctx context.Context, url string, collectionID string) (*model.Document, error) {
	doc := &model.Document{
		ID:                 generateID(),
		VectorCollectionID: collectionID,
		FileName:           filepath.Base(url),
		FileType:           filepath.Ext(url),
		FileURL:            url,
		Status:             model.DocumentStatusPending,
		UserID:             getUserIDFromContext(ctx),
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.repo.CreateDocument(ctx, doc, getUserIDFromContext(ctx)); err != nil {
		return nil, fmt.Errorf("创建文档记录失败: %w", err)
	}

	go s.processURLAsync(doc.ID, url)

	return doc, nil
}

func (s *ProcessService) processURLAsync(docID string, rawURL string) {
	ctx := context.Background()

	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("下载URL失败: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.markFailed(ctx, docID, fmt.Sprintf("下载URL失败: HTTP %d", resp.StatusCode))
		return
	}

	fileData, err := io.ReadAll(resp.Body)
	if err != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("读取URL内容失败: %v", err))
		return
	}

	contentType := resp.Header.Get("Content-Type")
	filename := determineFilename(rawURL, contentType)

	s.processDocumentAsync(docID, bytes.NewReader(fileData), int64(len(fileData)), filename)
}

func determineFilename(rawURL string, contentType string) string {
	base := filepath.Base(rawURL)
	ext := filepath.Ext(base)

	if ext != "" {
		return base
	}

	if strings.Contains(contentType, "text/html") {
		return "url_page.crawl"
	}
	if strings.Contains(contentType, "application/json") {
		return "url_data.json"
	}
	if strings.Contains(contentType, "text/xml") || strings.Contains(contentType, "application/xml") {
		return "url_data.xml"
	}
	if strings.Contains(contentType, "text/plain") {
		return "url_page.txt"
	}
	if strings.Contains(contentType, "text/markdown") {
		return "url_page.md"
	}
	if strings.Contains(contentType, "application/pdf") {
		return "url_doc.pdf"
	}
	return "url_content.crawl"
}

func (s *ProcessService) processDocumentAsyncFromPath(docID string, filePath string, fileSize int64, filename string) {
	f, err := os.Open(filePath)
	if err != nil {
		s.markFailed(context.Background(), docID, fmt.Sprintf("打开文件失败: %v", err))
		return
	}
	defer f.Close()
	s.processDocumentAsync(docID, f, fileSize, filename)
}

func (s *ProcessService) processDocumentAsync(docID string, reader io.Reader, fileSize int64, filename string) {
	ctx := context.Background()

	doc, err := s.repo.GetDocument(ctx, docID)
	if err != nil || doc == nil {
		logger.Error("异步处理：文档不存在", logger.StringField("doc_id", docID))
		return
	}

	doc.Status = model.DocumentStatusProcessing
	doc.UpdatedAt = time.Now()
	s.repo.UpdateDocument(ctx, doc)
	logger.Info("开始解析文档", logger.StringField("doc_id", docID), logger.StringField("filename", filename))

	if s.vectorService != nil && doc.VectorCollectionID != "" {
		colInfo, colErr := s.vectorService.GetCollectionInfo(ctx, doc.VectorCollectionID)
		if colErr == nil && colInfo.VLMModel != "" && colInfo.VLMBaseURL != "" {
			dynamicVLM, vlmErr := vlm.NewRemoteAPIVLM(&vlm.Config{
				Source:    "remote",
				ModelName: colInfo.VLMModel,
				BaseURL:   colInfo.VLMBaseURL,
				APIKey:    colInfo.VLMApiKey,
			})
			if vlmErr == nil {
				logger.Info("使用集合VLM配置", logger.StringField("collection_id", doc.VectorCollectionID), logger.StringField("vlm_model", colInfo.VLMModel))
				s.parserManager.SetVLMModel(dynamicVLM)
				defer s.parserManager.SetVLMModel(s.vlmModel)
			} else {
				logger.Warn("创建集合VLM失败，使用默认VLM", logger.StringField("collection_id", doc.VectorCollectionID), logger.ErrorField(vlmErr))
			}
		}
	}

	content, metadata, err := s.parseFile(ctx, reader, filename, doc.FileType)
	if err != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("解析文档失败: %v", err))
		return
	}

	content = strings.ToValidUTF8(content, "")
	doc.Content = content
	doc.Metadata = metadata
	doc.UpdatedAt = time.Now()
	s.repo.UpdateDocument(ctx, doc)
	logger.Info("文档解析完成", logger.StringField("doc_id", docID), logger.IntField("content_len", len(content)))

	chunks := s.splitContentIntoChunks(doc, content, metadata)
	logger.Info("文档分块完成", logger.StringField("doc_id", docID), logger.IntField("chunk_count", len(chunks)))

	batchSize := 100
	for batchStart := 0; batchStart < len(chunks); batchStart += batchSize {
		batchEnd := batchStart + batchSize
		if batchEnd > len(chunks) {
			batchEnd = len(chunks)
		}
		batch := chunks[batchStart:batchEnd]

		err = s.repo.WithTransaction(ctx, func(txRepo dao.ProcessRepository) error {
			for _, chunk := range batch {
				if err := txRepo.CreateChunk(ctx, chunk); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			s.markFailed(ctx, docID, fmt.Sprintf("保存分块失败: %v", err))
			return
		}
		logger.Info("分块批次保存完成",
			logger.StringField("doc_id", docID),
			logger.IntField("batch_start", batchStart),
			logger.IntField("batch_end", batchEnd))
	}

	logger.Info("开始并行处理",
		logger.StringField("doc_id", docID),
		logger.BoolField("vector_enabled", s.vectorService != nil),
		logger.BoolField("has_collection", doc.VectorCollectionID != ""),
		logger.StringField("collection_id", doc.VectorCollectionID))

	var wg sync.WaitGroup

	if s.vectorService != nil && doc.VectorCollectionID != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("开始向量化", logger.StringField("doc_id", docID), logger.StringField("collection_id", doc.VectorCollectionID))
			vecCtx, vecCancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer vecCancel()
			if err := s.processVectorization(vecCtx, doc, chunks); err != nil {
				logger.Warn("向量化失败（不影响文档状态）", logger.StringField("doc_id", docID), logger.ErrorField(err))
			} else {
				logger.Info("向量化完成", logger.StringField("doc_id", docID))
			}
		}()
	}

	if s.extractionService != nil && s.knowledgeService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("开始知识提取", logger.StringField("doc_id", docID))
			extCtx, extCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer extCancel()
			if err := s.processKnowledgeExtraction(extCtx, doc, content); err != nil {
				logger.Warn("知识提取失败（不影响文档状态）", logger.StringField("doc_id", docID), logger.ErrorField(err))
			} else {
				logger.Info("知识提取完成", logger.StringField("doc_id", docID))
			}
		}()
	}

	wg.Wait()

	doc.Status = model.DocumentStatusCompleted
	now := time.Now()
	doc.ProcessedAt = &now
	doc.UpdatedAt = now
	s.repo.UpdateDocument(ctx, doc)
	logger.Info("文档处理完成", logger.StringField("doc_id", docID))
}

func (s *ProcessService) markFailed(ctx context.Context, docID string, errMsg string) {
	doc, err := s.repo.GetDocument(ctx, docID)
	if err != nil || doc == nil {
		return
	}
	doc.Status = model.DocumentStatusFailed
	doc.ErrorMsg = &errMsg
	doc.UpdatedAt = time.Now()
	s.repo.UpdateDocument(ctx, doc)
	logger.Error("文档处理失败", logger.StringField("doc_id", docID), logger.StringField("error", errMsg))
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
	return s.repo.ListDocuments(ctx, getUserIDFromContext(ctx), status, page, pageSize)
}

func (s *ProcessService) ListDocumentDTOs(ctx context.Context, status *int, page, pageSize int) ([]*model.DocumentDTO, int64, error) {
	docs, total, err := s.repo.ListDocuments(ctx, getUserIDFromContext(ctx), status, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	docIDs := make([]string, len(docs))
	for i, doc := range docs {
		docIDs[i] = doc.ID
	}

	chunkCounts, err := s.repo.GetChunkCountsByDocumentIDs(ctx, docIDs)
	if err != nil {
		chunkCounts = make(map[string]int64)
	}

	result := make([]*model.DocumentDTO, len(docs))
	for i, doc := range docs {
		count := chunkCounts[doc.ID]
		result[i] = model.ToDocumentDTO(doc, int(count))
	}

	return result, total, nil
}

func (s *ProcessService) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	return s.repo.GetDocumentWithOwnerCheck(ctx, id, getUserIDFromContext(ctx))
}

func (s *ProcessService) GetDocumentDTO(ctx context.Context, id string) (*model.DocumentDTO, error) {
	doc, err := s.repo.GetDocumentWithOwnerCheck(ctx, id, getUserIDFromContext(ctx))
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, nil
	}

	count, err := s.repo.GetChunkCountByDocumentID(ctx, id)
	if err != nil {
		count = 0
	}

	return model.ToDocumentDTO(doc, int(count)), nil
}

func (s *ProcessService) GetChunks(ctx context.Context, docID string) ([]*model.DocumentChunk, error) {
	return s.repo.GetChunksByDocumentID(ctx, docID)
}

func (s *ProcessService) DeleteDocument(ctx context.Context, id string) error {
	return s.repo.DeleteDocument(ctx, id, getUserIDFromContext(ctx))
}

func (s *ProcessService) ProcessDocument(ctx context.Context, id string) error {
	return s.ReprocessDocument(ctx, id)
}

func (s *ProcessService) ReprocessDocument(ctx context.Context, id string) error {
	doc, err := s.GetDocument(ctx, id)
	if err != nil {
		return fmt.Errorf("获取文档失败: %w", err)
	}
	if doc == nil {
		return fmt.Errorf("文档不存在: %s", id)
	}

	doc.Status = model.DocumentStatusProcessing
	doc.UpdatedAt = time.Now()
	doc.ErrorMsg = nil
	if err := s.repo.UpdateDocument(ctx, doc); err != nil {
		return fmt.Errorf("更新文档状态失败: %w", err)
	}

	go s.reprocessAsync(id)

	return nil
}

func (s *ProcessService) reprocessAsync(docID string) {
	ctx := context.Background()

	doc, err := s.repo.GetDocument(ctx, docID)
	if err != nil || doc == nil {
		return
	}

	if doc.Content == "" {
		s.markFailed(ctx, docID, "文档内容为空，无法重新处理")
		return
	}

	if s.vectorService != nil && doc.VectorCollectionID != "" {
		colInfo, colErr := s.vectorService.GetCollectionInfo(ctx, doc.VectorCollectionID)
		if colErr == nil && colInfo.VLMModel != "" && colInfo.VLMBaseURL != "" {
			dynamicVLM, vlmErr := vlm.NewRemoteAPIVLM(&vlm.Config{
				Source:    "remote",
				ModelName: colInfo.VLMModel,
				BaseURL:   colInfo.VLMBaseURL,
				APIKey:    colInfo.VLMApiKey,
			})
			if vlmErr == nil {
				logger.Info("重处理：使用集合VLM配置", logger.StringField("collection_id", doc.VectorCollectionID), logger.StringField("vlm_model", colInfo.VLMModel))
				s.parserManager.SetVLMModel(dynamicVLM)
				defer s.parserManager.SetVLMModel(s.vlmModel)
			} else {
				logger.Warn("重处理：创建集合VLM失败，使用默认VLM", logger.StringField("collection_id", doc.VectorCollectionID), logger.ErrorField(vlmErr))
			}
		}
	}

	doc.Content = strings.ToValidUTF8(doc.Content, "")

	if err := s.repo.DeleteChunksByDocumentID(ctx, docID); err != nil {
		logger.Warn("删除旧chunks失败", logger.StringField("doc_id", docID), logger.ErrorField(err))
	}

	chunks := s.splitContentIntoChunks(doc, doc.Content, doc.Metadata)
	logger.Info("重新分块完成", logger.StringField("doc_id", docID), logger.IntField("chunk_count", len(chunks)))

	err = s.repo.WithTransaction(ctx, func(txRepo dao.ProcessRepository) error {
		for _, chunk := range chunks {
			if err := txRepo.CreateChunk(ctx, chunk); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("保存分块失败: %v", err))
		return
	}

	var wg sync.WaitGroup

	if s.vectorService != nil && doc.VectorCollectionID != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vecCtx, vecCancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer vecCancel()
			if err := s.processVectorization(vecCtx, doc, chunks); err != nil {
				logger.Warn("向量化失败（不影响文档状态）", logger.StringField("doc_id", docID), logger.ErrorField(err))
			}
		}()
	}

	if s.extractionService != nil && s.knowledgeService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			extCtx, extCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer extCancel()
			if err := s.processKnowledgeExtraction(extCtx, doc, doc.Content); err != nil {
				logger.Warn("知识提取失败（不影响文档状态）", logger.StringField("doc_id", docID), logger.ErrorField(err))
			}
		}()
	}

	wg.Wait()

	doc.Status = model.DocumentStatusCompleted
	now := time.Now()
	doc.ProcessedAt = &now
	doc.UpdatedAt = now
	s.repo.UpdateDocument(ctx, doc)
	logger.Info("文档重新处理完成", logger.StringField("doc_id", docID))
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
				Content:    strings.ToValidUTF8(currentChunk.String(), ""),
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
			Content:    strings.ToValidUTF8(currentChunk.String(), ""),
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

func (s *ProcessService) processVectorization(ctx context.Context, doc *model.Document, chunks []*model.DocumentChunk) error {
	if s.vectorService == nil || doc.VectorCollectionID == "" {
		return nil
	}

	concurrency := 10
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failedCount := 0

	for i, chunk := range chunks {
		wg.Add(1)
		sem <- struct{}{}

		go func(idx int, c *model.DocumentChunk) {
			defer wg.Done()
			defer func() { <-sem }()

			metadata := map[string]string{
				"document_id": c.DocumentID,
				"chunk_id":    c.ID,
				"chunk_type":  c.ChunkType,
			}

			vectorCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			vector, err := s.vectorService.Vectorize(vectorCtx, c.Content, doc.VectorCollectionID, metadata)
			cancel()

			if err != nil {
				logger.Warn("单个chunk向量化失败", logger.StringField("chunk_id", c.ID), logger.ErrorField(err))
				mu.Lock()
				failedCount++
				mu.Unlock()
				return
			}

			if vector != nil {
				id := vector.GetID()
				c.VectorID = &id
				if updateErr := s.repo.UpdateChunk(ctx, c); updateErr != nil {
					logger.Warn("保存chunk向量ID失败", logger.StringField("chunk_id", c.ID), logger.ErrorField(updateErr))
				}
			}
		}(i, chunk)
	}

	wg.Wait()

	if failedCount > 0 {
		logger.Warn("向量化部分失败",
			logger.StringField("doc_id", doc.ID),
			logger.IntField("failed_count", failedCount),
			logger.IntField("total_count", len(chunks)))
	}

	return nil
}

func (s *ProcessService) processKnowledgeExtraction(ctx context.Context, doc *model.Document, content string) error {
	if s.extractionService == nil || s.knowledgeService == nil {
		return nil
	}

	parameters := map[string]string{}

	if s.vectorService != nil && doc.VectorCollectionID != "" {
		colInfo, colErr := s.vectorService.GetCollectionInfo(ctx, doc.VectorCollectionID)
		if colErr == nil && colInfo.LLMModel != "" && colInfo.LLMBaseURL != "" {
			parameters["llm_model"] = colInfo.LLMModel
			parameters["llm_base_url"] = colInfo.LLMBaseURL
			parameters["llm_api_key"] = colInfo.LLMApiKey
			logger.Info("知识提取使用集合LLM配置",
				logger.StringField("collection_id", doc.VectorCollectionID),
				logger.StringField("llm_model", colInfo.LLMModel))
		}
	}

	entities, _, triples, _, _, err := s.extractionService.ExtractFromText(
		ctx,
		content,
		3,
		parameters,
	)
	if err != nil {
		return err
	}

	collectionID := doc.VectorCollectionID

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
		if _, err := s.knowledgeService.FindOrCreateEntity(ctx, entityType, name, collectionID, props, &desc, ""); err != nil {
			logger.Warn("创建实体失败",
				logger.StringField("type", entityType),
				logger.StringField("name", name),
				logger.StringField("collection_id", collectionID),
				logger.ErrorField(err))
		}
	}

	for _, triple := range triples {
		subject, _ := triple["subject"].(string)
		predicate, _ := triple["predicate"].(string)
		obj, _ := triple["object"].(string)

		if subject == "" || obj == "" || predicate == "" {
			continue
		}

		srcEnt, srcErr := s.knowledgeService.FindOrCreateEntity(ctx, "ENTITY", subject, collectionID, nil, nil, "")
		if srcErr != nil {
			logger.Warn("创建源实体失败",
				logger.StringField("subject", subject),
				logger.StringField("collection_id", collectionID),
				logger.ErrorField(srcErr))
			continue
		}
		tgtEnt, tgtErr := s.knowledgeService.FindOrCreateEntity(ctx, "ENTITY", obj, collectionID, nil, nil, "")
		if tgtErr != nil {
			logger.Warn("创建目标实体失败",
				logger.StringField("object", obj),
				logger.StringField("collection_id", collectionID),
				logger.ErrorField(tgtErr))
			continue
		}

		srcID := ""
		tgtID := ""
		if srcEnt != nil {
			srcID = srcEnt.ID
		}
		if tgtEnt != nil {
			tgtID = tgtEnt.ID
		}

		if srcID != "" && tgtID != "" {
			if _, err := s.knowledgeService.AddRelation(ctx, predicate, srcID, tgtID, collectionID, nil, nil); err != nil {
				logger.Warn("创建关系失败",
					logger.StringField("predicate", predicate),
					logger.StringField("source_id", srcID),
					logger.StringField("target_id", tgtID),
					logger.ErrorField(err))
			}
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
	return uuid.New().String()
}

func getUserIDFromContext(ctx context.Context) string {
	userID, err := auth.GetUserID(ctx)
	if err != nil {
		return ""
	}
	return userID
}
