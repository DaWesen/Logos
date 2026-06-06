import client from './client'

function extractData<T>(res: { data: unknown }, fallback: T): T {
  const d = res.data as Record<string, unknown>
  if (!d) return fallback
  if (d.data !== undefined && d.data !== null) return d.data as T
  if (typeof d === 'object' && !Array.isArray(d) && !('code' in d)) return d as T
  return fallback
}

export interface Metric {
  id?: string
  service_name: string
  type: number
  value: number
  unit?: string
  timestamp?: number
  tags?: Record<string, string>
}

export interface Alert {
  id?: string
  service_name: string
  level: number
  message: string
  metric_name: string
  metric_value: number
  threshold: number
  timestamp?: number
  resolved: boolean
  resolution_time?: string
}

export interface LogEntry {
  id?: string
  service_name: string
  level: string
  message: string
  timestamp?: number
  tags?: Record<string, string>
}

export interface ServiceStatus {
  service_name: string
  status: string
  last_check_time?: number
  error_message?: string
  metadata?: Record<string, string>
}

export async function recordMetric(metric: Omit<Metric, 'id' | 'timestamp'>) {
  const res = await client.post('/monitoring/metric', metric)
  return res.data
}

export async function batchRecordMetrics(metrics: Omit<Metric, 'id' | 'timestamp'>[]) {
  const res = await client.post('/monitoring/metric/batch', { metrics })
  return res.data
}

export async function queryMetrics(params: {
  service_name?: string
  type?: number
  start_time?: number
  end_time?: number
}): Promise<Metric[]> {
  try {
    const res = await client.get('/monitoring/metric', { params })
    return extractData<Metric[]>(res, [])
  } catch {
    return []
  }
}

export async function recordLog(log: Omit<LogEntry, 'id' | 'timestamp'>) {
  const res = await client.post('/monitoring/log', log)
  return res.data
}

export async function batchRecordLogs(logs: Omit<LogEntry, 'id' | 'timestamp'>[]) {
  const res = await client.post('/monitoring/log/batch', { logs })
  return res.data
}

export async function queryLogs(params: {
  service_name?: string
  level?: string
  query?: string
  start_time?: number
  end_time?: number
  page?: number
  page_size?: number
}): Promise<LogEntry[]> {
  try {
    const res = await client.get('/monitoring/log', { params })
    return extractData<LogEntry[]>(res, [])
  } catch {
    return []
  }
}

export async function queryAlerts(params: {
  service_name?: string
  level?: number
  resolved?: boolean
  start_time?: number
  end_time?: number
}): Promise<Alert[]> {
  try {
    const res = await client.get('/monitoring/alert', { params })
    return extractData<Alert[]>(res, [])
  } catch {
    return []
  }
}

export async function resolveAlert(alertId: string) {
  const res = await client.put(`/monitoring/alert/${alertId}/resolve`)
  return res.data
}

export async function updateServiceStatus(status: {
  service_name: string
  status: string
  error_message?: string
  metadata?: Record<string, string>
}) {
  const res = await client.put('/monitoring/service-status', status)
  return res.data
}

export async function getServiceStatus(serviceName: string): Promise<ServiceStatus | null> {
  try {
    const res = await client.get('/monitoring/service-status', { params: { service_name: serviceName } })
    return extractData<ServiceStatus | null>(res, null)
  } catch {
    return null
  }
}

export interface ServiceInfo {
  name: string
  port: number
  address: string
  etcd_name: string
}

export async function listServices(): Promise<ServiceInfo[]> {
  try {
    const res = await client.get('/monitoring/services')
    return extractData<ServiceInfo[]>(res, [])
  } catch {
    return []
  }
}

export async function listServiceStatus(): Promise<ServiceStatus[]> {
  try {
    const res = await client.get('/monitoring/service-status/list')
    return extractData<ServiceStatus[]>(res, [])
  } catch {
    return []
  }
}

export interface BenchConfig {
  url: string
  users: number
  duration: number
  rps: number
  scenario: string
}

export interface BenchResult {
  total: number
  success: number
  failed: number
  timeout: number
  successRate: number
  qps: number
  avgLatency: number
  minLatency: number
  maxLatency: number
  p50: number
  p90: number
  p95: number
  p99: number
  statusCodes: Record<number, number>
  errors: Record<string, number>
  running: boolean
  elapsed: number
}

export const BENCH_SCENARIOS = [
  { value: 'health', label: '健康检查' },
  { value: 'login', label: '用户登录' },
  { value: 'get_user', label: '获取用户' },
  { value: 'chat_history', label: '聊天历史' },
  { value: 'send_message', label: '发送消息' },
  { value: 'bot_list', label: 'Bot列表' },
  { value: 'contacts', label: '联系人' },
  { value: 'mixed', label: '混合场景' },
]
