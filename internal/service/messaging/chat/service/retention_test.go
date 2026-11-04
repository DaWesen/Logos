package service

import (
	"errors"
	"testing"
	"time"
)

// TestPurgeExpiredMessageIdempotency_StopsOnPartialBatch 清理必须分批：
// 返回不足一批说明已经删完，不应该再空跑一轮。
func TestPurgeExpiredMessageIdempotency_StopsOnPartialBatch(t *testing.T) {
	repo := &stubChatRepo{idempotencyPurgeRows: []int64{1000, 1000, 400}}
	svc := newAccessService(repo)

	got := svc.purgeExpiredIdempotency(time.Now())
	if got != 2400 {
		t.Fatalf("删除行数 = %d, want 2400", got)
	}
	if repo.idempotencyPurgeCalls != 3 {
		t.Fatalf("调用次数 = %d, want 3", repo.idempotencyPurgeCalls)
	}
}

// TestPurgeExpiredMessageIdempotency_BoundedBatches 单轮必须有上限：
// 否则积压很大时，清理任务会长时间占着数据库连接和行锁。
func TestPurgeExpiredMessageIdempotency_BoundedBatches(t *testing.T) {
	rows := make([]int64, idempotencyPurgeMaxBatches+2)
	for i := range rows {
		rows[i] = idempotencyPurgeBatch
	}
	repo := &stubChatRepo{idempotencyPurgeRows: rows}
	svc := newAccessService(repo)

	got := svc.purgeExpiredIdempotency(time.Now())
	want := int64(idempotencyPurgeBatch * idempotencyPurgeMaxBatches)
	if got != want {
		t.Fatalf("删除行数 = %d, want %d", got, want)
	}
	if repo.idempotencyPurgeCalls != idempotencyPurgeMaxBatches {
		t.Fatalf("调用次数 = %d, want %d", repo.idempotencyPurgeCalls, idempotencyPurgeMaxBatches)
	}
}

// TestPurgeExpiredMessageIdempotency_ErrorStopsCycle 失败后停止本轮并保留已删除计数，
// 下一轮 ticker 再继续，不能因为一次数据库抖动就 panic 或无限重试。
func TestPurgeExpiredMessageIdempotency_ErrorStopsCycle(t *testing.T) {
	repo := &stubChatRepo{
		idempotencyPurgeRows: []int64{1000},
		idempotencyPurgeErr:  errors.New("db unavailable"),
	}
	svc := newAccessService(repo)

	if got := svc.purgeExpiredIdempotency(time.Now()); got != 0 {
		t.Fatalf("失败时删除行数 = %d, want 0", got)
	}
	if repo.idempotencyPurgeCalls != 1 {
		t.Fatalf("失败后不应继续重试，调用次数 = %d", repo.idempotencyPurgeCalls)
	}
}
