package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

func TestPaymentOrderReaper_ReapOnceUsesBatchLimit(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &stubBillingRepo{reapCount: 3}
	reaper := NewPaymentOrderReaper(repo, WithReapBatch(50))

	calledAt := time.Now()
	reaper.reapOnce(context.Background())

	if repo.reapCalls != 1 {
		t.Fatalf("回收调用次数 = %d, want 1", repo.reapCalls)
	}
	if repo.reapLimit != 50 {
		t.Fatalf("单批上限 = %d, want 50", repo.reapLimit)
	}
	// 以"当前时间"作为过期分界，避免把尚未到期的订单误回收
	if repo.reapBefore.Before(calledAt) {
		t.Fatalf("过期分界 = %v, 早于调用时间 %v", repo.reapBefore, calledAt)
	}
}

// 非法配置不应生效，否则可能出现一次回收过多数据或 0 条永不回收。
func TestPaymentOrderReaper_IgnoresInvalidOptions(t *testing.T) {
	repo := &stubBillingRepo{}
	reaper := NewPaymentOrderReaper(repo, WithReapBatch(0), WithReapInterval(0))

	reaper.reapOnce(context.Background())

	if repo.reapLimit != defaultPaymentOrderReapBatch {
		t.Fatalf("单批上限 = %d, want 默认值 %d", repo.reapLimit, defaultPaymentOrderReapBatch)
	}
	if reaper.interval != defaultPaymentOrderReapInterval {
		t.Fatalf("回收周期 = %v, want 默认值 %v", reaper.interval, defaultPaymentOrderReapInterval)
	}
}

// 回收失败不能拖垮循环：记录日志后下个周期继续重试，Stop 时能及时退出。
func TestPaymentOrderReaper_ContinuesAfterRepositoryError(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &stubBillingRepo{reapErr: errors.New("db unavailable")}
	reaper := NewPaymentOrderReaper(repo, WithReapInterval(10*time.Millisecond))

	reaper.Start()
	time.Sleep(40 * time.Millisecond)
	reaper.Stop()

	if repo.reapCalls < 2 {
		t.Fatalf("回收调用次数 = %d, want >= 2（失败后应继续重试）", repo.reapCalls)
	}
}

// Stop 必须等待循环退出（优雅停机），不能在后台任务仍写库时返回。
func TestPaymentOrderReaper_StopWaitsForLoop(t *testing.T) {
	logger.SetLogger(zap.NewNop())

	repo := &stubBillingRepo{}
	reaper := NewPaymentOrderReaper(repo, WithReapInterval(time.Hour))
	reaper.Start()

	done := make(chan struct{})
	go func() {
		reaper.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 未在超时内返回，后台循环未退出")
	}

	if repo.reapCalls != 1 {
		t.Fatalf("回收调用次数 = %d, want 1（启动时应立即回收一次）", repo.reapCalls)
	}
}
