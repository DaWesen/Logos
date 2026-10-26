package outbox

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// dryRunDB 构造一个只生成 SQL、不建立连接的 GORM 实例：
// DryRun 跳过执行，DisableAutomaticPing 跳过握手，因此无需真实 PostgreSQL。
func dryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		postgres.New(postgres.Config{DSN: "postgres://test:test@127.0.0.1:5432/logos?sslmode=disable"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true},
	)
	if err != nil {
		t.Fatalf("初始化 DryRun GORM 失败: %v", err)
	}
	return db
}

// TestFetchPending_SQL 断言多副本领取语义：只取可投递状态、按创建时间先进先出，
// 并用 FOR UPDATE SKIP LOCKED 保证各副本不会领到同一批消息。
func TestFetchPending_SQL(t *testing.T) {
	db := dryRunDB(t)

	var (
		sql  string
		vars []interface{}
	)
	if err := db.Callback().Query().After("gorm:query").Register("test:capture_sql", func(tx *gorm.DB) {
		sql = tx.Statement.SQL.String()
		vars = append([]interface{}{}, tx.Statement.Vars...)
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	repo := NewOutboxRepository()
	if _, err := repo.FetchPending(context.Background(), db, 100); err != nil {
		t.Fatalf("FetchPending 在 DryRun 下不应报错: %v", err)
	}

	if sql == "" {
		t.Fatal("未能捕获 FetchPending 生成的 SQL")
	}

	mustContain := []string{
		"FOR UPDATE SKIP LOCKED",  // 多副本不重复领取
		"status = $1",             // 只领取 pending
		"retry_count < $2",        // 已达重试上限的行留给死信流程
		"next_retry_at IS NULL",   // 从未投递过
		"next_retry_at <= $3",     // 或退避已到期（含租约到期）
		"ORDER BY created_at ASC", // 先进先出
		"LIMIT $4",
	}
	for _, want := range mustContain {
		if !strings.Contains(sql, want) {
			t.Errorf("FetchPending SQL 缺少 %q，实际: %s", want, sql)
		}
	}

	contains := func(v interface{}) bool {
		for _, got := range vars {
			if got == v {
				return true
			}
		}
		return false
	}
	if !contains(StatusPending) {
		t.Errorf("绑定参数应包含状态 %q，实际 %v", StatusPending, vars)
	}
	if !contains(MaxRetryCount) {
		t.Errorf("绑定参数应包含重试上限 %d，实际 %v", MaxRetryCount, vars)
	}
	if !contains(100) {
		t.Errorf("绑定参数应包含批量上限 100，实际 %v", vars)
	}
}
