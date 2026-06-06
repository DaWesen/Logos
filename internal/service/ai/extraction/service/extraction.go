package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/ai/extraction/dao"
	"Logos/internal/service/ai/extraction/model"
	knowledgeModel "Logos/internal/service/ai/knowledge/model"
	"Logos/pkg/cache"
	"Logos/pkg/eino"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/outbox"

	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
)

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

type VectorService interface {
	Vectorize(ctx context.Context, text string, collectionID string, metadata map[string]string) (interface{ GetID() string }, error)
}

type ExtractionService interface {
	CreateTask(ctx context.Context, taskType int32, dataID, dataType string, parameters map[string]string, scheduledTime *string) (*model.ExtractionTask, error)
	UpdateTask(ctx context.Context, id string, taskType *int32, parameters map[string]string, scheduledTime *string) (*model.ExtractionTask, error)
	DeleteTask(ctx context.Context, taskID string) error
	GetTask(ctx context.Context, id string) (*model.ExtractionTask, error)
	ListTasks(ctx context.Context) ([]*model.ExtractionTask, error)
	ExecuteTask(ctx context.Context, taskID string) (*model.ExtractionResult, error)
	CancelTask(ctx context.Context, taskID string) error
	GetExtractionResult(ctx context.Context, id string) (*model.ExtractionResult, error)
	ListExtractionResults(ctx context.Context, taskID string) ([]*model.ExtractionResult, error)
	ExtractFromText(ctx context.Context, text string, taskType int32, parameters map[string]string) (entities []map[string]interface{}, relations []map[string]interface{}, triples []map[string]interface{}, keyphrases []string, err error)
	StartKafkaConsumer(ctx context.Context) error
}

type extractionServiceImpl struct {
	repo             dao.ExtractionRepository
	einoClient       *eino.EinoManager
	knowledgeService KnowledgeService
	vectorService    VectorService
	outboxRepo       outbox.OutboxRepository
}

func NewExtractionService(repo dao.ExtractionRepository, einoClient *eino.EinoManager, knowledgeService KnowledgeService, vectorService VectorService, outboxRepo outbox.OutboxRepository) ExtractionService {
	return &extractionServiceImpl{
		repo:             repo,
		einoClient:       einoClient,
		knowledgeService: knowledgeService,
		vectorService:    vectorService,
		outboxRepo:       outboxRepo,
	}
}

func (s *extractionServiceImpl) CreateTask(ctx context.Context, taskType int32, dataID, dataType string, parameters map[string]string, scheduledTime *string) (*model.ExtractionTask, error) {
	logger.Info("创建抽取任务",
		logger.StringField("data_id", dataID),
		logger.IntField("type", int(taskType)))

	task := &model.ExtractionTask{
		ID:        uuid.New().String(),
		Type:      int(taskType),
		DataID:    dataID,
		DataType:  dataType,
		Status:    1,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if parameters != nil {
		paramBytes, _ := json.Marshal(parameters)
		task.Parameters = string(paramBytes)
	}

	if scheduledTime != nil && *scheduledTime != "" {
		t, err := time.Parse(time.RFC3339, *scheduledTime)
		if err == nil {
			task.ScheduledAt = &t
		}
	}

	if err := s.repo.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("创建任务失败: %w", err)
	}

	return task, nil
}

func (s *extractionServiceImpl) UpdateTask(ctx context.Context, id string, taskType *int32, parameters map[string]string, scheduledTime *string) (*model.ExtractionTask, error) {
	logger.Info("更新抽取任务",
		logger.StringField("id", id))

	task, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return nil, errors.New("任务不存在")
	}

	if taskType != nil {
		task.Type = int(*taskType)
	}
	if parameters != nil {
		paramBytes, _ := json.Marshal(parameters)
		task.Parameters = string(paramBytes)
	}
	if scheduledTime != nil {
		t, parseErr := time.Parse(time.RFC3339, *scheduledTime)
		if parseErr == nil {
			task.ScheduledAt = &t
		}
	}
	task.UpdatedAt = time.Now()

	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("更新任务失败: %w", err)
	}

	return task, nil
}

func (s *extractionServiceImpl) DeleteTask(ctx context.Context, taskID string) error {
	logger.Info("删除抽取任务",
		logger.StringField("id", taskID))

	return s.repo.DeleteTask(ctx, taskID)
}

