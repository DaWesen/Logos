import { useState, useEffect, useCallback } from 'react'
import { BookOpen, Plus, Upload, RefreshCw, Trash2, FileText, CheckCircle, AlertCircle, Loader, Link, Settings, Eye, Database } from 'lucide-react'
import type { ProcessDocument, VectorCollection, VectorPreviewItem } from '@/api/knowledge'
import { listDocuments, processFile, processUrl, deleteDocument, reprocessDocument, listCollections, createCollection, deleteCollection, getDocumentChunks, listVectors } from '@/api/knowledge'
import { load, save } from '@/lib/store'
import Modal from '@/components/Modal'
import './KnowledgePage.css'

const statusConfig: Record<string, { icon: typeof CheckCircle; color: string; label: string }> = {
  completed: { icon: CheckCircle, color: 'var(--ba-success)', label: '完成' },
  processing: { icon: Loader, color: 'var(--ba-blue)', label: '处理中' },
  pending: { icon: Loader, color: 'var(--ba-warning)', label: '等待中' },
  failed: { icon: AlertCircle, color: 'var(--ba-accent)', label: '失败' },
}

const modelTypeOptions = [
  { value: 0, label: 'BERT' },
  { value: 1, label: 'Word2Vec' },
  { value: 2, label: 'GloVe' },
  { value: 3, label: 'fastText' },
  { value: 4, label: 'SentenceBERT' },
  { value: 5, label: 'Custom' },
]

const indexTypeOptions = [
  { value: 0, label: 'FLAT' },
  { value: 1, label: 'IVF_FLAT' },
  { value: 2, label: 'IVF_PQ' },
  { value: 3, label: 'HNSW' },
]

interface ParseModelConfig {
  vlmModel: string
  vlmBaseUrl: string
  vlmApiKey: string
  llmModel: string
  llmBaseUrl: string
  llmApiKey: string
  asrModel: string
  asrBaseUrl: string
  asrApiKey: string
  embeddingModel: string
  embeddingBaseUrl: string
  embeddingApiKey: string
  embeddingDimension: number
}

const defaultParseConfig: ParseModelConfig = {
  vlmModel: '',
  vlmBaseUrl: '',
  vlmApiKey: '',
  llmModel: '',
  llmBaseUrl: '',
  llmApiKey: '',
  asrModel: '',
  asrBaseUrl: '',
  asrApiKey: '',
  embeddingModel: '',
  embeddingBaseUrl: '',
  embeddingApiKey: '',
  embeddingDimension: 768,
}

