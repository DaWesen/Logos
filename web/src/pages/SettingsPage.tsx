import { useState, useRef, useEffect } from 'react'
import { useAuth } from '@/hooks/useAuth'
import { updateProfile, updateAvatar } from '@/api/user'
import { toMediaUrl } from '@/api/chat'
import { getAISettings, saveAISettings, AI_PROVIDERS, type AISettings, type AIModelConfig } from '@/api/settings'
import { Save, User, Trash2, Camera, Sparkles, Eye, EyeOff, Key, Globe, Cpu, Thermometer } from 'lucide-react'
import './SettingsPage.css'

type AIFeature = 'summary' | 'reply' | 'todo' | 'translation'

const featureLabels: Record<AIFeature, string> = {
  summary: '对话摘要',
  reply: '智能回复',
  todo: '待办提取',
  translation: '翻译',
}

export default function SettingsPage() {
  const { user, refreshProfile } = useAuth()
  const [nickname, setNickname] = useState(user?.nickname || '')
  const [email, setEmail] = useState(user?.email || '')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [clearing, setClearing] = useState(false)
  const avatarRef = useRef<HTMLInputElement>(null)

  const [aiSettings, setAiSettings] = useState<AISettings>(getAISettings)
  const [aiSaving, setAiSaving] = useState(false)
  const [aiSaved, setAiSaved] = useState(false)
  const [showApiKeys, setShowApiKeys] = useState<Record<string, boolean>>({})
  const [expandedFeature, setExpandedFeature] = useState<AIFeature>('summary')

  const handleSave = async () => {
    setSaving(true)
    try {
      await updateProfile({ nickname, email })
      await refreshProfile()
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    } catch {
    } finally {
      setSaving(false)
    }
  }

  const handleAvatarUpload = async () => {
    const file = avatarRef.current?.files?.[0]
    if (!file) return
    try {
      await updateAvatar(file)
      await refreshProfile()
      alert('头像上传成功！')
    } catch (e) {
      console.error(e)
      alert('头像上传失败')
    }
    if (avatarRef.current) avatarRef.current.value = ''
  }

  const handleClearCache = () => {
    if (!window.confirm('确定要清理所有本地缓存数据吗？这会清空所有聊天记录和对话列表，不会影响服务器数据。')) return
    setClearing(true)
    try {
      const keepKeys = ['auth_user', 'auth_token', 'auth_refreshToken', 'user_profile']
      const keepData: any = {}
      keepKeys.forEach(key => {
        const val = localStorage.getItem(key)
        if (val) { keepData[key] = val }
      })
      localStorage.clear()
      Object.keys(keepData).forEach(key => {
        localStorage.setItem(key, keepData[key])
      })
      alert('缓存已彻底清理！页面将刷新...')
      setTimeout(() => location.reload(), 500)
    } finally {
      setClearing(false)
    }
  }

  const handleAISave = () => {
    setAiSaving(true)
    try {
      saveAISettings(aiSettings)
      setAiSaved(true)
      setTimeout(() => setAiSaved(false), 2000)
    } finally {
      setAiSaving(false)
    }
  }

  const updateModelConfig = (feature: AIFeature, field: keyof AIModelConfig, value: string | number) => {
    setAiSettings(prev => ({
      ...prev,
      [feature]: { ...prev[feature], [field]: value },
    }))
  }

  const handleProviderChange = (feature: AIFeature, provider: string) => {
    const found = AI_PROVIDERS.find(p => p.value === provider)
    setAiSettings(prev => ({
      ...prev,
      [feature]: {
        ...prev[feature],
        provider,
        baseUrl: found?.defaultUrl || prev[feature].baseUrl,
        model: found?.defaultModel || prev[feature].model,
      },
    }))
  }

  const toggleApiKeyVisibility = (feature: AIFeature) => {
    setShowApiKeys(prev => ({ ...prev, [feature]: !prev[feature] }))
  }

  return (
    <div className="settings-page">
      <div className="settings-header">
        <h2>设置</h2>
        <p className="settings-subtitle">管理个人资料和偏好</p>
      </div>

      <div className="settings-content">
        <div className="settings-section ba-card">
          <h3 className="settings-section-title">
            <User size={18} /> 个人资料
          </h3>
          <div className="settings-form">
            <div className="settings-avatar-section">
              <div
                className="ba-avatar ba-avatar-lg"
                style={{ position: 'relative', cursor: 'pointer' }}
                onClick={() => avatarRef.current?.click()}
              >
                {user?.avatar ? (
                  <img
                    src={toMediaUrl(user.avatar)}
                    alt=""
                    style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }}
                  />
                ) : (
                  (user?.nickname?.charAt(0) || '?')
                )}
                <div style={{
                  position: 'absolute',
                  bottom: -4,
                  right: -4,
                  background: 'var(--ba-blue)',
                  borderRadius: '50%',
                  width: 24,
                  height: 24,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  border: '2px solid var(--ba-bg-primary)',
                  boxShadow: '0 2px 8px rgba(0,0,0,0.2)'
                }}>
                  <Camera size={12} color="white" />
                </div>
              </div>
              <input
                ref={avatarRef}
                type="file"
                accept="image/*"
                hidden
                onChange={handleAvatarUpload}
              />
              <div className="settings-avatar-info">
                <div className="settings-avatar-name">{user?.nickname}</div>
                <div className="settings-avatar-id">ID: {user?.id}</div>
              </div>
            </div>

            <div className="login-field">
              <label>昵称</label>
              <input className="ba-input" value={nickname} onChange={(e) => setNickname(e.target.value)} />
            </div>

            <div className="login-field">
              <label>邮箱</label>
              <input className="ba-input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>

            <button className="ba-btn ba-btn-primary" onClick={handleSave} disabled={saving}>
              <Save size={16} /> {saving ? '保存中...' : saved ? '已保存 ✓' : '保存'}
            </button>
          </div>
        </div>

        <div className="settings-section ba-card">
          <h3 className="settings-section-title">
            <Sparkles size={18} /> AI 助手配置
          </h3>
          <p style={{ color: 'var(--ba-text-light)', fontSize: 13, marginBottom: 16 }}>
            为每个功能配置独立的 AI 模型参数，支持 OpenAI、Claude、DeepSeek 等多种提供商。
          </p>

          <div className="settings-ai-tabs">
            {(['summary', 'reply', 'todo', 'translation'] as AIFeature[]).map(f => (
              <button
                key={f}
                className={`settings-ai-tab ${expandedFeature === f ? 'active' : ''}`}
                onClick={() => setExpandedFeature(f)}
              >
                {featureLabels[f]}
              </button>
            ))}
          </div>

          <div className="settings-ai-form">
            <div className="settings-ai-row">
              <label><Cpu size={14} /> 提供商</label>
              <select
                className="ba-input settings-ai-select"
                value={aiSettings[expandedFeature].provider}
                onChange={(e) => handleProviderChange(expandedFeature, e.target.value)}
              >
                {AI_PROVIDERS.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}
              </select>
            </div>

            <div className="settings-ai-row">
              <label><Globe size={14} /> API Base URL</label>
              <input
                className="ba-input"
                placeholder="https://api.openai.com/v1"
                value={aiSettings[expandedFeature].baseUrl}
                onChange={(e) => updateModelConfig(expandedFeature, 'baseUrl', e.target.value)}
              />
            </div>

            <div className="settings-ai-row">
              <label><Cpu size={14} /> 模型</label>
              <input
                className="ba-input"
                placeholder="gpt-4o-mini"
                value={aiSettings[expandedFeature].model}
                onChange={(e) => updateModelConfig(expandedFeature, 'model', e.target.value)}
              />
            </div>

            <div className="settings-ai-row">
              <label><Key size={14} /> API Key</label>
              <div style={{ display: 'flex', gap: 8, flex: 1 }}>
                <input
                  className="ba-input"
                  style={{ flex: 1 }}
                  type={showApiKeys[expandedFeature] ? 'text' : 'password'}
                  placeholder="sk-..."
                  value={aiSettings[expandedFeature].apiKey}
                  onChange={(e) => updateModelConfig(expandedFeature, 'apiKey', e.target.value)}
                />
                <button
                  className="ba-btn ba-btn-secondary"
                  style={{ padding: '0 10px', minWidth: 36 }}
                  onClick={() => toggleApiKeyVisibility(expandedFeature)}
                >
                  {showApiKeys[expandedFeature] ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
            </div>

            <div className="settings-ai-row">
              <label><Thermometer size={14} /> Temperature</label>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, flex: 1 }}>
                <input
                  type="range"
                  min="0"
                  max="2"
                  step="0.1"
                  value={aiSettings[expandedFeature].temperature}
                  onChange={(e) => updateModelConfig(expandedFeature, 'temperature', parseFloat(e.target.value))}
                  style={{ flex: 1, accentColor: 'var(--ba-blue)' }}
                />
                <span style={{ fontSize: 13, color: '#0ff', fontWeight: 600, minWidth: 32, textAlign: 'right' }}>
                  {aiSettings[expandedFeature].temperature.toFixed(1)}
                </span>
              </div>
            </div>

            <div className="settings-ai-row">
              <label>默认分析消息条数</label>
              <select
                className="ba-input settings-ai-select"
                value={aiSettings.defaultMessageCount}
                onChange={(e) => setAiSettings({ ...aiSettings, defaultMessageCount: Number(e.target.value) })}
              >
                <option value={20}>20</option>
                <option value={50}>50</option>
                <option value={100}>100</option>
                <option value={200}>200</option>
                <option value={500}>500</option>
              </select>
            </div>

            <button className="ba-btn ba-btn-primary" onClick={handleAISave} disabled={aiSaving}>
              <Save size={16} /> {aiSaving ? '保存中...' : aiSaved ? '已保存 ✓' : '保存配置'}
            </button>
          </div>
        </div>

        <div className="settings-section ba-card">
          <h3 className="settings-section-title">
            <Trash2 size={18} /> 数据管理
          </h3>
          <p style={{ color: 'var(--ba-text-light)', fontSize: 13, marginBottom: 16 }}>
            清理本地缓存可以解决消息顺序、旧数据显示等问题。
          </p>
          <button className="ba-btn" onClick={handleClearCache} disabled={clearing}>
            <Trash2 size={16} /> {clearing ? '清理中...' : '清理本地缓存'}
          </button>
        </div>

        <div className="settings-section ba-card">
          <h3 className="settings-section-title">关于</h3>
          <div className="settings-about">
            <div className="settings-about-row">
              <span>版本</span>
              <span>1.0.0</span>
            </div>
            <div className="settings-about-row">
              <span>风格</span>
              <span>蔚蓝档案 Blue Archive</span>
            </div>
            <div className="settings-about-row">
              <span>技术栈</span>
              <span>React + TypeScript + Vite</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