func (s *extractionServiceImpl) GetTask(ctx context.Context, id string) (*model.ExtractionTask, error) {
	return s.repo.GetTask(ctx, id)
}

func (s *extractionServiceImpl) ListTasks(ctx context.Context) ([]*model.ExtractionTask, error) {
	return s.repo.ListTasks(ctx)
}

func (s *extractionServiceImpl) ExecuteTask(ctx context.Context, taskID string) (*model.ExtractionResult, error) {
	logger.Info("执行抽取任务",
		logger.StringField("task_id", taskID))

	if err := s.checkExtractionRateLimit(ctx); err != nil {
		return nil, err
	}

	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return nil, errors.New("任务不存在")
	}

	now := time.Now()
	task.Status = 2
	task.StartedAt = &now
	s.repo.UpdateTask(ctx, task)

	result := &model.ExtractionResult{
		ID:        uuid.New().String(),
		TaskID:    taskID,
		Status:    3,
		StartTime: now.Unix(),
	}

	var extractErr error

	switch task.Type {
	case 1:
		extractErr = s.executeEntityRecognition(ctx, task, result)
	case 2:
		extractErr = s.executeRelationExtraction(ctx, task, result)
	case 3:
		extractErr = s.executeTripleExtraction(ctx, task, result)
	case 5:
		extractErr = s.executeKeyphraseExtraction(ctx, task, result)
	default:
		extractErr = fmt.Errorf("不支持的任务类型: %d", task.Type)
	}

	result.EndTime = time.Now().Unix()

	if extractErr != nil {
		result.Status = 4
		errMsg := extractErr.Error()
		result.ErrorMsg = &errMsg
		logger.Error("抽取任务执行失败",
			logger.StringField("task_id", taskID),
			logger.ErrorField(extractErr))
		s.repo.CreateResult(ctx, result)
	} else {
		logger.Info("抽取任务执行成功",
			logger.StringField("task_id", taskID))

		err := s.repo.WithTransaction(ctx, func(txRepo dao.ExtractionRepository) error {
			if err := txRepo.UpdateTask(ctx, task); err != nil {
				return err
			}
			if err := txRepo.CreateResult(ctx, result); err != nil {
				return err
			}
			if err := s.saveExtractionEventToOutbox(ctx, txRepo.DB(), task, result); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			logger.Error("事务提交失败",
				logger.StringField("task_id", taskID),
				logger.ErrorField(err))
		}
	}

	return result, extractErr
}

func (s *extractionServiceImpl) saveExtractionEventToOutbox(ctx context.Context, tx *gorm.DB, task *model.ExtractionTask, result *model.ExtractionResult) error {
	event := map[string]interface{}{
		"task_id":      task.ID,
		"task_type":    task.Type,
		"result_id":    result.ID,
		"status":       result.Status,
		"data_id":      task.DataID,
		"data_type":    task.DataType,
		"entities":     result.Entities,
		"relations":    result.Relations,
		"triples":      result.Triples,
		"summary":      result.Summary,
		"keyphrases":   result.Keyphrases,
		"completed_at": time.Now().Format(time.RFC3339),
	}

	if err := s.outboxRepo.SaveWithTx(ctx, tx, "knowledge_extraction", task.ID, event); err != nil {
		logger.Error("保存知识抽取事件到Outbox失败",
			logger.ErrorField(err))
		return err
	}

	logger.Info("已保存知识抽取事件到Outbox",
		logger.StringField("topic", "knowledge_extraction"),
		logger.StringField("task_id", task.ID))

	if err := s.outboxRepo.SaveWithTx(ctx, tx, "vector_processing", task.ID, event); err != nil {
		logger.Error("保存向量化处理事件到Outbox失败",
			logger.ErrorField(err))
		return err
	}

	logger.Info("已保存向量化处理事件到Outbox",
		logger.StringField("topic", "vector_processing"),
		logger.StringField("task_id", task.ID))

	return nil
}

