package collector

import (
	"context"
	"strings"
	"testing"

	"Logos/internal/service/platform/monitoring/model"

	"go.etcd.io/etcd/api/v3/mvccpb"
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

// TestInstanceCounts 验证一次前缀查询的结果能正确按服务名归并。
//
// 服务名本身可能带 '/'，所以不能按固定下标切分；
// 必须从右侧剥掉 host:port，否则 logos.ai/knowledge 会被算到 logos.ai 名下。
func TestInstanceCounts(t *testing.T) {
	kvs := []*mvccpb.KeyValue{
		{Key: []byte("/grpc/logos.user/10.0.0.1:9000")},
		{Key: []byte("/grpc/logos.user/10.0.0.2:9000")},
		{Key: []byte("/grpc/logos.chat/10.0.0.3:9000")},
		{Key: []byte("/grpc/logos.ai/knowledge/10.0.0.4:9000")},
		// 非法/半截 key 直接忽略，不能 panic，也不能污染计数
		{Key: []byte("/grpc/")},
		{Key: []byte("/grpc/logos.chat")},
		{Key: []byte("/other/logos.user/10.0.0.5:9000")},
	}

	got := instanceCounts(kvs)
	want := map[string]int{
		"logos.user":         2,
		"logos.chat":         1,
		"logos.ai/knowledge": 1,
	}
	if len(got) != len(want) {
		t.Fatalf("instanceCounts() 服务数 = %d, want %d (%v)", len(got), len(want), got)
	}
	for name, count := range want {
		if got[name] != count {
			t.Errorf("%s 实例数 = %d, want %d", name, got[name], count)
		}
	}
}

// TestInstanceCounts_Empty 空结果不应产生任何服务条目，
// 由调用方按 noahServices 补 0，避免把「从未注册」与「解析失败」混淆。
func TestInstanceCounts_Empty(t *testing.T) {
	if got := instanceCounts(nil); len(got) != 0 {
		t.Fatalf("空结果应返回空 map，实际 %v", got)
	}
}

// TestUpsertStatuses_SQL 断言批量写入使用 ON CONFLICT DO UPDATE：
// 逐条 FirstOrCreate + Updates 是 38 次往返，批量后只有 1 次。
func TestUpsertStatuses_SQL(t *testing.T) {
	db := dryRunDB(t)
	c := &ServiceCollector{db: db}

	var sql string
	if err := db.Callback().Create().After("gorm:create").Register("test:capture_sql", func(tx *gorm.DB) {
		sql = tx.Statement.SQL.String()
	}); err != nil {
		t.Fatalf("注册 SQL 捕获回调失败: %v", err)
	}

	rows := []*model.ServiceStatus{
		{ServiceName: "logos.user", Status: "UP", LastCheckTime: 1, Metadata: "{}"},
		{ServiceName: "logos.chat", Status: "DOWN", LastCheckTime: 1, Metadata: "{}"},
	}
	if err := c.upsertStatuses(context.Background(), rows); err != nil {
		t.Fatalf("DryRun 下 upsert 不应报错: %v", err)
	}
	if sql == "" {
		t.Fatal("未能捕获 upsert SQL")
	}

	for _, want := range []string{"INSERT INTO", "ON CONFLICT", "DO UPDATE SET"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL 缺少 %q: %s", want, sql)
		}
	}
}

// TestUpsertStatuses_Empty 空批次必须直接返回，不能发出没有 VALUES 的 INSERT。
func TestUpsertStatuses_Empty(t *testing.T) {
	db := dryRunDB(t)
	c := &ServiceCollector{db: db}
	if err := c.upsertStatuses(context.Background(), nil); err != nil {
		t.Fatalf("空批次应直接返回，实际 %v", err)
	}
}
