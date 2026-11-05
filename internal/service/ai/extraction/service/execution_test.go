package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/ai/extraction/dao"
	"Logos/internal/service/ai/extraction/model"
	"Logos/pkg/cache"
	"Logos/pkg/mq"
	"Logos/pkg/outbox"

	"gorm.io/gorm"
)

// noLimitCache 让限流计数直接放行，避免单测连接真实 Redis。
type noLimitCache struct {
	cache.Cache
}

func (noLimitCache) IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	return 1, nil
}

func (noLimitCache) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return nil
}

func withNoLimitCache(t *testing.T) {
	t.Helper()
	original := rateLimitCacheFactory
	rateLimitCacheFactory = func() cache.Cache { return noLimitCache{} }
	t.Cleanup(func() { rateLimitCacheFactory = original })
}

// recordingOutbox 记录事务内写出的 outbox 事件，避免单测触碰真实数据库。
type recordingOutbox struct {
	outbox.OutboxRepository

	topics []string
	keys   []string
}

func (r *recordingOutbox) SaveWithTx(ctx context.Context, tx *gorm.DB, topic, key string, value interface{}) error {
	r.topics = append(r.topics, topic)
	r.keys = append(r.keys, key)
	return nil
}

type execRepo struct {
	dao.ExtractionRepository

	task *model.ExtractionTask

	updateErr error
	// failUpdateAt > 0 时，第 N 次 UpdateTask 返回 updateErr。
	failUpdateAt int
	createErr error
	txErr     error

	updates []int
	results []model.ExtractionResult
	created []*model.ExtractionTask
}

func (r *execRepo) GetTask(ctx context.Context, id string) (*model.ExtractionTask, error) {
	if r.task == nil || r.task.ID != id {
		return nil, nil
	}
	return r.task, nil
}

func (r *execRepo) UpdateTask(ctx context.Context, task *model.ExtractionTask) error {
	r.updates = append(r.updates, task.Status)
	if r.updateErr != nil && (r.failUpdateAt == 0 || len(r.updates) == r.failUpdateAt) {
		return r.updateErr
	}
	return nil
}

func (r *execRepo) CreateResult(ctx context.Context, result *model.ExtractionResult) error {
	r.results = append(r.results, *result)
	return r.createErr
}

func (r *execRepo) CreateTask(ctx context.Context, task *model.ExtractionTask) error {
	r.created = append(r.created, task)
	r.task = task
	return nil
}

func (r *execRepo) WithTransaction(ctx context.Context, fn func(txRepo dao.ExtractionRepository) error) error {
	if r.txErr != nil {
		return r.txErr
	}
	return fn(r)
}

func (r *execRepo) DB() *gorm.DB { return nil }

func newExecRepo() *execRepo {
	return &execRepo{task: &model.ExtractionTask{
		ID: "task-1", Type: 1, Status: 1, DataID: "ds-1", DataType: "data_source",
	}}
}

func TestExecuteTask_RunningUpdateFailureStopsExtraction(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	repo.updateErr = errors.New("write conflict")
	svc := &extractionServiceImpl{repo: repo}

	_, err := svc.ExecuteTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("置 RUNNING 失败应返回错误")
	}
	if len(repo.results) != 0 {
		t.Fatalf("状态未落库就不应执行抽取，实际写入 %d 条结果", len(repo.results))
	}
}

func TestExecuteTask_FailureResultIsPersisted(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	repo.task.Type = 99
	svc := &extractionServiceImpl{repo: repo}

	result, err := svc.ExecuteTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("不支持的任务类型应返回错误")
	}
	if result == nil || result.Status != 4 {
		t.Fatalf("失败结果应返回，实际 %#v", result)
	}
	if len(repo.results) != 1 || repo.results[0].Status != 4 {
		t.Fatalf("失败结果必须落库，实际 %#v", repo.results)
	}
	// repo.task 是同一指针，直接读字段无法区分「只在内存里改了」和
	// 「真的落库了」，所以校验 UpdateTask 的调用序列。
	if len(repo.updates) != 2 || repo.updates[1] != 4 {
		t.Fatalf("任务失败状态应落库，实际更新序列 %v", repo.updates)
	}
	if repo.task.EndedAt == nil {
		t.Fatal("失败任务应写入结束时间")
	}
}

