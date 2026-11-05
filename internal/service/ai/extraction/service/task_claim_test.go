package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/internal/service/ai/extraction/dao"
	"Logos/internal/service/ai/extraction/model"
	"Logos/pkg/cache"
	"Logos/pkg/logger"

	"go.uber.org/zap"
)

// claimRepo 只实现领取相关方法，用来验证「谁抢到执行权」的判定逻辑。
type claimRepo struct {
	dao.ExtractionRepository

	task *model.ExtractionTask

	claimCalls  int
	claimResult bool
	claimErr    error

	markCalls  int
	markResult bool
	markErr    error
}

func (r *claimRepo) GetTask(ctx context.Context, id string) (*model.ExtractionTask, error) {
	return r.task, nil
}

func (r *claimRepo) ClaimDueTask(ctx context.Context, taskID string, now time.Time) (bool, error) {
	r.claimCalls++
	return r.claimResult, r.claimErr
}

func (r *claimRepo) MarkTaskRunning(ctx context.Context, taskID string, now time.Time) (bool, error) {
	r.markCalls++
	return r.markResult, r.markErr
}

// 计划任务在同一时刻只允许一个实例执行：没抢到执行权不是错误，也不需要写结果。
func TestRunScheduledTask_SkipsWhenClaimRejected(t *testing.T) {
	repo := &claimRepo{claimResult: false}
	svc := &extractionServiceImpl{repo: repo}

	result, claimed, err := svc.RunScheduledTask(context.Background(), "task-1")

	if err != nil {
		t.Fatalf("未抢到执行权不应报错: %v", err)
	}
	if claimed || result != nil {
		t.Fatalf("claimed = %v, result = %v, want false/nil", claimed, result)
	}
	if repo.claimCalls != 1 {
		t.Fatalf("领取次数 = %d, want 1", repo.claimCalls)
	}
}

// 领取阶段的数据库错误必须上抛给调度器，不能当成「被别的实例抢走」静默跳过。
func TestRunScheduledTask_ReportsClaimError(t *testing.T) {
	repo := &claimRepo{claimErr: errors.New("db unavailable")}
	svc := &extractionServiceImpl{repo: repo}

	_, claimed, err := svc.RunScheduledTask(context.Background(), "task-1")

	if err == nil {
		t.Fatal("领取失败必须返回错误")
	}
	if claimed {
		t.Fatal("领取失败不应报告 claimed = true")
	}
}

// 手动重复触发：条件更新没抢到执行权时必须直接拒绝，不能再跑一遍模型。
func TestExecuteTask_RejectsConcurrentRun(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	original := rateLimitCacheFactory
	rateLimitCacheFactory = func() cache.Cache { return nil }
	defer func() { rateLimitCacheFactory = original }()

	repo := &claimRepo{
		task:       &model.ExtractionTask{ID: "task-1", Type: 1},
		markResult: false,
	}
	svc := &extractionServiceImpl{repo: repo}

	if _, err := svc.ExecuteTask(context.Background(), "task-1"); err == nil {
		t.Fatal("任务已在执行中时必须返回错误")
	} else if !strings.Contains(err.Error(), "正在执行中") {
		t.Fatalf("错误信息 = %q, 应说明任务正在执行", err.Error())
	}
	if repo.markCalls != 1 {
		t.Fatalf("状态抢占次数 = %d, want 1", repo.markCalls)
	}
}
