package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/internal/service/ai/extraction/dao"
	"Logos/internal/service/ai/extraction/model"
	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// schedulerRepo 只实现调度器用到的方法，其余通过内嵌接口占位，
// 单测不必为十几个无关方法写样板，也不会真的连数据库。
type schedulerRepo struct {
	dao.ExtractionRepository

	due       []*model.ExtractionTask
	dueErr    error
	dueCalls  int
	dueLimit  int
	dueBefore time.Time

	stale       []*model.ExtractionTask
	staleErr    error
	staleLimit  int
	staleCutoff time.Time

	failErr      error
	failSucceeds map[string]bool
	failReasons  map[string]string
	failCutoffs  map[string]time.Time
}

func (r *schedulerRepo) ListDueTasks(ctx context.Context, now time.Time, limit int) ([]*model.ExtractionTask, error) {
	r.dueCalls++
	r.dueBefore = now
	r.dueLimit = limit
	if r.dueErr != nil {
		return nil, r.dueErr
	}
	return r.due, nil
}

func (r *schedulerRepo) ListStaleRunningTasks(ctx context.Context, cutoff time.Time, limit int) ([]*model.ExtractionTask, error) {
	r.staleCutoff = cutoff
	r.staleLimit = limit
	if r.staleErr != nil {
		return nil, r.staleErr
	}
	return r.stale, nil
}

func (r *schedulerRepo) FailStaleTask(ctx context.Context, taskID string, cutoff time.Time, reason string) (bool, error) {
	if r.failErr != nil {
		return false, r.failErr
	}
	if r.failReasons == nil {
		r.failReasons = make(map[string]string)
	}
	if r.failCutoffs == nil {
		r.failCutoffs = make(map[string]time.Time)
	}
	r.failReasons[taskID] = reason
	r.failCutoffs[taskID] = cutoff
	return r.failSucceeds[taskID], nil
}

// executorStub 记录调度器的调用顺序，并可指定单条任务的结果。
type executorStub struct {
	order      []string
	errs       map[string]error
	notClaimed map[string]bool
}

func (e *executorStub) RunScheduledTask(ctx context.Context, taskID string) (*model.ExtractionResult, bool, error) {
	e.order = append(e.order, taskID)
	if err := e.errs[taskID]; err != nil {
		return nil, true, err
	}
	if e.notClaimed[taskID] {
		return nil, false, nil
	}
	return &model.ExtractionResult{TaskID: taskID}, true, nil
}

func TestTaskScheduler_RunDueOnceExecutesBatch(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &schedulerRepo{due: []*model.ExtractionTask{{ID: "t1"}, {ID: "t2"}}}
	executor := &executorStub{}
	scheduler := NewTaskScheduler(repo, executor, WithDueTaskBatch(7))

	scheduler.runOnce(context.Background())

	if repo.dueLimit != 7 {
		t.Fatalf("单批上限 = %d, want 7", repo.dueLimit)
	}
	if got := executor.order; len(got) != 2 || got[0] != "t1" || got[1] != "t2" {
		t.Fatalf("执行顺序 = %v, want [t1 t2]", got)
	}
}

// 一条任务失败不能阻断同批其余任务：它们已经各自落库，没有事务把它们绑在一起。
func TestTaskScheduler_RunDueOnceContinuesAfterTaskError(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &schedulerRepo{due: []*model.ExtractionTask{{ID: "t1"}, {ID: "t2"}, {ID: "t3"}}}
	executor := &executorStub{errs: map[string]error{"t2": errors.New("模型超时")}}
	scheduler := NewTaskScheduler(repo, executor)

	scheduler.runOnce(context.Background())

	if got := executor.order; len(got) != 3 {
		t.Fatalf("执行任务数 = %d (%v), want 3（失败任务不得中断整批）", len(got), got)
	}
}

// 多实例同时扫到同一条任务时，没抢到执行权的一方必须静默跳过，而不是记一次失败。
func TestTaskScheduler_RunDueOnceSkipsTaskClaimedElsewhere(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &schedulerRepo{due: []*model.ExtractionTask{{ID: "t1"}, {ID: "t2"}}}
	executor := &executorStub{notClaimed: map[string]bool{"t1": true}}
	scheduler := NewTaskScheduler(repo, executor)

	scheduler.runOnce(context.Background())

	if got := executor.order; len(got) != 2 {
		t.Fatalf("执行任务数 = %d (%v), want 2（未抢到的任务应跳过，不影响后续）", len(got), got)
	}
}

