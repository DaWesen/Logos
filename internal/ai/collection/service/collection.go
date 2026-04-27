package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"Logos/internal/ai/collection/dao"
	"Logos/internal/ai/collection/model"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
)

type KnowledgeService interface {
	AddKnowledge(ctx context.Context, title string, content string, sourceType string, sourceID string, metadata map[string]string) (interface{ GetID() string }, error)
}

type ExtractionService interface {
	CreateTask(ctx context.Context, taskType int32, dataID string, dataType string, parameters map[string]string, scheduledTime *string) (interface{ GetID() string }, error)
}

type CollectionService interface {
	AddDataSource(ctx context.Context, name string, dsType int32, url string, config map[string]string, description *string) (*model.DataSource, error)
	UpdateDataSource(ctx context.Context, id string, name *string, dsType *int32, url *string, config map[string]string, description *string) (*model.DataSource, error)
	DeleteDataSource(ctx context.Context, id string) error
	GetDataSource(ctx context.Context, id string) (*model.DataSource, error)
	ListDataSources(ctx context.Context) ([]*model.DataSource, error)

	CreateTask(ctx context.Context, dataSourceID string, name string, format int32, schedule *string) (*model.CollectionTask, error)
	UpdateTask(ctx context.Context, id string, name *string, format *int32, schedule *string) (*model.CollectionTask, error)
	DeleteTask(ctx context.Context, taskID string) error
	GetTask(ctx context.Context, id string) (*model.CollectionTask, error)
	ListTasks(ctx context.Context) ([]*model.CollectionTask, error)

	ExecuteTask(ctx context.Context, taskID string) (*model.CollectionResult, error)
	StopTask(ctx context.Context, taskID string) error

	GetCollectionResult(ctx context.Context, id string) (*model.CollectionResult, error)
	ListCollectionResults(ctx context.Context, taskID string) ([]*model.CollectionResult, error)
	StartKafkaConsumer(ctx context.Context) error
}

type collectionServiceImpl struct {
	repo              dao.CollectionRepository
	knowledgeService  KnowledgeService
	extractionService ExtractionService
	kafkaProducer     *mq.Producer
}

func NewCollectionService(repo dao.CollectionRepository, knowledgeService KnowledgeService, extractionService ExtractionService) CollectionService {
	return &collectionServiceImpl{
		repo:              repo,
		knowledgeService:  knowledgeService,
		extractionService: extractionService,
		kafkaProducer:     mq.NewProducer(),
	}
}

