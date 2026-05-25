package obs

import (
	"context"
	"log"

	"Logos/config"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"google.golang.org/grpc"
)

// InitGRPCProvider initializes OpenTelemetry for gRPC and returns shutdown function and server interceptor option.
func InitGRPCProvider(serviceName string) (shutdown func(context.Context), serverOption grpc.ServerOption, clientOption grpc.DialOption) {
	cfg := config.GetConfig()

	if !cfg.Tracing.Enable || cfg.Tracing.OtelEndpoint == "" {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			log.Printf("[OTel] suppressed error: %v", err)
		}))
		return func(ctx context.Context) {},
			grpc.StatsHandler(otelgrpc.NewServerHandler()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler())
	}

	exporter, err := otlptracegrpc.New(context.Background(),
		otlptracegrpc.WithEndpoint(cfg.Tracing.OtelEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		log.Printf("[OTel] failed to create exporter: %v", err)
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

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return func(ctx context.Context) { _ = tp.Shutdown(ctx) },
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler())
}
