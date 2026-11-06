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
	kafkaReplayCounter   metric.Int64Counter
	kafkaDuplicateNum    metric.Int64Counter
	kafkaFinalizeFailNum metric.Int64Counter
	qaIdempotencyNum     metric.Int64Counter
	outboxDispatchCount  metric.Int64Counter
	outboxDispatchDur    metric.Float64Histogram
	outboxDeadLetterNum  metric.Int64Counter
	outboxAbortNum       metric.Int64Counter
	safegoPanicCounter   metric.Int64Counter
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
	kafkaReplayCounter, _ = meter.Int64Counter(
		"logos.kafka.replay.count",
		metric.WithDescription("死信回放处置结果计数（result=replayed/quarantined/failed/commit_error）"),
	)
	kafkaDuplicateNum, _ = meter.Int64Counter(
		"logos.kafka.duplicate.count",
		metric.WithDescription("被消费端幂等闸门挡下的重复消息数（reason=already_done/in_flight/not_claimable）"),
	)
	kafkaFinalizeFailNum, _ = meter.Int64Counter(
		"logos.kafka.finalize.failure.count",
		metric.WithDescription("业务处理成功但消息终态写入失败的次数（消息会停留在 PROCESSING）"),
	)
	qaIdempotencyNum, _ = meter.Int64Counter(
		"logos.qa.idempotency.count",
		metric.WithDescription("问答业务幂等结果计数（result=hit/conflict/concurrent_hit）"),
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
	safegoPanicCounter, _ = meter.Int64Counter(
		"logos.safego.panic.count",
		metric.WithDescription("后台任务中被恢复的 panic 次数（按固定任务名）"),
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

// RecordBackgroundPanic 记录一次被 safego 隔离的后台 panic。
//
// 指标标签只接受调用方传入的固定任务名，不携带 panic 值：
// panic 文本可能包含用户输入或地址，放进标签既会撑爆基数，也可能泄露数据。
func RecordBackgroundPanic(ctx context.Context, task string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if safegoPanicCounter != nil {
		safegoPanicCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("task", task)))
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

// RecordKafkaReplay 记录一条死信回放的处置结果。
//
// 回放是人工触发的运维动作，日志只能回答「这一次做了什么」，指标才能回答
// 「最近是不是在反复回放」「失败有没有收敛」。result 只接受固定枚举，
// 不拼接 topic 与错误文本：调用频率低但标签一旦失控，代价会长期留在指标后端。
func RecordKafkaReplay(ctx context.Context, result string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if kafkaReplayCounter != nil {
		kafkaReplayCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
}

// RecordKafkaDuplicate 记录一条因幂等闸门而未再次执行的消息。
//
// 「静默跳过」和「真的没收到」在日志里都只是一行 warn，指标才能区分：
// already_done 说明回放/重复投递被正确挡住，in_flight 说明并发实例在抢同一条，
// not_claimable 则需要人工看——消息存在，但状态既不是待处理也不是可接管的处理中。
func RecordKafkaDuplicate(ctx context.Context, topic, reason string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if kafkaDuplicateNum != nil {
		kafkaDuplicateNum.Add(ctx, 1, metric.WithAttributes(
			attribute.String("topic", topic),
			attribute.String("reason", reason),
		))
	}
}

// RecordMessageFinalizeFailure 记录「业务已完成、但消息终态没写进去」的不一致。
//
// 这不是普通的消息处理失败：模型已经调用并计费，重试只会重复调用，
// 因此 handler 会提交 offset，只留下一条 PROCESSING 记录。这个指标是
// 唯一能把它和「正常处理中」区分开的信号，必须单独告警。
func RecordMessageFinalizeFailure(ctx context.Context, topic string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if kafkaFinalizeFailNum != nil {
		kafkaFinalizeFailNum.Add(ctx, 1, metric.WithAttributes(attribute.String("topic", topic)))
	}
}

// RecordQAIdempotency 记录问答服务按 request_id 做业务幂等的结果。
//
// hit 说明重放/重复投递被下游兜住，没有再次调用模型；concurrent_hit 说明
// 两个实例同时执行、后写入者回查到了先写入的答案；conflict 则可能是调用方
// 复用了别人的幂等键，或同一键携带了不同问题，需要按错误日志排查。
// result 只接受固定枚举，不把 request_id 放进标签，避免高基数。
func RecordQAIdempotency(ctx context.Context, result string) {
	reliabilityOnce.Do(initReliabilityMetrics)
	if qaIdempotencyNum != nil {
		qaIdempotencyNum.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
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
	gatewayConnOnce     sync.Once
	gatewayConnCounter  metric.Int64Counter
	gatewayAuthFailNum  metric.Int64Counter
	gatewayDropNum      metric.Int64Counter
	gatewayPruneNum     metric.Int64Counter
	gatewayRevokeNum    metric.Int64Counter
	gatewayLogReportNum metric.Int64Counter
	gatewayLogReportDur metric.Float64Histogram
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
	gatewayLogReportNum, _ = meter.Int64Counter(
		"logos.gateway.log_report.count",
		metric.WithDescription("网关访问日志上报计数（result=enqueued/dropped/ok/error）"),
	)
	gatewayLogReportDur, _ = meter.Float64Histogram(
		"logos.gateway.log_report.duration",
		metric.WithDescription("网关访问日志单条上报耗时"),
		metric.WithUnit("s"),
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

// RecordGatewayLogReport 记录一次访问日志上报结果。
//
// result=dropped 是这套旁路设计的关键信号：日志可以丢，但必须知道丢了多少。
// 没有这个计数，队列满时的行为在监控上和「一切正常」完全一样。
func RecordGatewayLogReport(ctx context.Context, result string, durationSeconds float64) {
	gatewayConnOnce.Do(initGatewayMetrics)
	if gatewayLogReportNum != nil {
		gatewayLogReportNum.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
	if gatewayLogReportDur != nil && durationSeconds > 0 {
		gatewayLogReportDur.Record(ctx, durationSeconds, metric.WithAttributes(attribute.String("result", result)))
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

// LLM 结构化输出修复指标：解析失败后的一次定向修复是否奏效。
// 只看 parse failed 无法区分「模型偶尔抽风」和「prompt/schema 真的有问题」；
// 修复成功率高说明是随机波动，修复也失败说明该改 prompt 或校验逻辑。
var (
	llmRepairOnce    sync.Once
	llmRepairCounter metric.Int64Counter
)

func initLLMRepairMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	llmRepairCounter, _ = meter.Int64Counter(
		"logos.llm.repair.count",
		metric.WithDescription("LLM 结构化输出修复结果计数（result=fixed/failed）"),
	)
}

// RecordLLMRepair 记录一次解析失败后的修复结果。
func RecordLLMRepair(ctx context.Context, module, result string) {
	llmRepairOnce.Do(initLLMRepairMetrics)
	if llmRepairCounter != nil {
		llmRepairCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("module", module),
			attribute.String("result", result),
		))
	}
}

// 数据库访问指标：GORM 初始化时统一挂回调，所有服务、所有 DAO 自动生效。
// 指标名用 statement 而不是 query：写操作（create/update/delete）也走这里。
var (
	dbMetricsOnce      sync.Once
	dbStatementDur     metric.Float64Histogram
	dbSlowQueryCounter metric.Int64Counter
)

func initDBMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	dbStatementDur, _ = meter.Float64Histogram(
		"logos.db.statement.duration",
		metric.WithDescription("数据库语句耗时（按 operation/table/result 拆分）"),
		metric.WithUnit("s"),
	)
	dbSlowQueryCounter, _ = meter.Int64Counter(
		"logos.db.slow_query.count",
		metric.WithDescription("超过慢查询阈值的数据库语句数"),
	)
}

// RecordDBQuery 记录一次数据库语句。
// operation 取 query/create/update/delete/row；result 取 ok/failed。
func RecordDBQuery(ctx context.Context, operation, table, result string, durationSeconds float64) {
	dbMetricsOnce.Do(initDBMetrics)
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("table", table),
		attribute.String("result", result),
	)
	if dbStatementDur != nil {
		dbStatementDur.Record(ctx, durationSeconds, attrs)
	}
}

// RecordDBSlowQuery 记录一次慢查询。
// 单独计数而不是只依赖直方图：告警按「每分钟慢查询条数」比按分位数更直观。
func RecordDBSlowQuery(ctx context.Context, operation, table string) {
	dbMetricsOnce.Do(initDBMetrics)
	if dbSlowQueryCounter != nil {
		dbSlowQueryCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("table", table),
		))
	}
}

// DBPoolStats 是 database/sql 连接池的一次快照。
//
// 单条 SQL 耗时只能回答「这条语句慢不慢」，回答不了「请求是不是在等连接」。
// 连接池打满时，SQL 甚至还没开始执行，业务侧看到的却只是整体延迟上升。
type DBPoolStats struct {
	OpenConnections     int64
	InUse               int64
	Idle                int64
	WaitCount           int64
	WaitDurationSeconds float64
}

var (
	dbPoolStatsOnce sync.Once
)

// RegisterDBPoolStats 注册连接池异步观测。
//
// 使用 OTel ObservableGauge / ObservableCounter，由采集器在 scrape 时回调，
// 不额外启动采样 goroutine，也不会在连接池空闲时产生空转。
// WaitCount / WaitDuration 是 database/sql 的累计值，因此用 ObservableCounter
// 表达，Prometheus 侧可以直接 rate()，而不是把它伪装成瞬时 gauge。
func RegisterDBPoolStats(stats func() DBPoolStats) {
	if stats == nil {
		return
	}

	dbPoolStatsOnce.Do(func() {
		meter := otel.GetMeterProvider().Meter(
			meterName,
			metric.WithInstrumentationVersion("1.0.0"),
		)

		connections, _ := meter.Int64ObservableGauge(
			"logos.db.pool.connections",
			metric.WithDescription("数据库连接池连接数（state=open/in_use/idle）"),
		)
		waitCount, _ := meter.Int64ObservableCounter(
			"logos.db.pool.wait.count",
			metric.WithDescription("等待获取数据库连接的累计次数"),
		)
		waitDuration, _ := meter.Float64ObservableCounter(
			"logos.db.pool.wait.duration",
			metric.WithDescription("等待获取数据库连接的累计耗时"),
			metric.WithUnit("s"),
		)

		_, _ = meter.RegisterCallback(
			func(_ context.Context, observer metric.Observer) error {
				snapshot := stats()
				observer.ObserveInt64(connections, snapshot.OpenConnections,
					metric.WithAttributes(attribute.String("state", "open")))
				observer.ObserveInt64(connections, snapshot.InUse,
					metric.WithAttributes(attribute.String("state", "in_use")))
				observer.ObserveInt64(connections, snapshot.Idle,
					metric.WithAttributes(attribute.String("state", "idle")))
				observer.ObserveInt64(waitCount, snapshot.WaitCount)
				observer.ObserveFloat64(waitDuration, snapshot.WaitDurationSeconds)
				return nil
			},
			connections,
			waitCount,
			waitDuration,
		)
	})
}

// 保留期清理指标：清理任务本身也要可观测。
// 只写日志会导致「表在增长，但没人知道清理有没有成功」；
// result=failed 且 rows=0 可以作为持续失败告警的依据。
var (
	retentionCleanupOnce     sync.Once
	retentionCleanupRows     metric.Int64Counter
	retentionCleanupFailures metric.Int64Counter
)

func initRetentionCleanupMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	retentionCleanupRows, _ = meter.Int64Counter(
		"logos.retention.cleanup.rows",
		metric.WithDescription("按保留期成功清理的行数（table/result）"),
	)
	retentionCleanupFailures, _ = meter.Int64Counter(
		"logos.retention.cleanup.failures",
		metric.WithDescription("保留期清理失败次数（table/result）"),
	)
}

// RecordRetentionCleanup 记录一次保留期清理结果。
// table 使用固定表名，不使用会话 ID / 主键，避免指标基数失控。
func RecordRetentionCleanup(ctx context.Context, table, result string, rows int64) {
	retentionCleanupOnce.Do(initRetentionCleanupMetrics)
	attrs := metric.WithAttributes(
		attribute.String("table", table),
		attribute.String("result", result),
	)
	if result != "ok" {
		if retentionCleanupFailures != nil {
			retentionCleanupFailures.Add(ctx, 1, attrs)
		}
		return
	}
	if retentionCleanupRows == nil {
		return
	}
	if rows < 0 {
		rows = 0
	}
	retentionCleanupRows.Add(ctx, rows, attrs)
}

// Elasticsearch 访问指标：检索链路的耗时与失败原因。
// 只统计「调用成功/失败」不够：ctx 透传之后，客户端断连会产生大量 canceled，
// 如果不单独打标签，这些请求会被算进 failed，把真实的 ES 故障率掩盖掉。
// index 用索引名而不是文档 ID，避免高基数。
var (
	esRequestOnce     sync.Once
	esRequestCounter  metric.Int64Counter
	esRequestDuration metric.Float64Histogram
)

func initESMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	esRequestCounter, _ = meter.Int64Counter(
		"logos.es.request.count",
		metric.WithDescription("Elasticsearch 请求结果计数（result=ok/failed/canceled）"),
	)
	esRequestDuration, _ = meter.Float64Histogram(
		"logos.es.request.duration",
		metric.WithDescription("Elasticsearch 请求耗时（按 index/operation/result 拆分）"),
		metric.WithUnit("s"),
	)
}

