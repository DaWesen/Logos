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
	kafkaProduceCounter  metric.Int64Counter
	kafkaProduceDuration metric.Float64Histogram
	kafkaRetryCounter    metric.Int64Counter
	outboxDispatchCount  metric.Int64Counter
	outboxDispatchDur    metric.Float64Histogram
	outboxDeadLetterNum  metric.Int64Counter
	outboxAbortNum       metric.Int64Counter
)

func initReliabilityMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)

	kafkaConsumeCounter, _ = meter.Int64Counter(
		"logos.kafka.consume.count",
		metric.WithDescription("Kafka 消息处理结果计数（result=ok/failed/panic）"),
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
	kafkaProduceCounter, _ = meter.Int64Counter(
		"logos.kafka.produce.count",
		metric.WithDescription("Kafka 生产结果计数（result=ok/failed）"),
	)
	kafkaProduceDuration, _ = meter.Float64Histogram(
		"logos.kafka.produce.duration",
		metric.WithDescription("Kafka 单条消息生产耗时"),
		metric.WithUnit("s"),
	)
	kafkaRetryCounter, _ = meter.Int64Counter(
		"logos.kafka.retry.count",
		metric.WithDescription("同一条 Kafka 消息就地重试的次数（不含最终成功那次）"),
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
	outboxAbortNum, _ = meter.Int64Counter(
		"logos.outbox.dispatch.abort.count",
		metric.WithDescription("Outbox 单轮投递因连续失败提前熔断的次数"),
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

// RecordKafkaProduce 记录一条 Kafka 消息的生产结果与耗时。
//
// 生产失败是「写链路」最早的可观测点：消费者侧的 failed 计数只有在消息成功入队后
// 才会出现，broker 不可用 / 认证失败 / topic 不存在时消费侧指标完全静默，
// 没有这条打点只能等业务反馈「消息发不出去」。
func RecordKafkaProduce(ctx context.Context, topic, result string, durationSeconds float64) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if kafkaProduceCounter != nil {
		kafkaProduceCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("topic", topic),
			attribute.String("result", result),
		))
	}
	if kafkaProduceDuration != nil {
		kafkaProduceDuration.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// RecordKafkaRetry 记录一次就地重试。
//
// 与 failed 计数分开：failed 会包含最终成功前的那几次失败，无法区分
// 「偶发抖动但重试成功」与「持续失败」。retry 计数突增而 poison 稳定，
// 说明下游只是短暂波动，不必告警；两者同时突增才是真故障。
func RecordKafkaRetry(ctx context.Context, topic string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if kafkaRetryCounter != nil {
		kafkaRetryCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", topic)))
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

// RecordOutboxDispatchAbort 记录一次「连败熔断」：本轮剩余消息被放回 pending。
// 与 failed 分开统计：failed 的增量说明单条失败，abort 的增量说明下游整体不可用，
// 后者才是需要立即告警的信号。
func RecordOutboxDispatchAbort(ctx context.Context, topic string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if outboxAbortNum != nil {
		outboxAbortNum.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// 网关连接生命周期指标。
//
// 网关是 IM 的入口，连接数的增删只能从 open/close 两个计数推导，缺少其中一个
// 就无法回答「在线连接是在增长还是衰减」「这轮重启淘汰了多少连接」。分开打点后：
//   - open - close 即当前在线连接，与 presence 的 Redis 视图互相校验；
//   - event=evict/slow/auth/shutdown 能定位「用户为什么掉线」，
//     否则所有断开在监控上都是同一个 close，无法归因。
var (
	gatewayConnOnce    sync.Once
	gatewayConnCounter metric.Int64Counter
	gatewayAuthFailNum metric.Int64Counter
	gatewayDropNum     metric.Int64Counter
	gatewayPruneNum    metric.Int64Counter
	gatewayRevokeNum   metric.Int64Counter
)

func initGatewayMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	gatewayConnCounter, _ = meter.Int64Counter(
		"logos.gateway.connection.count",
		metric.WithDescription("网关连接事件计数（protocol=websocket/tcp, event=open/close/evict/slow/auth/shutdown）"),
	)
	gatewayAuthFailNum, _ = meter.Int64Counter(
		"logos.gateway.auth.fail.count",
		metric.WithDescription("网关认证/令牌复核失败计数（protocol, reason=invalid/revoked/expired）"),
	)
	gatewayDropNum, _ = meter.Int64Counter(
		"logos.gateway.drop.count",
		metric.WithDescription("网关下行帧丢弃计数（protocol, reason=buffer_full）"),
	)
	gatewayPruneNum, _ = meter.Int64Counter(
		"logos.gateway.presence.prune.count",
		metric.WithDescription("presence 剔除的超时会话数"),
	)
	gatewayRevokeNum, _ = meter.Int64Counter(
		"logos.gateway.revocation.check.count",
		metric.WithDescription("网关撤销查询结果计数（protocol, result=clear/revoked/unknown）"),
	)
}

// RecordGatewayConnection 记录一次连接生命周期事件。
func RecordGatewayConnection(ctx context.Context, protocol, event string) {
	gatewayConnOnce.Do(initGatewayMetrics)
	if gatewayConnCounter != nil {
		gatewayConnCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("protocol", protocol),
			attribute.String("event", event),
		))
	}
}

// RecordGatewayAuthFailure 记录一次认证或长连接令牌复核失败。
func RecordGatewayAuthFailure(ctx context.Context, protocol, reason string) {
	gatewayConnOnce.Do(initGatewayMetrics)
	if gatewayAuthFailNum != nil {
		gatewayAuthFailNum.Add(ctx, 1, metric.WithAttributes(
			attribute.String("protocol", protocol),
			attribute.String("reason", reason),
		))
	}
}

// RecordGatewayDrop 记录一次下行帧丢弃（客户端缓冲满或已关闭）。
func RecordGatewayDrop(ctx context.Context, protocol, reason string) {
	gatewayConnOnce.Do(initGatewayMetrics)
	if gatewayDropNum != nil {
		gatewayDropNum.Add(ctx, 1, metric.WithAttributes(
			attribute.String("protocol", protocol),
			attribute.String("reason", reason),
		))
	}
}

// RecordPresencePruned 记录一次 presence 超时会话剔除的会话数。
//
// 为什么单独打点：剔除量突增意味着「大量连接没有正常注销」，常见原因是
// 节点滚动重启时没走优雅停机、或客户端心跳机制失效。只看在线人数无法区分
// 「用户真的下线」与「会话堆积后被批量清理」。
func RecordPresencePruned(ctx context.Context, count int) {
	gatewayConnOnce.Do(initGatewayMetrics)
	if gatewayPruneNum != nil && count > 0 {
		gatewayPruneNum.Add(ctx, int64(count))
	}
}

// RecordGatewayRevocationCheck 记录一次令牌撤销查询结果。
//
// result=unknown 表示 Redis 故障、撤销状态无法判断（网关按 fail-open 放行）。
// 这个标签的价值在于：没有它，「撤销链路失效」和「所有令牌都有效」在监控上
// 完全一样，登出/改密后的令牌可能继续使用却无人察觉。
func RecordGatewayRevocationCheck(ctx context.Context, protocol, result string) {
	gatewayConnOnce.Do(initGatewayMetrics)
	if gatewayRevokeNum != nil {
		gatewayRevokeNum.Add(ctx, 1, metric.WithAttributes(
			attribute.String("protocol", protocol),
			attribute.String("result", result),
		))
	}
}

// 推送链路指标。
//
// 离线推送是「用户不上线就不会感知」的后台链路：发送失败、令牌失效只体现在
// 业务日志里，无法从任何一个在线指标看出来。三组计数分别回答：
//   - send（platform,result）：推送成功率与失败类型分布（retryable/permanent/unknown）；
//   - token evicted（platform）：失效令牌被注销的数量，突增说明大量设备卸载/换机；
//   - provider 未注册计入 skipped，避免「没配 provider」被误读成「发送失败」。
var (
	pushOnce     sync.Once
	pushSendNum  metric.Int64Counter
	pushEvictNum metric.Int64Counter
)

func initPushMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	pushSendNum, _ = meter.Int64Counter(
		"logos.push.send.count",
		metric.WithDescription("推送发送结果计数（platform, result=sent/failed_retryable/failed_permanent/failed_unknown/skipped）"),
	)
	pushEvictNum, _ = meter.Int64Counter(
		"logos.push.token.evicted.count",
		metric.WithDescription("因设备令牌失效而被注销的 token 数（platform）"),
	)
}

// RecordPushSend 记录一次推送发送结果。
func RecordPushSend(ctx context.Context, platform, result string) {
	pushOnce.Do(initPushMetrics)
	if pushSendNum != nil {
		pushSendNum.Add(ctx, 1, metric.WithAttributes(
			attribute.String("platform", platform),
			attribute.String("result", result),
		))
	}
}

// RecordPushTokenEvicted 记录一次因供应商判定令牌失效而注销的 token。
//
// 与 send 的 failed_permanent 分开：failed_permanent 包含「请求本身有问题但令牌有效」
// （如 PayloadTooLarge），只有 evicted 才是真正减少了下一次推送的设备数。
func RecordPushTokenEvicted(ctx context.Context, platform string) {
	pushOnce.Do(initPushMetrics)
	if pushEvictNum != nil {
		pushEvictNum.Add(ctx, 1, metric.WithAttributes(attribute.String("platform", platform)))
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

// Token 用量来源指标：区分供应商返回的真实 usage 与本地估算。
// estimate 占比高说明上游没有返回 usage（如部分 OpenAI 兼容网关的流式响应），
// 计费精度下降，需要告警兜住。
var (
	tokenSourceOnce    sync.Once
	tokenSourceCounter metric.Int64Counter
)

func initTokenSourceMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	tokenSourceCounter, _ = meter.Int64Counter(
		"logos.llm.token_source.count",
		metric.WithDescription("模型调用 token 用量来源计数（source=eino/estimate/client）"),
	)
}

// RecordTokenSource 记录一次计费的 token 用量来源。
func RecordTokenSource(ctx context.Context, source string) {
	tokenSourceOnce.Do(initTokenSourceMetrics)
	if tokenSourceCounter != nil {
		tokenSourceCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("source", source)))
	}
}

// 支付回调指标：回调是公开接口，失败原因（验签失败/金额不一致/订单过期）
// 无法从业务日志聚合，用结果标签打点以便告警兜住支付链路异常。
var (
	paymentCallbackOnce    sync.Once
	paymentCallbackCounter metric.Int64Counter
)

func initPaymentCallbackMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	paymentCallbackCounter, _ = meter.Int64Counter(
		"logos.payment.callback.count",
		metric.WithDescription("支付回调处理结果计数（result=paid/invalid_signature/amount_mismatch/order_expired/...）"),
	)
}

