package obs

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRecordQAIdempotency_ReportsFixedResult(t *testing.T) {
	RecordQAIdempotency(context.Background(), "hit")

	var rm metricdata.ResourceMetrics
	if err := obsTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.qa.idempotency.count" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("幂等指标类型 = %T", m.Data)
			}
			for _, p := range sum.DataPoints {
				result, _ := p.Attributes.Value(attribute.Key("result"))
				if result.AsString() == "hit" && p.Value >= 1 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("未找到 result=hit 的问答幂等指标")
	}
}
