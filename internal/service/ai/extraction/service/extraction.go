package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"Logos/pkg/structured"
	"Logos/pkg/strutil"

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
	RunScheduledTask(ctx context.Context, taskID string) (*model.ExtractionResult, bool, error)
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

// rateLimitCacheFactory 让单测替换限流后端；生产环境走 Redis。
var rateLimitCacheFactory = cache.NewRedisCache

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
		Status:    model.TaskStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if parameters != nil {
		paramBytes, _ := json.Marshal(parameters)
		task.Parameters = string(paramBytes)
	}

	if scheduledTime != nil && *scheduledTime != "" {
		t, err := time.Parse(time.RFC3339, *scheduledTime)
		if err != nil {
			return nil, fmt.Errorf("scheduled_time 必须是 RFC3339 时间: %w", err)
		}
		task.ScheduledAt = &t
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
		if parseErr != nil {
			return nil, fmt.Errorf("scheduled_time 必须是 RFC3339 时间: %w", parseErr)
		}
		task.ScheduledAt = &t
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
	// 手动执行允许重跑已结束的任务（改完参数再试一次），但不能和正在执行的实例并发。
	// 状态判断必须交给数据库的条件更新，先查后改只能把竞态窗口缩小、不能消除。
	claimed, err := s.repo.MarkTaskRunning(ctx, taskID, now)
	if err != nil {
		return nil, fmt.Errorf("更新任务状态失败: %w", err)
	}
	if !claimed {
		return nil, errors.New("任务正在执行中，请勿重复触发")
	}

	task.Status = model.TaskStatusRunning
	task.StartedAt = &now

	return s.runClaimedTask(ctx, task, now)
}

// RunScheduledTask 领取并执行一个已到计划时间的任务，返回 (结果, 是否抢到执行权, 错误)。
//
// 第二个返回值为 false 表示任务已被其他实例领走、或已被用户取消：
// 这是多实例竞争的正常结果，调度器应跳过而不是记失败。
//
// 与 ExecuteTask 的差别是不占交互链路的限流配额：计划任务是后台批量工作，
// 用单批上限 + 串行执行形成背压；套用 20 次/分钟的交互限流只会让到期任务成批失败。
func (s *extractionServiceImpl) RunScheduledTask(ctx context.Context, taskID string) (*model.ExtractionResult, bool, error) {
	now := time.Now()
	claimed, err := s.repo.ClaimDueTask(ctx, taskID, now)
	if err != nil {
		return nil, false, fmt.Errorf("领取到期任务失败: %w", err)
	}
	if !claimed {
		return nil, false, nil
	}

	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return nil, true, fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return nil, true, errors.New("任务在领取后被删除")
	}

	logger.Info("执行计划抽取任务",
		logger.StringField("task_id", taskID),
		logger.StringField("scheduled_at", formatScheduledAt(task.ScheduledAt)))

	result, err := s.runClaimedTask(ctx, task, now)
	return result, true, err
}

