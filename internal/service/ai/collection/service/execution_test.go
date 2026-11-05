package service

import (
	"context"
	"errors"
	"testing"

	"Logos/internal/service/ai/collection/dao"
	"Logos/internal/service/ai/collection/model"
	"Logos/pkg/outbox"

	"gorm.io/gorm"
)

type fakeKnowledge struct {
	calls int
	err   error
	// failAt > 0 时只在该次调用返回 err，用于模拟中途失败。
	failAt int
}

func (f *fakeKnowledge) AddKnowledge(
	ctx context.Context,
	title, content, sourceType, sourceID string,
	metadata map[string]string,
) (interface{ GetID() string }, error) {
	f.calls++
	if f.err != nil && (f.failAt == 0 || f.calls == f.failAt) {
		return nil, f.err
	}
	return fakeKnowledgeID{}, nil
}

type fakeKnowledgeID struct{}

func (fakeKnowledgeID) GetID() string { return "knowledge-1" }

type recordingOutbox struct {
	outbox.OutboxRepository

	saved int
	err   error
}

func (r *recordingOutbox) SaveWithTx(ctx context.Context, tx *gorm.DB, topic, key string, value interface{}) error {
	r.saved++
	return r.err
}

type execRepo struct {
	dao.CollectionRepository

	task *model.CollectionTask
	ds   *model.DataSource

	dsErr error
	// failUpdateAt > 0 时，第 N 次 UpdateTask 返回 updateErr。
	failUpdateAt int
	updateErr    error
	txErr        error

	updates []string
	results []model.CollectionResult
}

func (r *execRepo) GetTaskWithOwnerCheck(ctx context.Context, id, userID string) (*model.CollectionTask, error) {
	if r.task == nil || r.task.UserID != userID {
		return nil, nil
	}
	return r.task, nil
}

func (r *execRepo) GetDataSourceWithOwnerCheck(ctx context.Context, id, userID string) (*model.DataSource, error) {
	if r.dsErr != nil {
		return nil, r.dsErr
	}
	if r.ds == nil || r.ds.UserID != userID {
		return nil, nil
	}
	return r.ds, nil
}

func (r *execRepo) UpdateTask(ctx context.Context, task *model.CollectionTask) error {
	r.updates = append(r.updates, task.Status)
	if r.failUpdateAt > 0 && len(r.updates) == r.failUpdateAt {
		return r.updateErr
	}
	return nil
}

func (r *execRepo) CreateResult(ctx context.Context, result *model.CollectionResult) error {
	r.results = append(r.results, *result)
	return nil
}

func (r *execRepo) WithTransaction(ctx context.Context, fn func(txRepo dao.CollectionRepository) error) error {
	if r.txErr != nil {
		return r.txErr
	}
	return fn(r)
}

func (r *execRepo) DB() *gorm.DB { return nil }

func newExecRepo(status string) *execRepo {
	return &execRepo{
		task: &model.CollectionTask{
			ID: "task-1", DataSourceID: "ds-1", UserID: "user-1", Status: status,
		},
		ds: &model.DataSource{ID: "ds-1", UserID: "user-1", Type: 2, Name: "doc"},
	}
}

func TestExecuteTask_MissingDataSourceDoesNotTouchTask(t *testing.T) {
	repo := newExecRepo("PENDING")
	repo.ds = nil
	svc := &collectionServiceImpl{repo: repo}

	_, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("数据源不存在应返回 ErrNotFound，实际 %v", err)
	}
	if len(repo.updates) != 0 {
		t.Fatalf("校验失败后不应改任务状态，实际 %v", repo.updates)
	}
}

func TestExecuteTask_DataSourceQueryErrorIsNotSwallowed(t *testing.T) {
	repo := newExecRepo("PENDING")
	repo.dsErr = errors.New("db down")
	svc := &collectionServiceImpl{repo: repo}

	_, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if err == nil {
		t.Fatal("数据源查询失败应返回错误")
	}
	if !errors.Is(err, repo.dsErr) {
		t.Fatalf("错误应可 unwrap 到原始错误，实际 %v", err)
	}
	if len(repo.updates) != 0 {
		t.Fatalf("校验失败后不应改任务状态，实际 %v", repo.updates)
	}
}