// RecordESRequest 记录一次 ES 访问。operation 取 search/add/update/delete/get/
// create_index/delete_index/refresh/stats；result 取 ok/failed/canceled。
//
// 耗时直方图也带 result 标签：canceled 请求通常在几毫秒内返回，
// 混进整体分位数会把 P99 拉低，看起来像「变快了」，实际是失败得更早。
func RecordESRequest(ctx context.Context, index, operation, result string, durationSeconds float64) {
	esRequestOnce.Do(initESMetrics)
	attrs := metric.WithAttributes(
		attribute.String("index", index),
		attribute.String("operation", operation),
		attribute.String("result", result),
	)
	if esRequestCounter != nil {
		esRequestCounter.Add(ctx, 1, attrs)
	}
	if esRequestDuration != nil {
		esRequestDuration.Record(ctx, durationSeconds, attrs)
	}
}

// Outbox 积压指标：只看投递成功率会漏掉「全部成功但在持续堆积」。
// 成功率高 + backlog 持续上涨，通常意味着生产速度已经超过投递吞吐，
// 这时候需要扩容 relay 或提高批量，而不是继续看 failed 计数。
var (
	outboxBacklogOnce  sync.Once
	outboxPendingGauge metric.Int64Gauge
	outboxOldestGauge  metric.Float64Gauge
)

func initOutboxBacklogMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	outboxPendingGauge, _ = meter.Int64Gauge(
		"logos.outbox.pending.count",
		metric.WithDescription("当前 pending 状态的 Outbox 消息数（含退避等待中的消息）"),
	)
	outboxOldestGauge, _ = meter.Float64Gauge(
		"logos.outbox.oldest.pending.seconds",
		metric.WithDescription("最老一条 pending 消息的等待时长，直接反映投递延迟"),
		metric.WithUnit("s"),
	)
}

// RecordOutboxBacklog 记录一次积压快照。
// pending 为可投递 + 退避中的消息总数；oldestSeconds 为最老一条的等待秒数。
func RecordOutboxBacklog(ctx context.Context, pending int64, oldestSeconds float64) {
	outboxBacklogOnce.Do(initOutboxBacklogMetrics)
	if outboxPendingGauge != nil {
		outboxPendingGauge.Record(ctx, pending)
	}
	if outboxOldestGauge != nil {
		outboxOldestGauge.Record(ctx, oldestSeconds)
	}
}

// 限流降级指标：Redis 不可用时是否切换到单机兜底。
// 没有这个计数，限流静默 fail-open 和「流量过低没触发」在监控上无法区分。
var (
	rateLimitDegradedOnce    sync.Once
	rateLimitDegradedCounter metric.Int64Counter
)

func initRateLimitDegradedMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	rateLimitDegradedCounter, _ = meter.Int64Counter(
		"logos.ratelimit.degraded.count",
		metric.WithDescription("Redis 限流不可用、切换单机兜底的次数（layer=global/user/ip/burst）"),
	)
}

// RecordRateLimitDegraded 记录一次限流降级。
func RecordRateLimitDegraded(ctx context.Context, layer string) {
	rateLimitDegradedOnce.Do(initRateLimitDegradedMetrics)
	if rateLimitDegradedCounter != nil {
		rateLimitDegradedCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("layer", layer)))
	}
}

// 语义缓存指标：命中率直接对应「省了多少次 LLM 调用」。
// 只看平均耗时看不出收益：miss 也要先做一次 embedding，命中才省掉生成。
var (
	semanticCacheOnce    sync.Once
	semanticCacheCounter metric.Int64Counter
)

func initSemanticCacheMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	semanticCacheCounter, _ = meter.Int64Counter(
		"logos.ai.semantic_cache.count",
		metric.WithDescription("语义缓存操作结果（op=lookup/store, result=hit/miss/error/...）"),
	)
}

// RecordSemanticCache 记录一次语义缓存操作。
// op 取 lookup/store；lookup 的 result 取 hit/miss/error/disabled/unavailable，
// store 取 ok/failed。
func RecordSemanticCache(ctx context.Context, op, result string) {
	semanticCacheOnce.Do(initSemanticCacheMetrics)
	if semanticCacheCounter != nil {
		semanticCacheCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("op", op),
			attribute.String("result", result),
		))
	}
}