// RecordPaymentCallback 记录一次支付回调的处理结果。
func RecordPaymentCallback(ctx context.Context, result string) {
	paymentCallbackOnce.Do(initPaymentCallbackMetrics)
	if paymentCallbackCounter != nil {
		paymentCallbackCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
}

// 支付单回收指标：后台批量回收过期 pending 订单，反映"下单后未支付"的订单量。
// 数量突增往往意味着支付渠道不可用或前端引流异常。
var (
	paymentReapOnce sync.Once
	paymentReaped   metric.Int64Counter
)

func initPaymentReapMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	paymentReaped, _ = meter.Int64Counter(
		"logos.payment.order.expired.count",
		metric.WithDescription("后台批量回收的过期支付单数量"),
	)
}

// RecordPaymentOrderReaped 记录一次批量回收的支付单数量。
func RecordPaymentOrderReaped(ctx context.Context, count int64) {
	paymentReapOnce.Do(initPaymentReapMetrics)
	if paymentReaped != nil {
		paymentReaped.Add(ctx, count)
	}
}

// 回收失败指标：回收器静默失败会让 pending 订单持续堆积，
// 单靠业务日志很难发现，这里单独计数并用告警兜住。
var (
	paymentReapFailOnce    sync.Once
	paymentReapFailCounter metric.Int64Counter
)

func initPaymentReapFailMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	paymentReapFailCounter, _ = meter.Int64Counter(
		"logos.payment.reap.failure.count",
		metric.WithDescription("后台回收过期支付单失败次数"),
	)
}

// RecordPaymentReapFailure 记录一次批量回收失败。
func RecordPaymentReapFailure(ctx context.Context) {
	paymentReapFailOnce.Do(initPaymentReapFailMetrics)
	if paymentReapFailCounter != nil {
		paymentReapFailCounter.Add(ctx, 1)
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
