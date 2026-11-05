package pgsql

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// dbTestReader 让测试能读到真实指标；必须在任何 Record 之前装好 Provider，
// 因为指标是首次调用时惰性初始化的。
var dbTestReader *sdkmetric.ManualReader

func TestMain(m *testing.M) {
	dbTestReader = sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(dbTestReader)))
	os.Exit(m.Run())
}

type dbMetricRow struct {
	ID int `gorm:"primaryKey"`
}

func (dbMetricRow) TableName() string {
	return "db_metric_rows"
}

func dryRunPostgres(t *testing.T) *gorm.DB {
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

// TestDBResult_Classifies RecordNotFound 不能算数据库故障，
// 否则大量「查不到」的正常请求会把错误率打穿。
func TestDBResult_Classifies(t *testing.T) {
	if got := dbResult(nil); got != "ok" {
		t.Fatalf("nil err = %q, want ok", got)
	}
	if got := dbResult(gorm.ErrRecordNotFound); got != "ok" {
		t.Fatalf("ErrRecordNotFound = %q, want ok", got)
	}
	if got := dbResult(errors.New("connection reset")); got != "failed" {
		t.Fatalf("真实错误 = %q, want failed", got)
	}
}

// TestRegisterDBMetrics_RecordsStatement 校验回调真的挂上了：
// 任意一次 Find 都应产生带 table/operation/result 的耗时数据点。
func TestRegisterDBMetrics_RecordsStatement(t *testing.T) {
	db := dryRunPostgres(t)
	if err := registerDBMetrics(db); err != nil {
		t.Fatalf("注册数据库指标失败: %v", err)
	}

	// before/after 必须是两个独立回调。GORM 的 Register 会改写回调对象自身，
	// 复用同一个 Before(...) 对象会让 before 被 after 覆盖，指标从此恒为空。
	for _, name := range []string{"logos:db:metrics:before:query", "logos:db:metrics:after:query"} {
		if db.Callback().Query().Get(name) == nil {
			t.Fatalf("回调 %s 未注册", name)
		}
	}

	before := dbMetricCount(t, "query", "db_metric_rows")

	var rows []dbMetricRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("DryRun 查询失败: %v", err)
	}

	if dbMetricCount(t, "query", "db_metric_rows") == 0 {
		t.Fatal("未找到 db_metric_rows/query/ok 的数据库耗时数据点")
	}
	// 一次查询只能记一次，重复注册回调会把延迟和慢查询计数翻倍。
	if after := dbMetricCount(t, "query", "db_metric_rows"); after != before+1 {
		t.Fatalf("一次 Find 产生了 %d 个数据点，want 1", after-before)
	}
}

func dbMetricCount(t *testing.T, operation, table string) uint64 {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := dbTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var count uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.db.statement.duration" {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("耗时指标类型 = %T", m.Data)
			}
			for _, p := range hist.DataPoints {
				op, _ := p.Attributes.Value(attribute.Key("operation"))
				tbl, _ := p.Attributes.Value(attribute.Key("table"))
				result, _ := p.Attributes.Value(attribute.Key("result"))
				if op.AsString() == operation && tbl.AsString() == table && result.AsString() == "ok" {
					count += p.Count
				}
			}
		}
	}
	return count
}

// TestRegisterDBMetrics_CoversRawExec 原生 Exec 走的是 raw processor。
// 保留策略的批量 DELETE、启动期的表结构修正都在这里，漏掉就恰好漏掉最慢的语句。
func TestRegisterDBMetrics_CoversRawExec(t *testing.T) {
	db := dryRunPostgres(t)
	if err := registerDBMetrics(db); err != nil {
		t.Fatalf("注册数据库指标失败: %v", err)
	}

	if err := db.Exec("DELETE FROM db_metric_rows WHERE id = ?", 1).Error; err != nil {
		t.Fatalf("DryRun Exec 失败: %v", err)
	}

	// 原生语句拿不到 table 名，统一落到 unknown，不能因此直接丢弃。
	if got := dbMetricCount(t, "raw", "unknown"); got != 1 {
		t.Fatalf("raw/unknown 数据点 = %d, want 1（Exec 没有被埋点或重复埋点）", got)
	}
}

// TestRegisterDBMetrics_RejectsMissingAnchor 锚点回调被移除时，
// GORM 只会把我们的回调排到链尾（计时恒为 0），不会报错。
// 这种「假指标」必须变成显式错误，并且不能半注册。
func TestRegisterDBMetrics_RejectsMissingAnchor(t *testing.T) {
	db := dryRunPostgres(t)
	if err := db.Callback().Query().Remove("gorm:query"); err != nil {
		t.Fatalf("移除 gorm:query 失败: %v", err)
	}

	err := registerDBMetrics(db)
	if err == nil {
		t.Fatal("锚点缺失时 registerDBMetrics 应返回错误")
	}
	if !strings.Contains(err.Error(), "gorm:query") || !strings.Contains(err.Error(), "query") {
		t.Fatalf("错误信息未指明缺失的锚点: %v", err)
	}
	if db.Callback().Query().Get("logos:db:metrics:before:query") != nil {
		t.Fatal("锚点缺失时不应注册 before 回调，否则会留下恒为 0 的假指标")
	}

	// 单个 processor 失败不应影响其他 processor 的埋点。
	if db.Callback().Create().Get("logos:db:metrics:before:create") == nil {
		t.Fatal("query 注册失败时 create 的埋点也被跳过了")
	}
}