func (s *collectionServiceImpl) AddDataSource(ctx context.Context, name string, dsType int32, url string, config map[string]string, description *string) (*model.DataSource, error) {
	logger.Info("添加数据源",
		logger.StringField("name", name),
		logger.IntField("type", int(dsType)))

	ds := &model.DataSource{
		ID:          uuid.New().String(),
		Name:        name,
		Type:        int(dsType),
		URL:         url,
		Config:      dao.MarshalConfig(config),
		Description: description,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.repo.AddDataSource(ctx, ds); err != nil {
		return nil, fmt.Errorf("添加数据源失败: %w", err)
	}
	return ds, nil
}

func (s *collectionServiceImpl) UpdateDataSource(ctx context.Context, id string, name *string, dsType *int32, url *string, config map[string]string, description *string) (*model.DataSource, error) {
	logger.Info("更新数据源",
		logger.StringField("id", id))

	ds, err := s.repo.GetDataSource(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("查询数据源失败: %w", err)
	}
	if ds == nil {
		return nil, errors.New("数据源不存在")
	}

	if name != nil {
		ds.Name = *name
	}
	if dsType != nil {
		ds.Type = int(*dsType)
	}
	if url != nil {
		ds.URL = *url
	}
	if config != nil {
		ds.Config = dao.MarshalConfig(config)
	}
	if description != nil {
		ds.Description = description
	}
	ds.UpdatedAt = time.Now()

	if err := s.repo.UpdateDataSource(ctx, ds); err != nil {
		return nil, fmt.Errorf("更新数据源失败: %w", err)
	}
	return ds, nil
}

func (s *collectionServiceImpl) DeleteDataSource(ctx context.Context, id string) error {
	logger.Info("删除数据源", logger.StringField("id", id))
	return s.repo.DeleteDataSource(ctx, id)
}

func (s *collectionServiceImpl) GetDataSource(ctx context.Context, id string) (*model.DataSource, error) {
	return s.repo.GetDataSource(ctx, id)
}

func (s *collectionServiceImpl) ListDataSources(ctx context.Context) ([]*model.DataSource, error) {
	return s.repo.ListDataSources(ctx)
}

func (s *collectionServiceImpl) CreateTask(ctx context.Context, dataSourceID string, name string, format int32, schedule *string) (*model.CollectionTask, error) {
	logger.Info("创建采集任务",
		logger.StringField("name", name),
		logger.StringField("data_source_id", dataSourceID))

	ds, err := s.repo.GetDataSource(ctx, dataSourceID)
	if err != nil {
		return nil, fmt.Errorf("查询数据源失败: %w", err)
	}
	if ds == nil {
		return nil, errors.New("数据源不存在")
	}

	task := &model.CollectionTask{
		ID:           uuid.New().String(),
		DataSourceID: dataSourceID,
		Name:         name,
		Format:       int(format),
		Status:       "PENDING",
		Schedule:     schedule,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.repo.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("创建任务失败: %w", err)
	}
	return task, nil
}

func (s *collectionServiceImpl) UpdateTask(ctx context.Context, id string, name *string, format *int32, schedule *string) (*model.CollectionTask, error) {
	logger.Info("更新采集任务", logger.StringField("id", id))

	task, err := s.repo.GetTask(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return nil, errors.New("任务不存在")
	}

	if name != nil {
		task.Name = *name
	}
	if format != nil {
		task.Format = int(*format)
	}
	if schedule != nil {
		task.Schedule = schedule
	}
	task.UpdatedAt = time.Now()

	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("更新任务失败: %w", err)
	}
	return task, nil
}

func (s *collectionServiceImpl) DeleteTask(ctx context.Context, taskID string) error {
	logger.Info("删除采集任务", logger.StringField("id", taskID))
	return s.repo.DeleteTask(ctx, taskID)
}

func (s *collectionServiceImpl) GetTask(ctx context.Context, id string) (*model.CollectionTask, error) {
	return s.repo.GetTask(ctx, id)
}

func (s *collectionServiceImpl) ListTasks(ctx context.Context) ([]*model.CollectionTask, error) {
	return s.repo.ListTasks(ctx)
}

func (s *collectionServiceImpl) ExecuteTask(ctx context.Context, taskID string) (*model.CollectionResult, error) {
	logger.Info("执行采集任务",
		logger.StringField("task_id", taskID))

	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return nil, errors.New("任务不存在")
	}

	now := time.Now()
	task.Status = "RUNNING"
	s.repo.UpdateTask(ctx, task)

	result := &model.CollectionResult{
		ID:             uuid.New().String(),
		TaskID:         taskID,
		Status:         "RUNNING",
		CollectedCount: 0,
		ProcessedCount: 0,
		StartTime:      now.Unix(),
		EndTime:        now.Unix(),
	}

	ds, _ := s.repo.GetDataSource(ctx, task.DataSourceID)

	collectErr := s.doCollect(ctx, task, ds, result)

	result.EndTime = time.Now().Unix()
	if collectErr != nil {
		result.Status = "FAILED"
		errMsg := collectErr.Error()
		result.ErrorMsg = &errMsg
		task.Status = "FAILED"
		logger.Error("采集任务执行失败",
			logger.StringField("task_id", taskID),
			logger.ErrorField(collectErr))
	} else {
		result.Status = "SUCCESS"
		task.Status = "SUCCESS"
		logger.Info("采集任务执行成功",
			logger.StringField("task_id", taskID),
			logger.Int64Field("collected", result.CollectedCount),
			logger.Int64Field("processed", result.ProcessedCount))

		s.sendCollectionCompleteEvent(ctx, task, result)
	}

	s.repo.CreateResult(ctx, result)
	s.repo.UpdateTask(ctx, task)

	return result, collectErr
}

