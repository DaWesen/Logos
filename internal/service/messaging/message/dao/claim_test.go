package dao

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

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

// 领取必须是「行锁 + SKIP LOCKED + 租约」，否则并发消费者会重复领取，
// 客户端崩溃后消息也会永久卡在 PROCESSED。
func TestClaimPendingSQL_UsesSkipLockedAndLease(t *testing.T) {
	for _, want := range []string{
		"FOR UPDATE SKIP LOCKED",
		"lease_until IS NOT NULL AND lease_until <",
		"ORDER BY priority DESC, timestamp ASC",
		"UPDATE queue_messages",
		"RETURNING m.*",
	} {
		if !strings.Contains(claimPendingSQL, want) {
			t.Fatalf("领取 SQL 缺少 %q:\n%s", want, claimPendingSQL)
		}
	}
}

// 确认语句必须同时校验状态与领取者，避免旧消费者迟到的 ACK 覆盖新消费者。
func TestAckMessages_SQLChecksStatusAndOwner(t *testing.T) {
	db := dryRunDB(t)
	var sql string
	if err := db.Callback().Update().After("gorm:update").Register("test:capture_ack", func(tx *gorm.DB) {
		sql = tx.Statement.SQL.String()
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	repo := NewMessageRepository(db)
	err := repo.AckMessages(context.Background(), []string{"m1"}, "group-a")
	if !errors.Is(err, ErrMessageNotClaimed) {
		t.Fatalf("DryRun 下没有真实行受影响，应返回 ErrMessageNotClaimed, got %v", err)
	}
	for _, want := range []string{"status IN", "claimed_by ="} {
		if !strings.Contains(sql, want) {
			t.Fatalf("确认 SQL 缺少 %q:\n%s", want, sql)
		}
	}
}