export default function KnowledgePage() {
  const [documents, setDocuments] = useState<ProcessDocument[]>(() => load('kb_docs', []))
  const [collections, setCollections] = useState<VectorCollection[]>(() => load('kb_colls', []))
  const [parseConfig, setParseConfig] = useState<ParseModelConfig>(() => load('kb_parse_config', defaultParseConfig))
  const [showUpload, setShowUpload] = useState(false)
  const [showUrl, setShowUrl] = useState(false)
  const [showNewCollection, setShowNewCollection] = useState(false)
  const [showParseConfig, setShowParseConfig] = useState(false)
  const [urlInput, setUrlInput] = useState('')
  const [newCollName, setNewCollName] = useState('')
  const [newCollModelType, setNewCollModelType] = useState(4)
  const [newCollIndexType, setNewCollIndexType] = useState(3)
  const [newCollDimension, setNewCollDimension] = useState(768)
  const [newCollVlmModel, setNewCollVlmModel] = useState('')
  const [newCollVlmBaseUrl, setNewCollVlmBaseUrl] = useState('')
  const [newCollVlmApiKey, setNewCollVlmApiKey] = useState('')
  const [newCollLlmModel, setNewCollLlmModel] = useState('')
  const [newCollLlmBaseUrl, setNewCollLlmBaseUrl] = useState('')
  const [newCollLlmApiKey, setNewCollLlmApiKey] = useState('')
  const [newCollAsrModel, setNewCollAsrModel] = useState('')
  const [newCollAsrBaseUrl, setNewCollAsrBaseUrl] = useState('')
  const [newCollAsrApiKey, setNewCollAsrApiKey] = useState('')
  const [newCollEmbeddingModel, setNewCollEmbeddingModel] = useState('')
  const [newCollEmbeddingBaseUrl, setNewCollEmbeddingBaseUrl] = useState('')
  const [newCollEmbeddingApiKey, setNewCollEmbeddingApiKey] = useState('')
  const [uploading, setUploading] = useState(false)
  const [showApiKeys, setShowApiKeys] = useState<Record<string, boolean>>({})
  const [viewDoc, setViewDoc] = useState<ProcessDocument | null>(null)
  const [viewChunks, setViewChunks] = useState<Array<{ id: string; chunk_index: number; chunk_type: string; content: string }>>([])
  const [loadingChunks, setLoadingChunks] = useState(false)
  const [selectedCollectionId, setSelectedCollectionId] = useState<string>('')
  const [previewColl, setPreviewColl] = useState<VectorCollection | null>(null)
  const [previewVectors, setPreviewVectors] = useState<VectorPreviewItem[]>([])
  const [previewTotal, setPreviewTotal] = useState(0)
  const [previewPage, setPreviewPage] = useState(1)
  const [loadingPreview, setLoadingPreview] = useState(false)
  const [previewError, setPreviewError] = useState<string | null>(null)

  useEffect(() => { save('kb_docs', documents) }, [documents])
  useEffect(() => { save('kb_colls', collections) }, [collections])
  useEffect(() => { save('kb_parse_config', parseConfig) }, [parseConfig])

  const loadData = useCallback(async () => {
    try {
      const [docsData, collData] = await Promise.all([listDocuments(), listCollections()])
      if (docsData.items && docsData.items.length > 0) setDocuments(docsData.items)
      else setDocuments([])
      if (collData && collData.length > 0) {
        setCollections(collData)
        if (!selectedCollectionId) setSelectedCollectionId(collData[0].id)
      } else {
        setCollections([])
      }
    } catch { /* keep local data */ }
  }, [selectedCollectionId])

  useEffect(() => { loadData() }, [loadData])

  useEffect(() => {
    const hasProcessing = documents.some((d) => d.status === 'processing' || d.status === 'pending')
    if (!hasProcessing) return
    const timer = setInterval(() => { loadData() }, 3000)
    return () => clearInterval(timer)
  }, [documents, loadData])

  const handleUpload = async (file: File) => {
    setUploading(true)
    const localId = `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const localDoc: ProcessDocument = {
      id: localId, name: file.name, source_type: 'file', source_url: '',
      status: 'processing', chunk_count: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
      vector_collection_id: selectedCollectionId,
    }
    setDocuments((prev) => [localDoc, ...prev])
    setShowUpload(false)
    try {
      const result = await processFile(file, selectedCollectionId)
      if (result && result.id) {
        setDocuments((prev) => prev.map((d) => d.id === localId ? { ...d, ...result } : d))
      } else {
        setDocuments((prev) => prev.map((d) => d.id === localId ? { ...d, status: 'failed' } : d))
      }
    } catch {
      setDocuments((prev) => prev.map((d) => d.id === localId ? { ...d, status: 'failed' } : d))
    } finally { setUploading(false) }
  }

  const handleProcessUrl = async () => {
    if (!urlInput.trim()) return
    const localId = `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const localDoc: ProcessDocument = {
      id: localId, name: urlInput.trim(), source_type: 'url', source_url: urlInput.trim(),
      status: 'processing', chunk_count: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
      vector_collection_id: selectedCollectionId,
    }
    setDocuments((prev) => [localDoc, ...prev])
    setShowUrl(false)
    const url = urlInput.trim()
    setUrlInput('')
    try {
      const result = await processUrl(url, selectedCollectionId)
      if (result && result.id) {
        setDocuments((prev) => prev.map((d) => d.id === localId ? { ...d, ...result } : d))
      } else {
        setDocuments((prev) => prev.map((d) => d.id === localId ? { ...d, status: 'failed' } : d))
      }
    } catch {
      setDocuments((prev) => prev.map((d) => d.id === localId ? { ...d, status: 'failed' } : d))
    }
  }

  const handleDelete = async (id: string) => {
    setDocuments((prev) => prev.filter((d) => d.id !== id))
    if (!id.startsWith('local-')) {
      try { await deleteDocument(id) } catch { /* ignore */ }
    }
  }

  const handleReprocess = async (id: string) => {
    if (id.startsWith('local-')) return
    setDocuments((prev) => prev.map((d) => d.id === id ? { ...d, status: 'processing' } : d))
    try { await reprocessDocument(id); loadData() } catch {
      setDocuments((prev) => prev.map((d) => d.id === id ? { ...d, status: 'failed' } : d))
    }
  }

  const handleCreateCollection = async () => {
    if (!newCollName.trim()) return
    const localColl: VectorCollection = {
      id: `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, name: newCollName.trim(), model_type: newCollModelType,
      index_type: newCollIndexType, dimension: newCollDimension, size: 0, parameters: {},
      vlm_model: newCollVlmModel, vlm_base_url: newCollVlmBaseUrl, vlm_api_key: newCollVlmApiKey,
      llm_model: newCollLlmModel, llm_base_url: newCollLlmBaseUrl, llm_api_key: newCollLlmApiKey,
      asr_model: newCollAsrModel, asr_base_url: newCollAsrBaseUrl, asr_api_key: newCollAsrApiKey,
      embedding_model: newCollEmbeddingModel, embedding_base_url: newCollEmbeddingBaseUrl, embedding_api_key: newCollEmbeddingApiKey,
      created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
    }
    setCollections((prev) => [localColl, ...prev])
    setShowNewCollection(false)
    const name = newCollName.trim()
    resetNewCollForm()
    try {
      const result = await createCollection(name, newCollModelType, newCollIndexType, newCollDimension, undefined, {
        vlm_model: newCollVlmModel, vlm_base_url: newCollVlmBaseUrl, vlm_api_key: newCollVlmApiKey,
        llm_model: newCollLlmModel, llm_base_url: newCollLlmBaseUrl, llm_api_key: newCollLlmApiKey,
        asr_model: newCollAsrModel, asr_base_url: newCollAsrBaseUrl, asr_api_key: newCollAsrApiKey,
        embedding_model: newCollEmbeddingModel, embedding_base_url: newCollEmbeddingBaseUrl, embedding_api_key: newCollEmbeddingApiKey,
      })
      if (result && result.id) {
        setCollections((prev) => prev.map((c) => c.id === localColl.id ? { ...c, ...result } : c))
      }
    } catch { /* keep local */ }
  }

  const resetNewCollForm = () => {
    setNewCollName('')
    setNewCollModelType(4)
    setNewCollIndexType(3)
    setNewCollDimension(768)
    setNewCollVlmModel('')
    setNewCollVlmBaseUrl('')
    setNewCollVlmApiKey('')
    setNewCollLlmModel('')
    setNewCollLlmBaseUrl('')
    setNewCollLlmApiKey('')
    setNewCollAsrModel('')
    setNewCollAsrBaseUrl('')
    setNewCollAsrApiKey('')
    setNewCollEmbeddingModel('')
    setNewCollEmbeddingBaseUrl('')
    setNewCollEmbeddingApiKey('')
  }

  const fillFromGlobalConfig = () => {
    setNewCollVlmModel(parseConfig.vlmModel)
    setNewCollVlmBaseUrl(parseConfig.vlmBaseUrl)
    setNewCollVlmApiKey(parseConfig.vlmApiKey)
    setNewCollLlmModel(parseConfig.llmModel)
    setNewCollLlmBaseUrl(parseConfig.llmBaseUrl)
    setNewCollLlmApiKey(parseConfig.llmApiKey)
    setNewCollAsrModel(parseConfig.asrModel)
    setNewCollAsrBaseUrl(parseConfig.asrBaseUrl)
    setNewCollAsrApiKey(parseConfig.asrApiKey)
    setNewCollEmbeddingModel(parseConfig.embeddingModel)
    setNewCollEmbeddingBaseUrl(parseConfig.embeddingBaseUrl)
    setNewCollEmbeddingApiKey(parseConfig.embeddingApiKey)
  }

  const openNewCollection = () => {
    setShowNewCollection(true)
    if (!newCollVlmModel && !newCollLlmModel && !newCollAsrModel && !newCollEmbeddingModel) {
      fillFromGlobalConfig()
    }
  }

  const handleDeleteCollection = async (id: string) => {
    setCollections((prev) => prev.filter((c) => c.id !== id))
    try { await deleteCollection(id) } catch { /* ignore */ }
  }

  const toggleApiKey = (key: string) => setShowApiKeys((prev) => ({ ...prev, [key]: !prev[key] }))

  const handleViewDoc = async (doc: ProcessDocument) => {
    setViewDoc(doc)
    setLoadingChunks(true)
    setViewChunks([])
    try {
      const chunks = await getDocumentChunks(doc.id)
      setViewChunks((chunks as Array<{ id: string; chunk_index: number; chunk_type: string; content: string }>) || [])
    } catch {
      setViewChunks([])
    } finally {
      setLoadingChunks(false)
    }
  }

  const updateParseConfig = (field: keyof ParseModelConfig, value: string | number) => {
    setParseConfig((prev) => ({ ...prev, [field]: value }))
  }

  const handlePreviewVectors = async (coll: VectorCollection, page = 1) => {
    setPreviewColl(coll)
    setPreviewPage(page)
    setLoadingPreview(true)
    setPreviewVectors([])
    setPreviewTotal(0)
    setPreviewError(null)
    try {
      const result = await listVectors(coll.id, page, 10)
      setPreviewVectors(result.vectors)
      setPreviewTotal(result.total)
    } catch (e) {
      setPreviewError(e instanceof Error ? e.message : String(e))
      setPreviewVectors([])
      setPreviewTotal(0)
    } finally {
      setLoadingPreview(false)
    }
  }

  return (
    <div className="knowledge-page">
      <div className="knowledge-header">
        <div>
          <h2>知识库</h2>
          <p className="knowledge-subtitle">文档处理 + 向量集合管理，为 RAG 提供知识来源</p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="ba-btn ba-btn-secondary" onClick={() => setShowParseConfig(true)}>
            <Settings size={16} /> 解析模型
          </button>
          <button className="ba-btn ba-btn-secondary" onClick={() => setShowUrl(true)}>
            <Link size={16} /> URL 导入
          </button>
          <button className="ba-btn ba-btn-secondary" onClick={() => setShowUpload(true)}>
            <Upload size={16} /> 上传文档
          </button>
          <button className="ba-btn ba-btn-primary" onClick={openNewCollection}>
            <Plus size={16} /> 新建集合
          </button>
        </div>
      </div>

      {collections.length > 0 && (
        <div className="knowledge-section">
          <h3 className="knowledge-section-title">向量集合</h3>
          <div className="knowledge-grid">
            {collections.map((coll) => (
              <div key={coll.id} className="knowledge-card ba-card fade-in">
                <div className="knowledge-card-header">
                  <div className="knowledge-card-icon"><BookOpen size={24} /></div>
                </div>
                <div className="knowledge-card-body">
                  <h3 className="knowledge-card-name">{coll.name}</h3>
                  <div className="knowledge-card-meta">
                    <span><FileText size={13} /> {coll.size || 0} 向量</span>
                    <span>{modelTypeOptions.find((m) => m.value === coll.model_type)?.label || 'Custom'}</span>
                    <span>{indexTypeOptions.find((i) => i.value === coll.index_type)?.label || 'HNSW'}</span>
                    <span>{coll.dimension}D</span>
                  </div>
                  <div className="knowledge-card-meta" style={{ marginTop: 4, flexWrap: 'wrap' }}>
                    {coll.vlm_model && <span style={{ color: 'var(--ba-blue)' }}>VLM: {coll.vlm_model}</span>}
                    {coll.llm_model && <span style={{ color: 'var(--ba-blue)' }}>LLM: {coll.llm_model}</span>}
                    {coll.asr_model && <span style={{ color: 'var(--ba-blue)' }}>ASR: {coll.asr_model}</span>}
                    {coll.embedding_model && <span style={{ color: 'var(--ba-blue)' }}>Embed: {coll.embedding_model}</span>}
                  </div>
                </div>
                <div className="knowledge-card-actions">
                  <button className="ba-btn ba-btn-secondary" onClick={() => handlePreviewVectors(coll)} style={{ padding: '6px 10px' }}>
                    <Database size={14} />
                  </button>
                  <button className="ba-btn ba-btn-danger" onClick={() => handleDeleteCollection(coll.id)} style={{ padding: '6px 10px' }}>
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="knowledge-section">
        <h3 className="knowledge-section-title">文档列表</h3>
        <div className="knowledge-grid">
          {documents.map((doc) => {
            const status = statusConfig[doc.status] || statusConfig.pending
            const StatusIcon = status.icon
            return (
              <div key={doc.id} className="knowledge-card ba-card fade-in">
                <div className="knowledge-card-header">
                  <div className="knowledge-card-icon"><FileText size={24} /></div>
                  <div className="knowledge-card-status" style={{ color: status.color }}>
                    <StatusIcon size={14} /><span>{status.label}</span>
                  </div>
                </div>
                <div className="knowledge-card-body">
                  <h3 className="knowledge-card-name">{doc.name}</h3>
                  <div className="knowledge-card-meta">
                    <span>{doc.source_type}</span>
                    {doc.chunk_count > 0 && <span><FileText size={13} /> {doc.chunk_count} 分块</span>}
                    {doc.vector_collection_id && (() => {
                      const coll = collections.find(c => c.id === doc.vector_collection_id)
                      return coll ? <span><Database size={13} /> {coll.name}</span> : null
                    })()}
                  </div>
                </div>
                <div className="knowledge-card-actions">
                  {doc.status === 'completed' && (
                    <button className="ba-btn ba-btn-secondary" onClick={() => handleViewDoc(doc)} style={{ padding: '6px 10px' }}>
                      <Eye size={14} />
                    </button>
                  )}
                  <button className="ba-btn ba-btn-secondary" onClick={() => handleReprocess(doc.id)}>
                    <RefreshCw size={14} /> 重处理
                  </button>
                  <button className="ba-btn ba-btn-danger" onClick={() => handleDelete(doc.id)} style={{ padding: '6px 10px' }}>
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>
            )
          })}
          {documents.length === 0 && (
            <div className="knowledge-empty">
              <BookOpen size={48} />
              <p>暂无文档</p>
              <p className="knowledge-empty-hint">上传文件或导入URL开始处理</p>
            </div>
          )}
        </div>
      </div>

      <Modal open={showParseConfig} onClose={() => setShowParseConfig(false)} title="解析模型配置">
        <div className="new-chat-form" style={{ maxHeight: '60vh', overflowY: 'auto' }}>
          <p style={{ fontSize: 13, color: 'var(--ba-text-light)', marginBottom: 16 }}>
            配置各类解析模型，用于文档处理中的图片理解、文本提取、语音转写、向量化等任务
          </p>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>🖼️ VLM 视觉语言模型（图片/文档理解）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="gpt-4o / qwen-vl-max / glm-4v" value={parseConfig.vlmModel} onChange={(e) => updateParseConfig('vlmModel', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1" value={parseConfig.vlmBaseUrl} onChange={(e) => updateParseConfig('vlmBaseUrl', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.vlm ? 'text' : 'password'} placeholder="sk-..." value={parseConfig.vlmApiKey} onChange={(e) => updateParseConfig('vlmApiKey', e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('vlm')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.vlm ? '隐藏' : '显示'}</button>
            </div>
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>🧠 LLM 大语言模型（文本提取/摘要）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="gpt-4o-mini / qwen-plus / glm-4-flash" value={parseConfig.llmModel} onChange={(e) => updateParseConfig('llmModel', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1" value={parseConfig.llmBaseUrl} onChange={(e) => updateParseConfig('llmBaseUrl', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.llm ? 'text' : 'password'} placeholder="sk-..." value={parseConfig.llmApiKey} onChange={(e) => updateParseConfig('llmApiKey', e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('llm')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.llm ? '隐藏' : '显示'}</button>
            </div>
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>🎙️ ASR 语音识别模型（音频转写）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="whisper-1 / paraformer / sensevoice" value={parseConfig.asrModel} onChange={(e) => updateParseConfig('asrModel', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1" value={parseConfig.asrBaseUrl} onChange={(e) => updateParseConfig('asrBaseUrl', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.asr ? 'text' : 'password'} placeholder="sk-..." value={parseConfig.asrApiKey} onChange={(e) => updateParseConfig('asrApiKey', e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('asr')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.asr ? '隐藏' : '显示'}</button>
            </div>
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>📐 Embedding 向量模型（文本向量化）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="text-embedding-3-small / bge-large-zh" value={parseConfig.embeddingModel} onChange={(e) => updateParseConfig('embeddingModel', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1" value={parseConfig.embeddingBaseUrl} onChange={(e) => updateParseConfig('embeddingBaseUrl', e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.embedding ? 'text' : 'password'} placeholder="sk-..." value={parseConfig.embeddingApiKey} onChange={(e) => updateParseConfig('embeddingApiKey', e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('embedding')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.embedding ? '隐藏' : '显示'}</button>
            </div>
          </div>
          <div className="login-field">
            <label>向量维度</label>
            <input className="ba-input" type="number" placeholder="768" value={parseConfig.embeddingDimension} onChange={(e) => updateParseConfig('embeddingDimension', Number(e.target.value) || 768)} />
          </div>
        </div>
      </Modal>

      <Modal open={showUpload} onClose={() => setShowUpload(false)} title="上传文档">
        <div className="new-chat-form">
          <p style={{ fontSize: 13, color: 'var(--ba-text-light)', marginBottom: 12 }}>
            支持文档(PDF/DOC/PPT/XLS)、图片(JPG/PNG/GIF/WebP)、音频(MP3/WAV/FLAC)、视频(MP4/WebM/AVI)等所有格式
          </p>
          {collections.length > 0 && (
            <div className="login-field" style={{ marginBottom: 16 }}>
              <label><Database size={12} /> 选择知识库</label>
              <select
                className="ba-input"
                value={selectedCollectionId}
                onChange={(e) => setSelectedCollectionId(e.target.value)}
              >
                {collections.map((coll) => (
                  <option key={coll.id} value={coll.id}>{coll.name} ({coll.size} 向量)</option>
                ))}
              </select>
            </div>
          )}
          <label className="ba-btn ba-btn-primary knowledge-upload-btn" style={{ width: '100%', justifyContent: 'center' }}>
            <Upload size={16} /> {uploading ? '上传中...' : '选择文件'}
            <input
              type="file" hidden
              accept=".pdf,.doc,.docx,.ppt,.pptx,.xls,.xlsx,.txt,.md,.rtf,.odt,.odp,.ods,.jpg,.jpeg,.png,.gif,.webp,.bmp,.tiff,.svg,.mp3,.wav,.flac,.aac,.ogg,.m4a,.wma,.mp4,.webm,.avi,.mov,.mkv,.flv,.wmv,.m4v,.csv,.json,.yaml,.yml,.html,.xml"
              onChange={(e) => { const file = e.target.files?.[0]; if (file) handleUpload(file); e.target.value = '' }}
            />
          </label>
        </div>
      </Modal>

      <Modal open={showUrl} onClose={() => setShowUrl(false)} title="URL 导入">
        <div className="new-chat-form">
          <div className="login-field">
            <label>URL 地址</label>
            <input className="ba-input" placeholder="https://example.com/document" value={urlInput} onChange={(e) => setUrlInput(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && handleProcessUrl()} />
          </div>
          {collections.length > 0 && (
            <div className="login-field">
              <label><Database size={12} /> 选择知识库</label>
              <select
                className="ba-input"
                value={selectedCollectionId}
                onChange={(e) => setSelectedCollectionId(e.target.value)}
              >
                {collections.map((coll) => (
                  <option key={coll.id} value={coll.id}>{coll.name} ({coll.size} 向量)</option>
                ))}
              </select>
            </div>
          )}
          <button className="ba-btn ba-btn-primary" onClick={handleProcessUrl} style={{ marginTop: 16, width: '100%' }}>导入</button>
        </div>
      </Modal>

      <Modal open={showNewCollection} onClose={() => setShowNewCollection(false)} title="新建向量集合">
        <div className="new-chat-form" style={{ maxHeight: '60vh', overflowY: 'auto' }}>
          <div className="login-field">
            <label>集合名称</label>
            <input className="ba-input" placeholder="输入集合名称" value={newCollName} onChange={(e) => setNewCollName(e.target.value)} />
          </div>
          <div className="login-field">
            <label>向量模型</label>
            <select className="ba-input" value={newCollModelType} onChange={(e) => setNewCollModelType(Number(e.target.value))}>
              {modelTypeOptions.map((o) => (<option key={o.value} value={o.value}>{o.label}</option>))}
            </select>
          </div>
          <div className="login-field">
            <label>索引类型</label>
            <select className="ba-input" value={newCollIndexType} onChange={(e) => setNewCollIndexType(Number(e.target.value))}>
              {indexTypeOptions.map((o) => (<option key={o.value} value={o.value}>{o.label}</option>))}
            </select>
          </div>
          <div className="login-field">
            <label>向量维度</label>
            <input className="ba-input" type="number" placeholder="768" value={newCollDimension} onChange={(e) => setNewCollDimension(Number(e.target.value) || 768)} />
          </div>

          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 8, marginBottom: 4 }}>
            <span style={{ fontSize: 12, color: 'var(--ba-text-light)' }}>留空则使用解析模型配置中的通用值</span>
            <button className="ba-btn ba-btn-secondary" onClick={fillFromGlobalConfig} style={{ padding: '4px 10px', fontSize: 12 }}>
              填入通用配置
            </button>
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>🖼️ VLM 视觉语言模型（图片/文档理解）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="gpt-4o / qwen-vl-max / glm-4v" value={newCollVlmModel} onChange={(e) => setNewCollVlmModel(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1" value={newCollVlmBaseUrl} onChange={(e) => setNewCollVlmBaseUrl(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.new_vlm ? 'text' : 'password'} placeholder="sk-..." value={newCollVlmApiKey} onChange={(e) => setNewCollVlmApiKey(e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('new_vlm')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.new_vlm ? '隐藏' : '显示'}</button>
            </div>
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>🧠 LLM 大语言模型（文本提取/摘要）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="gpt-4o-mini / qwen-plus / glm-4-flash" value={newCollLlmModel} onChange={(e) => setNewCollLlmModel(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1" value={newCollLlmBaseUrl} onChange={(e) => setNewCollLlmBaseUrl(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.new_llm ? 'text' : 'password'} placeholder="sk-..." value={newCollLlmApiKey} onChange={(e) => setNewCollLlmApiKey(e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('new_llm')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.new_llm ? '隐藏' : '显示'}</button>
            </div>
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>🎙️ ASR 语音识别模型（音频转写）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="whisper-1 / paraformer / sensevoice" value={newCollAsrModel} onChange={(e) => setNewCollAsrModel(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1" value={newCollAsrBaseUrl} onChange={(e) => setNewCollAsrBaseUrl(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.new_asr ? 'text' : 'password'} placeholder="sk-..." value={newCollAsrApiKey} onChange={(e) => setNewCollAsrApiKey(e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('new_asr')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.new_asr ? '隐藏' : '显示'}</button>
            </div>
          </div>

          <h4 style={{ color: 'var(--ba-blue)', margin: '16px 0 8px' }}>📐 Embedding 向量模型（文本向量化）</h4>
          <div className="login-field">
            <label>模型名称</label>
            <input className="ba-input" placeholder="text-embedding-3-small / bge-large-zh / doubao-embedding-vision-250615" value={newCollEmbeddingModel} onChange={(e) => setNewCollEmbeddingModel(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Base URL</label>
            <input className="ba-input" placeholder="https://api.openai.com/v1 / https://ark.cn-beijing.volces.com/api/v3/embeddings/multimodal" value={newCollEmbeddingBaseUrl} onChange={(e) => setNewCollEmbeddingBaseUrl(e.target.value)} />
          </div>
          <div className="login-field">
            <label>API Key</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="ba-input" type={showApiKeys.new_embedding ? 'text' : 'password'} placeholder="sk-..." value={newCollEmbeddingApiKey} onChange={(e) => setNewCollEmbeddingApiKey(e.target.value)} style={{ flex: 1 }} />
              <button className="ba-btn ba-btn-secondary" onClick={() => toggleApiKey('new_embedding')} style={{ padding: '6px 12px', whiteSpace: 'nowrap' }}>{showApiKeys.new_embedding ? '隐藏' : '显示'}</button>
            </div>
          </div>

          <button className="ba-btn ba-btn-primary" onClick={handleCreateCollection} style={{ marginTop: 16, width: '100%' }}>创建</button>
        </div>
      </Modal>

      <Modal open={!!viewDoc} onClose={() => { setViewDoc(null); setViewChunks([]) }} title={viewDoc?.name || '文档详情'}>
        <div style={{ maxHeight: '60vh', overflowY: 'auto' }}>
          {viewDoc && (
            <div style={{ marginBottom: 12, padding: '8px 12px', background: 'var(--ba-bg-hover)', borderRadius: 8, fontSize: 13 }}>
              <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
                <span>类型: {viewDoc.source_type}</span>
                <span>分块: {viewDoc.chunk_count}</span>
                <span>状态: {statusConfig[viewDoc.status]?.label || viewDoc.status}</span>
              </div>
            </div>
          )}
          {loadingChunks && <div style={{ textAlign: 'center', padding: 24, color: 'var(--ba-text-light)' }}>加载中...</div>}
          {!loadingChunks && viewChunks.length === 0 && <div style={{ textAlign: 'center', padding: 24, color: 'var(--ba-text-light)' }}>暂无分块数据</div>}
          {!loadingChunks && viewChunks.map((chunk, i) => (
            <div key={chunk.id || i} style={{ marginBottom: 8, padding: '10px 12px', background: 'var(--ba-bg-hover)', borderRadius: 8, fontSize: 13 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                <span style={{ color: 'var(--ba-blue)', fontWeight: 600 }}>分块 #{chunk.chunk_index ?? i}</span>
                <span style={{ color: 'var(--ba-text-light)', fontSize: 12 }}>{chunk.chunk_type}</span>
              </div>
              <p style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.6, color: 'var(--ba-text)' }}>{chunk.content}</p>
            </div>
          ))}
        </div>
      </Modal>

      <Modal open={!!previewColl} onClose={() => { setPreviewColl(null); setPreviewVectors([]); setPreviewTotal(0); setPreviewError(null) }} title={previewColl ? `向量预览 - ${previewColl.name}` : '向量预览'}>
        <div style={{ maxHeight: '60vh', overflowY: 'auto' }}>
          {previewColl && (
            <div style={{ marginBottom: 12, padding: '8px 12px', background: 'var(--ba-bg-hover)', borderRadius: 8, fontSize: 13 }}>
              <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
                <span>维度: {previewColl.dimension}D</span>
                <span>向量数: {previewTotal}</span>
                <span>{modelTypeOptions.find((m) => m.value === previewColl.model_type)?.label || 'Custom'}</span>
                <span>{indexTypeOptions.find((i) => i.value === previewColl.index_type)?.label || 'HNSW'}</span>
                {previewColl.embedding_model && <span style={{ color: 'var(--ba-blue)' }}>Embed: {previewColl.embedding_model}</span>}
              </div>
            </div>
          )}
          {loadingPreview && <div style={{ textAlign: 'center', padding: 24, color: 'var(--ba-text-light)' }}>加载中...</div>}
          {previewError && (
            <div style={{ textAlign: 'center', padding: 24, color: 'var(--ba-accent)' }}>
              <p>加载失败: {previewError}</p>
            </div>
          )}
          {!loadingPreview && !previewError && previewVectors.length === 0 && (
            <div style={{ textAlign: 'center', padding: 24, color: 'var(--ba-text-light)' }}>
              <Database size={32} style={{ marginBottom: 8, opacity: 0.5 }} />
              <p>暂无向量数据</p>
              <p style={{ fontSize: 12 }}>上传文档并处理后，向量将自动存入此集合</p>
            </div>
          )}
          {!loadingPreview && previewVectors.map((vec, i) => (
            <div key={vec.id || i} style={{ marginBottom: 8, padding: '10px 12px', background: 'var(--ba-bg-hover)', borderRadius: 8, fontSize: 13 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                <span style={{ color: 'var(--ba-blue)', fontWeight: 600, fontSize: 11, fontFamily: 'monospace' }}>
                  {vec.id.length > 20 ? `...${vec.id.slice(-12)}` : vec.id}
                </span>
                <span style={{ color: 'var(--ba-text-light)', fontSize: 11 }}>#{(previewPage - 1) * 10 + i + 1}</span>
              </div>
              <p style={{
                margin: 0,
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                lineHeight: 1.6,
                color: 'var(--ba-text)',
                maxHeight: 120,
                overflow: 'hidden',
                position: 'relative',
              }}>
                {vec.content}
                {vec.content && vec.content.length > 300 && (
                  <span style={{
                    position: 'absolute',
                    bottom: 0,
                    left: 0,
                    right: 0,
                    height: 40,
                    background: 'linear-gradient(transparent, var(--ba-bg-hover))',
                    pointerEvents: 'none',
                  }} />
                )}
              </p>
            </div>
          ))}
          {!loadingPreview && previewTotal > 10 && (
            <div style={{ display: 'flex', justifyContent: 'center', gap: 8, marginTop: 12, alignItems: 'center' }}>
              <button
                className="ba-btn ba-btn-secondary"
                disabled={previewPage <= 1}
                onClick={() => previewColl && handlePreviewVectors(previewColl, previewPage - 1)}
                style={{ padding: '4px 12px', fontSize: 12 }}
              >
                上一页
              </button>
              <span style={{ fontSize: 12, color: 'var(--ba-text-light)' }}>
                {previewPage} / {Math.ceil(previewTotal / 10)}
              </span>
              <button
                className="ba-btn ba-btn-secondary"
                disabled={previewPage >= Math.ceil(previewTotal / 10)}
                onClick={() => previewColl && handlePreviewVectors(previewColl, previewPage + 1)}
                style={{ padding: '4px 12px', fontSize: 12 }}
              >
                下一页
              </button>
            </div>
          )}
        </div>
      </Modal>
    </div>
  )
}
