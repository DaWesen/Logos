import { useState, useEffect, useCallback, useRef } from 'react'
import { Activity, Server, AlertTriangle, Search, Clock, BarChart3, CheckCircle, XCircle, FileText, RefreshCw, Zap, Gauge, Play, Square } from 'lucide-react'
import {
  queryMetrics,
  queryLogs,
  queryAlerts,
  listServiceStatus,
  listServices,
  resolveAlert,
  batchRecordMetrics,
  type Metric,
  type Alert,
  type LogEntry,
  type ServiceStatus,
  type ServiceInfo,
  type BenchConfig,
  type BenchResult,
  BENCH_SCENARIOS
} from '@/api/monitoring'
import './MonitoringPage.css'

// ---- 折线图组件 ----
interface ChartPoint { t: number; v: number }

interface LineData { points: ChartPoint[]; color: string; label: string }

function MetricChart({ title, unit, lines, maxPoints = 60 }: {
  title: string; unit: string; lines: LineData[]; maxPoints?: number
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    const container = containerRef.current
    if (!canvas || !container) return
    const dpr = window.devicePixelRatio || 1
    const w = container.clientWidth
    const h = container.clientHeight
    canvas.width = w * dpr
    canvas.height = h * dpr
    canvas.style.width = w + 'px'
    canvas.style.height = h + 'px'
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.scale(dpr, dpr)

    ctx.clearRect(0, 0, w, h)

    // 网格线
    ctx.strokeStyle = 'rgba(148,163,184,0.08)'
    ctx.lineWidth = 1
    for (let i = 1; i < 4; i++) {
      const y = (h / 4) * i
      ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(w, y); ctx.stroke()
    }
    for (let i = 1; i < 6; i++) {
      const x = (w / 6) * i
      ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, h); ctx.stroke()
    }

    // 收集所有数据点用于计算Y轴范围
    const allPoints: ChartPoint[] = []
    for (const line of lines) { allPoints.push(...line.points) }

    if (allPoints.length < 2) {
      ctx.fillStyle = 'rgba(148,163,184,0.3)'
      ctx.font = '12px system-ui'
      ctx.textAlign = 'center'
      ctx.fillText('等待数据...', w / 2, h / 2)
      return
    }

    const maxVal = Math.max(...allPoints.map(d => d.v), 1)
    const minVal = Math.min(...allPoints.map(d => d.v), 0)
    const range = maxVal - minVal || 1
    const padTop = 8, padBot = 8
    const chartH = h - padTop - padBot

    const toX = (i: number) => (i / (maxPoints - 1)) * w
    const toY = (v: number) => padTop + chartH - ((v - minVal) / range) * chartH

    // 绘制每条线
    for (const line of lines) {
      const data = line.points.slice(-maxPoints)
      if (data.length < 2) continue

      // 填充渐变
      const grad = ctx.createLinearGradient(0, padTop, 0, h)
      grad.addColorStop(0, line.color + '20')
      grad.addColorStop(1, line.color + '02')
      ctx.beginPath()
      ctx.moveTo(toX(maxPoints - data.length), toY(data[0].v))
      for (let i = 1; i < data.length; i++) {
        const x0 = toX(maxPoints - data.length + i - 1)
        const y0 = toY(data[i - 1].v)
        const x1 = toX(maxPoints - data.length + i)
        const y1 = toY(data[i].v)
        const cx = (x0 + x1) / 2
        ctx.bezierCurveTo(cx, y0, cx, y1, x1, y1)
      }
      ctx.lineTo(toX(maxPoints - 1), h)
      ctx.lineTo(toX(maxPoints - data.length), h)
      ctx.closePath()
      ctx.fillStyle = grad
      ctx.fill()

      // 折线
      ctx.beginPath()
      ctx.moveTo(toX(maxPoints - data.length), toY(data[0].v))
      for (let i = 1; i < data.length; i++) {
        const x0 = toX(maxPoints - data.length + i - 1)
        const y0 = toY(data[i - 1].v)
        const x1 = toX(maxPoints - data.length + i)
        const y1 = toY(data[i].v)
        const cx = (x0 + x1) / 2
        ctx.bezierCurveTo(cx, y0, cx, y1, x1, y1)
      }
      ctx.strokeStyle = line.color
      ctx.lineWidth = 2
      ctx.stroke()

      // 最新值标记点
      const lastX = toX(maxPoints - 1)
      const lastY = toY(data[data.length - 1].v)
      ctx.beginPath()
      ctx.arc(lastX, lastY, 3, 0, Math.PI * 2)
      ctx.fillStyle = line.color
      ctx.fill()
    }
  }, [lines, maxPoints])

  // 显示有数据的主线的最新值，优先取有数据的线
  const mainLine = lines.find(l => l.points.length > 0) || lines[0]
  const latest = mainLine && mainLine.points.length > 0 ? mainLine.points[mainLine.points.length - 1].v : 0

  return (
    <div className="metric-chart-card ba-card">
      <div className="metric-chart-header">
        <span className="metric-chart-title">{title}</span>
        <div className="metric-chart-legend">
          {lines.map((line, i) => (
            <span key={i} className="metric-chart-legend-item">
              <span className="metric-chart-legend-dot" style={{ background: line.color }} />
              {line.label}
            </span>
          ))}
        </div>
        <span className="metric-chart-value" style={{ color: mainLine?.color || '#3b82f6' }}>
          {typeof latest === 'number' ? (latest < 10 ? latest.toFixed(2) : latest.toFixed(1)) : latest}
          <span className="metric-chart-unit">{unit}</span>
        </span>
      </div>
      <div className="metric-chart-canvas-wrap" ref={containerRef}>
        <canvas ref={canvasRef} />
      </div>
    </div>
  )
}

