package pgsql

import (
	"context"
	"errors"
	"os"
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
	registerDBMetrics(db)

	var rows []dbMetricRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("DryRun 查询失败: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := dbTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var found bool
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
				table, _ := p.Attributes.Value(attribute.Key("table"))
				op, _ := p.Attributes.Value(attribute.Key("operation"))
				result, _ := p.Attributes.Value(attribute.Key("result"))
				if table.AsString() == "db_metric_rows" &&
					op.AsString() == "query" && result.AsString() == "ok" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("未找到 db_metric_rows/query/ok 的数据库耗时数据点")
	}
}
