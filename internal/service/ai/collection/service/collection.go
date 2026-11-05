package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"Logos/internal/service/ai/collection/dao"
	"Logos/internal/service/ai/collection/model"
	"Logos/pkg/auth"
	"Logos/pkg/logger"
	"Logos/pkg/mq"
	"Logos/pkg/outbox"

	"gorm.io/gorm"
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
	outboxRepo        outbox.OutboxRepository
}

var (
	ErrUnauthenticated = auth.ErrMissingUser
	ErrNotFound        = errors.New("资源不存在")
	// ErrKnowledgeUnavailable 表示知识服务未配置。
	// 采集的唯一产物就是知识条目，服务不可用时继续跑只会写出
	// collected_count=0 的 SUCCESS，把配置故障伪装成业务成功。
	ErrKnowledgeUnavailable = errors.New("知识服务不可用")
)

func NewCollectionService(repo dao.CollectionRepository, knowledgeService KnowledgeService, extractionService ExtractionService, outboxRepo outbox.OutboxRepository) CollectionService {
	return &collectionServiceImpl{
		repo:              repo,
		knowledgeService:  knowledgeService,
		extractionService: extractionService,
		outboxRepo:        outboxRepo,
	}
}

func requireUserID(ctx context.Context) (string, error) {
	userID, err := auth.RequireUserID(ctx)
	if err != nil {
		return "", ErrUnauthenticated
	}
	return userID, nil
}

func (s *collectionServiceImpl) AddDataSource(ctx context.Context, name string, dsType int32, url string, config map[string]string, description *string) (*model.DataSource, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

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
		UserID:      userID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.repo.AddDataSource(ctx, ds); err != nil {
		return nil, fmt.Errorf("添加数据源失败: %w", err)
	}
	return ds, nil
}