func TestExecuteTask_FailureStatusUpdateErrorIsJoined(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	repo.task.Type = 99
	repo.updateErr = errors.New("write conflict")
	repo.failUpdateAt = 2
	svc := &extractionServiceImpl{repo: repo}

	_, err := svc.ExecuteTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("失败状态落库失败应返回错误")
	}
	if !errors.Is(err, repo.updateErr) {
		t.Fatalf("错误应包含状态落库失败原因，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "不支持的任务类型") {
		t.Fatalf("错误应保留原始抽取失败原因，实际 %v", err)
	}
}

func TestExecuteTask_FailurePersistErrorIsJoined(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	repo.task.Type = 99
	repo.createErr = errors.New("insert failed")
	svc := &extractionServiceImpl{repo: repo}

	_, err := svc.ExecuteTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("结果落库失败应返回错误")
	}
	if !errors.Is(err, repo.createErr) {
		t.Fatalf("错误应包含落库失败原因，实际 %v", err)
	}
}

func TestExecuteTask_SuccessTransactionFailureIsNotSuccess(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	repo.txErr = errors.New("commit failed")
	svc := &extractionServiceImpl{repo: repo}

	result, err := svc.ExecuteTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("事务提交失败不应返回成功")
	}
	if result != nil {
		t.Fatalf("提交失败时不应返回可查询结果，实际 %#v", result)
	}
}

func TestHandleCollectionEvent_UsesDataSourceID(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	repo.task = nil
	outboxRepo := &recordingOutbox{}
	svc := &extractionServiceImpl{repo: repo, outboxRepo: outboxRepo}

	payload, _ := json.Marshal(map[string]interface{}{
		"data_source_id":       "ds-42",
		"collection_result_id": "result-1",
		"status":               "SUCCESS",
	})

	if err := svc.handleCollectionEvent(&mq.Message{Value: payload}); err != nil {
		t.Fatalf("采集成功事件应能建出抽取任务，实际 %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("应创建 1 个抽取任务，实际 %d 个", len(repo.created))
	}
	if repo.created[0].DataID != "ds-42" {
		t.Fatalf("任务应使用 data_source_id 作为数据 ID，实际 %q", repo.created[0].DataID)
	}
	if repo.created[0].DataType != "data_source" {
		t.Fatalf("任务数据类型应为 data_source，实际 %q", repo.created[0].DataType)
	}
	if len(outboxRepo.topics) != 2 {
		t.Fatalf("成功抽取应写出 2 条 outbox 事件，实际 %d 条", len(outboxRepo.topics))
	}
}

func TestHandleCollectionEvent_RejectsMissingDataSource(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	svc := &extractionServiceImpl{repo: repo}

	payload, _ := json.Marshal(map[string]interface{}{"status": "SUCCESS"})

	if err := svc.handleCollectionEvent(&mq.Message{Value: payload}); err == nil {
		t.Fatal("缺少 data_source_id 应返回错误")
	}
	if len(repo.created) != 0 {
		t.Fatalf("不应创建无数据源的抽取任务，实际 %d 个", len(repo.created))
	}
}

func TestHandleCollectionEvent_SkipsFailedCollection(t *testing.T) {
	withNoLimitCache(t)
	repo := newExecRepo()
	svc := &extractionServiceImpl{repo: repo}

	payload, _ := json.Marshal(map[string]interface{}{
		"data_source_id": "ds-42",
		"status":         "FAILED",
	})

	if err := svc.handleCollectionEvent(&mq.Message{Value: payload}); err != nil {
		t.Fatalf("采集失败事件应被跳过，实际 %v", err)
	}
	if len(repo.created) != 0 {
		t.Fatalf("采集失败不应创建抽取任务，实际 %d 个", len(repo.created))
	}
}