func TestExecuteTask_RunningUpdateFailureStopsCollection(t *testing.T) {
	repo := newExecRepo("PENDING")
	repo.failUpdateAt = 1
	repo.updateErr = errors.New("write conflict")
	knowledge := &fakeKnowledge{}
	svc := &collectionServiceImpl{repo: repo, knowledgeService: knowledge}

	_, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if err == nil {
		t.Fatal("置 RUNNING 失败应返回错误")
	}
	if knowledge.calls != 0 {
		t.Fatalf("状态未落库就不应开始采集，实际调用 %d 次", knowledge.calls)
	}
	if len(repo.results) != 0 {
		t.Fatalf("不应写入结果，实际 %d 条", len(repo.results))
	}
}

func TestExecuteTask_KnowledgeFailureIsReportedAsFailed(t *testing.T) {
	repo := newExecRepo("PENDING")
	knowledge := &fakeKnowledge{err: errors.New("knowledge service timeout")}
	svc := &collectionServiceImpl{repo: repo, knowledgeService: knowledge}

	result, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if err == nil {
		t.Fatal("知识写入失败应返回错误，而不是 SUCCESS")
	}
	if result == nil {
		t.Fatal("失败也应返回已落库的结果")
	}
	if result.Status != "FAILED" {
		t.Fatalf("result.Status = %q, want FAILED", result.Status)
	}
	if result.CollectedCount != 0 || result.ProcessedCount != 0 {
		t.Fatalf("写入失败不应计入采集数，实际 collected=%d processed=%d",
			result.CollectedCount, result.ProcessedCount)
	}
	if len(repo.updates) != 2 || repo.updates[1] != "FAILED" {
		t.Fatalf("任务状态应为 RUNNING -> FAILED，实际 %v", repo.updates)
	}
	if len(repo.results) != 1 || repo.results[0].Status != "FAILED" {
		t.Fatalf("失败结果应落库，实际 %#v", repo.results)
	}
}

func TestExecuteTask_MissingKnowledgeServiceIsNotSuccess(t *testing.T) {
	repo := newExecRepo("PENDING")
	svc := &collectionServiceImpl{repo: repo}

	result, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if !errors.Is(err, ErrKnowledgeUnavailable) {
		t.Fatalf("知识服务缺失应返回 ErrKnowledgeUnavailable，实际 %v", err)
	}
	if result == nil || result.Status != "FAILED" {
		t.Fatalf("应返回 FAILED 结果，实际 %#v", result)
	}
}

func TestExecuteTask_PartialCollectionCountsOnlyPersistedItems(t *testing.T) {
	repo := newExecRepo("PENDING")
	repo.ds.Type = 1
	knowledge := &fakeKnowledge{}
	svc := &collectionServiceImpl{repo: repo, knowledgeService: knowledge}

	// 内部系统数据源固定采集 10 条；让第 4 条失败。
	knowledge.err = errors.New("boom")
	knowledge.failAt = 4
	result, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if err == nil {
		t.Fatal("第 4 条失败应返回错误")
	}
	if result.CollectedCount != 3 || result.ProcessedCount != 3 {
		t.Fatalf("只应统计成功落库的 3 条，实际 collected=%d processed=%d",
			result.CollectedCount, result.ProcessedCount)
	}
	if knowledge.calls != 4 {
		t.Fatalf("应在第 4 条失败后停止，实际调用 %d 次", knowledge.calls)
	}
}

func TestExecuteTask_PersistenceFailureIsNotReportedAsSuccess(t *testing.T) {
	repo := newExecRepo("PENDING")
	repo.txErr = errors.New("commit failed")
	svc := &collectionServiceImpl{repo: repo, knowledgeService: &fakeKnowledge{}}

	result, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if err == nil {
		t.Fatal("结果落库失败不应返回成功")
	}
	if result != nil {
		t.Fatalf("落库失败时不应返回可查询的结果，实际 %#v", result)
	}
}

func TestExecuteTask_SuccessWritesResultAndOutbox(t *testing.T) {
	repo := newExecRepo("PENDING")
	outboxRepo := &recordingOutbox{}
	svc := &collectionServiceImpl{
		repo:             repo,
		knowledgeService: &fakeKnowledge{},
		outboxRepo:       outboxRepo,
	}

	result, err := svc.ExecuteTask(collectionUserCtx("user-1"), "task-1")
	if err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	if result.Status != "SUCCESS" {
		t.Fatalf("result.Status = %q, want SUCCESS", result.Status)
	}
	if len(repo.results) != 1 || repo.results[0].Status != "SUCCESS" {
		t.Fatalf("成功结果应落库，实际 %#v", repo.results)
	}
	if outboxRepo.saved != 1 {
		t.Fatalf("成功采集应写一条 outbox 事件，实际 %d 条", outboxRepo.saved)
	}
}
