package service

import (
	"context"
	"sync"
	"time"

	"Logos/internal/service/ai/extraction/dao"
	"Logos/internal/service/ai/extraction/model"
	"Logos/pkg/logger"
	"Logos/pkg/obs"
)

const (
	defaultDueTaskScanInterval = 30 * time.Second
	defaultDueTaskBatch        = 20
	defaultStaleTaskAfter      = 30 * time.Minute
	defaultDueTaskTimeout      = 5 * time.Minute
)

const staleExtractionTaskReason = "执行超时：超过 30 分钟未完成，可能是进程重启或任务中断，请重新执行"

// ScheduledTaskExecutor 是调度器需要的最小能力集。
// 刻意不依赖完整的 ExtractionService：调度器只关心「领取 + 执行」，
// 单测里也就不必实现十几个无关方法。
type ScheduledTaskExecutor interface {
	RunScheduledTask(ctx context.Context, taskID string) (*model.ExtractionResult, bool, error)
}

// TaskScheduler 周期性执行到期的抽取任务，并回收卡死的运行中任务。
//
// 任务表里一直有 scheduled_at 字段，创建/更新接口也会校验并写入，
// 但在它之前没有任何组件消费这个字段：用户设了计划时间，任务永远停在
// 「待执行」，接口还返回创建成功——字段存在不等于功能存在。
// 执行侧与 PaymentOrderReaper / StaleDocumentReaper 同一套形态：
// 有界批量、串行执行、单条失败不影响整批、Stop 等当前批结束。
type TaskScheduler struct {
	repo     dao.ExtractionRepository
	executor ScheduledTaskExecutor
	interval time.Duration
	batch    int
	timeout  time.Duration

	staleAfter time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type TaskSchedulerOption func(*TaskScheduler)

// WithDueTaskInterval 设置扫描周期。
func WithDueTaskInterval(d time.Duration) TaskSchedulerOption {
	return func(s *TaskScheduler) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithDueTaskBatch 设置单批上限，避免一次把大量到期任务同时压给模型。
func WithDueTaskBatch(n int) TaskSchedulerOption {
	return func(s *TaskScheduler) {
		if n > 0 {
			s.batch = n
		}
	}
}

// WithStaleTaskAfter 设置运行中任务的超时阈值，必须明显大于单个任务的最长耗时。
func WithStaleTaskAfter(d time.Duration) TaskSchedulerOption {
	return func(s *TaskScheduler) {
		if d > 0 {
			s.staleAfter = d
		}
	}
}

// WithDueTaskTimeout 设置单个任务的执行上限，防止一次调用把整轮扫描挂住。
func WithDueTaskTimeout(d time.Duration) TaskSchedulerOption {
	return func(s *TaskScheduler) {
		if d > 0 {
			s.timeout = d
		}
	}
}

func NewTaskScheduler(repo dao.ExtractionRepository, executor ScheduledTaskExecutor, opts ...TaskSchedulerOption) *TaskScheduler {
	s := &TaskScheduler{
		repo:       repo,
		executor:   executor,
		interval:   defaultDueTaskScanInterval,
		batch:      defaultDueTaskBatch,
		timeout:    defaultDueTaskTimeout,
		staleAfter: defaultStaleTaskAfter,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s
}

// Start 非阻塞启动：先立即跑一轮（补上停机期间到期的任务），随后按周期触发。
func (s *TaskScheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		s.runOnce(s.ctx)

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.runOnce(s.ctx)
			}
		}
	}()
}

// Stop 停止扫描并等待当前批执行完毕：正在跑的任务不能在半路被丢弃。
func (s *TaskScheduler) Stop() {
	s.cancel()
	s.wg.Wait()
}

// runOnce 先回收卡死任务，再执行到期任务；两步互不影响，任何一步失败都不阻断另一步。
func (s *TaskScheduler) runOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.reapStaleOnce(ctx)
	s.runDueOnce(ctx)
}

func (s *TaskScheduler) runDueOnce(ctx context.Context) {
	due, err := s.repo.ListDueTasks(ctx, time.Now(), s.batch)
	if err != nil {
		obs.RecordExtractionScheduleScanFailure(ctx)
		logger.Warn("扫描到期抽取任务失败", logger.ErrorField(err))
		return
	}
	if len(due) == 0 {
		return
	}
	if len(due) == s.batch {
		// 取满一批说明可能还有积压：下一轮会继续取，但积压本身要留痕，
		// 否则「调度器很忙」这件事只有数据库知道。
		logger.Warn("到期抽取任务达到单批上限，可能存在积压",
			logger.IntField("batch", s.batch))
	}

	var succeeded, failed, skipped int
	for _, task := range due {
		if ctx.Err() != nil {
			// 停机信号已到：不再领取新任务，没领的仍然是待执行，下个实例会接着跑。
			break
		}

		// 单个任务用「脱离取消 + 独立超时」的 ctx：优雅停机时在途任务继续跑完，
		// 不会被 cancel 打断成一条「失败」记录，新实例也不会重复领取（状态已是运行中）。
		// 超时上限保证 Stop 不会被一个卡死的模型调用无限拖住。
		taskCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.timeout)
		_, claimed, err := s.executor.RunScheduledTask(taskCtx, task.ID)
		cancel()
		switch {
		case !claimed && err == nil:
			// 被其他实例领走或已被用户取消，属于正常竞争结果。
			skipped++
		case err != nil:
			// 单条失败只影响自己：任务已落失败结果，同批其余任务继续执行。
			failed++
			logger.Warn("执行计划抽取任务失败",
				logger.StringField("task_id", task.ID),
				logger.ErrorField(err))
		default:
			succeeded++
		}
	}

	obs.RecordExtractionScheduledRun(ctx, "success", int64(succeeded))
	obs.RecordExtractionScheduledRun(ctx, "failure", int64(failed))
	obs.RecordExtractionScheduledRun(ctx, "skipped", int64(skipped))
	logger.Info("执行到期抽取任务",
		logger.IntField("succeeded", succeeded),
		logger.IntField("failed", failed),
		logger.IntField("skipped", skipped))
}

func (s *TaskScheduler) reapStaleOnce(ctx context.Context) {
	cutoff := time.Now().Add(-s.staleAfter)
	stale, err := s.repo.ListStaleRunningTasks(ctx, cutoff, s.batch)
	if err != nil {
		obs.RecordExtractionScheduleScanFailure(ctx)
		logger.Warn("扫描卡死抽取任务失败", logger.ErrorField(err))
		return
	}

	reaped := 0
	for _, task := range stale {
		if ctx.Err() != nil {
			break
		}
		ok, err := s.repo.FailStaleTask(ctx, task.ID, cutoff, staleExtractionTaskReason)
		if err != nil {
			logger.Warn("回收卡死抽取任务失败",
				logger.StringField("task_id", task.ID),
				logger.ErrorField(err))
			continue
		}
		if ok {
			reaped++
		}
	}
	if reaped == 0 {
		return
	}

	obs.RecordExtractionScheduledRun(ctx, "stale_reaped", int64(reaped))
	logger.Info("回收卡死抽取任务",
		logger.IntField("count", reaped),
		logger.StringField("stale_after", s.staleAfter.String()))
}
