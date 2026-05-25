import { useState, useEffect, useCallback } from 'react'
import { Plug, Plus, Trash2, RefreshCw, Zap, Loader, CheckCircle, AlertCircle, Wrench, Globe, Clock, Cloud, Code, HardDrive, Send, History } from 'lucide-react'
import type { MCPService, MCPTool } from '@/api/mcp'
import { listMCPServices, createMCPService, updateMCPService, deleteMCPService, testMCPService, callMCPTool, listMCPTools } from '@/api/mcp'
import Modal from '@/components/Modal'
import './MCPPage.css'

const builtinTools = [
  { name: 'calculator', description: '数学计算工具，支持基本运算和复杂表达式', icon: Zap },
  { name: 'time', description: '获取当前时间、日期和时区信息', icon: Clock },
  { name: 'weather', description: '查询指定城市的天气信息', icon: Cloud },
  { name: 'web_search', description: '搜索互联网获取信息', icon: Globe },
  { name: 'code_execution', description: '执行代码并返回结果', icon: Code },
  { name: 'filesystem', description: '文件系统操作，读写文件', icon: HardDrive },
  { name: 'http_request', description: '发送 HTTP 请求获取数据', icon: Send },
]

const defaultFormData = {
  name: '',
  description: '',
  transport_type: 'sse' as 'sse' | 'http-streamable',
  url: '',
  auth_type: 'none' as 'none' | 'api_key' | 'bearer',
  auth_key: '',
  auth_token: '',
  auth_header_name: '',
  timeout: 30,
  retry_count: 3,
  retry_delay: 1,
}