// 缓存回源合并指标：result=fill 表示真正查了库/模型，result=shared 表示
// 搭了别人回源的便车。只看缓存命中率无法回答「缓存失效瞬间有多少请求
// 同时打到了后端」，shared 与 fill 的比值才是击穿防护的实际效果。
var (
	cacheFillOnce    sync.Once
	cacheFillCounter metric.Int64Counter
)

func initCacheFillMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	cacheFillCounter, _ = meter.Int64Counter(
		"logos.cache.fill.count",
		metric.WithDescription("缓存回源请求计数（result=fill/shared/error）"),
	)
}

// RecordCacheFill 记录一次缓存回源。
// result 取 fill（真正执行回源）/shared（合并到别人的回源）/error（回源失败）。
func RecordCacheFill(ctx context.Context, cache, result string) {
	cacheFillOnce.Do(initCacheFillMetrics)
	if cacheFillCounter != nil {
		cacheFillCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("cache", cache),
			attribute.String("result", result),
		))
	}
}

// 消息幂等指标：replayed 占总量的比例就是客户端重试率。
// 只看「消息条数」看不出重试风暴：重试被幂等挡住了，业务上一切正常，
// 但网关/客户端可能正在成倍放大请求。replayed 突增是排查网络抖动的入口。
var (
	messageIdempotencyOnce    sync.Once
	messageIdempotencyCounter metric.Int64Counter
)

func initMessageIdempotencyMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	messageIdempotencyCounter, _ = meter.Int64Counter(
		"logos.message.idempotency.count",
		metric.WithDescription("消息发送幂等结果计数（result=created/replayed）"),
	)
}

// RecordMessageIdempotency 记录一次幂等写入结果。
func RecordMessageIdempotency(ctx context.Context, result string) {
	messageIdempotencyOnce.Do(initMessageIdempotencyMetrics)
	if messageIdempotencyCounter != nil {
		messageIdempotencyCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
}

// Redis 调用指标：缓存是加速器，不能变成故障放大器。
// 只有「接口慢」时无法判断是 Redis 抖动还是下游 DB/检索慢，
// 所以缓存客户端自己带 op/result 标签和耗时直方图。
var (
	redisOpOnce     sync.Once
	redisOpCounter  metric.Int64Counter
	redisOpDuration metric.Float64Histogram
)

func initRedisOpMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	redisOpCounter, _ = meter.Int64Counter(
		"logos.cache.redis.count",
		metric.WithDescription("Redis 操作结果计数（op=get/set/delete/...，result=hit/miss/ok/error/timeout/canceled）"),
	)
	redisOpDuration, _ = meter.Float64Histogram(
		"logos.cache.redis.duration",
		metric.WithDescription("Redis 单次操作耗时"),
		metric.WithUnit("s"),
	)
}

// RecordCacheOp 记录一次 Redis 操作。
func RecordCacheOp(ctx context.Context, op, result string, durationSeconds float64) {
	redisOpOnce.Do(initRedisOpMetrics)
	attrs := metric.WithAttributes(
		attribute.String("op", op),
		attribute.String("result", result),
	)
	if redisOpCounter != nil {
		redisOpCounter.Add(ctx, 1, attrs)
	}
	if redisOpDuration != nil {
		redisOpDuration.Record(ctx, durationSeconds, attrs)
	}
}

