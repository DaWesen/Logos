import type { Message } from '@/types'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Bot, Copy, Languages, Check, FileText, Download, Edit, RotateCcw, Share2 } from 'lucide-react'
import { useState } from 'react'
import { translateMessage, toMediaUrl, withdrawMessage, editMessage } from '@/api/chat'
import { getAISettings } from '@/api/settings'
import './ChatBubble.css'

interface Props {
  message: Message
  isOwn: boolean
  showAvatar?: boolean
  onEdit?: (message: Message) => void
  onForward?: (message: Message) => void
  onWithdraw?: (messageId: string) => void
  onFileOpen?: (url: string, filename: string) => void
}

// 显示头像的首字母
const getAvatarChar = (name?: string, isOwn?: boolean) => {
  if (!name) {
    return isOwn ? '我' : '用'
  }
  return name.charAt(0).toUpperCase()
}

export default function ChatBubble({ message, isOwn, showAvatar = true, onEdit, onForward, onWithdraw, onFileOpen }: Props) {
  const [translated, setTranslated] = useState<string | null>(null)
  const [translating, setTranslating] = useState(false)
  const [copied, setCopied] = useState(false)
  const [withdrawing, setWithdrawing] = useState(false)
  const [showLangPicker, setShowLangPicker] = useState(false)

  const handleTranslate = async (targetLang: string) => {
    setShowLangPicker(false)
    if (translating) return
    setTranslating(true)
    try {
      const settings = getAISettings()
      const result = await translateMessage(message.content, targetLang, message.id, settings.translation)
      setTranslated(result)
    } catch {
      setTranslated('翻译失败')
    } finally {
      setTranslating(false)
    }
  }

  const handleCopy = () => {
    navigator.clipboard.writeText(message.content)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const handleEdit = () => {
    if (!message.id.startsWith('local-')) {
      onEdit?.(message)
    }
  }

  const handleForward = () => {
    onForward?.(message)
  }

  const handleWithdraw = async () => {
    if (withdrawing || message.id.startsWith('local-')) return
    setWithdrawing(true)
    try {
      await withdrawMessage(message.id)
      onWithdraw?.(message.id)
    } catch {
      alert('撤回失败')
    } finally {
      setWithdrawing(false)
    }
  }

  const isImageFile = (url: string) => {
    return /\.(jpg|jpeg|png|gif|webp|bmp|svg|tiff)$/i.test(url)
  }

  const isVideoFile = (url: string) => {
    return /\.(mp4|webm|avi|mov|mkv)$/i.test(url)
  }

  const formatTime = (dateStr: string) => {
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) return ''
    return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
  }

  const mediaUrl = toMediaUrl(message.mediaUrl || '')
  const mediaMeta = message.mediaMeta || ''
  
  if (message.messageType !== 'text' && message.messageType !== 'system' && message.messageType !== 'withdrawn') {
    console.log('%c💬 [Bubble] 渲染媒体消息', 'background: #E91E63; color: white; padding:2px 5px; border-radius:3px;', {
      id: message.id, type: message.messageType,
      rawMediaUrl: message.mediaUrl,
      processedMediaUrl: mediaUrl,
      mediaMeta,
      content: `"${message.content}"`,
    })
  }

  if (message.messageType === 'system') {
    return (
      <div className="chat-bubble-system fade-in">
        <span className="chat-bubble-system-text">{message.content}</span>
      </div>
    )
  }

  if (message.messageType === 'withdrawn') {
    return (
      <div className="chat-bubble-system fade-in">
        <span className="chat-bubble-system-text">{isOwn ? '你撤回了一条消息' : `${message.senderName || '对方'}撤回了一条消息`}</span>
      </div>
    )
  }

  return (
    <div className={`chat-bubble-row ${isOwn ? 'own' : 'other'} fade-in`}>
      {/* 对方消息：头像在左，气泡在右 */}
      {!isOwn && showAvatar && (
        <div className={`ba-avatar ba-avatar-sm ${message.isBot ? 'bot-avatar' : ''}`}>
          {message.isBot ? (
            message.senderAvatar ? (
              message.senderAvatar.startsWith('http') || message.senderAvatar.startsWith('/') ? (
                <img
                  src={toMediaUrl(message.senderAvatar)}
                  alt=""
                  style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }}
                />
              ) : (
                <span style={{ fontSize: 16 }}>{message.senderAvatar}</span>
              )
            ) : (
              <Bot size={16} />
            )
          ) : (
            message.senderAvatar ? (
              <img 
                src={toMediaUrl(message.senderAvatar)} 
                alt="" 
                style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
              />
            ) : (
              getAvatarChar(message.senderName, false)
            )
          )}
        </div>
      )}
      
      <div className="chat-bubble-wrapper">
        {!isOwn && showAvatar && (
          <div className="chat-bubble-sender">{message.senderName || '用户'}</div>
        )}
        <div className={`chat-bubble ${isOwn ? 'own-bubble' : 'other-bubble'} ${message.isBot ? 'bot-bubble' : ''}`}>
          {message.messageType === 'image' && mediaUrl && (
            <div className="chat-bubble-image" style={{ position: 'relative' }}>
              <img src={mediaUrl} alt="" loading="lazy" onClick={() => onFileOpen ? onFileOpen(mediaUrl, mediaMeta || 'image') : window.open(mediaUrl, '_blank')} style={{ cursor: 'pointer' }} />
              {(message.uploading || (message.uploadProgress !== undefined && message.uploadProgress < 100)) &&
                <div className="chat-bubble-upload-overlay" style={{ clipPath: `inset(0 ${100 - (message.uploadProgress || 0)}% 0 0)` }} />}
            </div>
          )}
          {message.messageType === 'video' && mediaUrl && (
            <div className="chat-bubble-video" style={{ position: 'relative' }}>
              <video src={mediaUrl} controls={!(message.uploading || (message.uploadProgress !== undefined && message.uploadProgress < 100))} style={{ maxWidth: '100%', borderRadius: 8 }} />
              {(message.uploading || (message.uploadProgress !== undefined && message.uploadProgress < 100)) &&
                <div className="chat-bubble-upload-overlay" style={{ clipPath: `inset(0 ${100 - (message.uploadProgress || 0)}% 0 0)` }}><span className="chat-bubble-upload-text">{message.uploadProgress || 0}%</span></div>}
            </div>
          )}
          {message.messageType === 'video' && !mediaUrl && (
            <div className="chat-bubble-file" style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', background: 'var(--ba-card-hover)', borderRadius: 8 }}>
              <FileText size={20} style={{ color: 'var(--ba-blue)', flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{mediaMeta || '视频'}</div>
                <div style={{ fontSize: 11, color: 'var(--ba-text-light)' }}>加载中...</div>
              </div>
            </div>
          )}
          {message.messageType === 'voice' && mediaUrl && (
            <div className="chat-bubble-voice" style={{ position: 'relative' }}>
              <audio src={mediaUrl} controls />
              {(message.uploading || (message.uploadProgress !== undefined && message.uploadProgress < 100)) &&
                <div className="chat-bubble-upload-overlay" style={{ clipPath: `inset(0 ${100 - (message.uploadProgress || 0)}% 0 0)` }}><span className="chat-bubble-upload-text">{message.uploadProgress || 0}%</span></div>}
            </div>
          )}
          {message.messageType === 'voice' && !mediaUrl && (
            <div className="chat-bubble-file" style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', background: 'var(--ba-card-hover)', borderRadius: 8 }}>
              <FileText size={20} style={{ color: 'var(--ba-blue)', flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{mediaMeta || '语音'}</div>
                <div style={{ fontSize: 11, color: 'var(--ba-text-light)' }}>加载中...</div>
              </div>
            </div>
          )}
          {message.messageType === 'file' && (
            <div className="chat-bubble-file" style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', background: 'var(--ba-card-hover)', borderRadius: 8, cursor: 'pointer' }}
              onClick={() => { if (mediaUrl) onFileOpen ? onFileOpen(mediaUrl, mediaMeta || '文件') : window.open(mediaUrl, '_blank') }}>
              <FileText size={20} style={{ color: 'var(--ba-blue)', flexShrink: 0 }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{mediaMeta || '文件'}</div>
                <div style={{ fontSize: 11, color: 'var(--ba-text-light)' }}>
                  {mediaUrl ? '点击打开/下载' : '文件'}
                </div>
              </div>
              <Download size={16} style={{ color: 'var(--ba-text-light)' }} />
            </div>
          )}
          {(message.messageType === 'text' || (!mediaUrl && message.messageType !== 'system' && message.messageType !== 'withdrawn' && message.messageType !== 'video' && message.messageType !== 'voice' && message.messageType !== 'image')) && (
            <div className="chat-bubble-content">
              {message.isBot ? (
                <ReactMarkdown remarkPlugins={[remarkGfm]}>{message.content}</ReactMarkdown>
              ) : (
                message.content
              )}
            </div>
          )}
          {translated && (
            <div className="chat-bubble-translation">
              <span className="translation-label">译文：</span>
              {translated}
            </div>
          )}
        </div>
        <div className="chat-bubble-meta">
          <span className="chat-bubble-time">{formatTime(message.createdAt)}</span>
          {isOwn && (
            <span className={`chat-bubble-read-mark ${message.isRead ? 'read' : 'unread'}`}>
              {message.isRead ? '✓✓' : '✓'}
            </span>
          )}
          <div className="chat-bubble-actions">
            {!isOwn && (
              <div style={{ position: 'relative' }}>
                <button className="bubble-action" onClick={() => setShowLangPicker(!showLangPicker)} title="翻译" disabled={translating}>
                  <Languages size={13} />
                </button>
                {showLangPicker && (
                  <div className="lang-picker">
                    <button onClick={() => handleTranslate('zh')}>中文</button>
                    <button onClick={() => handleTranslate('en')}>English</button>
                    <button onClick={() => handleTranslate('ja')}>日本語</button>
                    <button onClick={() => handleTranslate('ko')}>한국어</button>
                    <button onClick={() => handleTranslate('fr')}>Français</button>
                  </div>
                )}
              </div>
            )}
            <button className="bubble-action" onClick={handleCopy} title="复制">
              {copied ? <Check size={13} /> : <Copy size={13} />}
            </button>
            <button className="bubble-action" onClick={handleForward} title="转发">
              <Share2 size={13} />
            </button>
            {isOwn && !message.id.startsWith('local-') && (
              <>
                <button className="bubble-action" onClick={handleEdit} title="编辑">
                  <Edit size={13} />
                </button>
                <button className="bubble-action" onClick={handleWithdraw} title="撤回" disabled={withdrawing}>
                  <RotateCcw size={13} />
                </button>
              </>
            )}
          </div>
        </div>
      </div>

      {/* 自己消息：头像在右 */}
      {isOwn && (
        <div className="ba-avatar ba-avatar-sm">
          {message.senderAvatar ? (
            <img 
              src={toMediaUrl(message.senderAvatar)} 
              alt="" 
              style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
            />
          ) : (
            getAvatarChar(message.senderName, true)
          )}
        </div>
      )}
    </div>
  )
}