func formatScheduledAt(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// runClaimedTask 执行一个已经被原子置为运行中的任务：调用抽取、落库结果、回写最终状态。
// 调用前必须已经通过条件更新抢到执行权，否则并发触发会重复调用模型。
func (s *extractionServiceImpl) runClaimedTask(ctx context.Context, task *model.ExtractionTask, startedAt time.Time) (*model.ExtractionResult, error) {
	result := &model.ExtractionResult{
		ID:        uuid.New().String(),
		TaskID:    task.ID,
		Status:    model.ResultStatusSucceeded,
		StartTime: startedAt.Unix(),
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
	endTime := time.Now()
	task.EndedAt = &endTime

	if extractErr != nil {
		result.Status = model.ResultStatusFailed
		errMsg := extractErr.Error()
		result.ErrorMsg = &errMsg
		task.Status = model.TaskStatusFailed
		logger.Error("抽取任务执行失败",
			logger.StringField("task_id", task.ID),
			logger.ErrorField(extractErr))
		if updateErr := s.repo.UpdateTask(ctx, task); updateErr != nil {
			logger.Error("更新失败任务状态失败",
				logger.StringField("task_id", task.ID),
				logger.ErrorField(updateErr))
			return result, errors.Join(extractErr, fmt.Errorf("更新任务状态失败: %w", updateErr))
		}
		// 失败结果也要落库：否则前端只能看到任务卡在 RUNNING，
		// 排查时连失败原因都拿不到。
		if saveErr := s.repo.CreateResult(ctx, result); saveErr != nil {
			logger.Error("保存失败结果失败",
				logger.StringField("task_id", task.ID),
				logger.ErrorField(saveErr))
			return result, errors.Join(extractErr, fmt.Errorf("保存失败结果失败: %w", saveErr))
		}
	} else {
		task.Status = model.TaskStatusSucceeded
		logger.Info("抽取任务执行成功",
			logger.StringField("task_id", task.ID))

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
				logger.StringField("task_id", task.ID),
				logger.ErrorField(err))
			// 结果与 outbox 没提交就不能报成功：调用方会立刻去查结果，
			// 查到的却是空。
			return nil, fmt.Errorf("保存抽取结果失败: %w", err)
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

	if task.Status != model.TaskStatusPending && task.Status != model.TaskStatusRunning {
		return fmt.Errorf("任务状态不允许取消，当前状态: %d", task.Status)
	}

	task.Status = model.TaskStatusCancelled
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

	// 采集服务发出的字段是 data_source_id，不是 data_id。
	// 旧代码读错键，解析结果恒为空串：每个采集事件都会建出一个
	// data_id="" 的抽取任务，任务详情里看不出数据来自哪。
	dataID, err := requiredString(event, "data_source_id")
	if err != nil {
		return fmt.Errorf("采集事件缺少可用的 data_source_id，拒绝创建无数据源的抽取任务: %w", err)
	}
	status, err := requiredString(event, "status")
	if err != nil {
		return fmt.Errorf("采集事件缺少可用的 status，无法判断是否应触发抽取: %w", err)
	}
	// collection_result_id 只用于回填来源，缺失不影响抽取本身。
	collectionID, _ := event["collection_result_id"].(string)

	if status != "SUCCESS" {
		logger.Info("采集未成功，跳过自动抽取",
			logger.StringField("data_source_id", dataID),
			logger.StringField("status", status))
		return nil
	}

	parameters := map[string]string{
		"data_source_id": dataID,
		"collection_id":  collectionID,
	}

	ctx := context.Background()
	task, err := s.CreateTask(ctx, 1, dataID, "data_source", parameters, nil)
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
	parameters, err := decodeTaskParameters(task.Parameters)
	if err != nil {
		return err
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
		if err := s.persistEntities(ctx, entities, parameters["collection_id"]); err != nil {
			return err
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

	entities, err := s.parseObjectArray(ctx, response, params,
		`[{"text":"实体文本","type":"PERSON","confidence":0.9,"startPos":0,"endPos":2}]`)
	if err != nil {
		return nil, err
	}
	logger.Info("实体识别完成",
		logger.IntField("count", len(entities)))

	return entities, nil
}

func (s *extractionServiceImpl) executeRelationExtraction(ctx context.Context, task *model.ExtractionTask, result *model.ExtractionResult) error {
	parameters, err := decodeTaskParameters(task.Parameters)
	if err != nil {
		return err
	}

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
		if err := s.persistRelations(ctx, relations, parameters["collection_id"]); err != nil {
			return err
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

	relations, err := s.parseObjectArray(ctx, response, params,
		`[{"type":"WORKS_AT","sourceId":"实体A","targetId":"实体B","confidence":0.9,"text":"关系描述"}]`)
	if err != nil {
		return nil, err
	}
	logger.Info("关系抽取完成",
		logger.IntField("count", len(relations)))

	return relations, nil
}

func (s *extractionServiceImpl) executeTripleExtraction(ctx context.Context, task *model.ExtractionTask, result *model.ExtractionResult) error {
	parameters, err := decodeTaskParameters(task.Parameters)
	if err != nil {
		return err
	}

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
		if err := s.persistTriples(ctx, triples, parameters["collection_id"]); err != nil {
			return err
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

	triples, err := s.parseObjectArray(ctx, response, params,
		`[{"subject":"主语","predicate":"谓语","object":"宾语","confidence":0.9}]`)
	if err != nil {
		return nil, err
	}
	logger.Info("三元组抽取完成",
		logger.IntField("count", len(triples)))

	return triples, nil
}

func (s *extractionServiceImpl) executeKeyphraseExtraction(ctx context.Context, task *model.ExtractionTask, result *model.ExtractionResult) error {
	parameters, err := decodeTaskParameters(task.Parameters)
	if err != nil {
		return err
	}

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

	keyphrases, err := s.parseStringArray(ctx, response, params,
		`["关键词1","关键词2"]`)
	if err != nil {
		return nil, err
	}

	logger.Info("关键短语抽取完成",
		logger.IntField("count", len(keyphrases)))

	return keyphrases, nil
}

func (s *extractionServiceImpl) chatWithParams(ctx context.Context, messages []string, params map[string]string) (string, error) {
	if s.einoClient == nil {
		return "", errors.New("LLM客户端未初始化")
	}

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

// parseObjectArray 解析对象数组，失败时最多做一次定向修复。
// 修复仍失败就返回错误，让抽取任务进入失败态；不再把「模型输出坏了」
// 伪装成「成功抽到 0 条」，否则调用方无法区分空结果和格式失败。
func (s *extractionServiceImpl) parseObjectArray(
	ctx context.Context, raw string, params map[string]string, schemaHint string,
) ([]map[string]interface{}, error) {
	var results []map[string]interface{}
	_, err := structured.ParseWithRepair(ctx, "extraction", raw, &results,
		func(ctx context.Context, raw string, parseErr error) (string, error) {
			return s.chatWithParams(ctx, []string{
				"你是一个JSON修复助手，只输出修正后的JSON。",
				structured.RepairPrompt(raw, parseErr, schemaHint),
			}, params)
		})
	if err != nil {
		logger.Warn("解析LLM JSON响应失败",
			logger.ErrorField(err),
			logger.StringField("raw", strutil.TruncateByRunes(raw, 200)))
		return nil, fmt.Errorf("解析LLM JSON响应失败: %w", err)
	}
	return results, nil
}

func (s *extractionServiceImpl) parseStringArray(
	ctx context.Context, raw string, params map[string]string, schemaHint string,
) ([]string, error) {
	var results []string
	_, err := structured.ParseWithRepair(ctx, "extraction", raw, &results,
		func(ctx context.Context, raw string, parseErr error) (string, error) {
			return s.chatWithParams(ctx, []string{
				"你是一个JSON修复助手，只输出修正后的JSON。",
				structured.RepairPrompt(raw, parseErr, schemaHint),
			}, params)
		})
	if err != nil {
		logger.Warn("解析LLM关键短语响应失败",
			logger.ErrorField(err),
			logger.StringField("raw", strutil.TruncateByRunes(raw, 200)))
		return nil, fmt.Errorf("解析LLM关键短语响应失败: %w", err)
	}
	return results, nil
}

func (s *extractionServiceImpl) checkExtractionRateLimit(ctx context.Context) error {
	key := "ratelimit:extraction:global"

	limiter := rateLimitCacheFactory()
	// NewRedisCache 在 Redis 不可用（或处于重连冷却期）时返回 nil。
	// 旧代码直接调用方法，nil 接口会 panic：一次 Redis 抖动就能
	// 把抽取服务打挂。限流是保护措施，不该成为单点故障，
	// 拿不到缓存时放行并留日志。
	if limiter == nil {
		logger.Warn("限流缓存不可用，本次放行")
		return nil
	}

	current, err := limiter.IncrBy(ctx, key, 1)
	if err != nil {
		logger.Warn("抽取任务限流计数失败，放行",
			logger.ErrorField(err))
		return nil
	}

	if current == 1 {
		if err := limiter.Expire(ctx, key, time.Minute); err != nil {
			logger.Warn("设置限流窗口失败，窗口可能不过期",
				logger.ErrorField(err))
		}
	}

	if current > 20 {
		logger.Warn("抽取任务执行频率超限",
			logger.Int64Field("count", current))
		return fmt.Errorf("抽取任务过于频繁，请%d秒后再试", 60)
	}

	return nil
}