// 工具执行指标：工具是模型与外部系统之间的故障边界。
// 只看「工具调用成功/失败」无法回答两个关键问题：失败后有没有重试、
// 重试是否被熔断器挡住。拆成 call/retry/breaker 三个计数后，
// retry 上升但 rejected 稳定说明只是抖动；rejected 上升说明后端持续不可用。
var (
	toolCallOnce       sync.Once
	toolCallCounter    metric.Int64Counter
	toolRetryCounter   metric.Int64Counter
	toolBreakerCounter metric.Int64Counter
)

func initToolMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	toolCallCounter, _ = meter.Int64Counter(
		"logos.tool.call.count",
		metric.WithDescription("工具单次尝试结果计数（result=ok/error/timeout/canceled/rejected）"),
	)
	toolRetryCounter, _ = meter.Int64Counter(
		"logos.tool.retry.count",
		metric.WithDescription("工具因可重试错误发起的重试次数"),
	)
	toolBreakerCounter, _ = meter.Int64Counter(
		"logos.tool.breaker.count",
		metric.WithDescription("工具熔断器状态事件（event=open/half_open/closed/open_rejected/half_open_rejected）"),
	)
}

// RecordToolCall 记录一次工具尝试结果。
func RecordToolCall(ctx context.Context, tool, result string) {
	toolCallOnce.Do(initToolMetrics)
	if toolCallCounter != nil {
		toolCallCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("tool", tool),
			attribute.String("result", result),
		))
	}
}

// QQ Bridge 媒体中转指标。
//
// 媒体链路是「尽力而为」：失败会降级成文本占位，不会让消息发送失败，
// 因此只看消息成功率永远发现不了 QQ CDN 或 MinIO 已经在持续失败。
// 用 stage 区分 download/upload/queue，result 区分 ok/failed/canceled/too_large。
// queue 单独统计「等槽位等到预算耗尽」：它不是下载/上传故障，
// 处理动作是扩容，而不是去查 QQ CDN。
var (
	qqMediaOnce    sync.Once
	qqMediaCounter metric.Int64Counter
	qqMediaBytes   metric.Int64Histogram
)

func initQQMediaMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	qqMediaCounter, _ = meter.Int64Counter(
		"logos.qqbridge.media.count",
		metric.WithDescription("QQ 媒体中转结果计数（stage=download/upload/queue，result=ok/failed/canceled/too_large）"),
	)
	qqMediaBytes, _ = meter.Int64Histogram(
		"logos.qqbridge.media.bytes",
		metric.WithDescription("QQ 媒体中转文件大小分布"),
		metric.WithUnit("By"),
	)
}

// RecordQQMediaRelay 记录一次 QQ 媒体中转阶段结果。
func RecordQQMediaRelay(ctx context.Context, stage, result string, size int64) {
	qqMediaOnce.Do(initQQMediaMetrics)
	if qqMediaCounter != nil {
		qqMediaCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("stage", stage),
			attribute.String("result", result),
		))
	}
	if size > 0 && qqMediaBytes != nil {
		qqMediaBytes.Record(ctx, size)
	}
}

// QQ 出站消息指标。
//
// result=ok/failed/skipped；failed 表示可重试的发送失败（会触发 Kafka 重投），
// skipped 表示消息本身不可恢复（缺字段/不支持的会话类型），不重试。
// 两者混在一起会把「数据问题」误判成「QQ 不可用」。
var (
	qqOutboundOnce    sync.Once
	qqOutboundCounter metric.Int64Counter
)

func initQQOutboundMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	qqOutboundCounter, _ = meter.Int64Counter(
		"logos.qqbridge.outbound.count",
		metric.WithDescription("QQ 出站消息结果计数（result=ok/failed/skipped）"),
	)
}

// RecordQQOutbound 记录一次 QQ 出站结果。reason 区分具体失败原因。
func RecordQQOutbound(ctx context.Context, result, reason string) {
	qqOutboundOnce.Do(initQQOutboundMetrics)
	if qqOutboundCounter != nil {
		qqOutboundCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("result", result),
			attribute.String("reason", reason),
		))
	}
}

