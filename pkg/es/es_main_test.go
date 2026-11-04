package es

import (
	"context"
	"fmt"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// esTestReader 让本包的测试能读到真实的指标数据。
//
// obs 里的 ES 指标是「首次调用时惰性初始化」，一旦在默认的 noop MeterProvider
// 上创建，后续再换 Provider 也拿不到数据。所以必须在任何测试跑之前装好 Reader。
var esTestReader *sdkmetric.ManualReader

func TestMain(m *testing.M) {
	esTestReader = sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(esTestReader)))

	code := m.Run()
	if err := otel.GetMeterProvider().(*sdkmetric.MeterProvider).Shutdown(
		// Shutdown 只用于刷新，不影响测试结论，失败时打印出来方便排查。
		context.Background(),
	); err != nil {
		fmt.Fprintf(os.Stderr, "关闭 MeterProvider 失败: %v\n", err)
	}
	os.Exit(code)
}
