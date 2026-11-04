package obs

import (
	"context"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var obsTestReader *sdkmetric.ManualReader

func TestMain(m *testing.M) {
	obsTestReader = sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(obsTestReader)))
	os.Exit(m.Run())
}

func TestRegisterDBPoolStats_ReportsSnapshot(t *testing.T) {
	RegisterDBPoolStats(func() DBPoolStats {
		return DBPoolStats{
			OpenConnections:     10,
			InUse:               7,
			Idle:                3,
			WaitCount:           42,
			WaitDurationSeconds: 1.5,
		}
	})

	var rm metricdata.ResourceMetrics
	if err := obsTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var connections, waitCount, waitDuration bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "logos.db.pool.connections":
				gauge, ok := m.Data.(metricdata.Gauge[int64])
				if !ok {
					t.Fatalf("连接池指标类型 = %T", m.Data)
				}
				for _, p := range gauge.DataPoints {
					state, _ := p.Attributes.Value(attribute.Key("state"))
					if state.AsString() == "in_use" && p.Value == 7 {
						connections = true
					}
				}
			case "logos.db.pool.wait.count":
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok || len(sum.DataPoints) == 0 {
					t.Fatalf("等待次数指标类型/数据异常: %T", m.Data)
				}
				waitCount = sum.DataPoints[0].Value == 42
			case "logos.db.pool.wait.duration":
				sum, ok := m.Data.(metricdata.Sum[float64])
				if !ok || len(sum.DataPoints) == 0 {
					t.Fatalf("等待耗时指标类型/数据异常: %T", m.Data)
				}
				waitDuration = sum.DataPoints[0].Value == 1.5
			}
		}
	}

	if !connections || !waitCount || !waitDuration {
		t.Fatalf("连接池指标缺失: connections=%v waitCount=%v waitDuration=%v",
			connections, waitCount, waitDuration)
	}
}

func TestRecordRetentionCleanup_ReportsRowsByTable(t *testing.T) {
	RecordRetentionCleanup(context.Background(), "message_idempotency", "ok", 42)

	var rm metricdata.ResourceMetrics
	if err := obsTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.retention.cleanup.rows" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("清理指标类型 = %T", m.Data)
			}
			for _, p := range sum.DataPoints {
				table, _ := p.Attributes.Value(attribute.Key("table"))
				result, _ := p.Attributes.Value(attribute.Key("result"))
				if table.AsString() == "message_idempotency" && result.AsString() == "ok" && p.Value == 42 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("未找到 message_idempotency/ok/42 的清理指标")
	}
}

func TestRecordQQMediaRelay_ReportsStageAndResult(t *testing.T) {
	RecordQQMediaRelay(context.Background(), "upload", "failed", 4096)

	var rm metricdata.ResourceMetrics
	if err := obsTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var countFound, bytesFound bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "logos.qqbridge.media.count":
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok {
					t.Fatalf("媒体计数指标类型 = %T", m.Data)
				}
				for _, p := range sum.DataPoints {
					stage, _ := p.Attributes.Value(attribute.Key("stage"))
					result, _ := p.Attributes.Value(attribute.Key("result"))
					if stage.AsString() == "upload" && result.AsString() == "failed" && p.Value == 1 {
						countFound = true
					}
				}
			case "logos.qqbridge.media.bytes":
				hist, ok := m.Data.(metricdata.Histogram[int64])
				if !ok || len(hist.DataPoints) == 0 {
					t.Fatalf("媒体大小指标类型/数据异常: %T", m.Data)
				}
				if hist.DataPoints[0].Sum == 4096 {
					bytesFound = true
				}
			}
		}
	}

	if !countFound || !bytesFound {
		t.Fatalf("QQ 媒体指标缺失: count=%v bytes=%v", countFound, bytesFound)
	}
}

func TestRecordRetentionCleanup_ReportsFailure(t *testing.T) {
	RecordRetentionCleanup(context.Background(), "message_changes", "failed", 0)

	var rm metricdata.ResourceMetrics
	if err := obsTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.retention.cleanup.failures" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("清理失败指标类型 = %T", m.Data)
			}
			for _, p := range sum.DataPoints {
				table, _ := p.Attributes.Value(attribute.Key("table"))
				result, _ := p.Attributes.Value(attribute.Key("result"))
				if table.AsString() == "message_changes" && result.AsString() == "failed" && p.Value == 1 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("未找到 message_changes/failed 的清理失败指标")
	}
}