// QQ 快速路径指标。
//
// 快速路径直连 Bot，失败后靠补偿事件交回慢路径。只看消息成功率看不出
// 「快速路径在持续失败、每次都退回 Kafka 往返」，必须单独统计补偿次数。
// result=recovered 表示补偿事件已成功发布；failed 表示连补偿都没发出去，
// 用户这条消息不会再有任何回复；busy 表示并发槽位打满、快速路径根本没跑，
// 消息退回慢路径（容量信号，不是故障）。
var (
	qqFastPathOnce    sync.Once
	qqFastPathCounter metric.Int64Counter
)

func initQQFastPathMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	qqFastPathCounter, _ = meter.Int64Counter(
		"logos.qqbridge.fastpath.count",
		metric.WithDescription("QQ 快速路径补偿结果计数（result=recovered/failed）"),
	)
}

// RecordQQFastPath 记录一次快速路径失败后的补偿结果。
func RecordQQFastPath(ctx context.Context, result, reason string) {
	qqFastPathOnce.Do(initQQFastPathMetrics)
	if qqFastPathCounter != nil {
		qqFastPathCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("result", result),
			attribute.String("reason", reason),
		))
	}
}

// RecordToolRetry 记录一次工具重试。
func RecordToolRetry(ctx context.Context, tool string) {
	toolCallOnce.Do(initToolMetrics)
	if toolRetryCounter != nil {
		toolRetryCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("tool", tool)))
	}
}

