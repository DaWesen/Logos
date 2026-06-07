package obs

import (
	"context"
	"log"

	"Logos/config"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"google.golang.org/grpc"
)

// InitGRPCProvider initializes OpenTelemetry (traces + metrics) for gRPC
// and returns shutdown function, server interceptor option, and client interceptor option.
func InitGRPCProvider(serviceName string) (shutdown func(context.Context), serverOption grpc.ServerOption, clientOption grpc.DialOption) {
	cfg := config.GetConfig()

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Printf("[OTel] suppressed error: %v", err)
	}))

	if !cfg.Tracing.Enable || cfg.Tracing.OtelEndpoint == "" {
		return func(ctx context.Context) {},
			grpc.StatsHandler(otelgrpc.NewServerHandler()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler())
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
	)
	if err != nil {
		log.Printf("[OTel] failed to create resource: %v", err)
		return func(ctx context.Context) {},
			grpc.StatsHandler(otelgrpc.NewServerHandler()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler())
	}

	// Initialize TracerProvider
	traceExporter, err := otlptracegrpc.New(context.Background(),
		otlptracegrpc.WithEndpoint(cfg.Tracing.OtelEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		log.Printf("[OTel] failed to create trace exporter: %v", err)
	} else {
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
	}

	// Initialize MeterProvider
	metricExporter, err := otlpmetricgrpc.New(context.Background(),
		otlpmetricgrpc.WithEndpoint(cfg.Tracing.OtelEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		log.Printf("[OTel] failed to create metric exporter: %v", err)
	} else {
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
	}

	return func(ctx context.Context) {
			if tp := otel.GetTracerProvider(); tp != nil {
				if provider, ok := tp.(*sdktrace.TracerProvider); ok {
					_ = provider.Shutdown(ctx)
				}
			}
			if mp := otel.GetMeterProvider(); mp != nil {
				if provider, ok := mp.(*sdkmetric.MeterProvider); ok {
					_ = provider.Shutdown(ctx)
				}
			}
		},
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler())
}
