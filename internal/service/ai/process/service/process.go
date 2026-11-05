package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	"Logos/pkg/safego"
	"Logos/pkg/safehttp"

	"github.com/google/uuid"
)

var (
	// ErrVectorizationAllFailed 表示所有 chunk 都没能生成向量。
	// 这不是「部分降级」，文档实际不可检索，不能继续标记为 Completed。
	ErrVectorizationAllFailed = errors.New("all document chunks failed to vectorize")
	// ErrUnauthenticated 表示调用方没有提供可用的用户身份。
	// 用户态接口必须 fail closed，不能把空 userID 解释成「查所有用户」。
	ErrUnauthenticated = errors.New("missing user identity")
	// ErrDocumentNotFound 表示文档不存在，或当前用户不是它的 owner。
	ErrDocumentNotFound = errors.New("document not found")
	// ErrKnowledgeExtractionAllFailed 表示抽取结果一条实体/关系都没写进去。
	// 知识提取本身允许降级，但「全部失败」必须让调用方知道图谱是空的。
	ErrKnowledgeExtractionAllFailed = errors.New("no entity or relation was written from knowledge extraction")
)

const (
	// 向量化并发度：既要把 N 次往返压成 N/10，又不能把向量服务打满。
	vectorizationConcurrency = 10
	// 单个 chunk 的向量化预算；外层还有一个更短的总预算，取两者更早的 deadline。
	vectorizeChunkTimeout = 60 * time.Second
	// 用户 URL 抓取的响应体上限：超过后直接失败，不把整个文件读进内存。
	maxURLDownloadBytes = 100 << 20
)

// vectorizationStats 用于区分「全部失败」和「部分失败」。
// 只返回 error 的话，调用方无法判断文档是完整可检索、部分可检索，还是完全不可检索。
type vectorizationStats struct {
	Total     int
	Succeeded int
	Failed    int
}

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
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	doc := &model.Document{
		ID:                 generateID(),
		VectorCollectionID: collectionID,
		FileName:           filename,
		FileType:           filepath.Ext(filename),
		FileURL:            fileURL,
		FileSize:           int64(len(fileData)),
		Status:             model.DocumentStatusPending,
		UserID:             userID,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.repo.CreateDocument(ctx, doc, userID); err != nil {
		return nil, fmt.Errorf("创建文档记录失败: %w", err)
	}

	safego.Go("process.document", func() {
		s.processDocumentAsync(doc.ID, bytes.NewReader(fileData), int64(len(fileData)), filename)
	})

	return doc, nil
}

func (s *ProcessService) ProcessFileFromPath(ctx context.Context, filename string, filePath string, fileSize int64, fileURL string, collectionID string) (*model.Document, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	doc := &model.Document{
		ID:                 generateID(),
		VectorCollectionID: collectionID,
		FileName:           filename,
		FileType:           filepath.Ext(filename),
		FileURL:            fileURL,
		FileSize:           fileSize,
		Status:             model.DocumentStatusPending,
		UserID:             userID,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.repo.CreateDocument(ctx, doc, userID); err != nil {
		return nil, fmt.Errorf("创建文档记录失败: %w", err)
	}

	safego.Go("process.document", func() {
		s.processDocumentAsyncFromPath(doc.ID, filePath, fileSize, filename)
	})

	return doc, nil
}

func (s *ProcessService) ProcessURL(ctx context.Context, url string, collectionID string) (*model.Document, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	// 先校验再建文档：非法 URL 不应该在库里留下一条注定失败的 pending 记录。
	if _, err := safehttp.ValidateURL(url); err != nil {
		return nil, fmt.Errorf("URL 不合法: %w", err)
	}

	doc := &model.Document{
		ID:                 generateID(),
		VectorCollectionID: collectionID,
		FileName:           filepath.Base(url),
		FileType:           filepath.Ext(url),
		FileURL:            url,
		Status:             model.DocumentStatusPending,
		UserID:             userID,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}

	if err := s.repo.CreateDocument(ctx, doc, userID); err != nil {
		return nil, fmt.Errorf("创建文档记录失败: %w", err)
	}

	safego.Go("process.url", func() {
		s.processURLAsync(ctx, doc.ID, url)
	})

	return doc, nil
}

func (s *ProcessService) processURLAsync(parent context.Context, docID string, rawURL string) {
	// 下载不应被客户端断开打断（否则文档永远停在 Pending），但仍要有独立上限。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Minute)
	defer cancel()

	// 用户可控 URL 必须防 SSRF：协议白名单 + DNS 解析后逐 IP 校验 + 重定向逐跳校验。
	httpClient := safehttp.NewClient(60*time.Second, 5)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("创建下载请求失败: %v", err))
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("下载URL失败: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.markFailed(ctx, docID, fmt.Sprintf("下载URL失败: HTTP %d", resp.StatusCode))
		return
	}

	if resp.ContentLength > maxURLDownloadBytes {
		s.markFailed(ctx, docID, fmt.Sprintf("文件超过下载上限: %d > %d", resp.ContentLength, maxURLDownloadBytes))
		return
	}

	// 多读 1 字节判断是否超限；chunked 响应没有 Content-Length，必须靠这里兜底。
	fileData, err := io.ReadAll(io.LimitReader(resp.Body, maxURLDownloadBytes+1))
	if err != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("读取URL内容失败: %v", err))
		return
	}
	if int64(len(fileData)) > maxURLDownloadBytes {
		s.markFailed(ctx, docID, fmt.Sprintf("文件超过下载上限 %d 字节", maxURLDownloadBytes))
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

	enrichment := s.runEnrichment(ctx, doc, chunks, content)
	if enrichment.vectorErr != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("向量化失败: %v", enrichment.vectorErr))
		return
	}
	if enrichment.partialMsg != "" {
		doc.ErrorMsg = &enrichment.partialMsg
	} else {
		doc.ErrorMsg = nil
	}

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
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, 0, err
	}
	return s.repo.ListDocuments(ctx, userID, status, page, pageSize)
}

