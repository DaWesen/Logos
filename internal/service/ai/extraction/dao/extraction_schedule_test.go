package dao

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sqlRecorder 收集 DryRun 模式下 GORM 生成的 SQL，用来守住调度查询的过滤条件。
// 这些条件一旦被「简化」，单测仍然全绿，但线上会退化成重复执行或全表扫描。
type sqlRecorder struct {
	mu   sync.Mutex
	sqls []string
}

func (r *sqlRecorder) LogMode(logger.LogLevel) logger.Interface { return r }

func (r *sqlRecorder) Info(context.Context, string, ...interface{}) {}

func (r *sqlRecorder) Warn(context.Context, string, ...interface{}) {}

func (r *sqlRecorder) Error(context.Context, string, ...interface{}) {}

func (r *sqlRecorder) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sqls = append(r.sqls, sql)
}

func (r *sqlRecorder) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.sqls, "\n")
}

// stubConnPool 让 GORM 在 DryRun 下完全不需要真实数据库；一旦有语句真的
// 尝试执行，返回可辨认的错误而不是去连 127.0.0.1。
type stubConnPool struct{}

func (stubConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("unexpected prepare in dry-run test")
}

func (stubConnPool) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, errors.New("unexpected exec in dry-run test")
}

func (stubConnPool) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, errors.New("unexpected query in dry-run test")
}

func (stubConnPool) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	panic("unexpected query row in dry-run test")
}

func newDryRunRepository(t *testing.T) (*extractionRepository, *sqlRecorder) {
	t.Helper()

	recorder := &sqlRecorder{}
	db, err := gorm.Open(
		postgres.New(postgres.Config{Conn: stubConnPool{}}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: recorder},
	)
	if err != nil {
		t.Fatalf("初始化 DryRun 数据库失败: %v", err)
	}
	return &extractionRepository{db: db}, recorder
}

func assertSQLContains(t *testing.T, sql string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL 缺少 %q，实际:\n%s", want, sql)
		}
	}
}

func TestListDueTasks_SQLFiltersPendingAndDue(t *testing.T) {
	repo, recorder := newDryRunRepository(t)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	if _, err := repo.ListDueTasks(context.Background(), now, 20); err != nil {
		t.Fatalf("DryRun 查询不应失败: %v", err)
	}

	got := recorder.joined()
	assertSQLContains(t, got, "status = 1", "scheduled_at IS NOT NULL", "scheduled_at <=", "ORDER BY scheduled_at asc", "LIMIT 20")
}

func TestClaimDueTask_SQLRequiresPendingAndDue(t *testing.T) {
	repo, recorder := newDryRunRepository(t)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	if _, err := repo.ClaimDueTask(context.Background(), "task-1", now); err != nil {
		t.Fatalf("DryRun 更新不应失败: %v", err)
	}

	got := recorder.joined()
	assertSQLContains(t, got, "UPDATE", "status = 1", "scheduled_at IS NOT NULL", "scheduled_at <=")
}

func TestMarkTaskRunning_SQLExcludesRunning(t *testing.T) {
	repo, recorder := newDryRunRepository(t)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	if _, err := repo.MarkTaskRunning(context.Background(), "task-1", now); err != nil {
		t.Fatalf("DryRun 更新不应失败: %v", err)
	}

	got := recorder.joined()
	assertSQLContains(t, got, "UPDATE", "status <> 2")
}

func TestListStaleRunningTasks_SQLOrdersByStartedAt(t *testing.T) {
	repo, recorder := newDryRunRepository(t)
	cutoff := time.Date(2026, 11, 5, 11, 30, 0, 0, time.UTC)

	if _, err := repo.ListStaleRunningTasks(context.Background(), cutoff, 50); err != nil {
		t.Fatalf("DryRun 查询不应失败: %v", err)
	}

	got := recorder.joined()
	assertSQLContains(t, got, "status = 2", "started_at IS NOT NULL", "started_at <", "ORDER BY started_at asc", "LIMIT 50")
}
