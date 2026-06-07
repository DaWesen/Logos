package obs

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "logos"

// ServiceMeters holds the OpenTelemetry instruments for a service.
type ServiceMeters struct {
	// RequestCount counts the number of gRPC requests received.
	RequestCount metric.Int64Counter
	// RequestDuration records the duration of gRPC requests.
	RequestDuration metric.Float64Histogram
	// ActiveConnections tracks the number of active connections (IM/Gateway).
	ActiveConnections metric.Int64UpDownCounter
	// MessagesSent counts messages sent through the system.
	MessagesSent metric.Int64Counter
	// MessagesReceived counts messages received by the system.
	MessagesReceived metric.Int64Counter
	// BotRequests counts the number of bot conversation requests.
	BotRequests metric.Int64Counter
	// LLMCallDuration records the duration of LLM API calls.
	LLMCallDuration metric.Float64Histogram
	// LLMCallCount counts the number of LLM API calls.
	LLMCallCount metric.Int64Counter
	// VectorSearchDuration records the duration of vector searches.
	VectorSearchDuration metric.Float64Histogram
	// KnowledgeDocuments tracks the number of documents in knowledge bases.
	KnowledgeDocuments metric.Int64Counter
	// DBQueryDuration records the duration of database queries.
	DBQueryDuration metric.Float64Histogram
	// KafkaMessagesProduced counts Kafka messages produced.
	KafkaMessagesProduced metric.Int64Counter
	// KafkaMessagesConsumed counts Kafka messages consumed.
	KafkaMessagesConsumed metric.Int64Counter
}

// InitServiceMeters creates and registers all service-level metric instruments.
// Not all services will use every instrument; unused instruments will be nil-safe
// via the NoopMeter fallback.
func InitServiceMeters(serviceName string) *ServiceMeters {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)

	m := &ServiceMeters{}

	m.RequestCount, _ = meter.Int64Counter(
		"logos.grpc.request.count",
		metric.WithDescription("Total number of gRPC requests"),
	)

	m.RequestDuration, _ = meter.Float64Histogram(
		"logos.grpc.request.duration",
		metric.WithDescription("Duration of gRPC requests in seconds"),
		metric.WithUnit("s"),
	)

	m.ActiveConnections, _ = meter.Int64UpDownCounter(
		"logos.connections.active",
		metric.WithDescription("Number of active connections"),
	)

	m.MessagesSent, _ = meter.Int64Counter(
		"logos.messages.sent",
		metric.WithDescription("Total number of messages sent"),
	)

	m.MessagesReceived, _ = meter.Int64Counter(
		"logos.messages.received",
		metric.WithDescription("Total number of messages received"),
	)

	m.BotRequests, _ = meter.Int64Counter(
		"logos.bot.requests",
		metric.WithDescription("Total number of bot conversation requests"),
	)

	m.LLMCallDuration, _ = meter.Float64Histogram(
		"logos.llm.call.duration",
		metric.WithDescription("Duration of LLM API calls in seconds"),
		metric.WithUnit("s"),
	)

	m.LLMCallCount, _ = meter.Int64Counter(
		"logos.llm.call.count",
		metric.WithDescription("Total number of LLM API calls"),
	)

	m.VectorSearchDuration, _ = meter.Float64Histogram(
		"logos.vector.search.duration",
		metric.WithDescription("Duration of vector searches in seconds"),
		metric.WithUnit("s"),
	)

	m.KnowledgeDocuments, _ = meter.Int64Counter(
		"logos.knowledge.documents",
		metric.WithDescription("Total number of documents in knowledge bases"),
	)

	m.DBQueryDuration, _ = meter.Float64Histogram(
		"logos.db.query.duration",
		metric.WithDescription("Duration of database queries in seconds"),
		metric.WithUnit("s"),
	)

	m.KafkaMessagesProduced, _ = meter.Int64Counter(
		"logos.kafka.produced",
		metric.WithDescription("Total number of Kafka messages produced"),
	)

	m.KafkaMessagesConsumed, _ = meter.Int64Counter(
		"logos.kafka.consumed",
		metric.WithDescription("Total number of Kafka messages consumed"),
	)

	return m
}

// RecordRequest records a gRPC request with its duration.
func (m *ServiceMeters) RecordRequest(ctx context.Context, method string, durationSeconds float64) {
	if m.RequestCount != nil {
		m.RequestCount.Add(ctx, 1, metric.WithAttributes(attribute.String("method", method)))
	}
	if m.RequestDuration != nil {
		m.RequestDuration.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("method", method)))
	}
}

// RecordActiveConnection adjusts the active connection count.
func (m *ServiceMeters) RecordActiveConnection(ctx context.Context, delta int64) {
	if m.ActiveConnections != nil {
		m.ActiveConnections.Add(ctx, delta)
	}
}

// RecordMessageSent increments the messages sent counter.
func (m *ServiceMeters) RecordMessageSent(ctx context.Context, msgType string) {
	if m.MessagesSent != nil {
		m.MessagesSent.Add(ctx, 1, metric.WithAttributes(attribute.String("type", msgType)))
	}
}

// RecordMessageReceived increments the messages received counter.
func (m *ServiceMeters) RecordMessageReceived(ctx context.Context, msgType string) {
	if m.MessagesReceived != nil {
		m.MessagesReceived.Add(ctx, 1, metric.WithAttributes(attribute.String("type", msgType)))
	}
}

// RecordBotRequest increments the bot requests counter.
func (m *ServiceMeters) RecordBotRequest(ctx context.Context, botID string) {
	if m.BotRequests != nil {
		m.BotRequests.Add(ctx, 1, metric.WithAttributes(attribute.String("bot_id", botID)))
	}
}

// RecordLLMCall records an LLM API call with its duration.
func (m *ServiceMeters) RecordLLMCall(ctx context.Context, model string, durationSeconds float64) {
	if m.LLMCallCount != nil {
		m.LLMCallCount.Add(ctx, 1, metric.WithAttributes(attribute.String("model", model)))
	}
	if m.LLMCallDuration != nil {
		m.LLMCallDuration.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("model", model)))
	}
}

// RecordVectorSearch records a vector search with its duration.
func (m *ServiceMeters) RecordVectorSearch(ctx context.Context, collectionID string, durationSeconds float64) {
	if m.VectorSearchDuration != nil {
		m.VectorSearchDuration.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("collection", collectionID)))
	}
}

// RecordDBQuery records a database query with its duration.
func (m *ServiceMeters) RecordDBQuery(ctx context.Context, operation string, durationSeconds float64) {
	if m.DBQueryDuration != nil {
		m.DBQueryDuration.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("operation", operation)))
	}
}

// RecordKafkaProduced increments the Kafka produced counter.
func (m *ServiceMeters) RecordKafkaProduced(ctx context.Context, topic string) {
	if m.KafkaMessagesProduced != nil {
		m.KafkaMessagesProduced.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// RecordKafkaConsumed increments the Kafka consumed counter.
func (m *ServiceMeters) RecordKafkaConsumed(ctx context.Context, topic string) {
	if m.KafkaMessagesConsumed != nil {
		m.KafkaMessagesConsumed.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", topic)))
	}
}