// 扫描失败时不能继续执行任何任务：列表内容不可信，宁可不跑也不乱跑。
func TestTaskScheduler_RunDueOnceSkipsExecutionWhenListFails(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &schedulerRepo{dueErr: errors.New("db unavailable")}
	executor := &executorStub{}
	scheduler := NewTaskScheduler(repo, executor)

	scheduler.runOnce(context.Background())

	if len(executor.order) != 0 {
		t.Fatalf("扫描失败仍执行了 %v", executor.order)
	}
}

// 卡死任务（状态一直停在运行中）必须被回收，否则用户再也点不动它。
func TestTaskScheduler_ReapsStaleRunningTask(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &schedulerRepo{
		stale:        []*model.ExtractionTask{{ID: "stale-1"}},
		failSucceeds: map[string]bool{"stale-1": true},
	}
	scheduler := NewTaskScheduler(repo, &executorStub{}, WithStaleTaskAfter(time.Minute))

	before := time.Now()
	scheduler.runOnce(context.Background())

	if _, ok := repo.failReasons["stale-1"]; !ok {
		t.Fatal("未尝试回收卡死任务")
	}
	if repo.failReasons["stale-1"] == "" {
		t.Fatal("回收失败必须写入原因，否则用户只看到失败状态")
	}
	wantCutoff := before.Add(-time.Minute)
	if diff := repo.staleCutoff.Sub(wantCutoff); diff < -time.Second || diff > time.Second {
		t.Fatalf("超时分界 = %v, want 约 %v", repo.staleCutoff, wantCutoff)
	}
	if repo.staleLimit != defaultDueTaskBatch {
		t.Fatalf("回收批量 = %d, want %d", repo.staleLimit, defaultDueTaskBatch)
	}
}

// 非法配置不能生效：0 会让 ticker panic 或让批量永远为空。
func TestTaskScheduler_IgnoresInvalidOptions(t *testing.T) {
	scheduler := NewTaskScheduler(&schedulerRepo{}, &executorStub{},
		WithDueTaskInterval(0), WithDueTaskBatch(0), WithStaleTaskAfter(0), WithDueTaskTimeout(0))

	if scheduler.interval != defaultDueTaskScanInterval {
		t.Fatalf("扫描周期 = %v, want %v", scheduler.interval, defaultDueTaskScanInterval)
	}
	if scheduler.batch != defaultDueTaskBatch {
		t.Fatalf("单批上限 = %d, want %d", scheduler.batch, defaultDueTaskBatch)
	}
	if scheduler.staleAfter != defaultStaleTaskAfter {
		t.Fatalf("超时阈值 = %v, want %v", scheduler.staleAfter, defaultStaleTaskAfter)
	}
	if scheduler.timeout != defaultDueTaskTimeout {
		t.Fatalf("单任务超时 = %v, want %v", scheduler.timeout, defaultDueTaskTimeout)
	}
}

// 启动立即跑一轮、失败后继续下一轮、Stop 能及时退出（优雅停机）。
func TestTaskScheduler_StartRunsImmediatelyAndRetriesAfterFailure(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &schedulerRepo{dueErr: errors.New("db unavailable")}
	scheduler := NewTaskScheduler(repo, &executorStub{}, WithDueTaskInterval(10*time.Millisecond))

	scheduler.Start()
	time.Sleep(50 * time.Millisecond)
	scheduler.Stop()

	if repo.dueCalls < 2 {
		t.Fatalf("扫描次数 = %d, want >= 2（启动立即执行，失败后继续重试）", repo.dueCalls)
	}
}

func TestTaskScheduler_StopReturnsWhenIdle(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	scheduler := NewTaskScheduler(&schedulerRepo{}, &executorStub{}, WithDueTaskInterval(time.Hour))
	scheduler.Start()

	done := make(chan struct{})
	go func() {
		scheduler.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 未在超时内返回，后台循环未退出")
	}
}
