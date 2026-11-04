package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"Logos/pkg/logger"

	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	// 隔离对配置文件的依赖：Stop 会打日志，默认 logger 需要 config/config.yaml。
	logger.SetLogger(zap.NewNop())
	m.Run()
}

// TestClaimUpdateQuery_WritesLeaseOwner 领取时必须同时写入 fencing token，
// 否则租约到期后新旧副本无法区分。
func TestClaimUpdateQuery_WritesLeaseOwner(t *testing.T) {
	db := dryRunDB(t)
	repo := NewOutboxRepository().(*outboxRepositoryImpl)

	query := repo.claimUpdateQuery(context.Background(), db, []string{"a", "b"}, time.Now().Add(time.Minute), "owner-1")
	sql := strings.ToLower(query.Statement.SQL.String())
	for _, want := range []string{"update", "next_retry_at", "lease_owner"} {
		if !strings.Contains(sql, want) {
			t.Errorf("领取 SQL 缺少 %q: %s", want, sql)
		}
	}
}

// TestRenewLease_SQL 续租必须带 owner 与 status 条件：
// 仅按 id 续租会把已被其他副本接管的消息又「抢」回来。
func TestRenewLease_SQL(t *testing.T) {
	db := dryRunDB(t)
	repo := NewOutboxRepository().(*outboxRepositoryImpl)

	query := repo.renewLeaseQuery(context.Background(), db, []string{"a", "b"}, "owner-1", time.Now().Add(time.Minute))
	query.Update("next_retry_at", time.Now().Add(time.Minute))
	sql := strings.ToLower(query.Statement.SQL.String())
	for _, want := range []string{"lease_owner", "status"} {
		if !strings.Contains(sql, want) {
			t.Errorf("续租 SQL 缺少 %q: %s", want, sql)
		}
	}
}

// TestRenewLease_Empty 空 id 列表直接返回，不能生成 `IN ()` 这种非法 SQL。
func TestRenewLease_Empty(t *testing.T) {
	db := dryRunDB(t)
	repo := NewOutboxRepository()
	n, err := repo.RenewLease(context.Background(), db, nil, "owner-1", time.Now())
	if err != nil || n != 0 {
		t.Fatalf("空列表应直接返回 (0, nil)，实际 (%d, %v)", n, err)
	}
}

// TestMarkSent_RequiresOwner 状态写回必须校验 fencing token。
func TestMarkSent_RequiresOwner(t *testing.T) {
	db := dryRunDB(t)
	repo := NewOutboxRepository().(*outboxRepositoryImpl)
	query := repo.markSentQuery(context.Background(), db, "id-1", "owner-1")
	query.Update("status", StatusSent)
	sql := strings.ToLower(query.Statement.SQL.String())
	if !strings.Contains(sql, "lease_owner") {
		t.Errorf("MarkSent SQL 未校验 lease_owner: %s", sql)
	}
}

// TestLeaseWindow_CoversWorstCase 构造性不变式：
// 无论 sendTimeout 怎么配，租约窗口都必须覆盖这一块的最坏耗时。
func TestLeaseWindow_CoversWorstCase(t *testing.T) {
	for _, tc := range []struct {
		size    int
		timeout time.Duration
	}{
		{1, 10 * time.Second},
		{20, 10 * time.Second},
		{20, 30 * time.Second},
		{1, 0},
		{20, 0},
	} {
		window := leaseWindow(tc.size, tc.timeout)
		if window < dispatchLease {
			t.Errorf("leaseWindow(%d, %s) = %s，低于下限 %s", tc.size, tc.timeout, window, dispatchLease)
		}
		if tc.timeout > 0 {
			worst := time.Duration(tc.size) * tc.timeout
			if window <= worst {
				t.Errorf("leaseWindow(%d, %s) = %s，未覆盖最坏耗时 %s", tc.size, tc.timeout, window, worst)
			}
		}
	}
}

// TestDispatchChunkSizeFor_RespectsLease 单块条数必须让最坏耗时落在租约内。
func TestDispatchChunkSizeFor_RespectsLease(t *testing.T) {
	for _, timeout := range []time.Duration{0, time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 2 * time.Minute} {
		size := dispatchChunkSizeFor(timeout)
		if size < 1 || size > maxDispatchChunkSize {
			t.Fatalf("dispatchChunkSizeFor(%s) = %d，越界", timeout, size)
		}
		if timeout > 0 {
			worst := time.Duration(size) * timeout
			if worst > dispatchLease-leaseRenewMargin && size > 1 {
				t.Errorf("dispatchChunkSizeFor(%s) = %d，最坏耗时 %s 超出预算", timeout, size, worst)
			}
		}
	}
}

// TestErrLeaseLost_IsSentinel 调用方必须能用 errors.Is 判断租约丢失。
func TestErrLeaseLost_IsSentinel(t *testing.T) {
	wrapped := errors.Join(ErrLeaseLost, errors.New("detail"))
	if !errors.Is(wrapped, ErrLeaseLost) {
		t.Fatal("ErrLeaseLost 必须可被 errors.Is 识别")
	}
}

// TestNewLeaseOwner_Unique 每个 relay 实例的 token 必须不同，
// 否则多副本部署时 fencing 形同虚设。
func TestNewLeaseOwner_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		owner := newLeaseOwner()
		if owner == "" {
			t.Fatal("lease owner 不能为空")
		}
		if seen[owner] {
			t.Fatalf("lease owner 重复: %s", owner)
		}
		seen[owner] = true
	}
}

// TestStop_ReturnsWithinBound 未启动的 relay 调用 Stop 必须立即返回，
// 不能因为等待不存在的 goroutine 而挂住停机流程。
func TestStop_ReturnsWithinBound(t *testing.T) {
	r := NewRelay(nil, nil)
	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 未在有界时间内返回")
	}
}