func (s *collectionServiceImpl) UpdateDataSource(ctx context.Context, id string, name *string, dsType *int32, url *string, config map[string]string, description *string) (*model.DataSource, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("更新数据源",
		logger.StringField("id", id))

	ds, err := s.repo.GetDataSourceWithOwnerCheck(ctx, id, userID)
	if err != nil {
		return nil, fmt.Errorf("查询数据源失败: %w", err)
	}
	if ds == nil {
		return nil, ErrNotFound
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
	userID, err := requireUserID(ctx)
	if err != nil {
		return err
	}

	logger.Info("删除数据源", logger.StringField("id", id))
	ds, err := s.repo.GetDataSourceWithOwnerCheck(ctx, id, userID)
	if err != nil {
		return fmt.Errorf("查询数据源失败: %w", err)
	}
	if ds == nil {
		return ErrNotFound
	}
	return s.repo.DeleteDataSource(ctx, id)
}

func (s *collectionServiceImpl) GetDataSource(ctx context.Context, id string) (*model.DataSource, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.GetDataSourceWithOwnerCheck(ctx, id, userID)
}

func (s *collectionServiceImpl) ListDataSources(ctx context.Context) ([]*model.DataSource, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListDataSources(ctx, userID)
}

func (s *collectionServiceImpl) CreateTask(ctx context.Context, dataSourceID string, name string, format int32, schedule *string) (*model.CollectionTask, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("创建采集任务",
		logger.StringField("name", name),
		logger.StringField("data_source_id", dataSourceID))

	ds, err := s.repo.GetDataSourceWithOwnerCheck(ctx, dataSourceID, userID)
	if err != nil {
		return nil, fmt.Errorf("查询数据源失败: %w", err)
	}
	if ds == nil {
		return nil, ErrNotFound
	}

	task := &model.CollectionTask{
		ID:           uuid.New().String(),
		DataSourceID: dataSourceID,
		Name:         name,
		Format:       int(format),
		Status:       "PENDING",
		Schedule:     schedule,
		UserID:       userID,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.repo.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("创建任务失败: %w", err)
	}
	return task, nil
}

func (s *collectionServiceImpl) UpdateTask(ctx context.Context, id string, name *string, format *int32, schedule *string) (*model.CollectionTask, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("更新采集任务", logger.StringField("id", id))

	task, err := s.repo.GetTaskWithOwnerCheck(ctx, id, userID)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return nil, ErrNotFound
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
	userID, err := requireUserID(ctx)
	if err != nil {
		return err
	}

	logger.Info("删除采集任务", logger.StringField("id", taskID))
	task, err := s.repo.GetTaskWithOwnerCheck(ctx, taskID, userID)
	if err != nil {
		return fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return ErrNotFound
	}
	return s.repo.DeleteTask(ctx, taskID)
}

func (s *collectionServiceImpl) GetTask(ctx context.Context, id string) (*model.CollectionTask, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.GetTaskWithOwnerCheck(ctx, id, userID)
}

func (s *collectionServiceImpl) ListTasks(ctx context.Context) ([]*model.CollectionTask, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListTasks(ctx, userID)
}

func (s *collectionServiceImpl) ExecuteTask(ctx context.Context, taskID string) (*model.CollectionResult, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	logger.Info("执行采集任务",
		logger.StringField("task_id", taskID))

	task, err := s.repo.GetTaskWithOwnerCheck(ctx, taskID, userID)
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	if task == nil {
		return nil, ErrNotFound
	}

	// 先校验数据源再改任务状态。
	//
	// 旧实现先写 RUNNING 再查数据源，且把查询错误丢掉：数据源被删或
	// 查库抖动时，任务会永久停在 RUNNING，doCollect 还会解引用 nil 直接
	// panic。校验前置后，失败路径不会留下任何状态变更。
	ds, err := s.repo.GetDataSourceWithOwnerCheck(ctx, task.DataSourceID, userID)
	if err != nil {
		return nil, fmt.Errorf("查询数据源失败: %w", err)
	}
	if ds == nil {
		return nil, fmt.Errorf("%w: 任务关联的数据源不存在", ErrNotFound)
	}

	now := time.Now()
	task.Status = "RUNNING"
	task.UpdatedAt = now
	if err := s.repo.UpdateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("更新任务状态失败: %w", err)
	}

	result := &model.CollectionResult{
		ID:             uuid.New().String(),
		TaskID:         taskID,
		Status:         "RUNNING",
		CollectedCount: 0,
		ProcessedCount: 0,
		StartTime:      now.Unix(),
		EndTime:        now.Unix(),
	}

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
	}
	task.UpdatedAt = time.Now()

	err = s.repo.WithTransaction(ctx, func(txRepo dao.CollectionRepository) error {
		if err := txRepo.CreateResult(ctx, result); err != nil {
			return err
		}
		if err := txRepo.UpdateTask(ctx, task); err != nil {
			return err
		}
		if result.Status == "SUCCESS" {
			if err := s.saveCollectionEventToOutbox(ctx, txRepo.DB(), task, result); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.Error("保存采集结果事务失败",
			logger.StringField("task_id", taskID),
			logger.ErrorField(err))
		// 结果没落库就不能返回 SUCCESS：调用方会以为这次采集可查，
		// 实际结果列表里什么都没有。
		return nil, fmt.Errorf("保存采集结果失败: %w", err)
	}

	return result, collectErr
}

// addKnowledge 统一处理知识写入，保证采集计数只在真正落库后递增。
func (s *collectionServiceImpl) addKnowledge(
	ctx context.Context,
	title, content, sourceType, sourceID string,
	metadata map[string]string,
) error {
	if s.knowledgeService == nil {
		return ErrKnowledgeUnavailable
	}
	if _, err := s.knowledgeService.AddKnowledge(ctx, title, content, sourceType, sourceID, metadata); err != nil {
		return fmt.Errorf("写入知识库失败: %w", err)
	}
	return nil
}

func (s *collectionServiceImpl) saveCollectionEventToOutbox(ctx context.Context, txDB *gorm.DB, task *model.CollectionTask, result *model.CollectionResult) error {
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

	if err := s.outboxRepo.SaveWithTx(ctx, txDB, "data_collection", task.ID, event); err != nil {
		logger.Error("保存采集完成事件到outbox失败",
			logger.ErrorField(err))
		return err
	}

	logger.Info("已保存采集完成事件到outbox",
		logger.StringField("topic", "data_collection"),
		logger.StringField("task_id", task.ID))
	return nil
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

		if err := s.addKnowledge(ctx, title, content, sourceType, ds.ID, metadata); err != nil {
			return fmt.Errorf("采集第 %d 条数据失败: %w", i+1, err)
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

	if err := s.addKnowledge(ctx, ds.Name, fmt.Sprintf("文档采集: 来自 %s (%s)", ds.URL, formatStr), "document", ds.ID, metadata); err != nil {
		return err
	}
	result.CollectedCount++
	result.ProcessedCount++

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

	if err := s.addKnowledge(ctx, fmt.Sprintf("API采集-%s", ds.Name), fmt.Sprintf("API数据采集结果 from %s [%s]", apiEndpoint, method), "api", ds.ID, metadata); err != nil {
		return err
	}
	result.CollectedCount++
	result.ProcessedCount++

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

	if err := s.addKnowledge(ctx, fmt.Sprintf("DB采集-%s", ds.Name), fmt.Sprintf("数据库采集结果 from %s.%s (查询: %s)", dbName, tableName, query), "database", ds.ID, metadata); err != nil {
		return err
	}
	result.CollectedCount++
	result.ProcessedCount++

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

	if err := s.addKnowledge(ctx, fmt.Sprintf("网页采集-%s", ds.Name), fmt.Sprintf("网站爬取结果 from %s", ds.URL), "website", ds.ID, metadata); err != nil {
		return err
	}
	result.CollectedCount++
	result.ProcessedCount++

	return nil
}

func (s *collectionServiceImpl) StopTask(ctx context.Context, taskID string) error {
	userID, err := requireUserID(ctx)
	if err != nil {
		return err
	}

	logger.Info("停止采集任务",
		logger.StringField("task_id", taskID))

	task, err := s.repo.GetTaskWithOwnerCheck(ctx, taskID, userID)
	if err != nil {
		return err
	}
	if task == nil {
		return ErrNotFound
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
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}

	result, err := s.repo.GetResult(ctx, id)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, ErrNotFound
	}
	task, err := s.repo.GetTaskWithOwnerCheck(ctx, result.TaskID, userID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ErrNotFound
	}
	return result, nil
}

func (s *collectionServiceImpl) ListCollectionResults(ctx context.Context, taskID string) ([]*model.CollectionResult, error) {
	userID, err := requireUserID(ctx)
	if err != nil {
		return nil, err
	}
	task, err := s.repo.GetTaskWithOwnerCheck(ctx, taskID, userID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, ErrNotFound
	}
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

// user_activity 主题的字段名，集中定义避免生产端/消费端拼错键名后各写一份。
const (
	userActivityFieldUserID     = "user_id"
	userActivityFieldAction     = "action"
	userActivityFieldTargetType = "target_type"
)

// userActivityEvent 是 user_activity 主题消费者认的载荷契约。
//
// 注意：当前生产端（messaging 服务的 saveMessageEventToOutbox）写的是通用
// 消息信封（message_id/topic/content/priority/headers/correlation_id/
// timestamp），并没有 user_id/action/target_type。也就是说这个消费者与生产端
// 存在 schema 漂移，收到的消息大概率不满足本契约。
//
// 旧实现用 `event["user_id"].(string)` 直接丢弃断言结果：字段缺失或类型不符
// 都会退化成空串，随后打一条全空的「处理用户活动」日志并 return nil。结果是
// offset 正常提交、指标记为 ok，schema 不一致在生产上完全不可见，还白白跑了
// 一条什么也没做的业务链路。
type userActivityEvent struct {
	UserID     string
	Action     string
	TargetType string
}

func (s *collectionServiceImpl) handleUserActivityEvent(msg *mq.Message) error {
	if msg == nil {
		return fmt.Errorf("%w: 用户活动事件消息为空", mq.ErrSkipMessage)
	}

	logger.Info("收到用户活动事件",
		logger.StringField("key", msg.Key),
		logger.IntField("value_len", len(msg.Value)))

	var event map[string]interface{}
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		// 连 JSON 都解析不了说明生产端写坏了：重试不会让字节变合法，但保留
		// 普通 error 走「重试 → 死信」，至少还能在死信队列里找到原始载荷。
		logger.Error("解析用户活动事件失败",
			logger.ErrorField(err),
			logger.StringField("key", msg.Key))
		return fmt.Errorf("解析用户活动事件失败: %w", err)
	}

	activity, err := parseUserActivityEvent(event)
	if err != nil {
		logger.Warn("用户活动事件不符合契约，跳过",
			logger.ErrorField(err),
			logger.StringField("key", msg.Key))
		return err
	}

	logger.Info("处理用户活动",
		logger.StringField("user_id", activity.UserID),
		logger.StringField("action", activity.Action),
		logger.StringField("target_type", activity.TargetType))

	return nil
}

// parseUserActivityEvent 按契约解析用户活动事件。
//
// 区分两类失败：
//   - 必填字段缺失/为空/类型不符：消息本身不可用，返回 mq.ErrSkipMessage，
//     消费者直接提交 offset 并记 skipped 指标，不浪费 5 次重试、不污染死信；
//   - target_type 是可选字段，缺失允许为空，但存在时类型必须正确。
func parseUserActivityEvent(event map[string]interface{}) (userActivityEvent, error) {
	var activity userActivityEvent

	// JSON 里的 null 能成功反序列化成 nil map，必须单独拒绝，否则后面按
	// 「字段缺失」处理会得到一条全空的合法事件。
	if event == nil {
		return activity, fmt.Errorf("%w: 用户活动事件载荷为 null", mq.ErrSkipMessage)
	}

	userID, err := requiredUserActivityString(event, userActivityFieldUserID)
	if err != nil {
		return activity, err
	}
	action, err := requiredUserActivityString(event, userActivityFieldAction)
	if err != nil {
		return activity, err
	}

	targetType, _, err := userActivityStringField(event, userActivityFieldTargetType)
	if err != nil {
		return activity, err
	}

	activity.UserID = userID
	activity.Action = action
	activity.TargetType = targetType
	return activity, nil
}

// requiredUserActivityString 读取必填字符串字段，并对空白值给出可定位的错误。
func requiredUserActivityString(event map[string]interface{}, name string) (string, error) {
	value, exists, err := userActivityStringField(event, name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("%w: 用户活动事件缺少字段 %s", mq.ErrSkipMessage, name)
	}

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%w: 用户活动事件字段 %s 为空", mq.ErrSkipMessage, name)
	}
	return trimmed, nil
}

// userActivityStringField 按事件契约读取字符串字段，返回 (值, 字段是否存在, 错误)。
//
// 缺失与类型错误必须区分：缺失可以交给调用方决定是否必填，而类型错误说明
// 生产者和消费者对 schema 的理解已经不一致，重试不会自愈。
func userActivityStringField(event map[string]interface{}, name string) (string, bool, error) {
	raw, exists := event[name]
	if !exists || raw == nil {
		return "", false, nil
	}

	value, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("%w: 用户活动事件字段 %s 期望 string，实际为 %T",
			mq.ErrSkipMessage, name, raw)
	}
	return value, true, nil
}