func (s *extractionServiceImpl) CancelTask(ctx context.Context, taskID string) error {
	logger.Info("取消抽取任务",
		logger.StringField("task_id", taskID))

	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return errors.New("任务不存在")
	}

	if task.Status != 1 && task.Status != 2 {
		return fmt.Errorf("任务状态不允许取消，当前状态: %d", task.Status)
	}

	task.Status = 5
	now := time.Now()
	task.EndedAt = &now
	return s.repo.UpdateTask(ctx, task)
}

func (s *extractionServiceImpl) GetExtractionResult(ctx context.Context, id string) (*model.ExtractionResult, error) {
	return s.repo.GetResult(ctx, id)
}

func (s *extractionServiceImpl) ListExtractionResults(ctx context.Context, taskID string) ([]*model.ExtractionResult, error) {
	return s.repo.ListResultsByTaskID(ctx, taskID)
}

func (s *extractionServiceImpl) ExtractFromText(ctx context.Context, text string, taskType int32, parameters map[string]string) (entities []map[string]interface{}, relations []map[string]interface{}, triples []map[string]interface{}, keyphrases []string, err error) {
	logger.Info("实时文本抽取",
		logger.IntField("type", int(taskType)),
		logger.StringField("text_length", fmt.Sprintf("%d", len(text))))

	if text == "" {
		return nil, nil, nil, nil, errors.New("文本不能为空")
	}

	switch taskType {
	case 1:
		entities, err = s.recognizeEntities(ctx, text, parameters)
	case 2:
		relations, err = s.extractRelations(ctx, text, parameters)
	case 3:
		triples, err = s.extractTriples(ctx, text, parameters)
	case 5:
		keyphrases, err = s.extractKeyphrases(ctx, text, parameters)
	default:
		err = fmt.Errorf("不支持的抽取类型: %d", taskType)
	}

	return entities, relations, triples, keyphrases, err
}

func (s *extractionServiceImpl) StartKafkaConsumer(ctx context.Context) error {
	logger.Info("启动Extraction Service的Kafka消费者")

	consumer := mq.NewConsumer(mq.TopicDataCollection, "extraction-group")

	go func() {
		if err := consumer.Subscribe(ctx, s.handleCollectionEvent); err != nil {
			logger.Error("订阅数据采集事件失败",
				logger.ErrorField(err))
		}
	}()

	logger.Info("Extraction Service Kafka消费者已启动",
		logger.StringField("topic", mq.TopicDataCollection))

	return nil
}

func (s *extractionServiceImpl) handleCollectionEvent(msg *mq.Message) error {
	logger.Info("收到数据采集完成事件",
		logger.StringField("key", msg.Key),
		logger.IntField("value_len", len(msg.Value)))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("解析采集事件失败",
			logger.ErrorField(err))
		return err
	}

	dataID, _ := event["data_id"].(string)
	dataType, _ := event["data_type"].(string)
	collectionID, _ := event["collection_result_id"].(string)

	parameters := map[string]string{
		"data_source_id": dataID,
		"collection_id":  collectionID,
	}

	ctx := context.Background()
	task, err := s.CreateTask(ctx, 1, dataID, dataType, parameters, nil)
	if err != nil {
		logger.Error("自动创建抽取任务失败",
			logger.ErrorField(err))
		return err
	}

	logger.Info("自动创建抽取任务成功",
		logger.StringField("task_id", task.ID),
		logger.StringField("data_id", dataID))

	_, execErr := s.ExecuteTask(ctx, task.ID)
	if execErr != nil {
		logger.Error("自动执行抽取任务失败",
			logger.StringField("task_id", task.ID),
			logger.ErrorField(execErr))
		return execErr
	}

	logger.Info("自动执行抽取任务完成",
		logger.StringField("task_id", task.ID))

	return nil
}

func (s *extractionServiceImpl) executeEntityRecognition(ctx context.Context, task *model.ExtractionTask, result *model.ExtractionResult) error {
	parameters := make(map[string]string)
	if task.Parameters != "" {
		json.Unmarshal([]byte(task.Parameters), &parameters)
	}

	text := parameters["text"]
	if text == "" {
		text = parameters["content"]
	}

	entities, err := s.recognizeEntities(ctx, text, parameters)
	if err != nil {
		return err
	}

	entityBytes, _ := json.Marshal(entities)
	strEntities := string(entityBytes)
	result.Entities = strEntities

	if s.knowledgeService != nil {
		collectionID := parameters["collection_id"]
		for _, entity := range entities {
			entityType, _ := entity["type"].(string)
			name, _ := entity["text"].(string)
			props := make(map[string]string)
			for k, v := range entity {
				if strV, ok := v.(string); ok && k != "type" && k != "text" && k != "confidence" {
					props[k] = strV
				}
			}
			desc := fmt.Sprintf("置信度: %.2f", entity["confidence"])
			s.knowledgeService.AddEntity(ctx, entityType, name, collectionID, props, &desc, "")
		}
	}

	return nil
}

