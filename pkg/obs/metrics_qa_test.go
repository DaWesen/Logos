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

// 检索失败是被吞掉的：回答照样能生成。只有按来源拆分结果，
// 「RAG 一直超时、模型一直在裸答」才在监控上可见。
func TestRecordQARetrieval_ReportsTimeoutBySource(t *testing.T) {
	assertCounterHasAttrs(t,
		"logos.qa.retrieval.count",
		map[string]string{"source": "vector", "result": "timeout"},
		func() {
			RecordQARetrieval(context.Background(), "vector", "timeout", 0.8)
		})
}

func TestRecordQAContextItem_ReportsDropped(t *testing.T) {
	assertCounterHasLabel(t, "logos.qa.context.item.count", "result", "dropped", func() {
		RecordQAContextItem(context.Background(), "dropped", 3)
	})
}

func assertCounterHasLabel(t *testing.T, name, key, want string, record func()) {
	t.Helper()
	assertCounterHasAttrs(t, name, map[string]string{key: want}, record)
}

func assertCounterHasAttrs(t *testing.T, name string, want map[string]string, record func()) {
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
				matched := p.Value >= 1
				for key, wantValue := range want {
					value, _ := p.Attributes.Value(attribute.Key(key))
					if value.AsString() != wantValue {
						matched = false
					}
				}
				if matched {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("未找到标签 %v 的指标 %s", want, name)
	}
}
