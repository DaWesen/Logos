package es

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// TestESResult_ClassifiesError 结果标签的判定必须稳定：
// 取消不能算失败，失败也不能被算成取消，否则告警的分母就是错的。
func TestESResult_ClassifiesError(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want string
	}{
		{"成功", context.Background(), nil, "ok"},
		{"成功但 ctx 已取消", canceledCtx, nil, "ok"},
		{"直接取消", context.Background(), context.Canceled, "canceled"},
		{"包装后的取消", context.Background(), fmt.Errorf("wrap: %w", context.Canceled), "canceled"},
		{"超时", context.Background(), context.DeadlineExceeded, "canceled"},
		{"ctx 取消导致的其它错误", canceledCtx, errors.New("use of closed connection"), "canceled"},
		{"真实失败", context.Background(), errors.New("es 500"), "failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := esResult(tc.ctx, tc.err); got != tc.want {
				t.Fatalf("esResult() = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// collectESMetric 从 ManualReader 里取出指定指标的所有数据点。
// 指标是包级单例，测试之间会累加，所以每个用例都用独立的 index 名隔离。
func collectESMetric(t *testing.T, name string) []metricdata.DataPoint[int64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := esTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s 的数据类型 = %T，期望 Sum[int64]", name, m.Data)
			}
			return sum.DataPoints
		}
	}
	return nil
}

// countByIndexAndResult 统计某个 index 下各 result 的次数。
func countByIndexAndResult(points []metricdata.DataPoint[int64], index string) map[string]int64 {
	counts := map[string]int64{}
	for _, p := range points {
		if v, ok := p.Attributes.Value(attribute.Key("index")); !ok || v.AsString() != index {
			continue
		}
		result, _ := p.Attributes.Value(attribute.Key("result"))
		counts[result.AsString()] += p.Value
	}
	return counts
}

// TestSearchMetrics_RecordsOK 成功检索必须产生一条 result=ok 的打点，
// 否则「检索链路有没有被调用」只能靠日志猜。
func TestSearchMetrics_RecordsOK(t *testing.T) {
	const index = "metrics_ok"

	mgr, srv := newTestESManager(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":0},"hits":[]}}`))
	})
	defer srv.Close()

	var result SearchResult
	if err := mgr.SearchWithContext(context.Background(), index, SearchQuery{Size: 1}, &result); err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	counts := countByIndexAndResult(collectESMetric(t, "logos.es.request.count"), index)
	if counts["ok"] != 1 {
		t.Fatalf("result=ok 计数 = %d，期望 1（实际分布 %v）", counts["ok"], counts)
	}
}

// TestSearchMetrics_CanceledIsNotFailed 取消不能算进 failed：
// ctx 透传后客户端断连是常态，混在一起会稀释真实的 ES 故障率。
func TestSearchMetrics_CanceledIsNotFailed(t *testing.T) {
	const index = "metrics_canceled"
	started := make(chan struct{})

	mgr, srv := newTestESManager(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var result SearchResult
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.SearchWithContext(ctx, index, SearchQuery{Size: 1}, &result) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("ES 请求未发出")
	}
	if err := <-errCh; err == nil {
		t.Fatal("ctx 取消后应返回错误")
	}

	counts := countByIndexAndResult(collectESMetric(t, "logos.es.request.count"), index)
	if counts["canceled"] != 1 {
		t.Fatalf("result=canceled 计数 = %d，期望 1（实际分布 %v）", counts["canceled"], counts)
	}
	if counts["failed"] != 0 {
		t.Fatalf("取消被错误计入 failed：%v", counts)
	}
}

// TestSearchMetrics_ServerErrorIsFailed ES 返回错误状态码时必须是 failed，
// 不能因为 ctx 还活着就被当成成功。
func TestSearchMetrics_ServerErrorIsFailed(t *testing.T) {
	const index = "metrics_failed"

	mgr, srv := newTestESManager(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	var result SearchResult
	if err := mgr.SearchWithContext(context.Background(), index, SearchQuery{Size: 1}, &result); err == nil {
		t.Fatal("500 应返回错误")
	}

	counts := countByIndexAndResult(collectESMetric(t, "logos.es.request.count"), index)
	if counts["failed"] != 1 {
		t.Fatalf("result=failed 计数 = %d，期望 1（实际分布 %v）", counts["failed"], counts)
	}
}

// TestESMetrics_RecordsDuration 耗时直方图必须真的记录，
// 否则「检索慢在哪」只能靠猜；这里只校验次数与标签，不断言具体耗时。
func TestESMetrics_RecordsDuration(t *testing.T) {
	const index = "metrics_duration"

	mgr, srv := newTestESManager(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":0},"hits":[]}}`))
	})
	defer srv.Close()

	var result SearchResult
	if err := mgr.SearchWithContext(context.Background(), index, SearchQuery{Size: 1}, &result); err != nil {
		t.Fatalf("检索失败: %v", err)
	}

	var rm metricdata.ResourceMetrics
	if err := esTestReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var found bool
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.es.request.duration" {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("耗时指标类型 = %T，期望 Histogram[float64]", m.Data)
			}
			for _, p := range hist.DataPoints {
				if v, ok := p.Attributes.Value(attribute.Key("index")); ok && v.AsString() == index {
					if p.Count != 1 {
						t.Fatalf("耗时样本数 = %d，期望 1", p.Count)
					}
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("未找到该 index 的耗时直方图数据点")
	}
}
