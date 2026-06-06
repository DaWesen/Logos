import { useState, useEffect } from 'react'
import { X, Download, FileText, Image, File, ZoomIn, ZoomOut, Maximize2 } from 'lucide-react'
import Modal from './Modal'
import './FileViewerModal.css'

interface FileViewerModalProps {
  open: boolean
  onClose: () => void
  url: string
  filename: string
}

function getFileExtension(url: string): string {
  const clean = url.split('?')[0]
  const ext = clean.split('.').pop()?.toLowerCase() || ''
  return ext
}

function isTextFile(ext: string): boolean {
  return ['txt', 'md', 'json', 'yaml', 'yml', 'xml', 'html', 'css', 'js', 'ts', 'tsx', 'jsx', 'py', 'go', 'java', 'c', 'cpp', 'h', 'rs', 'rb', 'php', 'sh', 'bash', 'zsh', 'sql', 'log', 'csv', 'toml', 'ini', 'cfg', 'conf', 'env', 'gitignore', 'dockerfile', 'makefile'].includes(ext)
}

function isImageFile(ext: string): boolean {
  return ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'tiff', 'ico'].includes(ext)
}

function isVideoFile(ext: string): boolean {
  return ['mp4', 'webm', 'avi', 'mov', 'mkv', 'flv'].includes(ext)
}

function isAudioFile(ext: string): boolean {
  return ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a', 'wma'].includes(ext)
}

function isPdfFile(ext: string): boolean {
  return ext === 'pdf'
}

export default function FileViewerModal({ open, onClose, url, filename }: FileViewerModalProps) {
  const [textContent, setTextContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [imageZoom, setImageZoom] = useState(1)

  const ext = getFileExtension(url || filename)

  useEffect(() => {
    if (!open || !url) return
    setTextContent(null)
    setError(null)
    setImageZoom(1)

    if (isTextFile(ext)) {
      setLoading(true)
      fetch(url)
        .then((res) => {
          if (!res.ok) throw new Error(`HTTP ${res.status}`)
          return res.text()
        })
        .then((text) => {
          setTextContent(text)
          setLoading(false)
        })
        .catch((err) => {
          setError(`加载失败: ${err.message}`)
          setLoading(false)
        })
    }
  }, [open, url, ext])

  const handleDownload = () => {
    const a = document.createElement('a')
    a.href = url
    a.download = filename || 'download'
    a.target = '_blank'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  }

  const renderContent = () => {
    if (loading) {
      return (
        <div className="file-viewer-loading">
          <div className="file-viewer-spinner" />
          <p>加载中...</p>
        </div>
      )
    }

    if (error) {
      return (
        <div className="file-viewer-error">
          <FileText size={48} style={{ marginBottom: 12, opacity: 0.5 }} />
          <p>{error}</p>
          <button className="ba-btn ba-btn-secondary" onClick={handleDownload} style={{ marginTop: 12 }}>
            <Download size={14} /> 下载文件
          </button>
        </div>
      )
    }

    if (isImageFile(ext)) {
      return (
        <div className="file-viewer-image-container">
          <div className="file-viewer-image-toolbar">
            <button className="ba-btn ba-btn-secondary" onClick={() => setImageZoom((z) => Math.min(z * 1.25, 5))} title="放大"><ZoomIn size={16} /></button>
            <button className="ba-btn ba-btn-secondary" onClick={() => setImageZoom((z) => Math.max(z / 1.25, 0.2))} title="缩小"><ZoomOut size={16} /></button>
            <button className="ba-btn ba-btn-secondary" onClick={() => setImageZoom(1)} title="重置"><Maximize2 size={16} /></button>
            <span style={{ fontSize: 12, color: 'var(--ba-text-light)' }}>{Math.round(imageZoom * 100)}%</span>
          </div>
          <div className="file-viewer-image-scroll">
            <img src={url} alt={filename} style={{ transform: `scale(${imageZoom})`, transformOrigin: 'top left' }} />
          </div>
        </div>
      )
    }

    if (isVideoFile(ext)) {
      return (
        <div className="file-viewer-video-container">
          <video src={url} controls autoPlay style={{ maxWidth: '100%', maxHeight: '60vh', borderRadius: 8 }} />
        </div>
      )
    }

    if (isAudioFile(ext)) {
      return (
        <div className="file-viewer-audio-container">
          <div className="file-viewer-audio-icon">🎵</div>
          <p style={{ color: 'var(--ba-text)', marginBottom: 16 }}>{filename}</p>
          <audio src={url} controls autoPlay style={{ width: '100%', maxWidth: 400 }} />
        </div>
      )
    }

    if (isPdfFile(ext)) {
      return (
        <div className="file-viewer-pdf-container">
          <iframe src={url} style={{ width: '100%', height: '60vh', border: 'none', borderRadius: 8 }} title={filename} />
        </div>
      )
    }

    if (isTextFile(ext) && textContent !== null) {
      return (
        <div className="file-viewer-text-container">
          <pre className="file-viewer-code"><code>{textContent}</code></pre>
        </div>
      )
    }

    // 不支持的类型，提供下载
    return (
      <div className="file-viewer-unsupported">
        <File size={48} style={{ marginBottom: 12, opacity: 0.5 }} />
        <p style={{ color: 'var(--ba-text)', marginBottom: 4 }}>{filename}</p>
        <p style={{ color: 'var(--ba-text-light)', fontSize: 13, marginBottom: 16 }}>
          .{ext} 文件无法在线预览
        </p>
        <button className="ba-btn ba-btn-primary" onClick={handleDownload}>
          <Download size={14} /> 下载文件
        </button>
      </div>
    )
  }

  return (
    <Modal open={open} onClose={onClose} title={filename || '文件查看'} width={720}>
      <div className="file-viewer-modal">
        <div className="file-viewer-header">
          <div className="file-viewer-meta">
            <span className="file-viewer-ext">.{ext}</span>
            <span className="file-viewer-filename">{filename}</span>
          </div>
          <button className="ba-btn ba-btn-secondary" onClick={handleDownload} style={{ padding: '4px 12px', fontSize: 12 }}>
            <Download size={14} /> 下载
          </button>
        </div>
        <div className="file-viewer-body">
          {renderContent()}
        </div>
      </div>
    </Modal>
  )
}