func (s *extractionServiceImpl) recognizeEntities(ctx context.Context, text string, params map[string]string) ([]map[string]interface{}, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		logger.Warn("Eino未初始化，返回空实体列表")
		return []map[string]interface{}{}, nil
	}

	domain := ""
	if params != nil {
		domain = params["domain"]
	}

	prompt := fmt.Sprintf(`你是一个专业的实体识别系统。请从以下文本中识别出所有命名实体。
%s
请以严格的JSON数组格式输出，每个对象包含：
- "text": 实体文本
- "type": 实体类型（如PERSON、ORG、LOC、PRODUCT、EVENT等）
- "confidence": 置信度（0.0-1.0）
- "startPos": 起始位置（整数）
- "endPos": 结束位置（整数）

只输出JSON数组，不要其他内容。

文本：
%s`, domainHint(domain), text)

	messages := []string{
		"你是一个专业的NLP信息抽取系统，擅长从文本中提取结构化知识。",
		prompt,
	}

	response, err := s.chatWithParams(ctx, messages, params)
	if err != nil {
		return nil, fmt.Errorf("LLM实体识别失败: %w", err)
	}

	entities := parseJSONArray(response)
	logger.Info("实体识别完成",
		logger.IntField("count", len(entities)))

	return entities, nil
}

func (s *extractionServiceImpl) executeRelationExtraction(ctx context.Context, task *model.ExtractionTask, result *model.ExtractionResult) error {
	parameters := make(map[string]string)
	json.Unmarshal([]byte(task.Parameters), &parameters)

	text := parameters["text"]
	if text == "" {
		text = parameters["content"]
	}

	relations, err := s.extractRelations(ctx, text, parameters)
	if err != nil {
		return err
	}

	relationBytes, _ := json.Marshal(relations)
	strRelations := string(relationBytes)
	result.Relations = strRelations

	if s.knowledgeService != nil {
		collectionID := parameters["collection_id"]
		for _, rel := range relations {
			relType, _ := rel["type"].(string)
			sourceID, _ := rel["sourceId"].(string)
			targetID, _ := rel["targetId"].(string)
			props := make(map[string]string)
			for k, v := range rel {
				if strV, ok := v.(string); ok && k != "type" && k != "sourceId" && k != "targetId" && k != "confidence" {
					props[k] = strV
				}
			}
			textVal, _ := rel["text"].(string)
			s.knowledgeService.AddRelation(ctx, relType, sourceID, targetID, collectionID, props, &textVal)
		}
	}

	return nil
}

func (s *extractionServiceImpl) extractRelations(ctx context.Context, text string, params map[string]string) ([]map[string]interface{}, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return []map[string]interface{}{}, nil
	}

	domain := ""
	if params != nil {
		domain = params["domain"]
	}

	prompt := fmt.Sprintf(`你是一个专业的关系抽取系统。请从以下文本中识别出实体之间的关系。
%s
请以严格的JSON数组格式输出，每个对象包含：
- "type": 关系类型（如WORKS_AT、LOCATED_IN、FOUNDED等）
- "sourceId": 源实体标识
- "targetId": 目标实体标识
- "confidence": 置信度（0.0-1.0）
- "text": 关系描述文本

只输出JSON数组，不要其他内容。

文本：
%s`, domainHint(domain), text)

	messages := []string{
		"你是一个专业的NLP关系抽取系统。",
		prompt,
	}

	response, err := s.chatWithParams(ctx, messages, params)
	if err != nil {
		return nil, fmt.Errorf("LLM关系抽取失败: %w", err)
	}

	relations := parseJSONArray(response)
	logger.Info("关系抽取完成",
		logger.IntField("count", len(relations)))

	return relations, nil
}

