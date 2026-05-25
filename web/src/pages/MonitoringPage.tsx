import { useState, useEffect, useCallback } from 'react'
import { Activity, Server, AlertTriangle, Search, Clock, BarChart3, CheckCircle, XCircle, FileText, RefreshCw, Zap } from 'lucide-react'
import {
  queryMetrics,
  queryLogs,
  queryAlerts,
  listServiceStatus,
  listServices,
  resolveAlert,
  type Metric,
  type Alert,
  type LogEntry,
  type ServiceStatus,
  type ServiceInfo
} from '@/api/monitoring'
import './MonitoringPage.css'

const FALLBACK_SERVICES = [
  'logos.gateway', 'logos.user', 'logos.monitoring', 'logos.billing', 'logos.im',
  'logos.chat', 'logos.contact', 'logos.message', 'logos.bot', 'logos.vector',
  'logos.summary', 'logos.moderation', 'logos.mcp', 'logos.knowledge',
  'logos.search', 'logos.extraction', 'logos.question', 'logos.recommend', 'logos.collection'
]
const metricTypes = ['CPU', '内存', '磁盘', '网络', '请求', '错误', '延迟', '吞吐量']
const alertLevels = ['INFO', '警告', '错误', '严重']

export default function MonitoringPage() {
  const [activeTab, setActiveTab] = useState<'services' | 'metrics' | 'logs' | 'alerts'>('services')
  const [loading, setLoading] = useState(false)
  const [metrics, setMetrics] = useState<Metric[]>([])
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [serviceStatus, setServiceStatus] = useState<ServiceStatus[]>([])
  const [serviceInfo, setServiceInfo] = useState<ServiceInfo[]>([])

  const [metricFilter, setMetricFilter] = useState({ service_name: '', type: 0 })
  const [logFilter, setLogFilter] = useState({ service_name: '', level: '' })
  const [alertFilter, setAlertFilter] = useState({ service_name: '', level: 0, resolved: undefined as boolean | undefined })

  const serviceNames = serviceInfo.length > 0 ? serviceInfo.map(s => s.name) : FALLBACK_SERVICES

  const loadServices = useCallback(async () => {
    setLoading(true)
    try {
      const [statusData, infoData] = await Promise.all([listServiceStatus(), listServices()])
      setServiceStatus(Array.isArray(statusData) ? statusData : [])
      setServiceInfo(Array.isArray(infoData) ? infoData : [])
    } catch {
      setServiceStatus([])
      setServiceInfo([])
    } finally {
      setLoading(false)
    }
  }, [])

  const loadMetrics = useCallback(async () => {
    setLoading(true)
    try {
      const data = await queryMetrics({
        ...metricFilter,
        start_time: Date.now() - 86400000,
        end_time: Date.now(),
      })
      setMetrics(Array.isArray(data) ? data : [])
    } catch {
      setMetrics([])
    } finally {
      setLoading(false)
    }
  }, [metricFilter])

  const loadLogs = useCallback(async () => {
    setLoading(true)
    try {
      const data = await queryLogs({
        ...logFilter,
        page: 1,
        page_size: 50,
      })
      setLogs(Array.isArray(data) ? data : [])
    } catch {
      setLogs([])
    } finally {
      setLoading(false)
    }
  }, [logFilter])

  const loadAlerts = useCallback(async () => {
    setLoading(true)
    try {
      const data = await queryAlerts({
        ...alertFilter,
        start_time: Date.now() - 86400000,
        end_time: Date.now(),
      })
      setAlerts(Array.isArray(data) ? data : [])
    } catch {
      setAlerts([])
    } finally {
      setLoading(false)
    }
  }, [alertFilter])

  useEffect(() => {
    if (activeTab === 'services') loadServices()
    if (activeTab === 'metrics') loadMetrics()
    if (activeTab === 'logs') loadLogs()
    if (activeTab === 'alerts') loadAlerts()
  }, [activeTab, loadServices, loadMetrics, loadLogs, loadAlerts])

  const formatTime = (ts?: number | string) => {
    if (!ts) return '-'
    const numTs = typeof ts === 'string' ? new Date(ts).getTime() : ts
    if (isNaN(numTs)) return String(ts)
    return new Date(numTs).toLocaleString('zh-CN')
  }

  const getLevelColor = (level: string | number) => {
    const l = typeof level === 'number' ? alertLevels[level] || 'INFO' : level
    switch (l) {
      case 'ERROR': case '严重': return 'red'
      case 'WARN': case '警告': return 'orange'
      case 'INFO': return 'blue'
      default: return 'gray'
    }
  }

  const getStatusIcon = (status: string) => {
    switch (status?.toUpperCase()) {
      case 'UP': return <CheckCircle size={16} style={{ color: 'var(--ba-success)' }} />
      case 'DOWN': return <XCircle size={16} style={{ color: 'var(--ba-error)' }} />
      case 'DEGRADED': return <AlertTriangle size={16} style={{ color: 'var(--ba-warning)' }} />
      default: return <Clock size={16} style={{ color: 'var(--ba-text-light)' }} />
    }
  }

  const getStatusLabel = (status: string) => {
    switch (status?.toUpperCase()) {
      case 'UP': return '运行中'
      case 'DOWN': return '已停止'
      case 'DEGRADED': return '异常'
      default: return '未知'
    }
  }

  const getStatusColor = (status: string) => {
    switch (status?.toUpperCase()) {
      case 'UP': return 'var(--ba-success)'
      case 'DOWN': return 'var(--ba-error)'
      case 'DEGRADED': return 'var(--ba-warning)'
      default: return 'var(--ba-text-light)'
    }
  }

  const handleResolveAlert = async (alertId: string) => {
    try {
      await resolveAlert(alertId)
      loadAlerts()
    } catch {}
  }

  const upCount = serviceStatus.filter(s => s.status?.toUpperCase() === 'UP').length
  const downCount = serviceStatus.filter(s => s.status?.toUpperCase() === 'DOWN').length
  const unresolvedAlerts = alerts.filter(a => !a.resolved).length

  return (
    <div className="monitoring-page">
      <div className="monitoring-header">
        <div>
          <h2>系统观测</h2>
          <p className="monitoring-subtitle">监控系统状态、指标、日志和告警</p>
        </div>
        <button className="ba-btn ba-btn-primary" onClick={() => {
          if (activeTab === 'services') loadServices()
          if (activeTab === 'metrics') loadMetrics()
          if (activeTab === 'logs') loadLogs()
          if (activeTab === 'alerts') loadAlerts()
        }} disabled={loading}>
          <RefreshCw size={16} className={loading ? 'loading-spin' : ''} /> 刷新
        </button>
      </div>

      <div className="monitoring-overview">
        <div className="monitoring-stat-card ba-card">
          <div className="monitoring-stat-icon" style={{ background: 'rgba(34,197,94,0.1)', color: 'var(--ba-success)' }}>
            <CheckCircle size={20} />
          </div>
          <div className="monitoring-stat-info">
            <div className="monitoring-stat-value">{upCount}</div>
            <div className="monitoring-stat-label">服务运行中</div>
          </div>
        </div>
        <div className="monitoring-stat-card ba-card">
          <div className="monitoring-stat-icon" style={{ background: 'rgba(239,68,68,0.1)', color: 'var(--ba-error)' }}>
            <XCircle size={20} />
          </div>
          <div className="monitoring-stat-info">
            <div className="monitoring-stat-value">{downCount}</div>
            <div className="monitoring-stat-label">服务已停止</div>
          </div>
        </div>
        <div className="monitoring-stat-card ba-card">
          <div className="monitoring-stat-icon" style={{ background: 'rgba(59,130,246,0.1)', color: 'var(--ba-primary)' }}>
            <Activity size={20} />
          </div>
          <div className="monitoring-stat-info">
            <div className="monitoring-stat-value">{serviceStatus.length}</div>
            <div className="monitoring-stat-label">服务总数</div>
          </div>
        </div>
        <div className="monitoring-stat-card ba-card">
          <div className="monitoring-stat-icon" style={{ background: 'rgba(245,158,11,0.1)', color: 'var(--ba-warning)' }}>
            <AlertTriangle size={20} />
          </div>
          <div className="monitoring-stat-info">
            <div className="monitoring-stat-value">{unresolvedAlerts}</div>
            <div className="monitoring-stat-label">未解决告警</div>
          </div>
        </div>
      </div>

      <div className="monitoring-tabs">
        <button className={`monitoring-tab ${activeTab === 'services' ? 'active' : ''}`} onClick={() => setActiveTab('services')}>
          <Server size={16} /> 服务状态
        </button>
        <button className={`monitoring-tab ${activeTab === 'metrics' ? 'active' : ''}`} onClick={() => setActiveTab('metrics')}>
          <BarChart3 size={16} /> 指标
        </button>
        <button className={`monitoring-tab ${activeTab === 'logs' ? 'active' : ''}`} onClick={() => setActiveTab('logs')}>
          <FileText size={16} /> 日志
        </button>
        <button className={`monitoring-tab ${activeTab === 'alerts' ? 'active' : ''}`} onClick={() => setActiveTab('alerts')}>
          <AlertTriangle size={16} /> 告警
        </button>
      </div>

      {activeTab === 'services' && (
        <div className="monitoring-content">
          {serviceStatus.length > 0 || serviceInfo.length > 0 ? (
            <div className="service-grid">
              {(() => {
                const reportedNames = new Set(serviceStatus.map(s => s.service_name))
                const unreported = serviceInfo.filter(s => !reportedNames.has(s.name))
                const allServices = [
                  ...serviceStatus.map((service) => {
                    const info = serviceInfo.find(s => s.name === service.service_name)
                    return { ...service, info, reported: true }
                  }),
                  ...unreported.map((info) => ({
                    service_name: info.name,
                    status: '',
                    last_check_time: undefined,
                    error_message: '',
                    metadata: undefined,
                    info,
                    reported: false,
                  })),
                ]
                return allServices.map((svc) => {
                  const info = svc.info
                  return (
                    <div key={svc.service_name} className="service-card ba-card fade-in" style={!svc.reported ? { opacity: 0.6 } : undefined}>
                      <div className="service-card-header">
                        {svc.reported ? getStatusIcon(svc.status) : <Clock size={16} style={{ color: 'var(--ba-text-light)' }} />}
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div className="service-name">{svc.service_name}</div>
                          <div className="service-status" style={{ color: svc.reported ? getStatusColor(svc.status) : 'var(--ba-text-light)' }}>
                            {svc.reported ? getStatusLabel(svc.status) : '未上报'}
                          </div>
                        </div>
                      </div>
                      <div className="service-card-body">
                        <div className="service-conn-section">
                          <div className="service-conn-row">
                            <span className="service-conn-label">服务发现</span>
                            <span className="service-conn-value" style={{ color: 'var(--ba-primary)' }}>{info?.etcd_name || svc.service_name}</span>
                          </div>
                          <div className="service-conn-row">
                            <span className="service-conn-label">直连地址</span>
                            <span className="service-conn-value" style={{ fontFamily: 'monospace' }}>
                              {info?.address || (info?.port ? `127.0.0.1:${info.port}` : '-')}
                            </span>
                          </div>
                        </div>
                        <div className="service-meta">
                          {svc.last_check_time && (
                            <span className="service-meta-item"><Clock size={12} /> {formatTime(svc.last_check_time)}</span>
                          )}
                          {svc.error_message && (
                            <span className="service-meta-item" style={{ color: 'var(--ba-error)' }}>{svc.error_message}</span>
                          )}
                        </div>
                        {svc.metadata && Object.keys(svc.metadata).length > 0 && (
                          <div className="service-tags">
                            {Object.entries(svc.metadata).map(([k, v]) => (
                              <span key={k} className="service-tag">{k}: {v}</span>
                            ))}
                          </div>
                        )}
                      </div>
                    </div>
                  )
                })
              })()}
            </div>
          ) : (
            <div className="knowledge-empty">
              <Server size={48} />
              <p>暂无服务数据</p>
              <p className="knowledge-empty-hint">等待服务上报状态</p>
            </div>
          )}
        </div>
      )}

      {activeTab === 'metrics' && (
        <div className="monitoring-content">
          <div className="monitoring-filter">
            <select className="ba-input" value={metricFilter.service_name} onChange={(e) => setMetricFilter({ ...metricFilter, service_name: e.target.value })}>
              <option value="">全部服务</option>
              {serviceNames.map((s) => (<option key={s} value={s}>{s}</option>))}
            </select>
            <select className="ba-input" value={metricFilter.type} onChange={(e) => setMetricFilter({ ...metricFilter, type: parseInt(e.target.value) })}>
              <option value="0">全部类型</option>
              {metricTypes.map((t, i) => (<option key={i} value={i + 1}>{t}</option>))}
            </select>
            <button className="ba-btn ba-btn-primary" onClick={loadMetrics} disabled={loading}>
              <Search size={16} /> {loading ? '加载中...' : '查询'}
            </button>
          </div>
          {metrics.length > 0 ? (
            <div className="metrics-list">
              {metrics.map((metric, i) => (
                <div key={metric.id || i} className="metric-item ba-card">
                  <div className="metric-header">
                    <span className="metric-service">{metric.service_name}</span>
                    <span className="metric-type">{metric.type ? metricTypes[metric.type - 1] || `类型${metric.type}` : '未知'}</span>
                  </div>
                  <div className="metric-body">
                    <div className="metric-value">{typeof metric.value === 'number' ? metric.value.toFixed(2) : metric.value}{metric.unit || ''}</div>
                    <div className="metric-time">{formatTime(metric.timestamp)}</div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="knowledge-empty">
              <BarChart3 size={48} />
              <p>暂无指标数据</p>
              <p className="knowledge-empty-hint">服务运行后将自动上报指标</p>
            </div>
          )}
        </div>
      )}

      {activeTab === 'logs' && (
        <div className="monitoring-content">
          <div className="monitoring-filter">
            <select className="ba-input" value={logFilter.service_name} onChange={(e) => setLogFilter({ ...logFilter, service_name: e.target.value })}>
              <option value="">全部服务</option>
              {serviceNames.map((s) => (<option key={s} value={s}>{s}</option>))}
            </select>
            <select className="ba-input" value={logFilter.level} onChange={(e) => setLogFilter({ ...logFilter, level: e.target.value })}>
              <option value="">全部级别</option>
              <option value="DEBUG">DEBUG</option>
              <option value="INFO">INFO</option>
              <option value="WARN">WARN</option>
              <option value="ERROR">ERROR</option>
            </select>
            <button className="ba-btn ba-btn-primary" onClick={loadLogs} disabled={loading}>
              <Search size={16} /> {loading ? '加载中...' : '查询'}
            </button>
          </div>
          {logs.length > 0 ? (
            <div className="logs-list">
              {logs.map((log, i) => (
                <div key={log.id || i} className="log-item ba-card">
                  <div className="log-header">
                    <span className="log-service">{log.service_name}</span>
                    <span className="log-level" style={{
                      background: `rgba(${log.level === 'ERROR' ? '239,68,68' : log.level === 'WARN' ? '245,158,11' : log.level === 'INFO' ? '59,130,246' : '107,114,128'},0.1)`,
                      color: log.level === 'ERROR' ? 'var(--ba-error)' : log.level === 'WARN' ? 'var(--ba-warning)' : log.level === 'INFO' ? 'var(--ba-primary)' : 'var(--ba-text-light)'
                    }}>
                      {log.level}
                    </span>
                  </div>
                  <div className="log-message">{log.message}</div>
                  <div className="log-time">{formatTime(log.timestamp)}</div>
                </div>
              ))}
            </div>
          ) : (
            <div className="knowledge-empty">
              <FileText size={48} />
              <p>暂无日志数据</p>
              <p className="knowledge-empty-hint">服务运行后将自动记录日志</p>
            </div>
          )}
        </div>
      )}

      {activeTab === 'alerts' && (
        <div className="monitoring-content">
          <div className="monitoring-filter">
            <select className="ba-input" value={alertFilter.service_name} onChange={(e) => setAlertFilter({ ...alertFilter, service_name: e.target.value })}>
              <option value="">全部服务</option>
              {serviceNames.map((s) => (<option key={s} value={s}>{s}</option>))}
            </select>
            <select className="ba-input" value={alertFilter.level || ''} onChange={(e) => setAlertFilter({ ...alertFilter, level: parseInt(e.target.value) || 0 })}>
              <option value="">全部级别</option>
              {alertLevels.map((l, i) => (<option key={i} value={i + 1}>{l}</option>))}
            </select>
            <select className="ba-input" value={alertFilter.resolved === true ? 'true' : alertFilter.resolved === false ? 'false' : ''} onChange={(e) => setAlertFilter({ ...alertFilter, resolved: e.target.value === 'true' ? true : e.target.value === 'false' ? false : undefined })}>
              <option value="">全部状态</option>
              <option value="false">未解决</option>
              <option value="true">已解决</option>
            </select>
            <button className="ba-btn ba-btn-primary" onClick={loadAlerts} disabled={loading}>
              <Search size={16} /> {loading ? '加载中...' : '查询'}
            </button>
          </div>
          {alerts.length > 0 ? (
            <div className="alerts-list">
              {alerts.map((alert, i) => (
                <div key={alert.id || i} className={`alert-item ba-card ${alert.resolved ? 'resolved' : ''}`}>
                  <div className="alert-header">
                    <AlertTriangle size={20} style={{
                      color: alert.level === 3 ? 'var(--ba-error)' : alert.level === 2 ? 'var(--ba-warning)' : alert.level === 1 ? 'var(--ba-primary)' : 'var(--ba-text-light)'
                    }} />
                    <div style={{ flex: 1 }}>
                      <div className="alert-service">{alert.service_name}</div>
                      <div className="alert-message">{alert.message}</div>
                    </div>
                    {alert.resolved ? (
                      <span className="resolved-badge">已解决</span>
                    ) : (
                      <button className="ba-btn ba-btn-sm" style={{ fontSize: 11, padding: '2px 10px' }} onClick={() => alert.id && handleResolveAlert(alert.id)}>
                        <Zap size={12} /> 解决
                      </button>
                    )}
                  </div>
                  <div className="alert-body">
                    <div className="alert-metric">
                      {alert.metric_name}: {typeof alert.metric_value === 'number' ? alert.metric_value.toFixed(2) : alert.metric_value} / 阈值 {alert.threshold}
                    </div>
                    <div className="alert-time">{formatTime(alert.timestamp)}</div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="knowledge-empty">
              <CheckCircle size={48} style={{ color: 'var(--ba-success)' }} />
              <p>暂无告警</p>
              <p className="knowledge-empty-hint">系统运行正常</p>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
