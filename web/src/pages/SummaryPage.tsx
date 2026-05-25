import { useState } from 'react'
import { FileText, Sparkles, ListTodo, MessageSquare } from 'lucide-react'
import client from '@/api/client'
import './SummaryPage.css'

interface TodoItem {
  id: string
  content: string
  assignee: string
  deadline: string
  status: string
}

interface ReplyCandidate {
  id: string
  content: string
  confidence: number
  type: string
}

function extractData<T>(res: { data: unknown }, fallback: T): T {
  const d = res.data as Record<string, unknown>
  if (!d) return fallback
  if (d.data !== undefined && d.data !== null) return d.data as T
  if (typeof d === 'object' && !Array.isArray(d) && !('code' in d)) return d as T
  return fallback
}

export default function SummaryPage() {
  const [chatId, setChatId] = useState('')
  const [summary, setSummary] = useState('')
  const [keyPoints, setKeyPoints] = useState<string[]>([])
  const [participants, setParticipants] = useState<string[]>([])
  const [todos, setTodos] = useState<TodoItem[]>([])
  const [replies, setReplies] = useState<ReplyCandidate[]>([])
  const [loading, setLoading] = useState(false)
  const [activeTab, setActiveTab] = useState<'summary' | 'todos' | 'replies'>('summary')

  const handleSummarize = async () => {
    if (!chatId.trim()) return
    setLoading(true)
    setSummary('')
    setKeyPoints([])
    setParticipants([])
    setTodos([])
    setReplies([])
    try {
      const res = await client.post('/summary/messages', {
        chat_id: chatId.trim(),
        chat_type: 1,
        include_todos: true,
        include_candidates: true,
      })
      const data = extractData<Record<string, unknown>>(res, {})
      setSummary(String(data.summary || ''))
      setKeyPoints(Array.isArray(data.key_points) ? data.key_points as string[] : [])
      setParticipants(Array.isArray(data.participants) ? data.participants as string[] : [])
      setTodos(Array.isArray(data.todos) ? data.todos as TodoItem[] : [])
      setReplies(Array.isArray(data.reply_candidates) ? data.reply_candidates as ReplyCandidate[] : [])
    } catch {
      setSummary('摘要生成失败，请稍后重试')
    } finally {
      setLoading(false)
    }
  }

  const handleExtractTodos = async () => {
    if (!chatId.trim()) return
    setLoading(true)
    try {
      const res = await client.post('/summary/todos', {
        chat_id: chatId.trim(),
        chat_type: 1,
      })
      const data = extractData<Record<string, unknown>>(res, {})
      setTodos(Array.isArray(data.todos) ? data.todos as TodoItem[] : [])
      setActiveTab('todos')
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  const handleGenerateReplies = async () => {
    if (!chatId.trim()) return
    setLoading(true)
    try {
      const res = await client.post('/summary/reply-candidates', {
        chat_id: chatId.trim(),
        chat_type: 1,
        candidate_count: 3,
      })
      const data = extractData<Record<string, unknown>>(res, {})
      setReplies(Array.isArray(data.candidates) ? data.candidates as ReplyCandidate[] : [])
      setActiveTab('replies')
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="summary-page">
      <div className="summary-header">
        <div>
          <h2>智能摘要</h2>
          <p className="summary-subtitle">AI 驱动的聊天摘要、待办提取、智能回复</p>
        </div>
      </div>

      <div className="summary-input-section ba-card">
        <div className="login-field">
          <label>聊天 ID</label>
          <input
            className="ba-input"
            placeholder="输入聊天会话 ID"
            value={chatId}
            onChange={(e) => setChatId(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleSummarize()}
          />
        </div>
        <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
          <button className="ba-btn ba-btn-primary" onClick={handleSummarize} disabled={loading}>
            <Sparkles size={16} /> {loading ? '生成中...' : '生成摘要'}
          </button>
          <button className="ba-btn ba-btn-secondary" onClick={handleExtractTodos} disabled={loading}>
            <ListTodo size={16} /> 提取待办
          </button>
          <button className="ba-btn ba-btn-secondary" onClick={handleGenerateReplies} disabled={loading}>
            <MessageSquare size={16} /> 智能回复
          </button>
        </div>
      </div>

      {(summary || todos.length > 0 || replies.length > 0) && (
        <div className="summary-result-section">
          <div className="summary-tabs">
            <button className={`summary-tab ${activeTab === 'summary' ? 'active' : ''}`} onClick={() => setActiveTab('summary')}>
              <FileText size={14} /> 摘要
            </button>
            <button className={`summary-tab ${activeTab === 'todos' ? 'active' : ''}`} onClick={() => setActiveTab('todos')}>
              <ListTodo size={14} /> 待办 ({todos.length})
            </button>
            <button className={`summary-tab ${activeTab === 'replies' ? 'active' : ''}`} onClick={() => setActiveTab('replies')}>
              <MessageSquare size={14} /> 回复 ({replies.length})
            </button>
          </div>

          {activeTab === 'summary' && (
            <div className="summary-content ba-card">
              {summary && <div className="summary-text">{summary}</div>}
              {keyPoints.length > 0 && (
                <div className="summary-key-points">
                  <h4>关键要点</h4>
                  <ul>{keyPoints.map((p, i) => <li key={i}>{p}</li>)}</ul>
                </div>
              )}
              {participants.length > 0 && (
                <div className="summary-participants">
                  <h4>参与者</h4>
                  <div className="summary-participant-list">
                    {participants.map((p, i) => <span key={i} className="summary-participant-tag">{p}</span>)}
                  </div>
                </div>
              )}
              {!summary && keyPoints.length === 0 && <p className="summary-empty">暂无摘要内容</p>}
            </div>
          )}

          {activeTab === 'todos' && (
            <div className="summary-content ba-card">
              {todos.length > 0 ? (
                <div className="summary-todo-list">
                  {todos.map((todo) => (
                    <div key={todo.id} className="summary-todo-item">
                      <div className="summary-todo-status">
                        {todo.status === 'completed' ? '✅' : todo.status === 'in_progress' ? '🔄' : '⏳'}
                      </div>
                      <div className="summary-todo-content">
                        <div className="summary-todo-text">{todo.content}</div>
                        <div className="summary-todo-meta">
                          {todo.assignee && <span>👤 {todo.assignee}</span>}
                          {todo.deadline && <span>📅 {todo.deadline}</span>}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              ) : <p className="summary-empty">暂无待办事项</p>}
            </div>
          )}

          {activeTab === 'replies' && (
            <div className="summary-content ba-card">
              {replies.length > 0 ? (
                <div className="summary-reply-list">
                  {replies.map((reply) => (
                    <div key={reply.id} className="summary-reply-item">
                      <div className="summary-reply-content">{reply.content}</div>
                      <div className="summary-reply-meta">
                        <span className="summary-reply-confidence">置信度: {Math.round(reply.confidence * 100)}%</span>
                        <span className="summary-reply-type">{reply.type}</span>
                      </div>
                    </div>
                  ))}
                </div>
              ) : <p className="summary-empty">暂无回复候选</p>}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