func (s *extractionServiceImpl) executeTripleExtraction(ctx context.Context, task *model.ExtractionTask, result *model.ExtractionResult) error {
	parameters := make(map[string]string)
	json.Unmarshal([]byte(task.Parameters), &parameters)

	text := parameters["text"]
	if text == "" {
		text = parameters["content"]
	}

	triples, err := s.extractTriples(ctx, text, parameters)
	if err != nil {
		return err
	}

	tripleBytes, _ := json.Marshal(triples)
	strTriples := string(tripleBytes)
	result.Triples = strTriples

	if s.knowledgeService != nil {
		collectionID := parameters["collection_id"]
		for _, triple := range triples {
			subject, _ := triple["subject"].(string)
			predicate, _ := triple["predicate"].(string)
			obj, _ := triple["object"].(string)

			sourceEnt, _ := s.knowledgeService.AddEntity(ctx, "ENTITY", subject, collectionID, nil, nil, "")
			targetEnt, _ := s.knowledgeService.AddEntity(ctx, "ENTITY", obj, collectionID, nil, nil, "")

			srcID := ""
			tgtID := ""
			if sourceEnt != nil {
				srcID = sourceEnt.GetID()
			}
			if targetEnt != nil {
				tgtID = targetEnt.GetID()
			}

			if srcID != "" && tgtID != "" {
				s.knowledgeService.AddRelation(ctx, predicate, srcID, tgtID, collectionID, nil, nil)
			}
		}
	}

	return nil
}

func (s *extractionServiceImpl) extractTriples(ctx context.Context, text string, params map[string]string) ([]map[string]interface{}, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return []map[string]interface{}{}, nil
	}

	domain := ""
	if params != nil {
		domain = params["domain"]
	}

	prompt := fmt.Sprintf(`你是一个专业的三元组抽取系统（OpenIE）。请从以下文本中抽取主谓宾三元组。
%s
请以严格的JSON数组格式输出，每个对象包含：
- "subject": 主语
- "predicate": 谓语/关系
- "object": 宾语
- "confidence": 置信度（0.0-1.0）

只输出JSON数组，不要其他内容。

文本：
%s`, domainHint(domain), text)

	messages := []string{
		"你是一个专业的开放域信息抽取系统，擅长从非结构化文本中抽取主谓宾三元组。",
		prompt,
	}

	response, err := s.chatWithParams(ctx, messages, params)
	if err != nil {
		return nil, fmt.Errorf("LLM三元组抽取失败: %w", err)
	}

	triples := parseJSONArray(response)
	logger.Info("三元组抽取完成",
		logger.IntField("count", len(triples)))

	return triples, nil
}

func (s *extractionServiceImpl) executeKeyphraseExtraction(ctx context.Context, task *model.ExtractionTask, result *model.ExtractionResult) error {
	parameters := make(map[string]string)
	json.Unmarshal([]byte(task.Parameters), &parameters)

	text := parameters["text"]
	if text == "" {
		text = parameters["content"]
	}

	keyphrases, err := s.extractKeyphrases(ctx, text, parameters)
	if err != nil {
		return err
	}

	kpBytes, _ := json.Marshal(keyphrases)
	strKPs := string(kpBytes)
	result.Keyphrases = &strKPs
	return nil
}

func (s *extractionServiceImpl) extractKeyphrases(ctx context.Context, text string, params map[string]string) ([]string, error) {
	if s.einoClient == nil || !s.einoClient.HasChatModel() {
		return []string{}, nil
	}

	topK := "10"
	language := "中文"
	if params != nil {
		if k, ok := params["top_k"]; ok {
			topK = k
		}
		if l, ok := params["language"]; ok {
			language = l
		}
	}

	prompt := fmt.Sprintf(`请从以下%s文本中提取%s个关键短语/关键词。
关键短语应该能准确概括文本的核心主题和重要概念。
请以严格JSON数组格式输出字符串数组，不要其他内容。

文本：
%s`, language, topK, text)

	messages := []string{
		"你是一个专业的关键词和关键短语抽取系统。",
		prompt,
	}

	response, err := s.chatWithParams(ctx, messages, params)
	if err != nil {
		return nil, fmt.Errorf("LLM关键短语抽取失败: %w", err)
	}

	var keyphrases []string
	if err := json.Unmarshal([]byte(response), &keyphrases); err != nil {
		keyphrases = []string{response}
	}

	logger.Info("关键短语抽取完成",
		logger.IntField("count", len(keyphrases)))

	return keyphrases, nil
}

