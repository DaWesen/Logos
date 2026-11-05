package safego

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

var testReader *sdkmetric.ManualReader

func TestMain(m *testing.M) {
	testReader = sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(testReader)))
	os.Exit(m.Run())
}

func TestRun_RecoversPanicWithTaskAndStack(t *testing.T) {
	err := Run("test.run", func() {
		panic("boom")
	})

	var panicErr *PanicError
	if !errors.As(err, &panicErr) {
		t.Fatalf("Run 应返回 *PanicError，实际 %T: %v", err, err)
	}
	if panicErr.Task != "test.run" {
		t.Fatalf("Task = %q, want test.run", panicErr.Task)
	}
	if panicErr.Value != "boom" {
		t.Fatalf("Value = %v, want boom", panicErr.Value)
	}
	if len(panicErr.Stack) == 0 {
		t.Fatal("panic 堆栈为空，线上无法定位")
	}
	if !strings.Contains(err.Error(), "test.run") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("错误信息缺少任务名或 panic 值: %v", err)
	}
}

func TestRun_ReturnsNilWhenTaskCompletes(t *testing.T) {
	if err := Run("test.ok", func() {}); err != nil {
		t.Fatalf("正常任务不应返回错误: %v", err)
	}
}

func TestRun_RecordsOneMetricPerPanic(t *testing.T) {
	_ = Run("test.metric", func() { panic("first") })
	if got := countPanics(t, "test.metric"); got != 1 {
		t.Fatalf("首次 panic 计数 = %d, want 1", got)
	}

	_ = Run("test.metric", func() { panic("second") })
	if got := countPanics(t, "test.metric"); got != 2 {
		t.Fatalf("第二次 panic 后计数 = %d, want 2", got)
	}
}

func TestGo_RecoversPanicAndKeepsOtherTasksRunning(t *testing.T) {
	started := make(chan struct{})
	Go("test.go_panic", func() {
		panic("boom")
	})
	Go("test.go_ok", func() {
		close(started)
	})

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("一个后台任务 panic 后，其他后台任务未能继续执行")
	}
}

func countPanics(t *testing.T, task string) int64 {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := testReader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("采集指标失败: %v", err)
	}

	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "logos.safego.panic.count" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("panic 指标类型 = %T", m.Data)
			}
			for _, p := range sum.DataPoints {
				value, _ := p.Attributes.Value(attribute.Key("task"))
				if value.AsString() == task {
					total += p.Value
				}
			}
		}
	}
	return total
}