func (s *collectionServiceImpl) sendCollectionCompleteEvent(ctx context.Context, task *model.CollectionTask, result *model.CollectionResult) {
	if s.kafkaProducer == nil {
		logger.Warn("Kafka生产者未初始化，跳过事件发送")
		return
	}

	event := map[string]interface{}{
		"task_id":              task.ID,
		"data_source_id":       task.DataSourceID,
		"collection_result_id": result.ID,
		"status":               result.Status,
		"collected_count":      result.CollectedCount,
		"processed_count":      result.ProcessedCount,
		"started_at":           result.StartTime,
		"completed_at":         result.EndTime,
		"format":               task.Format,
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		logger.Error("序列化采集完成事件失败",
			logger.ErrorField(err))
		return
	}

	if err := s.kafkaProducer.Send(ctx, mq.TopicDataCollection, task.ID, eventData); err != nil {
		logger.Error("发送数据采集完成事件到Kafka失败",
			logger.ErrorField(err))
		return
	}

	logger.Info("已发送采集完成事件到Kafka，将触发后续抽取流程",
		logger.StringField("topic", mq.TopicDataCollection),
		logger.StringField("task_id", task.ID))
}

func (s *collectionServiceImpl) doCollect(ctx context.Context, task *model.CollectionTask, ds *model.DataSource, result *model.CollectionResult) error {
	switch ds.Type {
	case 1:
		return s.collectFromInternalSystem(ctx, task, ds, result)
	case 2:
		return s.collectFromDocument(ctx, task, ds, result)
	case 3:
		return s.collectFromAPI(ctx, task, ds, result)
	case 4:
		return s.collectFromDatabase(ctx, task, ds, result)
	case 5:
		return s.collectFromWebsite(ctx, task, ds, result)
	default:
		return fmt.Errorf("不支持的数据源类型: %d", ds.Type)
	}
}

func (s *collectionServiceImpl) collectFromInternalSystem(ctx context.Context, task *model.CollectionTask, ds *model.DataSource, result *model.CollectionResult) error {
	config := dao.UnmarshalConfig(ds.Config)
	sourceType := config["source_type"]
	if sourceType == "" {
		sourceType = "internal_system"
	}

	for i := 0; i < 10; i++ {
		title := fmt.Sprintf("内部数据-%d-%s", i+1, task.Name)
		content := fmt.Sprintf("来自内部系统[%s]的模拟采集数据 #%d，关联数据源: %s", ds.Name, i+1, ds.ID)
		metadata := map[string]string{
			"data_source_id": ds.ID,
			"task_id":        task.ID,
			"format":         dao.DataFormatString(task.Format),
			"index":          fmt.Sprintf("%d", i+1),
		}

		if s.knowledgeService != nil {
			s.knowledgeService.AddKnowledge(ctx, title, content, sourceType, ds.ID, metadata)
		}
		result.CollectedCount++
		result.ProcessedCount++
	}

	return nil
}

func (s *collectionServiceImpl) collectFromDocument(ctx context.Context, task *model.CollectionTask, ds *model.DataSource, result *model.CollectionResult) error {
	formatStr := dao.DataFormatString(task.Format)
	metadata := map[string]string{
		"data_source_id": ds.ID,
		"format":         formatStr,
		"url":            ds.URL,
	}

	if s.knowledgeService != nil {
		s.knowledgeService.AddKnowledge(ctx, ds.Name, fmt.Sprintf("文档采集: 来自 %s (%s)", ds.URL, formatStr), "document", ds.ID, metadata)
		result.CollectedCount++
		result.ProcessedCount++
	}

	return nil
}

func (s *collectionServiceImpl) collectFromAPI(ctx context.Context, task *model.CollectionTask, ds *model.DataSource, result *model.CollectionResult) error {
	config := dao.UnmarshalConfig(ds.Config)
	apiEndpoint := ds.URL
	method := config["method"]
	if method == "" {
		method = "GET"
	}

	logger.Info("从API采集数据",
		logger.StringField("endpoint", apiEndpoint),
		logger.StringField("method", method))

	metadata := map[string]string{
		"data_source_id": ds.ID,
		"api_endpoint":   apiEndpoint,
		"api_method":     method,
		"format":         dao.DataFormatString(task.Format),
	}

	if s.knowledgeService != nil {
		s.knowledgeService.AddKnowledge(ctx, fmt.Sprintf("API采集-%s", ds.Name), fmt.Sprintf("API数据采集结果 from %s [%s]", apiEndpoint, method), "api", ds.ID, metadata)
		result.CollectedCount++
		result.ProcessedCount++
	}

	return nil
}