func (s *extractionServiceImpl) chatWithParams(ctx context.Context, messages []string, params map[string]string) (string, error) {
	if params != nil {
		baseURL := params["llm_base_url"]
		apiKey := params["llm_api_key"]
		modelName := params["llm_model"]
		if baseURL != "" && modelName != "" {
			logger.Info("使用动态LLM配置",
				logger.StringField("model", modelName),
				logger.StringField("base_url", baseURL))
			chatModel, err := eino.NewDynamicChatModel(apiKey, modelName, baseURL)
			if err != nil {
				logger.Warn("创建动态ChatModel失败，回退到全局配置", logger.ErrorField(err))
			} else {
				var schemaMessages []*schema.Message
				for i, msg := range messages {
					if i == 0 {
						schemaMessages = append(schemaMessages, schema.SystemMessage(msg))
					} else if i%2 == 1 {
						schemaMessages = append(schemaMessages, schema.UserMessage(msg))
					} else {
						schemaMessages = append(schemaMessages, schema.AssistantMessage(msg, nil))
					}
				}
				response, genErr := chatModel.Generate(ctx, schemaMessages)
				if genErr != nil {
					return "", fmt.Errorf("动态LLM生成失败: %w", genErr)
				}
				return response.Content, nil
			}
		}
	}
	return s.einoClient.Chat(ctx, messages)
}

func domainHint(domain string) string {
	if domain == "" {
		return ""
	}
	return fmt.Sprintf("\n领域背景：%s\n请根据该领域的特点进行抽取。", domain)
}

func parseJSONArray(jsonStr string) []map[string]interface{} {
	jsonStr = trimJSONResponse(jsonStr)
	var results []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &results); err != nil {
		logger.Warn("解析LLM JSON响应失败，尝试修复",
			logger.ErrorField(err),
			logger.StringField("raw", jsonStr[:min(len(jsonStr), 200)]))
		fixed := fixJSONResponse(jsonStr)
		if fixed != "" {
			json.Unmarshal([]byte(fixed), &results)
		}
	}
	return results
}

func trimJSONResponse(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		if idx := strings.Index(s, "\r"); idx >= 0 {
			s = s[idx+1:]
		}
	}
	if strings.HasSuffix(s, "```") {
		if idx := strings.LastIndex(s, "\n"); idx >= 0 {
			s = s[:idx]
		} else {
			s = s[:len(s)-3]
		}
	}
	s = strings.TrimSpace(s)
	start := indexOfJSONArray(s)
	if start >= 0 {
		s = s[start:]
	}
	end := lastIndexOfJSONArray(s)
	if end >= 0 {
		s = s[:end+1]
	}
	return s
}

func fixJSONResponse(s string) string {
	start := indexOfJSONArray(s)
	if start < 0 {
		return ""
	}
	s = s[start:]
	depth := 0
	inString := false
	escaped := false
	for i, c := range s {
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return ""
}

func indexOfJSONArray(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '[' {
			for j := i + 1; j < len(s); j++ {
				c := s[j]
				if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
					continue
				}
				if c == '{' || c == '"' || c == ']' {
					return i
				}
				break
			}
		}
	}
	return -1
}

func lastIndexOfJSONArray(s string) int {
	for i := len(s) - 1; i > 0; i-- {
		if s[i] == ']' {
			return i
		}
	}
	return -1
}

func (s *extractionServiceImpl) checkExtractionRateLimit(ctx context.Context) error {
	key := "ratelimit:extraction:global"

	cache := cache.NewRedisCache()

	current, err := cache.IncrBy(ctx, key, 1)
	if err != nil {
		logger.Warn("抽取任务限流计数失败，放行",
			logger.ErrorField(err))
		return nil
	}

	if current == 1 {
		cache.Expire(ctx, key, time.Minute)
	}

	if current > 20 {
		logger.Warn("抽取任务执行频率超限",
			logger.Int64Field("count", current))
		return fmt.Errorf("抽取任务过于频繁，请%d秒后再试", 60)
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
