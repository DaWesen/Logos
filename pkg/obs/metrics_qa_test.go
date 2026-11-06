package obs

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRecordQAIdempotency_ReportsFixedResult(t *testing.T) {
	assertCounterHasLabel(t, "logos.qa.idempotency.count", "result", "hit", func() {
		RecordQAIdempotency(context.Background(), "hit")
	})
}

// 降级率必须单独可见：接口返回 200 + 兜底文案时，只看请求成功率
// 会把「模型全挂」显示成 100% 可用。
func TestRecordQAAnswer_ReportsDegraded(t *testing.T) {
	assertCounterHasLabel(t, "logos.qa.answer.count", "result", "degraded", func() {
		RecordQAAnswer(context.Background(), "degraded")
	})
}

func assertCounterHasLabel(t *testing.T, name, key, want string, record func()) {
	t.Helper()
	record()

	var rm metricdata.ResourceMetrics
	if err := obsTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}
	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("指标 %s 类型 = %T", name, m.Data)
			}
			for _, p := range sum.DataPoints {
				value, _ := p.Attributes.Value(attribute.Key(key))
				if value.AsString() == want && p.Value >= 1 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("未找到 %s=%s 的指标 %s", key, want, name)
	}
}
