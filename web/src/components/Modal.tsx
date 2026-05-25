import { useEffect, useRef } from 'react'
import { X } from 'lucide-react'
import './Modal.css'

interface Props {
  open: boolean
  onClose: () => void
  title: string
  children: React.ReactNode
  width?: number
  style?: React.CSSProperties
}

export default function Modal({ open, onClose, title, children, width = 480, style }: Props) {
  const overlayRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (open) {
      document.body.style.overflow = 'hidden'
    } else {
      document.body.style.overflow = ''
    }
    return () => { document.body.style.overflow = '' }
  }, [open])

  if (!open) return null

  return (
    <div className="modal-overlay" ref={overlayRef} onClick={(e) => e.target === overlayRef.current && onClose()}>
      <div className="modal-container fade-in" style={{ maxWidth: width, ...style }}>
        <div className="modal-header">
          <h3 className="modal-title">{title}</h3>
          <button className="modal-close" onClick={onClose}>
            <X size={18} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
      </div>
    </div>
  )
}