func (s *collectionServiceImpl) collectFromDatabase(ctx context.Context, task *model.CollectionTask, ds *model.DataSource, result *model.CollectionResult) error {
	config := dao.UnmarshalConfig(ds.Config)
	dbName := config["database"]
	tableName := config["table"]
	query := config["query"]

	logger.Info("从数据库采集数据",
		logger.StringField("database", dbName),
		logger.StringField("table", tableName))

	metadata := map[string]string{
		"data_source_id": ds.ID,
		"database":       dbName,
		"table":          tableName,
		"query":          query,
		"format":         dao.DataFormatString(task.Format),
	}

	if s.knowledgeService != nil {
		s.knowledgeService.AddKnowledge(ctx, fmt.Sprintf("DB采集-%s", ds.Name), fmt.Sprintf("数据库采集结果 from %s.%s (查询: %s)", dbName, tableName, query), "database", ds.ID, metadata)
		result.CollectedCount++
		result.ProcessedCount++
	}

	return nil
}

func (s *collectionServiceImpl) collectFromWebsite(ctx context.Context, task *model.CollectionTask, ds *model.DataSource, result *model.CollectionResult) error {
	logger.Info("从网站采集数据",
		logger.StringField("url", ds.URL))

	metadata := map[string]string{
		"data_source_id": ds.ID,
		"url":            ds.URL,
		"format":         dao.DataFormatString(task.Format),
	}

	if s.knowledgeService != nil {
		s.knowledgeService.AddKnowledge(ctx, fmt.Sprintf("网页采集-%s", ds.Name), fmt.Sprintf("网站爬取结果 from %s", ds.URL), "website", ds.ID, metadata)
		result.CollectedCount++
		result.ProcessedCount++
	}

	return nil
}

func (s *collectionServiceImpl) StopTask(ctx context.Context, taskID string) error {
	logger.Info("停止采集任务",
		logger.StringField("task_id", taskID))

	task, err := s.repo.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return errors.New("任务不存在")
	}

	if task.Status != "RUNNING" && task.Status != "PENDING" {
		return fmt.Errorf("当前状态不允许停止: %s", task.Status)
	}

	task.Status = "STOPPED"
	now := time.Now()
	task.UpdatedAt = now
	return s.repo.UpdateTask(ctx, task)
}

func (s *collectionServiceImpl) GetCollectionResult(ctx context.Context, id string) (*model.CollectionResult, error) {
	return s.repo.GetResult(ctx, id)
}

func (s *collectionServiceImpl) ListCollectionResults(ctx context.Context, taskID string) ([]*model.CollectionResult, error) {
	return s.repo.ListResultsByTaskID(ctx, taskID)
}

func (s *collectionServiceImpl) StartKafkaConsumer(ctx context.Context) error {
	logger.Info("启动Collection Service的Kafka消费者")

	consumer := mq.NewConsumer(mq.TopicUserActivity, "collection-group")

	go func() {
		if err := consumer.Subscribe(ctx, s.handleUserActivityEvent); err != nil {
			logger.Error("订阅用户活动事件失败",
				logger.ErrorField(err))
		}
	}()

	logger.Info("Collection Service Kafka消费者已启动",
		logger.StringField("topic", mq.TopicUserActivity))

	return nil
}

func (s *collectionServiceImpl) handleUserActivityEvent(msg *mq.Message) error {
	logger.Info("收到用户活动事件",
		logger.StringField("key", msg.Key))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("解析用户活动事件失败",
			logger.ErrorField(err))
		return err
	}

	userID, _ := event["user_id"].(string)
	action, _ := event["action"].(string)
	targetType, _ := event["target_type"].(string)

	logger.Info("处理用户活动",
		logger.StringField("user_id", userID),
		logger.StringField("action", action),
		logger.StringField("target_type", targetType))

	return nil
}
