package dao

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// 幂等闸门只允许两种认领：从未处理过的 PENDING，以及上一个处理者
// 已经失联（租约过期）的 PROCESSING。PROCESSED 是终态，回放必须被挡下。
func TestClaimForProcessingSQL_AllowsOnlyPendingOrExpiredProcessing(t *testing.T) {
	for _, want := range []string{
		"UPDATE queue_messages",
		"status = 'PROCESSING'",
		"status = 'PENDING'",
		"status = 'PROCESSING' AND lease_until IS NOT NULL AND lease_until <",
		"attempts = attempts + 1",
	} {
		if !strings.Contains(claimForProcessingSQL, want) {
			t.Fatalf("认领 SQL 缺少 %q:\n%s", want, claimForProcessingSQL)
		}
	}
	if strings.Contains(claimForProcessingSQL, "status = 'PROCESSED'") {
		t.Fatalf("认领 SQL 不应把 PROCESSED 当作可领取状态:\n%s", claimForProcessingSQL)
	}
}

// DryRun 下没有真实行受影响，认领应返回 false 而不是报错；
// 同时确认真正执行的是带条件的 UPDATE，而不是先查后改。
func TestTryClaimMessageForProcessing_DryRunReturnsFalse(t *testing.T) {
	db := dryRunDB(t)
	var sql string
	if err := db.Callback().Raw().After("gorm:raw").Register("test:capture_claim", func(tx *gorm.DB) {
		sql = tx.Statement.SQL.String()
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	repo := NewMessageRepository(db)
	ok, err := repo.TryClaimMessageForProcessing(
		context.Background(), "m1", "message-service-group", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("DryRun 认领不应报错: %v", err)
	}
	if ok {
		t.Fatal("DryRun 下没有行受影响，不应返回认领成功")
	}
	if !strings.Contains(sql, "UPDATE queue_messages") || !strings.Contains(sql, "lease_until") {
		t.Fatalf("认领 SQL 不符合预期:\n%s", sql)
	}
}

// 完成态必须同时清空 claimed_by 与 lease_until：
// 保留租约会让已成功的消息在租约过期后重新变成可接管状态。
func TestMarkMessageProcessed_ClearsClaimAndLease(t *testing.T) {
	db := dryRunDB(t)
	var sql string
	if err := db.Callback().Update().After("gorm:update").Register("test:capture_mark", func(tx *gorm.DB) {
		sql = tx.Statement.SQL.String()
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	repo := NewMessageRepository(db)
	err := repo.MarkMessageProcessed(context.Background(), "m1", "message-service-group")
	if !errors.Is(err, ErrMessageNotClaimed) {
		t.Fatalf("DryRun 下没有真实行受影响，应返回 ErrMessageNotClaimed, got %v", err)
	}
	for _, want := range []string{"status", "claimed_by", "lease_until", "status ="} {
		if !strings.Contains(sql, want) {
			t.Fatalf("完成 SQL 缺少 %q:\n%s", want, sql)
		}
	}
}

// 释放必须带 claimed_by 条件：租约过期后旧处理者迟到的释放
// 不能把新处理者的认领一起清掉。
func TestReleaseMessageClaim_GuardsOwner(t *testing.T) {
	db := dryRunDB(t)
	var sql string
	if err := db.Callback().Update().After("gorm:update").Register("test:capture_release", func(tx *gorm.DB) {
		sql = tx.Statement.SQL.String()
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	repo := NewMessageRepository(db)
	if err := repo.ReleaseMessageClaim(context.Background(), "m1", "message-service-group"); err != nil {
		t.Fatalf("释放没有行受影响不应报错: %v", err)
	}
	for _, want := range []string{"claimed_by =", "status =", "lease_until"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("释放 SQL 缺少 %q:\n%s", want, sql)
		}
	}
}