func (s *ProcessService) ListDocumentDTOs(ctx context.Context, status *int, page, pageSize int) ([]*model.DocumentDTO, int64, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, 0, err
	}

	docs, total, err := s.repo.ListDocuments(ctx, userID, status, page, pageSize)
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
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.GetDocumentWithOwnerCheck(ctx, id, userID)
}

func (s *ProcessService) GetDocumentDTO(ctx context.Context, id string) (*model.DocumentDTO, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	doc, err := s.repo.GetDocumentWithOwnerCheck(ctx, id, userID)
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
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	doc, err := s.repo.GetDocumentWithOwnerCheck(ctx, docID, userID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	return s.repo.GetChunksByDocumentID(ctx, docID)
}

func (s *ProcessService) DeleteDocument(ctx context.Context, id string) error {
	userID, err := requireUserID(ctx)
	if err != nil {
		return err
	}
	return s.repo.DeleteDocument(ctx, id, userID)
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

	safego.Go("process.reprocess", func() {
		s.reprocessAsync(id)
	})

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

	enrichment := s.runEnrichment(ctx, doc, chunks, doc.Content)
	if enrichment.vectorErr != nil {
		s.markFailed(ctx, docID, fmt.Sprintf("向量化失败: %v", enrichment.vectorErr))
		return
	}
	if enrichment.partialMsg != "" {
		doc.ErrorMsg = &enrichment.partialMsg
	} else {
		doc.ErrorMsg = nil
	}

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

	for i, text := range SplitTextIntoChunks(content, defaultChunkSize, defaultChunkOverlap) {
		chunks = append(chunks, &model.DocumentChunk{
			ID:         generateID(),
			DocumentID: doc.ID,
			ChunkIndex: i,
			ChunkType:  model.ChunkTypeText,
			Content:    strings.ToValidUTF8(text, ""),
			IsEnabled:  true,
		})
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

type enrichmentResult struct {
	vectorErr  error
	partialMsg string
}

// runEnrichment 并行执行「向量化」与「知识提取」两条旁路。
//
// 两条旁路的失败语义不同：知识提取失败可以降级（关键词检索仍在），
// 向量化全部失败则文档在向量库里不可检索，必须让调用方把状态标为 Failed。
func (s *ProcessService) runEnrichment(ctx context.Context, doc *model.Document, chunks []*model.DocumentChunk, content string) enrichmentResult {
	var (
		wg     sync.WaitGroup
		result enrichmentResult
	)

	// 向量化/知识提取是文档落库后的收尾工作：客户端断开不应该把它们打断，
	// 否则文档会永远停在 Processing。用 WithoutCancel 保留上下文值、脱离请求取消，
	// 再由各自的超时兜住最坏执行时间。
	baseCtx := context.WithoutCancel(ctx)

	if s.vectorService != nil && doc.VectorCollectionID != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("开始向量化",
				logger.StringField("doc_id", doc.ID),
				logger.StringField("collection_id", doc.VectorCollectionID))

			vecCtx, cancel := context.WithTimeout(baseCtx, 3*time.Minute)
			defer cancel()

			stats, err := s.processVectorization(vecCtx, doc, chunks)
			switch {
			case err != nil:
				result.vectorErr = err
			case stats.Failed > 0:
				result.partialMsg = fmt.Sprintf("向量化部分失败: %d/%d 个分块未生成向量", stats.Failed, stats.Total)
				logger.Warn("向量化部分失败",
					logger.StringField("doc_id", doc.ID),
					logger.IntField("failed", stats.Failed),
					logger.IntField("total", stats.Total))
			default:
				logger.Info("向量化完成", logger.StringField("doc_id", doc.ID))
			}
		}()
	}

	if s.extractionService != nil && s.knowledgeService != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("开始知识提取", logger.StringField("doc_id", doc.ID))

			extCtx, cancel := context.WithTimeout(baseCtx, 30*time.Second)
			defer cancel()

			if err := s.processKnowledgeExtraction(extCtx, doc, content); err != nil {
				logger.Warn("知识提取失败（不影响文档状态）",
					logger.StringField("doc_id", doc.ID),
					logger.ErrorField(err))
			} else {
				logger.Info("知识提取完成", logger.StringField("doc_id", doc.ID))
			}
		}()
	}

	wg.Wait()
	return result
}

