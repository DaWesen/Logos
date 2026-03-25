namespace go monitoring

include "common.thrift"

// 监控指标类型
enum MetricType {
    CPU = 1
    MEMORY = 2
    DISK = 3
    NETWORK = 4
    REQUEST = 5
    ERROR = 6
    LATENCY = 7
    THROUGHPUT = 8
}

// 告警级别
enum AlertLevel {
    INFO = 1
    WARNING = 2
    ERROR = 3
    CRITICAL = 4
}

// 监控指标
struct Metric {
    1: string id
    2: string serviceName
    3: MetricType type
    4: double value
    5: string unit
    6: i64 timestamp
    7: map<string, string> tags
}

// 告警
struct Alert {
    1: string id
    2: string serviceName
    3: AlertLevel level
    4: string message
    5: string metricName
    6: double metricValue
    7: double threshold
    8: i64 timestamp
    9: bool resolved
    10: optional string resolutionTime
}

// 日志
struct Log {
    1: string id
    2: string serviceName
    3: string level
    4: string message
    5: i64 timestamp
    6: map<string, string> fields
}

// 服务状态
struct ServiceStatus {
    1: string serviceName
    2: string status // UP, DOWN, DEGRADED
    3: i64 lastCheckTime
    4: optional string errorMessage
    5: map<string, string> metadata
}

// 记录指标请求
struct RecordMetricReq {
    1: string serviceName
    2: MetricType type
    3: double value
    4: string unit
    5: optional map<string, string> tags
}

// 批量记录指标请求
struct BatchRecordMetricReq {
    1: list<RecordMetricReq> metrics
}

// 记录日志请求
struct RecordLogReq {
    1: string serviceName
    2: string level
    3: string message
    4: optional map<string, string> fields
}

// 批量记录日志请求
struct BatchRecordLogReq {
    1: list<RecordLogReq> logs
}

// 查询指标请求
struct QueryMetricReq {
    1: string serviceName
    2: MetricType type
    3: i64 startTime
    4: i64 endTime
    5: optional map<string, string> tags
}

// 指标响应
struct MetricResp {
    1: common.BaseResp BaseResp
    2: list<Metric> metrics
    3: i64 total
}

// 查询告警请求
struct QueryAlertReq {
    1: optional string serviceName
    2: optional AlertLevel level
    3: optional bool resolved
    4: i64 startTime
    5: i64 endTime
}

// 告警响应
struct AlertResp {
    1: common.BaseResp BaseResp
    2: list<Alert> alerts
    3: i64 total
}

// 查询日志请求
struct QueryLogReq {
    1: optional string serviceName
    2: optional string level
    3: optional string query
    4: i64 startTime
    5: i64 endTime
    6: i32 page
    7: i32 pageSize
}

// 日志响应
struct LogResp {
    1: common.BaseResp BaseResp
    2: list<Log> logs
    3: i64 total
}

// 监控服务接口
service MonitoringService {
    // 指标管理
    common.BaseResp RecordMetric(1: RecordMetricReq req)
    common.BaseResp BatchRecordMetric(1: BatchRecordMetricReq req)
    MetricResp QueryMetrics(1: QueryMetricReq req)
    
    // 日志管理
    common.BaseResp RecordLog(1: RecordLogReq req)
    common.BaseResp BatchRecordLog(1: BatchRecordLogReq req)
    LogResp QueryLogs(1: QueryLogReq req)
    
    // 告警管理
    AlertResp QueryAlerts(1: QueryAlertReq req)
    common.BaseResp ResolveAlert(1: string alertId)
    
    // 服务状态
    common.BaseResp UpdateServiceStatus(1: ServiceStatus status)
    common.BaseResp GetServiceStatus(1: string serviceName)
    common.BaseResp ListServiceStatus()
}