export default function MCPPage() {
  const [services, setServices] = useState<MCPService[]>([])
  const [showDialog, setShowDialog] = useState(false)
  const [editingService, setEditingService] = useState<MCPService | null>(null)
  const [formData, setFormData] = useState(defaultFormData)
  const [testing, setTesting] = useState<string | null>(null)
  const [testResults, setTestResults] = useState<Record<string, { success: boolean; message: string }>>({})
  const [saving, setSaving] = useState(false)
  const [callParams, setCallParams] = useState('{"expression":"2+3*4"}')
  const [callResult, setCallResult] = useState<{ success: boolean; data?: string | Record<string, any>; error?: string } | null>(null)
  const [calling, setCalling] = useState(false)
  const [callLogs, setCallLogs] = useState<{ tool_name: string; params: string; status: string; created_at: string }[]>([])
  const [selectedTool, setSelectedTool] = useState('calculator')

  const loadData = useCallback(async () => {
    const data = await listMCPServices()
    setServices(data)
  }, [])

  useEffect(() => { loadData() }, [loadData])

  const openCreateDialog = () => {
    setEditingService(null)
    setFormData(defaultFormData)
    setShowDialog(true)
  }

  const openEditDialog = (service: MCPService) => {
    setEditingService(service)
    setFormData({
      name: service.name,
      description: service.description,
      transport_type: service.transport_type,
      url: service.url,
      auth_type: (service.auth_config?.type || 'none') as 'none' | 'api_key' | 'bearer',
      auth_key: service.auth_config?.key || '',
      auth_token: service.auth_config?.token || '',
      auth_header_name: service.auth_config?.header_name || '',
      timeout: Number(service.advanced_config?.timeout) || 30,
      retry_count: Number(service.advanced_config?.retry_count) || 3,
      retry_delay: Number(service.advanced_config?.retry_delay) || 1,
    })
    setShowDialog(true)
  }

  const handleSave = async () => {
    if (!formData.name.trim() || !formData.url.trim()) return
    setSaving(true)
    const payload: Partial<MCPService> = {
      name: formData.name.trim(),
      description: formData.description.trim(),
      transport_type: formData.transport_type,
      url: formData.url.trim(),
      headers: {},
      auth_config: {
        type: (formData.auth_type || 'none') as 'none' | 'api_key' | 'bearer',
        ...(formData.auth_type === 'api_key' ? { key: formData.auth_key || '', header_name: formData.auth_header_name || 'X-API-Key' } : {}),
        ...(formData.auth_type === 'bearer' ? { token: formData.auth_token || '' } : {}),
      },
      advanced_config: {
        timeout: String(formData.timeout || 30),
        retry_count: String(formData.retry_count || 3),
        retry_delay: String(formData.retry_delay || 1),
      },
      enabled: true,
    }
    if (editingService) {
      await updateMCPService(editingService.id, payload)
    } else {
      await createMCPService(payload)
    }
    setSaving(false)
    setShowDialog(false)
    loadData()
  }

  const handleDelete = async (id: string) => {
    setServices((prev) => prev.filter((s) => s.id !== id))
    try { await deleteMCPService(id) } catch { /* ignore */ }
  }

  const handleToggle = async (service: MCPService) => {
    const updated = { ...service, enabled: !service.enabled }
    setServices((prev) => prev.map((s) => s.id === service.id ? updated : s))
    try { await updateMCPService(service.id, { enabled: !service.enabled }) } catch { loadData() }
  }

  const handleTest = async (id: string) => {
    const svc = services.find(s => s.id === id)
    if (!svc) return
    setTesting(id)
    const result = await testMCPService({
      url: svc.url,
      transport_type: svc.transport_type,
      headers: svc.headers,
      auth_config: svc.auth_config,
    })
    setTestResults((prev) => ({ ...prev, [id]: result }))
    setTesting(null)
  }

  const updateField = (field: string, value: string | number) => {
    setFormData((prev) => ({ ...prev, [field]: value }))
  }

  const handleCallTool = async () => {
    setCalling(true)
    setCallResult(null)
    try {
      const params = JSON.parse(callParams)
      // Ensure all values are strings for the map<string,string> API
      const stringParams: Record<string, string> = {}
      for (const [k, v] of Object.entries(params)) {
        stringParams[k] = typeof v === 'string' ? v : JSON.stringify(v)
      }
      const payload = { tool_name: selectedTool, parameters: stringParams }
      const result = await callMCPTool(payload)
      setCallResult({ success: true, data: result as string | Record<string, any> | undefined })

      setCallLogs((prev) => [{
        tool_name: selectedTool,
        params: callParams,
        status: 'success',
        created_at: new Date().toLocaleString(),
      }, ...prev].slice(0, 50))
    } catch (err: any) {
      setCallResult({ success: false, error: err?.message || String(err) })
      setCallLogs((prev) => [{
        tool_name: selectedTool,
        params: callParams,
        status: 'error',
        created_at: new Date().toLocaleString(),
      }, ...prev].slice(0, 50))
    }
    setCalling(false)
  }

  return (
    <div className="mcp-page">
      <div className="mcp-header">
        <div>
          <h2>MCP 服务管理</h2>
          <p className="mcp-subtitle">管理 MCP 服务连接与内置工具，为 AI 提供外部能力扩展</p>
        </div>
        <button className="ba-btn ba-btn-primary" onClick={openCreateDialog}>
          <Plus size={16} /> 添加服务
        </button>
      </div>

      <div className="mcp-section">
        <h3 className="mcp-section-title">外部服务</h3>
        <div className="mcp-grid">
          {services.map((service) => {
            const testResult = testResults[service.id]
            return (
              <div key={service.id} className="mcp-card ba-card fade-in">
                <div className="mcp-card-header">
                  <div className="mcp-card-icon"><Plug size={24} /></div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span className={`mcp-tag ${service.transport_type === 'sse' ? 'mcp-tag-sse' : 'mcp-tag-http'}`}>
                      {service.transport_type === 'sse' ? 'SSE' : 'HTTP Streamable'}
                    </span>
                    <button
                      className={`mcp-toggle ${service.enabled ? 'active' : ''}`}
                      onClick={() => handleToggle(service)}
                      title={service.enabled ? '已启用' : '已禁用'}
                    />
                  </div>
                </div>
                <div className="mcp-card-body">
                  <h3 className="mcp-card-name">{service.name}</h3>
                  {service.description && <p className="mcp-card-desc">{service.description}</p>}
                  <div className="mcp-card-meta">
                    <span><Globe size={13} /> {service.url}</span>
                  </div>
                </div>
                {testResult && (
                  <div className={`mcp-test-result ${testResult.success ? 'mcp-test-success' : 'mcp-test-fail'}`}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      {testResult.success ? <CheckCircle size={14} style={{ color: 'var(--ba-success)' }} /> : <AlertCircle size={14} style={{ color: 'var(--ba-accent)' }} />}
                      <span style={{ fontWeight: 600 }}>{testResult.success ? '连接成功' : '连接失败'}</span>
                    </div>
                    {testResult.message && <p style={{ margin: '4px 0 0', fontSize: 12, color: 'var(--ba-text-light)' }}>{testResult.message}</p>}
                  </div>
                )}
                <div className="mcp-card-actions">
                  <button
                    className="ba-btn ba-btn-secondary"
                    onClick={() => handleTest(service.id)}
                    disabled={testing === service.id}
                  >
                    {testing === service.id ? <Loader size={14} className="spin" /> : <Zap size={14} />} 测试
                  </button>
                  <button className="ba-btn ba-btn-secondary" onClick={() => openEditDialog(service)}>
                    <RefreshCw size={14} /> 编辑
                  </button>
                  <button className="ba-btn ba-btn-danger" onClick={() => handleDelete(service.id)} style={{ padding: '6px 10px' }}>
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>
            )
          })}
          {services.length === 0 && (
            <div className="mcp-empty">
              <Plug size={48} />
              <p>暂无外部服务</p>
              <p className="mcp-empty-hint">添加 MCP 服务以扩展 AI 能力</p>
            </div>
          )}
        </div>
      </div>

      <div className="mcp-section">
        <h3 className="mcp-section-title">内置工具</h3>
        <div className="mcp-grid">
          {builtinTools.map((tool) => {
            const ToolIcon = tool.icon
            return (
              <div key={tool.name} className="mcp-card ba-card fade-in">
                <div className="mcp-card-header">
                  <div className="mcp-card-icon"><ToolIcon size={24} /></div>
                  <span className="mcp-tag mcp-tag-builtin">内置</span>
                </div>
                <div className="mcp-card-body">
                  <h3 className="mcp-card-name">{tool.name}</h3>
                  <p className="mcp-card-desc">{tool.description}</p>
                </div>
              </div>
            )
          })}
        </div>
      </div>

      <div className="mcp-section">
        <h3 className="mcp-section-title">调用工具</h3>
        <div className="mcp-call-box ba-card">
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 12 }}>
            <select className="ba-input" style={{ width: 180 }} value={selectedTool} onChange={(e) => { setSelectedTool(e.target.value); setCallResult(null) }}>
              {builtinTools.map((t) => <option key={t.name} value={t.name}>{t.name}</option>)}
            </select>
          </div>
          <div className="login-field" style={{ marginBottom: 8 }}>
            <label>参数（JSON）</label>
            <textarea
              className="ba-input"
              style={{ minHeight: 80, fontFamily: 'monospace', fontSize: 13 }}
              value={callParams}
              onChange={(e) => setCallParams(e.target.value)}
            />
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <button className="ba-btn ba-btn-primary" onClick={handleCallTool} disabled={calling}>
              {calling ? '调用中...' : <><Zap size={14} /> 调用工具</>}
            </button>
            {callResult && (
              <span style={{ fontSize: 13, color: callResult.success ? 'var(--ba-success)' : 'var(--ba-accent)' }}>
                {callResult.success ? '调用成功' : '调用失败'}
              </span>
            )}
          </div>
          {callResult && (
            <div className="mcp-result-box">
              <pre>{typeof callResult.data === 'string' ? callResult.data : JSON.stringify(callResult.data || callResult.error, null, 2)}</pre>
            </div>
          )}
        </div>
      </div>

      <div className="mcp-section">
        <h3 className="mcp-section-title">调用日志 <History size={16} /></h3>
        <div className="mcp-logs-box ba-card">
          {callLogs.length === 0 ? (
            <p style={{ color: 'var(--ba-text-light)', textAlign: 'center', padding: 20 }}>暂无调用记录</p>
          ) : (
            <table className="mcp-logs-table">
              <thead>
                <tr>
                  <th>工具</th>
                  <th>参数</th>
                  <th>状态</th>
                  <th>时间</th>
                </tr>
              </thead>
              <tbody>
                {callLogs.map((log, i) => (
                  <tr key={i}>
                    <td><strong>{log.tool_name}</strong></td>
                    <td style={{ fontFamily: 'monospace', fontSize: 12, maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{log.params}</td>
                    <td><span style={{ color: log.status === 'success' ? 'var(--ba-success)' : 'var(--ba-accent)' }}>{log.status === 'success' ? '成功' : '失败'}</span></td>
                    <td style={{ color: 'var(--ba-text-light)', fontSize: 12 }}>{log.created_at}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <Modal
        open={showDialog}
        onClose={() => setShowDialog(false)}
        title={editingService ? '编辑服务' : '添加 MCP 服务'}
        width={520}
      >
        <div className="new-chat-form" style={{ maxHeight: '60vh', overflowY: 'auto' }}>
          <div className="login-field">
            <label>服务名称</label>
            <input className="ba-input" placeholder="输入服务名称" value={formData.name} onChange={(e) => updateField('name', e.target.value)} />
          </div>
          <div className="login-field">
            <label>描述</label>
            <input className="ba-input" placeholder="服务描述（可选）" value={formData.description} onChange={(e) => updateField('description', e.target.value)} />
          </div>
          <div className="login-field">
            <label>传输类型</label>
            <select className="ba-input" value={formData.transport_type} onChange={(e) => updateField('transport_type', e.target.value)}>
              <option value="sse">SSE</option>
              <option value="http-streamable">HTTP Streamable</option>
            </select>
          </div>
          <div className="login-field">
            <label>服务 URL</label>
            <input className="ba-input" placeholder="https://example.com/mcp" value={formData.url} onChange={(e) => updateField('url', e.target.value)} />
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>认证配置</h4>
          <div className="login-field">
            <label>认证类型</label>
            <select className="ba-input" value={formData.auth_type} onChange={(e) => updateField('auth_type', e.target.value)}>
              <option value="none">无认证</option>
              <option value="api_key">API Key</option>
              <option value="bearer">Bearer Token</option>
            </select>
          </div>
          {formData.auth_type === 'api_key' && (
            <>
              <div className="login-field">
                <label>Header 名称</label>
                <input className="ba-input" placeholder="X-API-Key" value={formData.auth_header_name} onChange={(e) => updateField('auth_header_name', e.target.value)} />
              </div>
              <div className="login-field">
                <label>API Key</label>
                <input className="ba-input" type="password" placeholder="输入 API Key" value={formData.auth_key} onChange={(e) => updateField('auth_key', e.target.value)} />
              </div>
            </>
          )}
          {formData.auth_type === 'bearer' && (
            <div className="login-field">
              <label>Bearer Token</label>
              <input className="ba-input" type="password" placeholder="输入 Bearer Token" value={formData.auth_token} onChange={(e) => updateField('auth_token', e.target.value)} />
            </div>
          )}

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>高级配置</h4>
          <div className="login-field">
            <label>超时时间（秒）</label>
            <input className="ba-input" type="number" placeholder="30" value={formData.timeout} onChange={(e) => updateField('timeout', Number(e.target.value) || 30)} />
          </div>
          <div className="login-field">
            <label>重试次数</label>
            <input className="ba-input" type="number" placeholder="3" value={formData.retry_count} onChange={(e) => updateField('retry_count', Number(e.target.value) || 3)} />
          </div>
          <div className="login-field">
            <label>重试延迟（秒）</label>
            <input className="ba-input" type="number" placeholder="1" value={formData.retry_delay} onChange={(e) => updateField('retry_delay', Number(e.target.value) || 1)} />
          </div>

          <button className="ba-btn ba-btn-primary" onClick={handleSave} disabled={saving || !formData.name.trim() || !formData.url.trim()} style={{ marginTop: 16, width: '100%' }}>
            {saving ? '保存中...' : editingService ? '更新' : '创建'}
          </button>
        </div>
      </Modal>
    </div>
  )
}
