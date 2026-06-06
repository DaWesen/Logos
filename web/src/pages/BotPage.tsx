import { useState, useEffect, useCallback, useRef } from 'react'
import { Bot, Plus, Trash2, Edit3, Brain, BookOpen, Save, Key, Globe, User, MessageSquare, ChevronDown, ChevronUp, Camera, Database, Network, Eye, X } from 'lucide-react'
import { getBotList, createBot, updateBot, deleteBot, getUserMemory, setUserMemory, deleteUserMemory, type UserMemory } from '@/api/bot'
import { uploadChatMedia } from '@/api/chat'
import { listCollections } from '@/api/knowledge'
import { load, save } from '@/lib/store'
import Modal from '@/components/Modal'
import './BotPage.css'

interface ChatExample {
  user: string
  assistant: string
}

interface PromptConfig {
  persona: string
  instructions: string
  chat_examples: ChatExample[]
}

interface VectorCollection {
  id: string
  name: string
  model_type: number
  index_type: number
  dimension: number
  size: number
  parameters: Record<string, string>
  created_at: string
  updated_at: string
}

function parsePromptConfig(systemPrompt: string): PromptConfig {
  const config: PromptConfig = { persona: '', instructions: systemPrompt || '', chat_examples: [] }
  if (!systemPrompt) return config

  const personaMatch = systemPrompt.match(/^persona:\s*\n([\s\S]*?)(?=\n(?:instructions|chat_examples):|$)/m)
  if (personaMatch) {
    config.persona = personaMatch[1].replace(/^  /gm, '').trim()
  }

  const instructionsMatch = systemPrompt.match(/^instructions:\s*\n([\s\S]*?)(?=\n(?:persona|chat_examples):|$)/m)
  if (instructionsMatch) {
    config.instructions = instructionsMatch[1].replace(/^  /gm, '').trim()
  }

  const examplesMatch = systemPrompt.match(/^chat_examples:\s*\n([\s\S]*?)$/m)
  if (examplesMatch) {
    const block = examplesMatch[1]
    const pairs = block.split(/(?=^  - user:)/m)
    for (const pair of pairs) {
      const userMatch = pair.match(/user:\s*["']?(.*?)["']?\s*$/m)
      const assistantMatch = pair.match(/assistant:\s*["']?(.*?)["']?\s*$/m)
      if (userMatch && assistantMatch) {
        config.chat_examples.push({ user: userMatch[1], assistant: assistantMatch[1] })
      }
    }
  }

  if (personaMatch || examplesMatch) {
    if (!instructionsMatch) config.instructions = ''
  }

  return config
}

function buildSystemPrompt(config: PromptConfig): string {
  if (!config.persona && config.chat_examples.length === 0) {
    return config.instructions
  }

  const lines: string[] = []
  if (config.persona) {
    lines.push('persona:')
    lines.push(config.persona.split('\n').map((l) => '  ' + l).join('\n'))
  }
  if (config.instructions) {
    lines.push('instructions:')
    lines.push(config.instructions.split('\n').map((l) => '  ' + l).join('\n'))
  }
  if (config.chat_examples.length > 0) {
    lines.push('chat_examples:')
    for (const ex of config.chat_examples) {
      lines.push(`  - user: "${ex.user}"`)
      lines.push(`    assistant: "${ex.assistant}"`)
    }
  }
  return lines.join('\n')
}

interface BotItem {
  id: string
  name: string
  avatar: string
  description: string
  systemPrompt: string
  provider: string
  model: string
  baseUrl: string
  apiKey: string
  embeddingModel: string
  embeddingBaseUrl: string
  embeddingApiKey: string
  temperature: number
  enableMemory: boolean
  enableRag: boolean
  enableGraph: boolean
  autoSaveToKb: boolean
  knowledgeBaseIds: string[]
  createdAt: string
}

const defaultForm: Partial<BotItem> = {
  name: '',
  avatar: '',
  description: '',
  systemPrompt: '你是一个友好的AI助手。',
  provider: '',
  model: '',
  baseUrl: '',
  apiKey: '',
  embeddingModel: '',
  embeddingBaseUrl: '',
  embeddingApiKey: '',
  temperature: 0.7,
  enableMemory: true,
  enableRag: false,
  enableGraph: false,
  autoSaveToKb: false,
  knowledgeBaseIds: [],
}

const providerOptions = [
  { value: '', label: '自定义' },
  { value: 'openai', label: 'OpenAI 兼容 (GPT/DeepSeek/通义/...)' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'chatglm', label: '智谱 (ChatGLM)' },
  { value: 'anthropic', label: 'Anthropic (Claude)' },
  { value: 'platform', label: '平台内置' },
]

const providerDefaults: Record<string, { baseUrl: string; model: string }> = {
  openai: { baseUrl: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
  deepseek: { baseUrl: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
  chatglm: { baseUrl: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4' },
  anthropic: { baseUrl: 'https://api.anthropic.com/v1', model: 'claude-3-haiku' },
  platform: { baseUrl: '', model: '' },
}

const modelSuggestions = [
  'gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'gpt-4', 'gpt-3.5-turbo',
  'claude-3.5-sonnet', 'claude-3-opus', 'claude-3-haiku',
  'deepseek-chat', 'deepseek-coder', 'deepseek-reasoner',
  'qwen-max', 'qwen-plus', 'qwen-turbo',
  'glm-4', 'glm-4-flash', 'glm-4-plus',
  'moonshot-v1-8k', 'moonshot-v1-32k',
  'yi-large', 'yi-medium',
  'doubao-pro-32k', 'doubao-lite-32k',
  'ernie-4.0', 'ernie-3.5-turbo',
  'llama-3.1-70b', 'llama-3.1-8b',
  'mistral-large', 'mistral-medium',
  'gemini-pro', 'gemini-1.5-pro',
]

const baseUrlSuggestions = [
  'https://api.openai.com/v1',
  'https://api.deepseek.com/v1',
  'https://dashscope.aliyuncs.com/compatible-mode/v1',
  'https://open.bigmodel.cn/api/paas/v4',
  'https://api.moonshot.cn/v1',
  'https://api.lingyiwanwu.com/v1',
  'https://api.anthropic.com/v1',
]

const presetAvatars = [
  '🤖', '👩‍💻', '🧙‍♂️', '🦊', '🐱', '🎭', '🌸', '🔮', '📚', '🎵', '⚔️', '🏰',
]

// 知识库助手预设模板
const knowledgeBotPreset = {
  name: '知识库助手',
  description: '基于知识库的智能问答助手，可以检索知识库内容并回答用户的专业问题。',
  systemPrompt: `你是一个专业的知识库助手，擅长从知识库中检索信息来回答用户的问题。

工作方式：
1. 理解用户的问题
2. 从知识库中检索相关的信息
3. 基于检索结果给出准确、专业的回答
4. 如果知识库中没有相关内容，坦诚告知用户

回答原则：
- 优先使用知识库中的信息
- 引用知识库内容时要标明来源
- 保持回答专业、准确
- 不知道就说不知道，不要编造信息`,
  provider: 'platform',
  model: 'gpt-4o',
  enableMemory: true,
  enableRag: true,
  enableGraph: true,
}

export default function BotPage() {
  const [bots, setBots] = useState<BotItem[]>(() => load('bots', []))
  const [collections, setCollections] = useState<VectorCollection[]>([])
  const [showCreate, setShowCreate] = useState(false)
  const [editingBotId, setEditingBotId] = useState<string | null>(null)
  const [form, setForm] = useState<Partial<BotItem>>({ ...defaultForm })
  const [showApiKey, setShowApiKey] = useState(false)
  const [promptConfig, setPromptConfig] = useState<PromptConfig>({ persona: '', instructions: '你是一个友好的AI助手。', chat_examples: [] })
  const [showPersona, setShowPersona] = useState(false)
  const [showExamples, setShowExamples] = useState(false)
  const [promptMode, setPromptMode] = useState<'yaml' | 'raw'>('yaml')
  const avatarUploadRef = useRef<HTMLInputElement>(null)
  const [memoryBotId, setMemoryBotId] = useState<string | null>(null)
  const [memories, setMemories] = useState<UserMemory[]>([])
  const [newMemKey, setNewMemKey] = useState('')
  const [newMemValue, setNewMemValue] = useState('')
  const [newMemCategory, setNewMemCategory] = useState('fact')

  const loadCollections = useCallback(async () => {
    try {
      const list = await listCollections()
      if (Array.isArray(list) && list.length > 0) {
        setCollections(list)
      }
    } catch {
      // ignore
    }
  }, [])

  useEffect(() => {
    loadCollections()
  }, [loadCollections])

  useEffect(() => {
    save('bots', bots)
  }, [bots])

  const loadBots = useCallback(async () => {
    try {
      const list = await getBotList()
      if (Array.isArray(list) && list.length > 0) {
        setBots((prev) => {
          const localAvatars = new Map<string, string>()
          prev.forEach((b) => { if (b.avatar) localAvatars.set(b.id, b.avatar) })
          return list.map((item) => {
            const raw = item as unknown as Record<string, unknown>
            const config = (raw.config || {}) as Record<string, string>
            // API返回snake_case字段，需要映射到camelCase
            // provider可能是枚举数字(1=openai,2=claude,3=qianfan,4=platform)
            const providerNum = raw.provider as number | undefined
            const providerMap: Record<number, string> = { 1: 'openai', 2: 'anthropic', 3: 'chatglm', 4: 'platform' }
            const providerStr = (typeof providerNum === 'number' && providerNum > 0) ? providerMap[providerNum] : String(raw.provider || '')
            return {
              ...item,
              id: (raw.id || raw.Id) as string,
              name: (raw.name || raw.Name || '') as string,
              avatar: ((raw.avatar || raw.Avatar || '') as string) || localAvatars.get((raw.id || raw.Id) as string) || '',
              description: (raw.description || raw.Description || '') as string,
              systemPrompt: (raw.system_prompt || raw.systemPrompt || raw.SystemPrompt || '') as string,
              provider: providerStr,
              model: (raw.model || raw.Model || '') as string,
              baseUrl: (raw.base_url || raw.baseUrl || raw.BaseUrl || '') as string,
              apiKey: (raw.api_key || raw.apiKey || raw.ApiKey || '') as string,
              embeddingModel: (raw.embedding_model || raw.embeddingModel || raw.EmbeddingModel || '') as string,
              embeddingBaseUrl: (config.embedding_base_url || '') as string,
              embeddingApiKey: (config.embedding_api_key || '') as string,
              temperature: parseFloat(String(config.temperature || raw.temperature || '0.7')) || 0.7,
              enableRag: config.enable_rag === 'true',
              enableMemory: config.enable_memory === 'true',
              enableGraph: config.enable_graph === 'true',
              autoSaveToKb: config.auto_save_to_kb === 'true',
              knowledgeBaseIds: (config.collection_ids || '').split(',').filter(Boolean),
            } as BotItem
          })
        })
      }
    } catch {
      // keep local
    }
  }, [])

  useEffect(() => {
    loadBots()
  }, [loadBots])

  const syncPromptToForm = useCallback(() => {
    setForm((prev) => ({ ...prev, systemPrompt: buildSystemPrompt(promptConfig) }))
  }, [promptConfig])

  useEffect(() => {
    syncPromptToForm()
  }, [syncPromptToForm])

  const handleCreate = async () => {
    if (!form.name?.trim()) return
    const localBot: BotItem = {
      id: `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
      name: form.name,
      avatar: form.avatar || '',
      description: form.description || '',
      systemPrompt: form.systemPrompt || '',
      provider: form.provider || '',
      model: form.model || '',
      baseUrl: form.baseUrl || '',
      apiKey: form.apiKey || '',
      embeddingModel: form.embeddingModel || '',
      embeddingBaseUrl: form.embeddingBaseUrl || '',
      embeddingApiKey: form.embeddingApiKey || '',
      temperature: form.temperature || 0.7,
      enableMemory: form.enableMemory || false,
      enableRag: form.enableRag || false,
      enableGraph: form.enableGraph || false,
      autoSaveToKb: form.autoSaveToKb || false,
      knowledgeBaseIds: [],
      createdAt: new Date().toISOString(),
    }
    setBots((prev) => [localBot, ...prev])
    setShowCreate(false)
    resetForm()
    try {
      const created = await createBot(form)
      if (created && created.id) {
        // 合并返回的数据，保留本地设置的 avatar（后端可能返回空）
        const merged = { ...localBot, ...created, avatar: created.avatar || localBot.avatar }
        setBots((prev) => prev.map((b) => b.id === localBot.id ? merged : b))
      }
    } catch {
      // keep local
    }
  }

  const handleUpdate = async () => {
    if (!editingBotId) return
    setBots((prev) =>
      prev.map((b) =>
        b.id === editingBotId
          ? { ...b, ...form, name: form.name || b.name }
          : b,
      ),
    )
    setEditingBotId(null)
    resetForm()
    try {
      await updateBot(editingBotId, form)
    } catch {
      // ignore
    }
  }

  const handleDelete = async (id: string) => {
    setBots((prev) => prev.filter((b) => b.id !== id))
    try {
      await deleteBot(id)
    } catch {
      // ignore
    }
  }

  const resetForm = () => {
    setForm({ ...defaultForm })
    setPromptConfig({ persona: '', instructions: '你是一个友好的AI助手。', chat_examples: [] })
    setShowPersona(false)
    setShowExamples(false)
    setPromptMode('yaml')
    setShowApiKey(false)
  }

  const startEdit = (bot: BotItem) => {
    setEditingBotId(bot.id)
    const config = parsePromptConfig(bot.systemPrompt)
    setPromptConfig(config)
    setShowPersona(!!config.persona)
    setShowExamples(config.chat_examples.length > 0)
    setPromptMode(config.persona || config.chat_examples.length > 0 ? 'yaml' : 'yaml')
    // 编辑时如果有API Key则自动显示
    setShowApiKey(!!bot.apiKey || !!bot.embeddingApiKey)
    setForm({
      name: bot.name,
      avatar: bot.avatar,
      description: bot.description,
      systemPrompt: bot.systemPrompt,
      provider: bot.provider,
      model: bot.model,
      baseUrl: bot.baseUrl,
      apiKey: bot.apiKey,
      embeddingModel: bot.embeddingModel,
      embeddingBaseUrl: bot.embeddingBaseUrl,
      embeddingApiKey: bot.embeddingApiKey,
      temperature: bot.temperature,
      enableMemory: bot.enableMemory,
      enableRag: bot.enableRag,
      enableGraph: bot.enableGraph,
      autoSaveToKb: bot.autoSaveToKb,
      knowledgeBaseIds: bot.knowledgeBaseIds || [],
    })
  }

  const addChatExample = () => {
    setPromptConfig((prev) => ({
      ...prev,
      chat_examples: [...prev.chat_examples, { user: '', assistant: '' }],
    }))
    setShowExamples(true)
  }

  const removeChatExample = (index: number) => {
    setPromptConfig((prev) => ({
      ...prev,
      chat_examples: prev.chat_examples.filter((_, i) => i !== index),
    }))
  }

  const updateChatExample = (index: number, field: 'user' | 'assistant', value: string) => {
    setPromptConfig((prev) => ({
      ...prev,
      chat_examples: prev.chat_examples.map((ex, i) => i === index ? { ...ex, [field]: value } : ex),
    }))
  }

  const isFormOpen = showCreate || !!editingBotId
  const closeForm = () => {
    setShowCreate(false)
    setEditingBotId(null)
    resetForm()
  }
  const submitForm = editingBotId ? handleUpdate : handleCreate

  const openMemory = async (botId: string) => {
    setMemoryBotId(botId)
    setNewMemKey('')
    setNewMemValue('')
    setNewMemCategory('fact')
    const list = await getUserMemory(botId)
    setMemories(list)
  }

  const closeMemory = () => {
    setMemoryBotId(null)
    setMemories([])
  }

  const handleAddMemory = async () => {
    if (!memoryBotId || !newMemKey.trim() || !newMemValue.trim()) return
    await setUserMemory(memoryBotId, newMemKey.trim(), newMemValue.trim(), newMemCategory)
    setNewMemKey('')
    setNewMemValue('')
    const list = await getUserMemory(memoryBotId)
    setMemories(list)
  }

  const handleDeleteMemory = async (key: string) => {
    if (!memoryBotId) {
      console.error('删除记忆失败: memoryBotId 为空')
      return
    }
    try {
      console.log('删除记忆:', { botId: memoryBotId, key })
      await deleteUserMemory(memoryBotId, key)
      console.log('删除记忆成功，重新加载列表')
      const list = await getUserMemory(memoryBotId)
      console.log('重新加载的记忆列表:', list)
      setMemories(list)
    } catch (e) {
      console.error('删除记忆失败:', e)
    }
  }

  const categoryLabels: Record<string, string> = {
    preference: '偏好',
    habit: '习惯',
    fact: '信息',
    goal: '目标',
    relationship: '关系',
    style: '风格',
    other: '其他',
  }

  return (
    <div className="bot-page">
      <div className="bot-header">
        <div>
          <h2>Bot 管理</h2>
          <p className="bot-subtitle">创建和管理 AI Bot，自由配置 API 接入</p>
        </div>
        <button className="ba-btn ba-btn-primary" onClick={() => { resetForm(); setShowCreate(true) }}>
          <Plus size={16} /> 创建 Bot
        </button>
      </div>

      <div className="bot-grid">
        {bots.map((bot) => (
          <div key={bot.id} className="bot-card ba-card fade-in">
            <div className="bot-card-header">
              <div className="bot-card-avatar">
                {bot.avatar ? (
                  bot.avatar.startsWith('http') || bot.avatar.startsWith('/') ? (
                    <img src={bot.avatar} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                  ) : (
                    <span style={{ fontSize: 22 }}>{bot.avatar}</span>
                  )
                ) : (
                  <Bot size={24} />
                )}
              </div>
              <div className="bot-card-badges">
                {bot.enableMemory && <span className="ba-badge ba-badge-blue"><Brain size={10} /> 记忆</span>}
                {bot.enableRag && <span className="ba-badge ba-badge-green"><BookOpen size={10} /> RAG</span>}
                {bot.enableGraph && <span className="ba-badge ba-badge-purple"><Network size={10} /> 图谱</span>}
              </div>
            </div>
            <div className="bot-card-body">
              <h3 className="bot-card-name">{bot.name}</h3>
              <p className="bot-card-desc">{bot.description || '暂无描述'}</p>
              <div className="bot-card-meta">
                {bot.model && <span className="ba-badge ba-badge-blue">{bot.model}</span>}
                {bot.baseUrl && <span className="ba-badge" style={{ background: 'rgba(108,92,231,0.1)', color: '#6C5CE7', fontSize: 10 }}><Globe size={9} /> 自定义</span>}
                <span className="bot-card-temp">T: {bot.temperature}</span>
              </div>
            </div>
            <div className="bot-card-actions">
              {bot.enableMemory && (
                <button className="ba-btn ba-btn-secondary" onClick={() => openMemory(bot.id)} title="记忆管理">
                  <Brain size={14} /> 记忆
                </button>
              )}
              <button className="ba-btn ba-btn-secondary" onClick={() => startEdit(bot)}>
                <Edit3 size={14} /> 编辑
              </button>
              <button className="ba-btn ba-btn-danger" onClick={() => handleDelete(bot.id)} style={{ padding: '6px 10px' }}>
                <Trash2 size={14} />
              </button>
            </div>
          </div>
        ))}
        {bots.length === 0 && (
          <div className="knowledge-empty">
            <Bot size={48} />
            <p>暂无 Bot</p>
            <p className="knowledge-empty-hint">点击右上角按钮创建第一个 Bot</p>
          </div>
        )}
      </div>

      <Modal open={isFormOpen} onClose={closeForm} title={editingBotId ? '编辑 Bot' : '创建 Bot'} width={640}>
        <div className="bot-form">
          <div className="login-field">
            <label>名称</label>
            <input className="ba-input" placeholder="Bot 名称" value={form.name || ''} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          <div className="login-field">
            <label>头像</label>
            <div className="bot-avatar-field">
              <div className="bot-avatar-preview" onClick={() => avatarUploadRef.current?.click()}>
                {form.avatar ? (
                  form.avatar.startsWith('http') || form.avatar.startsWith('/') ? (
                    <img src={form.avatar} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                  ) : (
                    <span style={{ fontSize: 28 }}>{form.avatar}</span>
                  )
                ) : (
                  <Camera size={20} style={{ color: 'var(--ba-text-light)' }} />
                )}
              </div>
              <div className="bot-avatar-options">
                <div className="bot-avatar-emojis">
                  {presetAvatars.map((emoji) => (
                    <button
                      key={emoji}
                      className={`bot-avatar-emoji-btn ${form.avatar === emoji ? 'active' : ''}`}
                      onClick={() => setForm({ ...form, avatar: emoji })}
                      type="button"
                    >
                      {emoji}
                    </button>
                  ))}
                </div>
                <input
                  className="ba-input"
                  placeholder="或输入头像图片 URL"
                  value={(form.avatar && (form.avatar.startsWith('http') || form.avatar.startsWith('/'))) ? form.avatar : ''}
                  onChange={(e) => setForm({ ...form, avatar: e.target.value })}
                />
                <input
                  ref={avatarUploadRef}
                  type="file"
                  accept="image/*"
                  style={{ display: 'none' }}
                  onChange={async (e) => {
                    const file = e.target.files?.[0]
                    if (!file) return
                    try {
                      const result = await uploadChatMedia('avatar', file)
                      if (result.url) {
                        setForm({ ...form, avatar: result.url })
                      }
                    } catch {
                      // ignore
                    }
                    e.target.value = ''
                  }}
                />
              </div>
            </div>
          </div>
          <div className="login-field">
            <label>描述</label>
            <input className="ba-input" placeholder="Bot 描述" value={form.description || ''} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          </div>

          <div className="bot-form-section-title"><MessageSquare size={14} /> 提示词配置</div>
          
          <div className="login-field">
            <label>快速选择预设</label>
            <div className="bot-presets">
              <button 
                className="ba-btn ba-btn-secondary" 
                style={{ fontSize: 13 }}
                onClick={() => {
                  const preset = knowledgeBotPreset
                  setForm({
                    ...defaultForm,
                    name: preset.name,
                    description: preset.description,
                    systemPrompt: preset.systemPrompt,
                    provider: preset.provider,
                    model: preset.model,
                    enableMemory: preset.enableMemory,
                    enableRag: preset.enableRag,
                    enableGraph: preset.enableGraph,
                  })
                  setPromptConfig(parsePromptConfig(preset.systemPrompt))
                }}
              >
                <BookOpen size={14} /> 知识库助手预设
              </button>
            </div>
          </div>

          <div className="prompt-mode-toggle">
            <button
              className={`prompt-mode-btn ${promptMode === 'yaml' ? 'active' : ''}`}
              onClick={() => setPromptMode('yaml')}
            >
              结构化编辑
            </button>
            <button
              className={`prompt-mode-btn ${promptMode === 'raw' ? 'active' : ''}`}
              onClick={() => {
                if (promptMode === 'yaml') {
                  setForm((prev) => ({ ...prev, systemPrompt: buildSystemPrompt(promptConfig) }))
                }
                setPromptMode('raw')
              }}
            >
              原始文本
            </button>
          </div>

          {promptMode === 'yaml' ? (
            <div className="prompt-yaml-editor">
              <div className="login-field">
                <label>核心指令 <span style={{ color: 'var(--ba-text-light)', fontWeight: 400, fontSize: 12 }}>(必填)</span></label>
                <textarea
                  className="ba-input"
                  placeholder="Bot 的核心行为指令，如：你是一个专业的编程助手，擅长解答编程问题..."
                  rows={4}
                  value={promptConfig.instructions}
                  onChange={(e) => setPromptConfig((prev) => ({ ...prev, instructions: e.target.value }))}
                />
              </div>

              <div className="prompt-section-toggle">
                <button className="prompt-section-btn" onClick={() => setShowPersona(!showPersona)}>
                  <User size={14} />
                  <span>人设 (Persona)</span>
                  <span style={{ color: 'var(--ba-text-light)', fontSize: 11 }}>— 可选</span>
                  {showPersona ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                </button>
              </div>
              {showPersona && (
                <div className="prompt-section-content">
                  <textarea
                    className="ba-input"
                    placeholder="描述 Bot 的性格、身份、说话风格等，如：&#10;名字：小智&#10;性格：友善、幽默、耐心&#10;说话风格：喜欢用比喻解释复杂概念"
                    rows={4}
                    value={promptConfig.persona}
                    onChange={(e) => setPromptConfig((prev) => ({ ...prev, persona: e.target.value }))}
                  />
                </div>
              )}

              <div className="prompt-section-toggle">
                <button className="prompt-section-btn" onClick={() => {
                  if (!showExamples && promptConfig.chat_examples.length === 0) {
                    addChatExample()
                  } else {
                    setShowExamples(!showExamples)
                  }
                }}>
                  <MessageSquare size={14} />
                  <span>聊天示例</span>
                  <span style={{ color: 'var(--ba-text-light)', fontSize: 11 }}>— 可选</span>
                  {showExamples ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                </button>
              </div>
              {showExamples && (
                <div className="prompt-section-content">
                  {promptConfig.chat_examples.map((ex, i) => (
                    <div key={i} className="chat-example-item">
                      <div className="chat-example-header">
                        <span className="chat-example-label">示例 {i + 1}</span>
                        <button className="chat-example-remove" onClick={() => removeChatExample(i)}>
                          <Trash2 size={12} />
                        </button>
                      </div>
                      <div className="login-field" style={{ marginBottom: 6 }}>
                        <label style={{ fontSize: 12 }}>用户输入</label>
                        <input
                          className="ba-input"
                          placeholder="用户可能会说的话..."
                          value={ex.user}
                          onChange={(e) => updateChatExample(i, 'user', e.target.value)}
                        />
                      </div>
                      <div className="login-field" style={{ marginBottom: 0 }}>
                        <label style={{ fontSize: 12 }}>Bot 回复</label>
                        <input
                          className="ba-input"
                          placeholder="Bot 应该如何回复..."
                          value={ex.assistant}
                          onChange={(e) => updateChatExample(i, 'assistant', e.target.value)}
                        />
                      </div>
                    </div>
                  ))}
                  <button className="ba-btn ba-btn-secondary" onClick={addChatExample} style={{ marginTop: 8, width: '100%', fontSize: 13 }}>
                    <Plus size={14} /> 添加示例
                  </button>
                </div>
              )}

              <div className="prompt-preview">
                <div className="prompt-preview-header">生成预览 (YAML)</div>
                <pre className="prompt-preview-code">{buildSystemPrompt(promptConfig) || '(空)'}</pre>
              </div>
            </div>
          ) : (
            <div className="login-field">
              <label>系统提示词 (原始文本)</label>
              <textarea
                className="ba-input"
                placeholder="System Prompt"
                rows={6}
                value={form.systemPrompt || ''}
                onChange={(e) => setForm({ ...form, systemPrompt: e.target.value })}
              />
            </div>
          )}

          <div className="bot-form-section-title"><Globe size={14} /> API 配置</div>
          <div className="bot-form-row">
            <div className="login-field" style={{ flex: 1 }}>
              <label>提供商</label>
              <select className="ba-input" value={form.provider || ''} onChange={(e) => {
                const val = e.target.value
                const updates: Partial<BotItem> = { provider: val }
                const defaults = providerDefaults[val]
                if (defaults) {
                  if (!form.baseUrl) updates.baseUrl = defaults.baseUrl
                  if (!form.model) updates.model = defaults.model
                }
                setForm({ ...form, ...updates })
              }}>
                {providerOptions.map((p) => (
                  <option key={p.value} value={p.value}>{p.label}</option>
                ))}
              </select>
            </div>
            <div className="login-field" style={{ flex: 1 }}>
              <label>模型</label>
              <input
                className="ba-input"
                list="model-suggestions"
                placeholder="输入或选择模型"
                value={form.model || ''}
                onChange={(e) => setForm({ ...form, model: e.target.value })}
              />
              <datalist id="model-suggestions">
                {modelSuggestions.map((m) => (
                  <option key={m} value={m} />
                ))}
              </datalist>
            </div>
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input
              className="ba-input"
              list="url-suggestions"
              placeholder="https://api.openai.com/v1"
              value={form.baseUrl || ''}
              onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
            />
            <datalist id="url-suggestions">
              {baseUrlSuggestions.map((u) => (
                <option key={u} value={u} />
              ))}
            </datalist>
          </div>
          <div className="login-field">
            <label><Key size={12} /> API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                className="ba-input"
                type={showApiKey ? 'text' : 'password'}
                placeholder="sk-..."
                value={form.apiKey || ''}
                onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
                style={{ flex: 1 }}
              />
              <button
                className="ba-btn ba-btn-secondary"
                onClick={() => setShowApiKey(!showApiKey)}
                style={{ flexShrink: 0, padding: '6px 12px', fontSize: 12 }}
              >
                {showApiKey ? '隐藏' : '显示'}
              </button>
            </div>
          </div>
          <div className="login-field">
            <label>Embedding 模型（选填）</label>
            <input className="ba-input" placeholder="如 text-embedding-3-small" value={form.embeddingModel || ''} onChange={(e) => setForm({ ...form, embeddingModel: e.target.value })} />
          </div>
          <div className="login-field">
            <label>Embedding Base URL（选填，默认复用上方 Base URL）</label>
            <input className="ba-input" placeholder="留空则使用 LLM 的 Base URL" value={form.embeddingBaseUrl || ''} onChange={(e) => setForm({ ...form, embeddingBaseUrl: e.target.value })} />
          </div>
          <div className="login-field">
            <label>Embedding API Key（选填，默认复用上方 API Key）</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                className="ba-input"
                type={showApiKey ? 'text' : 'password'}
                placeholder="留空则使用 LLM 的 API Key"
                value={form.embeddingApiKey || ''}
                onChange={(e) => setForm({ ...form, embeddingApiKey: e.target.value })}
                style={{ flex: 1 }}
              />
            </div>
          </div>

          <div className="bot-form-section-title">参数</div>
          <div className="login-field">
            <label>温度 ({form.temperature})</label>
            <input type="range" min="0" max="1" step="0.1" value={form.temperature || 0.7} onChange={(e) => setForm({ ...form, temperature: parseFloat(e.target.value) })} style={{ width: '100%' }} />
          </div>
          <div className="bot-form-toggles">
            <label className="bot-toggle">
              <input type="checkbox" checked={form.enableMemory || false} onChange={(e) => setForm({ ...form, enableMemory: e.target.checked })} />
              <Brain size={16} /> 记忆能力
            </label>
            <label className="bot-toggle">
              <input type="checkbox" checked={form.enableRag || false} onChange={(e) => setForm({ ...form, enableRag: e.target.checked })} />
              <BookOpen size={16} /> RAG 检索
            </label>
            <label className="bot-toggle">
              <input type="checkbox" checked={form.enableGraph || false} onChange={(e) => setForm({ ...form, enableGraph: e.target.checked })} />
              <Network size={16} /> 知识图谱
            </label>
            {form.enableRag && (
              <label className="bot-toggle">
                <input type="checkbox" checked={form.autoSaveToKb || false} onChange={(e) => setForm({ ...form, autoSaveToKb: e.target.checked })} />
                <Save size={16} /> 自动存入知识库
              </label>
            )}
          </div>

          {form.enableRag && (
            <div className="login-field" style={{ marginTop: 8 }}>
              <label><Database size={12} /> 选择知识库</label>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6, maxHeight: 180, overflowY: 'auto' }}>
                {collections.length === 0 ? (
                  <div style={{ color: 'var(--ba-text-light)', fontSize: 13, padding: '8px 0' }}>
                    暂无知识库，请先在知识库页面创建
                  </div>
                ) : (
                  collections.map((coll) => (
                    <label key={coll.id} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, cursor: 'pointer' }}>
                      <input
                        type="checkbox"
                        checked={(form.knowledgeBaseIds || []).includes(coll.id)}
                        onChange={(e) => {
                          const current = form.knowledgeBaseIds || []
                          const updated = e.target.checked
                            ? [...current, coll.id]
                            : current.filter((id) => id !== coll.id)
                          setForm({ ...form, knowledgeBaseIds: updated })
                        }}
                      />
                      <span>{coll.name}</span>
                      <span style={{ color: 'var(--ba-text-light)', fontSize: 11 }}>({coll.size} 向量)</span>
                    </label>
                  ))
                )}
              </div>
            </div>
          )}

          <button className="ba-btn ba-btn-primary" onClick={submitForm} style={{ marginTop: 16, width: '100%' }}>
            <Save size={16} /> {editingBotId ? '保存' : '创建'}
          </button>
        </div>
      </Modal>

      <Modal open={!!memoryBotId} onClose={closeMemory} title="记忆管理" width={560}>
        <div className="memory-panel">
          <div className="memory-add">
            <div className="memory-add-row">
              <select className="ba-input" value={newMemCategory} onChange={(e) => setNewMemCategory(e.target.value)} style={{ width: 90, flexShrink: 0 }}>
                {Object.entries(categoryLabels).map(([k, v]) => (
                  <option key={k} value={k}>{v}</option>
                ))}
              </select>
              <input className="ba-input" placeholder="键名 (如 favorite_language)" value={newMemKey} onChange={(e) => setNewMemKey(e.target.value)} style={{ flex: 1 }} />
            </div>
            <div className="memory-add-row">
              <input className="ba-input" placeholder="值 (如 Python)" value={newMemValue} onChange={(e) => setNewMemValue(e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-primary" onClick={handleAddMemory} disabled={!newMemKey.trim() || !newMemValue.trim()} style={{ flexShrink: 0 }}>
                <Plus size={14} /> 添加
              </button>
            </div>
          </div>
          <div className="memory-list">
            {memories.length === 0 ? (
              <div className="memory-empty">
                <Brain size={32} style={{ color: 'var(--ba-text-light)', marginBottom: 8 }} />
                <p style={{ color: 'var(--ba-text-light)', fontSize: 13 }}>暂无记忆数据</p>
                <p style={{ color: 'var(--ba-text-light)', fontSize: 11 }}>Bot 会在对话中自动提取记忆，你也可以手动添加</p>
              </div>
            ) : (
              memories.map((mem) => (
                <div key={mem.id || mem.key} className="memory-item">
                  <div className="memory-item-header">
                    <span className={`memory-category memory-category-${mem.category || 'other'}`}>
                      {categoryLabels[mem.category] || mem.category || '其他'}
                    </span>
                    <span className="memory-item-key">{mem.key}</span>
                    {mem.source === 'auto_extract' && <span className="memory-source">自动</span>}
                    {mem.source === 'manual' && <span className="memory-source memory-source-manual">手动</span>}
                  </div>
                  <div className="memory-item-value">{mem.value}</div>
                  <button className="memory-item-delete" onClick={() => handleDeleteMemory(mem.key)} title="删除">
                    <X size={14} />
                  </button>
                </div>
              ))
            )}
          </div>
        </div>
      </Modal>
    </div>
  )
}