func (s *ProcessService) processVectorization(ctx context.Context, doc *model.Document, chunks []*model.DocumentChunk) (vectorizationStats, error) {
	if s.vectorService == nil || doc.VectorCollectionID == "" {
		return vectorizationStats{}, nil
	}

	stats := vectorizationStats{Total: len(chunks)}
	if len(chunks) == 0 {
		return stats, nil
	}

	sem := make(chan struct{}, vectorizationConcurrency)
	var wg sync.WaitGroup
	var succeeded atomic.Int64
	var failed atomic.Int64
	var firstErr error
	var errOnce sync.Once

loop:
	for _, chunk := range chunks {
		// 外层 ctx 已取消时不再发起新请求：排队中的 chunk 不应该继续占用配额。
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			failed.Add(1)
			errOnce.Do(func() { firstErr = ctx.Err() })
			break loop
		}

		wg.Add(1)

		go func(c *model.DocumentChunk) {
			defer wg.Done()
			defer func() { <-sem }()

			metadata := map[string]string{
				"document_id": c.DocumentID,
				"chunk_id":    c.ID,
				"chunk_type":  c.ChunkType,
			}

			// 继承外层 ctx，而不是 context.Background()：
			// 否则外层 3 分钟的总预算形同虚设，每个 chunk 都能跑满自己的 5 分钟。
			vectorCtx, cancel := context.WithTimeout(ctx, vectorizeChunkTimeout)
			vector, err := s.vectorService.Vectorize(vectorCtx, c.Content, doc.VectorCollectionID, metadata)
			cancel()

			if err != nil {
				logger.Warn("单个chunk向量化失败", logger.StringField("chunk_id", c.ID), logger.ErrorField(err))
				failed.Add(1)
				errOnce.Do(func() { firstErr = err })
				return
			}

			if vector == nil || vector.GetID() == "" {
				failed.Add(1)
				errOnce.Do(func() { firstErr = fmt.Errorf("vector service returned empty vector") })
				return
			}

			id := vector.GetID()
			c.VectorID = &id
			if updateErr := s.repo.UpdateChunk(ctx, c); updateErr != nil {
				logger.Warn("保存chunk向量ID失败", logger.StringField("chunk_id", c.ID), logger.ErrorField(updateErr))
				failed.Add(1)
				errOnce.Do(func() { firstErr = updateErr })
				return
			}
			succeeded.Add(1)
		}(chunk)
	}

	wg.Wait()

	stats.Succeeded = int(succeeded.Load())
	stats.Failed = int(failed.Load())

	// 调用方预算耗尽属于硬失败：即使之前有部分成功，也不能把整篇文档当成正常完成。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stats, fmt.Errorf("vectorization canceled after %d/%d chunks: %w", stats.Succeeded, stats.Total, ctxErr)
	}

	if stats.Failed > 0 {
		logger.Warn("向量化部分失败",
			logger.StringField("doc_id", doc.ID),
			logger.IntField("failed_count", stats.Failed),
			logger.IntField("success_count", stats.Succeeded),
			logger.IntField("total_count", stats.Total))
	}

	// 一个成功的都没有，文档在向量库里就是不可检索的。
	// 返回错误让上层标记 Failed，而不是把「空壳文档」标成 Completed。
	if stats.Succeeded == 0 {
		if firstErr == nil {
			firstErr = ErrVectorizationAllFailed
		}
		return stats, fmt.Errorf("%w: %d/%d failed: %v",
			ErrVectorizationAllFailed, stats.Failed, stats.Total, firstErr)
	}

	return stats, nil
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
	stats := knowledgeExtractionStats{}

	for i, entity := range entities {
		entityType, name, props, droppedProps, parseErr := parseExtractedEntity(entity)
		if parseErr != nil {
			stats.InvalidEntities++
			logger.Warn("跳过无法解析的实体",
				logger.StringField("doc_id", doc.ID),
				logger.IntField("index", i),
				logger.StringField("reason", parseErr.Error()))
			continue
		}
		stats.AttemptedEntities++
		if droppedProps > 0 {
			logger.Warn("实体属性含非字符串值，已忽略",
				logger.StringField("doc_id", doc.ID),
				logger.StringField("name", name),
				logger.IntField("dropped_properties", droppedProps))
		}

		desc := fmt.Sprintf("从文档 %s 提取", doc.FileName)
		if _, err := s.knowledgeService.FindOrCreateEntity(ctx, entityType, name, collectionID, props, &desc, ""); err != nil {
			stats.FailedEntities++
			logger.Error("创建实体失败",
				logger.StringField("doc_id", doc.ID),
				logger.StringField("type", entityType),
				logger.StringField("name", name),
				logger.StringField("collection_id", collectionID),
				logger.ErrorField(err))
			continue
		}
		stats.CreatedEntities++
	}

	for i, triple := range triples {
		subject, predicate, obj, parseErr := parseExtractedTriple(triple)
		if parseErr != nil {
			stats.InvalidRelations++
			logger.Warn("跳过无法解析的关系",
				logger.StringField("doc_id", doc.ID),
				logger.IntField("index", i),
				logger.StringField("reason", parseErr.Error()))
			continue
		}
		stats.AttemptedRelations++

		srcEnt, srcErr := s.knowledgeService.FindOrCreateEntity(ctx, "ENTITY", subject, collectionID, nil, nil, "")
		if srcErr != nil {
			stats.FailedRelations++
			logger.Error("创建源实体失败",
				logger.StringField("doc_id", doc.ID),
				logger.StringField("subject", subject),
				logger.StringField("collection_id", collectionID),
				logger.ErrorField(srcErr))
			continue
		}
		tgtEnt, tgtErr := s.knowledgeService.FindOrCreateEntity(ctx, "ENTITY", obj, collectionID, nil, nil, "")
		if tgtErr != nil {
			stats.FailedRelations++
			logger.Error("创建目标实体失败",
				logger.StringField("doc_id", doc.ID),
				logger.StringField("object", obj),
				logger.StringField("collection_id", collectionID),
				logger.ErrorField(tgtErr))
			continue
		}

		if srcEnt == nil || srcEnt.ID == "" || tgtEnt == nil || tgtEnt.ID == "" {
			stats.FailedRelations++
			logger.Error("关系端点缺少 ID，跳过该关系",
				logger.StringField("doc_id", doc.ID),
				logger.StringField("predicate", predicate),
				logger.StringField("subject", subject),
				logger.StringField("object", obj))
			continue
		}

		if _, err := s.knowledgeService.AddRelation(ctx, predicate, srcEnt.ID, tgtEnt.ID, collectionID, nil, nil); err != nil {
			stats.FailedRelations++
			logger.Error("创建关系失败",
				logger.StringField("doc_id", doc.ID),
				logger.StringField("predicate", predicate),
				logger.StringField("source_id", srcEnt.ID),
				logger.StringField("target_id", tgtEnt.ID),
				logger.ErrorField(err))
			continue
		}
		stats.CreatedRelations++
	}

	considered := stats.AttemptedEntities + stats.InvalidEntities + stats.AttemptedRelations + stats.InvalidRelations
	if considered == 0 {
		logger.Info("知识提取未返回实体或关系", logger.StringField("doc_id", doc.ID))
		return nil
	}

	if stats.CreatedEntities+stats.CreatedRelations == 0 {
		return fmt.Errorf("%w: 实体 %d/%d 失败、关系 %d/%d 失败、非法实体 %d、非法关系 %d",
			ErrKnowledgeExtractionAllFailed,
			stats.FailedEntities, stats.AttemptedEntities,
			stats.FailedRelations, stats.AttemptedRelations,
			stats.InvalidEntities, stats.InvalidRelations)
	}

	if stats.FailedEntities+stats.InvalidEntities+stats.FailedRelations+stats.InvalidRelations > 0 {
		logger.Warn("知识提取部分失败",
			logger.StringField("doc_id", doc.ID),
			logger.IntField("created_entities", stats.CreatedEntities),
			logger.IntField("failed_entities", stats.FailedEntities),
			logger.IntField("invalid_entities", stats.InvalidEntities),
			logger.IntField("created_relations", stats.CreatedRelations),
			logger.IntField("failed_relations", stats.FailedRelations),
			logger.IntField("invalid_relations", stats.InvalidRelations))
	}

	return nil
}