// RecordToolBreaker 记录一次熔断器状态事件。
func RecordToolBreaker(ctx context.Context, tool, event string) {
	toolCallOnce.Do(initToolMetrics)
	if toolBreakerCounter != nil {
		toolBreakerCounter.Add(ctx, 1, metric.WithAttributes(
			attribute.String("tool", tool),
			attribute.String("event", event),
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

// 计划抽取任务调度指标。
//
// scheduled_at 曾经只存不调度：任务永远停在待执行，接口却返回创建成功。
// 调度器上线后必须能区分「执行成功」「执行失败」「被其他实例领走」三种结果，
// 否则多副本同时扫描只会看到一堆无法解释的数字。result 只取 success /
// failure / skipped / stale_reaped 四个固定值，任务 ID 不进标签，避免基数爆炸。
var (
	extractionScheduledOnce sync.Once
	extractionScheduledNum  metric.Int64Counter
	extractionScheduleScan  metric.Int64Counter
)

func initExtractionScheduledMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	extractionScheduledNum, _ = meter.Int64Counter(
		"logos.extraction.scheduled.count",
		metric.WithDescription("计划抽取任务执行结果计数（result=success/failure/skipped/stale_reaped）"),
	)
	extractionScheduleScan, _ = meter.Int64Counter(
		"logos.extraction.schedule.scan_failure.count",
		metric.WithDescription("计划抽取任务扫描失败次数（数据库不可用或查询超时）"),
	)
}

// RecordExtractionScheduledRun 记录一批计划抽取任务的执行结果。
func RecordExtractionScheduledRun(ctx context.Context, result string, count int64) {
	if count <= 0 {
		return
	}
	extractionScheduledOnce.Do(initExtractionScheduledMetrics)
	if extractionScheduledNum != nil {
		extractionScheduledNum.Add(ctx, count, metric.WithAttributes(attribute.String("result", result)))
	}
}

// RecordExtractionScheduleScanFailure 记录一次到期任务扫描失败。
// 扫描失败意味着所有计划任务都不会执行，且不会产生任何用户侧报错，必须单独告警。
func RecordExtractionScheduleScanFailure(ctx context.Context) {
	extractionScheduledOnce.Do(initExtractionScheduledMetrics)
	if extractionScheduleScan != nil {
		extractionScheduleScan.Add(ctx, 1)
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
		metric.WithDescription("真实进入 handler 的 gRPC 请求数（method, code）"),
	)

	m.RequestDuration, _ = meter.Float64Histogram(
		"logos.grpc.request.duration",
		metric.WithDescription("gRPC handler 处理耗时（method, code）"),
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

// RecordRequest 记录一次真实进入 handler 的 gRPC 请求。
//
// code 取 gRPC codes.Code 的字符串形式（OK/InvalidArgument/Internal...）。
// 不带 code 标签时，失败请求会被算进同一个总量，成功率只能靠日志估算，
// 而生产日志通常并不完整；失败延迟与成功延迟混在同一个直方图里，
// 也会让 P99 无法区分「慢」和「一直在报错」。
func (m *ServiceMeters) RecordRequest(ctx context.Context, method, code string, durationSeconds float64) {
	attrs := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("code", code),
	)
	if m.RequestCount != nil {
		m.RequestCount.Add(ctx, 1, attrs)
	}
	if m.RequestDuration != nil {
		m.RequestDuration.Record(ctx, durationSeconds, attrs)
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

// 文档孤儿回收指标。
// 文档处理是进程内异步任务，进程重启/OOM 会让 Pending/Processing 永久卡住；
// reaped 表示本轮救回了多少条，failures 表示回收器本身是否健康。
var (
	documentReapOnce sync.Once
	documentReapNum  metric.Int64Counter
	documentReapFail metric.Int64Counter
)

func initDocumentReapMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	documentReapNum, _ = meter.Int64Counter(
		"logos.document.reap.count",
		metric.WithDescription("回收的孤儿文档数（长时间停留在 Pending/Processing）"),
	)
	documentReapFail, _ = meter.Int64Counter(
		"logos.document.reap.failure.count",
		metric.WithDescription("文档孤儿回收失败次数"),
	)
}

func RecordDocumentReaped(ctx context.Context, count int64) {
	if count <= 0 {
		return
	}
	documentReapOnce.Do(initDocumentReapMetrics)
	if documentReapNum != nil {
		documentReapNum.Add(ctx, count)
	}
}

func RecordDocumentReapFailure(ctx context.Context) {
	documentReapOnce.Do(initDocumentReapMetrics)
	if documentReapFail != nil {
		documentReapFail.Add(ctx, 1)
	}
}

// 就绪探测指标。
//
// 与请求失败率分开：依赖挂了但还没有请求打进来时，失败率是完全静默的，
// 而 readiness 指标能提前暴露「这个实例已经被摘流量」。result 只有
// ready / not_ready 两个值，不做成功率的分子分母，避免正常探测稀释故障。
var (
	readinessOnce  sync.Once
	readinessCount metric.Int64Counter
)

func initReadinessMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	readinessCount, _ = meter.Int64Counter(
		"logos.health.readiness.count",
		metric.WithDescription("就绪探测结果计数（result=ready/not_ready）"),
	)
}

// RecordReadiness 记录一次就绪状态判定。
func RecordReadiness(ctx context.Context, service, result string) {
	readinessOnce.Do(initReadinessMetrics)
	if readinessCount != nil {
		readinessCount.Add(ctx, 1, metric.WithAttributes(
			attribute.String("service", service),
			attribute.String("result", result),
		))
	}
}

// 监控数据保留策略指标。
//
// 只记录「删了多少」不够：清理失败时行数为 0，与「本来就没数据」无法区分，
// 所以 result=ok/failed 必须分开。连续 failed 意味着表会重新开始膨胀。
var (
	monitoringRetentionOnce    sync.Once
	monitoringRetentionDeleted metric.Int64Counter
)

func initMonitoringRetentionMetrics() {
	meter := otel.GetMeterProvider().Meter(
		meterName,
		metric.WithInstrumentationVersion("1.0.0"),
	)
	monitoringRetentionDeleted, _ = meter.Int64Counter(
		"logos.monitoring.retention.deleted",
		metric.WithDescription("监控数据保留策略删除的行数（kind=metrics/logs/alerts，result=ok/failed）"),
	)
}

// RecordMonitoringRetention 记录一次保留策略清理的结果与删除行数。
func RecordMonitoringRetention(ctx context.Context, kind, result string, rows int64) {
	monitoringRetentionOnce.Do(initMonitoringRetentionMetrics)
	if monitoringRetentionDeleted != nil {
		monitoringRetentionDeleted.Add(ctx, rows, metric.WithAttributes(
			attribute.String("kind", kind),
			attribute.String("result", result),
		))
	}
}
