package obs

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "logos"

// 可靠性链路指标（Kafka 消费 / Outbox 投递）。
// 这些指标由底层库（pkg/mq、pkg/outbox）直接打点，无法依赖各服务的 ServiceMeters 实例，
// 因此用包级单例 + 首次调用惰性初始化（首次调用发生在进程启动、MeterProvider 已就绪之后）。
var (
	reliabilityOnce sync.Once

	kafkaConsumeCounter  metric.Int64Counter
	kafkaConsumeDuration metric.Float64Histogram
	kafkaPoisonCounter   metric.Int64Counter
	outboxDispatchCount  metric.Int64Counter
	outboxDispatchDur    metric.Float64Histogram
	outboxDeadLetterNum  metric.Int64Counter
)

func initReliabilityMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)

	kafkaConsumeCounter, _ = meter.Int64Counter(
		"logos.kafka.consume.count",
		metric.WithDescription("Kafka 消息处理结果计数（result=ok/failed）"),
	)
	kafkaConsumeDuration, _ = meter.Float64Histogram(
		"logos.kafka.consume.duration",
		metric.WithDescription("Kafka 消息 handler 处理耗时"),
		metric.WithUnit("s"),
	)
	kafkaPoisonCounter, _ = meter.Int64Counter(
		"logos.kafka.poison.count",
		metric.WithDescription("被判为毒丸并转存死信队列的消息数"),
	)
	outboxDispatchCount, _ = meter.Int64Counter(
		"logos.outbox.dispatch.count",
		metric.WithDescription("Outbox 投递结果计数（result=sent/failed）"),
	)
	outboxDispatchDur, _ = meter.Float64Histogram(
		"logos.outbox.dispatch.duration",
		metric.WithDescription("Outbox 单条消息投递耗时"),
		metric.WithUnit("s"),
	)
	outboxDeadLetterNum, _ = meter.Int64Counter(
		"logos.outbox.deadletter.count",
		metric.WithDescription("Outbox 重试耗尽进入死信的消息数"),
	)
}

// RecordKafkaConsume 记录一条消息的处理结果与耗时。
func RecordKafkaConsume(ctx context.Context, topic, result string, durationSeconds float64) {
	reliabilityOnce.Do(initReliabilityMetrics)
	attrs := metric.WithAttributes(
		attribute.String("topic", topic),
		attribute.String("result", result),
	)
	if kafkaConsumeCounter != nil {
		kafkaConsumeCounter.Add(ctx, 1, attrs)
	}
	if kafkaConsumeDuration != nil {
		kafkaConsumeDuration.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// RecordKafkaPoison 记录一条毒丸消息（已转存或转存失败）。
func RecordKafkaPoison(ctx context.Context, topic string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if kafkaPoisonCounter != nil {
		kafkaPoisonCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// RecordOutboxDispatch 记录一条 Outbox 消息的投递结果与耗时。
func RecordOutboxDispatch(ctx context.Context, topic, result string, durationSeconds float64) {
	reliabilityOnce.Do(initReliabilityMetrics)
	attrs := metric.WithAttributes(
		attribute.String("topic", topic),
		attribute.String("result", result),
	)
	if outboxDispatchCount != nil {
		outboxDispatchCount.Add(ctx, 1, attrs)
	}
	if outboxDispatchDur != nil {
		outboxDispatchDur.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// RecordOutboxDeadLetter 记录一条重试耗尽、进入死信的 Outbox 消息。
func RecordOutboxDeadLetter(ctx context.Context, topic string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if outboxDeadLetterNum != nil {
		outboxDeadLetterNum.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// AI 结构化输出解析指标。
// LLM 返回的 JSON 可能带围栏、夹杂解释或被截断，解析失败时调用方通常降级为
// 空结果，属于静默失败；这里打点以便用告警兜住解析质量回退。
var (
	llmParseOnce    sync.Once
	llmParseCounter metric.Int64Counter
)

func initLLMParseMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	llmParseCounter, _ = meter.Int64Counter(
		"logos.llm.parse.count",
		metric.WithDescription("LLM 结构化输出解析结果计数（result=ok/failed）"),
	)
}

// RecordLLMParse 记录一次 LLM 结构化输出解析结果。
// module 取 memory/moderation/summary/extraction，result 取 ok/failed。
func RecordLLMParse(ctx context.Context, module, result string) {
	llmParseOnce.Do(initLLMParseMetrics)
	if llmParseCounter != nil {
		llmParseCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("module", module),
			attribute.String("result", result),
		))
	}
}

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
