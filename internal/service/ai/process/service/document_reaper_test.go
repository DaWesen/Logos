package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestDocumentReaper_UsesCutoffAndBatch 回收必须同时满足两个边界：
// 只看超过 staleAfter 的记录，且单批有条数上限。
func TestDocumentReaper_UsesCutoffAndBatch(t *testing.T) {
	repo := newFakeProcessRepo()
	repo.reapCount = 3

	staleAfter := 10 * time.Minute
	reaper := NewStaleDocumentReaper(repo,
		WithDocumentReapBatch(50),
		WithDocumentStaleAfter(staleAfter),
		WithDocumentReapInterval(time.Hour),
	)

	before := time.Now().Add(-staleAfter)
	reaper.reapOnce(context.Background())
	after := time.Now().Add(-staleAfter)

	cutoff, limit, reason := repo.reapSnapshot()
	if cutoff.Before(before.Add(-time.Second)) || cutoff.After(after.Add(time.Second)) {
		t.Fatalf("cutoff = %v，期望在 [%v, %v] 之间", cutoff, before, after)
	}
	if limit != 50 {
		t.Fatalf("batch = %d，期望 50", limit)
	}
	if reason == "" {
		t.Fatal("回收原因不能为空，用户需要知道为什么失败")
	}
}

// TestDocumentReaper_ContinuesAfterError 回收失败不能 panic，下一周期继续。
func TestDocumentReaper_ContinuesAfterError(t *testing.T) {
	repo := newFakeProcessRepo()
	repo.reapErr = errors.New("db down")

	reaper := NewStaleDocumentReaper(repo, WithDocumentReapInterval(10*time.Millisecond))
	reaper.reapOnce(context.Background())
	if got := repo.reapCalls.Load(); got != 1 {
		t.Fatalf("调用次数 = %d，期望 1", got)
	}
}

// TestDocumentReaper_StartStops 启动后立即执行一次，Stop 必须等待循环退出。
func TestDocumentReaper_StartStops(t *testing.T) {
	repo := newFakeProcessRepo()
	reaper := NewStaleDocumentReaper(repo, WithDocumentReapInterval(10*time.Millisecond))
	reaper.Start()

	deadline := time.Now().Add(time.Second)
	for repo.reapCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := repo.reapCalls.Load(); got < 2 {
		t.Fatalf("回收次数 = %d，期望至少 2（启动一次 + ticker 一次）", got)
	}

	done := make(chan struct{})
	go func() {
		reaper.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop 未在超时内退出")
	}
}