const FALLBACK_SERVICES = [
  'logos.gateway', 'logos.user', 'logos.monitoring', 'logos.billing', 'logos.im',
  'logos.chat', 'logos.contact', 'logos.message', 'logos.bot', 'logos.vector',
  'logos.summary', 'logos.moderation', 'logos.mcp', 'logos.knowledge',
  'logos.search', 'logos.extraction', 'logos.question', 'logos.recommend', 'logos.collection'
]
const metricTypes = ['CPU', '内存', '磁盘', '网络', '请求', '错误', '延迟', '吞吐量']
const metricUnits = ['%', '%', 'GB', 'Mbps', 'req', '%', 'ms', 'req/s']
const metricColors = ['#3b82f6', '#8b5cf6', '#f59e0b', '#10b981', '#06b6d4', '#ef4444', '#f97316', '#ec4899']
const alertLevels = ['INFO', '警告', '错误', '严重']

export default function MonitoringPage() {
  const [activeTab, setActiveTab] = useState<'services' | 'metrics' | 'logs' | 'alerts' | 'bench' | 'infra'>('services')
  const [loading, setLoading] = useState(false)
  const [metrics, setMetrics] = useState<Metric[]>([])
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [serviceStatus, setServiceStatus] = useState<ServiceStatus[]>([])
  const [serviceInfo, setServiceInfo] = useState<ServiceInfo[]>([])

  const [metricFilter, setMetricFilter] = useState({ service_name: '', type: 0 })
  const [logFilter, setLogFilter] = useState({ service_name: '', level: '' })
  const [alertFilter, setAlertFilter] = useState({ service_name: '', level: 0, resolved: undefined as boolean | undefined })

  // 折线图数据：每个指标类型一个数组
  const [chartData, setChartData] = useState<Record<string, Record<number, ChartPoint[]>>>({})
  const chartIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const [benchConfig, setBenchConfig] = useState<BenchConfig>({
    url: window.location.origin,
    users: 10,
    duration: 10,
    rps: 1,
    scenario: 'health',
  })
  const [benchResult, setBenchResult] = useState<BenchResult>({
    total: 0, success: 0, failed: 0, timeout: 0,
    successRate: 0, qps: 0,
    avgLatency: 0, minLatency: 0, maxLatency: 0,
    p50: 0, p90: 0, p95: 0, p99: 0,
    statusCodes: {}, errors: {},
    running: false, elapsed: 0,
  })
  const benchStopRef = useRef(false)
  const benchTimerRef = useRef<ReturnType<typeof setInterval> | null>(null)

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

  // 加载图表数据（最近60个点）
  const loadChartData = useCallback(async () => {
    try {
      const now = Date.now()
      const selectedService = metricFilter.service_name
      const results: Record<string, Record<number, ChartPoint[]>> = {}

      // 始终加载 system 数据（本机系统指标）
      const sysPromises = metricTypes.map((_, i) => {
        const type = i + 1
        return queryMetrics({
          service_name: 'system',
          type,
          start_time: now - 600000,
          end_time: now,
        }).then(data => ({ type, data: Array.isArray(data) ? data : [] }))
      })
      const sysResults = await Promise.all(sysPromises)
      results['system'] = {}
      for (const { type, data } of sysResults) {
        results['system'][type] = data
          .sort((a: Metric, b: Metric) => (a.timestamp || 0) - (b.timestamp || 0))
          .map((m: Metric) => ({ t: m.timestamp || 0, v: m.value }))
      }

      // 如果选了具体服务，加载该服务的应用指标(type 5-8: 请求/错误/延迟/吞吐)
      // type 1-4(系统指标)复用system数据
      if (selectedService && selectedService !== 'system') {
        const svcPromises = [5, 6, 7, 8].map(type => {
          return queryMetrics({
            service_name: selectedService,
            type,
            start_time: now - 600000,
            end_time: now,
          }).then(data => ({ type, data: Array.isArray(data) ? data : [] }))
        })
        const svcResults = await Promise.all(svcPromises)
        results[selectedService] = {}
        for (const { type, data } of svcResults) {
          results[selectedService][type] = data
            .sort((a: Metric, b: Metric) => (a.timestamp || 0) - (b.timestamp || 0))
            .map((m: Metric) => ({ t: m.timestamp || 0, v: m.value }))
        }
      }

      setChartData(results)
    } catch (e) { console.error('📊 [图表数据] 加载失败', e) }
  }, [metricFilter.service_name])

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
    if (activeTab === 'metrics') { loadMetrics(); loadChartData() }
    if (activeTab === 'logs') loadLogs()
    if (activeTab === 'alerts') loadAlerts()
  }, [activeTab, loadServices, loadMetrics, loadChartData, loadLogs, loadAlerts])

  // 指标tab自动刷新图表
  useEffect(() => {
    if (chartIntervalRef.current) { clearInterval(chartIntervalRef.current); chartIntervalRef.current = null }
    if (activeTab === 'metrics') {
      loadChartData()
      chartIntervalRef.current = setInterval(loadChartData, 3000)
    }
    return () => {
      if (chartIntervalRef.current) { clearInterval(chartIntervalRef.current); chartIntervalRef.current = null }
    }
  }, [activeTab, loadChartData])

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

  const getStatusIcon = (status: string, errorMsg?: string) => {
    switch (status?.toUpperCase()) {
      case 'UP': return <CheckCircle size={16} style={{ color: 'var(--ba-success)' }} />
      case 'DOWN':
        if (isNoInstanceError(errorMsg)) return <Clock size={16} style={{ color: 'var(--ba-text-light)' }} />
        return <XCircle size={16} style={{ color: 'var(--ba-error)' }} />
      case 'DEGRADED': return <AlertTriangle size={16} style={{ color: 'var(--ba-warning)' }} />
      default: return <Clock size={16} style={{ color: 'var(--ba-text-light)' }} />
    }
  }

  const getStatusLabel = (status: string, errorMsg?: string) => {
    switch (status?.toUpperCase()) {
      case 'UP': return '运行中'
      case 'DOWN':
        if (isNoInstanceError(errorMsg)) return '未检测到'
        return '已停止'
      case 'DEGRADED': return '异常'
      default: return '未上报'
    }
  }

  const getStatusColor = (status: string, errorMsg?: string) => {
    switch (status?.toUpperCase()) {
      case 'UP': return 'var(--ba-success)'
      case 'DOWN':
        if (isNoInstanceError(errorMsg)) return 'var(--ba-text-light)'
        return 'var(--ba-error)'
      case 'DEGRADED': return 'var(--ba-warning)'
      default: return 'var(--ba-text-light)'
    }
  }

  const isNoInstanceError = (msg?: string) => msg && /no service instances/i.test(msg)

  const handleResolveAlert = async (alertId: string) => {
    try {
      await resolveAlert(alertId)
      loadAlerts()
    } catch {}
  }

  const runBench = async () => {
    benchStopRef.current = false
    const startTime = Date.now()
    const latencies: number[] = []
    let total = 0
    let success = 0
    let failed = 0
    let timeout = 0
    const statusCodes: Record<number, number> = {}
    const errors: Record<string, number> = {}

    setBenchResult(prev => ({ ...prev, running: true, total: 0, success: 0, failed: 0, timeout: 0, qps: 0, successRate: 0, avgLatency: 0, minLatency: 0, maxLatency: 0, p50: 0, p90: 0, p95: 0, p99: 0, statusCodes: {}, errors: {}, elapsed: 0 }))

    benchTimerRef.current = setInterval(() => {
      setBenchResult(prev => ({ ...prev, elapsed: (Date.now() - startTime) / 1000 }))
    }, 200)

    // 先登录获取 token
    let authToken = ''
    if (benchConfig.scenario !== 'health') {
      try {
        const loginResp = await fetch(`${benchConfig.url}/api/v1/auth/login`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ username: '测试用户', password: '111111' }),
        })
        if (loginResp.ok) {
          const data = await loginResp.json()
          authToken = data?.data?.token || ''
        }
      } catch {
        // 登录失败也继续
      }
    }

    const doRequest = async (): Promise<{ status: number; latency: number; err?: string }> => {
      const reqStart = performance.now()
      try {
        const controller = new AbortController()
        const timer = setTimeout(() => controller.abort(), 10000)
        const headers: Record<string, string> = { 'Content-Type': 'application/json' }
        if (authToken) {
          headers['Authorization'] = `Bearer ${authToken}`
        }
        let resp: Response
        switch (benchConfig.scenario) {
          case 'health':
            resp = await fetch(`${benchConfig.url}/health`, { signal: controller.signal, headers })
            break
          case 'login':
            resp = await fetch(`${benchConfig.url}/api/v1/auth/login`, {
              method: 'POST',
              headers,
              body: JSON.stringify({ username: '测试用户', password: '111111' }),
              signal: controller.signal,
            })
            break
          case 'get_user':
            resp = await fetch(`${benchConfig.url}/api/v1/users/me`, { signal: controller.signal, headers })
            break
          case 'chat_history':
            resp = await fetch(`${benchConfig.url}/api/v1/chat/history?chat_id=1_2&limit=20`, { signal: controller.signal, headers })
            break
          case 'send_message':
            resp = await fetch(`${benchConfig.url}/api/v1/chat/message`, {
              method: 'POST',
              headers,
              body: JSON.stringify({ chat_id: '1_2', content: 'bench', chat_type: 1, message_type: 1 }),
              signal: controller.signal,
            })
            break
          case 'bot_list':
            resp = await fetch(`${benchConfig.url}/api/v1/bot`, { signal: controller.signal, headers })
            break
          case 'contacts':
            resp = await fetch(`${benchConfig.url}/api/v1/contact/list`, { signal: controller.signal, headers })
            break
          default: {
            const paths = ['/health', '/api/v1/bot', '/api/v1/contact/list']
            resp = await fetch(`${benchConfig.url}${paths[Math.floor(Math.random() * paths.length)]}`, { signal: controller.signal, headers })
          }
        }
        clearTimeout(timer)
        const latency = performance.now() - reqStart
        return { status: resp.status, latency }
      } catch (e: unknown) {
        const latency = performance.now() - reqStart
        const errMsg = e instanceof Error ? e.message : String(e)
        const isTimeout = errMsg.includes('abort') || errMsg.includes('timeout')
        if (isTimeout) return { status: 0, latency, err: 'timeout' }
        return { status: 0, latency, err: errMsg.substring(0, 80) }
      }
    }

    const updateResult = () => {
      const sorted = [...latencies].sort((a, b) => a - b)
      const sum = sorted.reduce((a, b) => a + b, 0)
      setBenchResult({
        total, success, failed, timeout,
        successRate: total > 0 ? (success / total) * 100 : 0,
        qps: (Date.now() - startTime) / 1000 > 0 ? success / ((Date.now() - startTime) / 1000) : 0,
        avgLatency: sorted.length > 0 ? sum / sorted.length : 0,
        minLatency: sorted.length > 0 ? sorted[0] : 0,
        maxLatency: sorted.length > 0 ? sorted[sorted.length - 1] : 0,
        p50: sorted.length > 0 ? sorted[Math.floor(sorted.length * 0.5)] : 0,
        p90: sorted.length > 0 ? sorted[Math.floor(sorted.length * 0.9)] : 0,
        p95: sorted.length > 0 ? sorted[Math.floor(sorted.length * 0.95)] : 0,
        p99: sorted.length > 0 ? sorted[Math.floor(sorted.length * 0.99)] : 0,
        statusCodes: { ...statusCodes },
        errors: { ...errors },
        running: !benchStopRef.current,
        elapsed: (Date.now() - startTime) / 1000,
      })
    }

    const workers: Promise<void>[] = []
    for (let u = 0; u < benchConfig.users; u++) {
      workers.push((async () => {
        const interval = 1000 / benchConfig.rps
        while (!benchStopRef.current && (Date.now() - startTime) < benchConfig.duration * 1000) {
          if (benchStopRef.current) break
          const result = await doRequest()
          total++
          if (result.err) {
            failed++
            if (result.err === 'timeout') timeout++
            errors[result.err] = (errors[result.err] || 0) + 1
          } else {
            success++
            statusCodes[result.status] = (statusCodes[result.status] || 0) + 1
          }
          latencies.push(result.latency)
          updateResult()
          await new Promise(r => setTimeout(r, interval))
        }
      })())
    }

    await Promise.all(workers)
    benchStopRef.current = true
    if (benchTimerRef.current) clearInterval(benchTimerRef.current)
    updateResult()

    if (total > 0) {
      try {
        const sorted = [...latencies].sort((a, b) => a - b)
        const sum = sorted.reduce((a, b) => a + b, 0)
        const avgLat = sorted.length > 0 ? sum / sorted.length : 0
        const errorRate = total > 0 ? (failed / total) * 100 : 0
        const qps = (Date.now() - startTime) / 1000 > 0 ? success / ((Date.now() - startTime) / 1000) : 0

        await batchRecordMetrics([
          { service_name: 'logos.gateway', type: 5, value: total, unit: 'req', tags: { scenario: benchConfig.scenario } },
          { service_name: 'logos.gateway', type: 6, value: errorRate, unit: '%', tags: { scenario: benchConfig.scenario } },
          { service_name: 'logos.gateway', type: 7, value: avgLat, unit: 'ms', tags: { scenario: benchConfig.scenario } },
          { service_name: 'logos.gateway', type: 8, value: qps, unit: 'req/s', tags: { scenario: benchConfig.scenario } },
        ])
      } catch {}
    }
  }

  const stopBench = () => {
    benchStopRef.current = true
    if (benchTimerRef.current) clearInterval(benchTimerRef.current)
    setBenchResult(prev => ({ ...prev, running: false }))
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
        <button className={`monitoring-tab ${activeTab === 'bench' ? 'active' : ''}`} onClick={() => setActiveTab('bench')}>
          <Gauge size={16} /> 压测
        </button>
        <button className={`monitoring-tab ${activeTab === 'infra' ? 'active' : ''}`} onClick={() => setActiveTab('infra')}>
          <Server size={16} /> 基础设施
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
                        {svc.reported ? getStatusIcon(svc.status, svc.error_message) : <Clock size={16} style={{ color: 'var(--ba-text-light)' }} />}
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div className="service-name">{svc.service_name}</div>
                          <div className="service-status" style={{ color: svc.reported ? getStatusColor(svc.status, svc.error_message) : 'var(--ba-text-light)' }}>
                            {svc.reported ? getStatusLabel(svc.status, svc.error_message) : '未上报'}
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
            <select className="ba-input" value={metricFilter.service_name} onChange={(e) => { setMetricFilter({ ...metricFilter, service_name: e.target.value }); setChartData({}) }}>
              <option value="">全部服务</option>
              <option value="system">system (本机)</option>
              {serviceNames.filter(s => s !== 'system').map((s) => (<option key={s} value={s}>{s}</option>))}
            </select>
            <select className="ba-input" value={metricFilter.type} onChange={(e) => setMetricFilter({ ...metricFilter, type: parseInt(e.target.value) })}>
              <option value="0">全部类型</option>
              {metricTypes.map((t, i) => (<option key={i} value={i + 1}>{t}</option>))}
            </select>
            <button className="ba-btn ba-btn-primary" onClick={() => { loadMetrics(); loadChartData() }} disabled={loading}>
              <Search size={16} /> {loading ? '加载中...' : '查询'}
            </button>
          </div>

          {/* 折线图区域 */}
          <div className="metric-charts-grid">
            {metricTypes.map((name, i) => {
              const type = i + 1
              const systemPoints = chartData['system']?.[type] || []
              const selectedService = metricFilter.service_name
              const servicePoints = (selectedService && selectedService !== 'system')
                ? (chartData[selectedService]?.[type] || [])
                : []

              // type 1-4(系统指标): 只显示本机线（所有服务共享同一台机器）
              // type 5-8(应用指标): 显示本机+服务双线
              const lines: LineData[] = [
                { points: systemPoints, color: metricColors[i], label: '本机' }
              ]
              if (type >= 5 && servicePoints.length > 0) {
                lines.push({ points: servicePoints, color: '#f59e0b', label: selectedService! })
              }

              return (
                <MetricChart
                  key={type}
                  title={name}
                  unit={metricUnits[i]}
                  lines={lines}
                />
              )
            })}
          </div>

          {/* 原始指标列表（折叠） */}
          {metrics.length > 0 && (
            <details className="metric-details">
              <summary className="metric-details-summary">原始指标数据 ({metrics.length})</summary>
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
            </details>
          )}

          {metrics.length === 0 && Object.keys(chartData).length === 0 && (
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

      {activeTab === 'bench' && (
        <div className="monitoring-content">
          <div className="bench-config ba-card">
            <div className="bench-config-title">压测配置</div>
            <div className="bench-config-grid">
              <div className="bench-config-item">
                <label>目标地址</label>
                <input className="ba-input" value={benchConfig.url} onChange={(e) => setBenchConfig({ ...benchConfig, url: e.target.value })} disabled={benchResult.running} />
              </div>
              <div className="bench-config-item">
                <label>并发数</label>
                <input className="ba-input" type="number" min={1} max={500} value={benchConfig.users} onChange={(e) => setBenchConfig({ ...benchConfig, users: parseInt(e.target.value) || 1 })} disabled={benchResult.running} />
              </div>
              <div className="bench-config-item">
                <label>持续时间(秒)</label>
                <input className="ba-input" type="number" min={1} max={300} value={benchConfig.duration} onChange={(e) => setBenchConfig({ ...benchConfig, duration: parseInt(e.target.value) || 1 })} disabled={benchResult.running} />
              </div>
              <div className="bench-config-item">
                <label>每用户RPS</label>
                <input className="ba-input" type="number" min={1} max={100} value={benchConfig.rps} onChange={(e) => setBenchConfig({ ...benchConfig, rps: parseInt(e.target.value) || 1 })} disabled={benchResult.running} />
              </div>
              <div className="bench-config-item">
                <label>压测场景</label>
                <select className="ba-input" value={benchConfig.scenario} onChange={(e) => setBenchConfig({ ...benchConfig, scenario: e.target.value })} disabled={benchResult.running}>
                  {BENCH_SCENARIOS.map(s => (<option key={s.value} value={s.value}>{s.label}</option>))}
                </select>
              </div>
              <div className="bench-config-item bench-config-actions">
                {!benchResult.running ? (
                  <button className="ba-btn ba-btn-primary" onClick={runBench}>
                    <Play size={16} /> 开始压测
                  </button>
                ) : (
                  <button className="ba-btn" style={{ background: 'var(--ba-error)', color: '#fff' }} onClick={stopBench}>
                    <Square size={16} /> 停止
                  </button>
                )}
              </div>
            </div>
          </div>

          {benchResult.total > 0 && (
            <>
              <div className="bench-progress">
                <div className="bench-progress-bar" style={{ width: `${Math.min((benchResult.elapsed / benchConfig.duration) * 100, 100)}%` }} />
                <span className="bench-progress-text">{benchResult.elapsed.toFixed(1)}s / {benchConfig.duration}s</span>
              </div>

              <div className="bench-overview">
                <div className="bench-stat-card ba-card">
                  <div className="bench-stat-value" style={{ color: 'var(--ba-primary)' }}>{benchResult.total}</div>
                  <div className="bench-stat-label">总请求</div>
                </div>
                <div className="bench-stat-card ba-card">
                  <div className="bench-stat-value" style={{ color: 'var(--ba-success)' }}>{benchResult.success}</div>
                  <div className="bench-stat-label">成功</div>
                </div>
                <div className="bench-stat-card ba-card">
                  <div className="bench-stat-value" style={{ color: 'var(--ba-error)' }}>{benchResult.failed}</div>
                  <div className="bench-stat-label">失败</div>
                </div>
                <div className="bench-stat-card ba-card">
                  <div className="bench-stat-value" style={{ color: benchResult.successRate >= 99 ? 'var(--ba-success)' : benchResult.successRate >= 95 ? 'var(--ba-warning)' : 'var(--ba-error)' }}>
                    {benchResult.successRate.toFixed(1)}%
                  </div>
                  <div className="bench-stat-label">成功率</div>
                </div>
                <div className="bench-stat-card ba-card">
                  <div className="bench-stat-value" style={{ color: 'var(--ba-primary)' }}>{benchResult.qps.toFixed(1)}</div>
                  <div className="bench-stat-label">QPS</div>
                </div>
                <div className="bench-stat-card ba-card">
                  <div className="bench-stat-value" style={{ color: 'var(--ba-warning)' }}>{benchResult.timeout}</div>
                  <div className="bench-stat-label">超时</div>
                </div>
              </div>

              <div className="bench-latency ba-card">
                <div className="bench-section-title">延迟分布</div>
                <div className="bench-latency-grid">
                  <div className="bench-latency-item"><span className="bench-latency-label">平均</span><span className="bench-latency-value">{benchResult.avgLatency.toFixed(1)}ms</span></div>
                  <div className="bench-latency-item"><span className="bench-latency-label">最小</span><span className="bench-latency-value">{benchResult.minLatency.toFixed(1)}ms</span></div>
                  <div className="bench-latency-item"><span className="bench-latency-label">最大</span><span className="bench-latency-value">{benchResult.maxLatency.toFixed(1)}ms</span></div>
                  <div className="bench-latency-item"><span className="bench-latency-label">P50</span><span className="bench-latency-value" style={{ color: 'var(--ba-success)' }}>{benchResult.p50.toFixed(1)}ms</span></div>
                  <div className="bench-latency-item"><span className="bench-latency-label">P90</span><span className="bench-latency-value" style={{ color: 'var(--ba-primary)' }}>{benchResult.p90.toFixed(1)}ms</span></div>
                  <div className="bench-latency-item"><span className="bench-latency-label">P95</span><span className="bench-latency-value" style={{ color: 'var(--ba-warning)' }}>{benchResult.p95.toFixed(1)}ms</span></div>
                  <div className="bench-latency-item"><span className="bench-latency-label">P99</span><span className="bench-latency-value" style={{ color: 'var(--ba-error)' }}>{benchResult.p99.toFixed(1)}ms</span></div>
                </div>
              </div>

              {Object.keys(benchResult.statusCodes).length > 0 && (
                <div className="bench-status ba-card">
                  <div className="bench-section-title">状态码分布</div>
                  <div className="bench-status-grid">
                    {Object.entries(benchResult.statusCodes).sort(([a], [b]) => Number(a) - Number(b)).map(([code, count]) => (
                      <div key={code} className="bench-status-item">
                        <span className="bench-status-code" style={{ color: Number(code) < 400 ? 'var(--ba-success)' : 'var(--ba-error)' }}>{code}</span>
                        <span className="bench-status-count">{count}</span>
                        <span className="bench-status-pct">({benchResult.total > 0 ? ((count / benchResult.total) * 100).toFixed(1) : 0}%)</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {Object.keys(benchResult.errors).length > 0 && (
                <div className="bench-errors ba-card">
                  <div className="bench-section-title">错误分布</div>
                  {Object.entries(benchResult.errors).map(([err, count]) => (
                    <div key={err} className="bench-error-item">
                      <span className="bench-error-msg">{err}</span>
                      <span className="bench-error-count">{count}</span>
                    </div>
                  ))}
                </div>
              )}
            </>
          )}

          {benchResult.total === 0 && !benchResult.running && (
            <div className="knowledge-empty">
              <Gauge size={48} />
              <p>配置参数后点击开始压测</p>
              <p className="knowledge-empty-hint">压测将在浏览器端直接发起请求</p>
            </div>
          )}
        </div>
      )}

      {activeTab === 'infra' && (
        <div className="monitoring-content">
          <div className="infra-grid">
            {[
              { name: 'Grafana', url: 'http://localhost:3000', desc: '指标可视化与监控仪表盘', icon: '📊', category: '可观测性' },
              { name: 'Jaeger', url: 'http://localhost:16686', desc: '分布式链路追踪 UI', icon: '🔍', category: '可观测性' },
              { name: 'Kiali', url: 'http://localhost:20001', desc: 'Istio 服务网格可视化', icon: '🕸️', category: '可观测性' },
              { name: 'Prometheus', url: 'http://localhost:9090', desc: '指标采集与查询', icon: '🔥', category: '可观测性' },
              { name: 'MinIO Console', url: 'http://localhost:9001', desc: '对象存储管理（minioadmin / minioadmin123）', icon: '📦', category: '存储' },
              { name: 'Elasticsearch', url: 'http://localhost:9200', desc: '全文搜索引擎 API', icon: '🔎', category: '存储' },
              { name: 'Neo4j Browser', url: 'http://localhost:7474', desc: '知识图谱可视化（neo4j / neo4j123456）', icon: '🧠', category: '存储' },
              { name: 'Milvus Attu', url: 'http://localhost:9091', desc: '向量数据库管理', icon: '🎯', category: '存储' },
              { name: 'Kafka UI', url: 'http://localhost:8080', desc: '消息队列管理', icon: '📨', category: '中间件' },
              { name: 'etcd', url: 'http://localhost:2379', desc: '服务注册与发现', icon: '🔑', category: '中间件' },
              { name: 'RedisInsight', url: 'http://localhost:8001', desc: 'Redis 缓存管理', icon: '⚡', category: '中间件' },
            ].map((tool) => (
              <a key={tool.name} className="infra-card ba-card" href={tool.url} target="_blank" rel="noopener noreferrer">
                <div className="infra-card-icon">{tool.icon}</div>
                <div className="infra-card-content">
                  <div className="infra-card-name">{tool.name}</div>
                  <div className="infra-card-desc">{tool.desc}</div>
                  <div className="infra-card-url">{tool.url}</div>
                </div>
                <div className="infra-card-category">{tool.category}</div>
              </a>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