type knowledgeExtractionStats struct {
	AttemptedEntities  int
	CreatedEntities    int
	FailedEntities     int
	InvalidEntities    int
	AttemptedRelations int
	CreatedRelations   int
	FailedRelations    int
	InvalidRelations   int
}

// parseExtractedEntity 把模型返回的 map 收敛成强类型字段。
// 以前用 `entity["text"].(string)` 双返回值直接丢弃 ok，缺字段与非字符串
// 都会被静默跳过；现在把原因返回给调用方记录，非法数据仍然不写库。
func parseExtractedEntity(raw map[string]interface{}) (entityType, name string, props map[string]string, droppedProps int, err error) {
	rawName, exists := raw["text"]
	if !exists {
		return "", "", nil, 0, errors.New("缺少 text 字段")
	}
	name, ok := rawName.(string)
	if !ok {
		return "", "", nil, 0, fmt.Errorf("text 字段不是字符串（%T）", rawName)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", nil, 0, errors.New("text 字段为空")
	}

	entityType = "ENTITY"
	if rawType, exists := raw["type"]; exists && rawType != nil {
		typeStr, ok := rawType.(string)
		if !ok {
			return "", "", nil, 0, fmt.Errorf("type 字段不是字符串（%T）", rawType)
		}
		if trimmed := strings.TrimSpace(typeStr); trimmed != "" {
			entityType = trimmed
		}
	}

	props = make(map[string]string)
	for k, v := range raw {
		if k == "type" || k == "text" || k == "confidence" {
			continue
		}
		if strV, ok := v.(string); ok {
			props[k] = strV
		} else {
			droppedProps++
		}
	}
	return entityType, name, props, droppedProps, nil
}

// parseExtractedTriple 校验三元组的三个端点；缺字段、类型错误、空串都返回原因，
// 避免整条关系被 `continue` 掉却没有任何日志。
func parseExtractedTriple(raw map[string]interface{}) (subject, predicate, object string, err error) {
	read := func(key string) (string, error) {
		value, exists := raw[key]
		if !exists {
			return "", fmt.Errorf("缺少 %s 字段", key)
		}
		str, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("%s 字段不是字符串（%T）", key, value)
		}
		str = strings.TrimSpace(str)
		if str == "" {
			return "", fmt.Errorf("%s 字段为空", key)
		}
		return str, nil
	}

	if subject, err = read("subject"); err != nil {
		return "", "", "", err
	}
	if predicate, err = read("predicate"); err != nil {
		return "", "", "", err
	}
	if object, err = read("object"); err != nil {
		return "", "", "", err
	}
	return subject, predicate, object, nil
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

func requireUserID(ctx context.Context) (string, error) {
	userID, err := auth.RequireUserID(ctx)
	if err != nil {
		return "", ErrUnauthenticated
	}
	return userID, nil
}
