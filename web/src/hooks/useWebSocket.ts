import { useState, useEffect, useRef, useCallback } from 'react'
import type { WsMessage } from '@/types'

interface UseWebSocketOptions {
  onMessage?: (msg: WsMessage) => void
  onOpen?: () => void
  onClose?: () => void
  onError?: (err: Event) => void
  reconnectInterval?: number
  maxRetries?: number
}

let wsInstance: WebSocket | null = null
let wsUrl = ''
let wsConnected = false
let wsManualClose = false
let wsRetryCount = 0
let wsListeners: {
  message: ((msg: WsMessage) => void)[]
  open: (() => void)[]
  close: (() => void)[]
  error: ((err: Event) => void)[]
} = { message: [], open: [], close: [], error: [] }

function notifyListeners(type: 'message' | 'open' | 'close' | 'error', data?: WsMessage | Event) {
  const list = wsListeners[type]
  for (let i = list.length - 1; i >= 0; i--) {
    try {
      if (type === 'message') (list[i] as (d: WsMessage) => void)(data as WsMessage)
      else if (type === 'error') (list[i] as (d: Event) => void)(data as Event)
      else (list[i] as () => void)()
    } catch { /* */ }
  }
}

function connectWebSocket(url: string, reconnectInterval = 5000, maxRetries = 10) {
  const token = localStorage.getItem('aim_token')
  if (!token) return

  if (wsInstance) {
    if (wsInstance.readyState === WebSocket.OPEN || wsInstance.readyState === WebSocket.CONNECTING) {
      return
    }
  }

  wsUrl = url
  wsManualClose = false

  try {
    const separator = url.includes('?') ? '&' : '?'
    const ws = new WebSocket(`${url}${separator}token=${encodeURIComponent(token)}`)
    wsInstance = ws

    ws.onopen = () => {
      wsConnected = true
      wsRetryCount = 0
      notifyListeners('open')
    }

    ws.onmessage = (event) => {
      try {
        const msg: WsMessage = JSON.parse(event.data)
        if (msg.type === 'connect') {
          wsConnected = true
          wsRetryCount = 0
          return
        }
        if (msg.type === 'error') {
          console.warn('WebSocket server error:', msg)
          return
        }
        if (msg.type === 'heartbeat') {
          return
        }
        notifyListeners('message', msg)
      } catch { /* */ }
    }

    ws.onclose = () => {
      wsConnected = false
      wsInstance = null
      notifyListeners('close')
      if (!wsManualClose && wsRetryCount < maxRetries) {
        wsRetryCount++
        setTimeout(() => {
          if (!wsManualClose) connectWebSocket(wsUrl, reconnectInterval, maxRetries)
        }, reconnectInterval)
      }
    }

    ws.onerror = () => {
      notifyListeners('error', new Event('WebSocket error'))
    }
  } catch { /* */ }
}

function disconnectWebSocket() {
  wsManualClose = true
  if (wsInstance) {
    wsInstance.onclose = null
    wsInstance.onerror = null
    wsInstance.onmessage = null
    wsInstance.onopen = null
    if (wsInstance.readyState === WebSocket.OPEN || wsInstance.readyState === WebSocket.CONNECTING) {
      wsInstance.close()
    }
    wsInstance = null
  }
  wsConnected = false
}

function sendWebSocketMessage(data: WsMessage) {
  if (wsInstance?.readyState === WebSocket.OPEN) {
    wsInstance.send(JSON.stringify(data))
  }
}

export function useWebSocket(url: string, options: UseWebSocketOptions = {}) {
  const { onMessage, onOpen, onClose, onError, reconnectInterval = 5000, maxRetries = 10 } = options
  const [connected, setConnected] = useState(wsConnected)
  const onMessageRef = useRef(onMessage)
  const onOpenRef = useRef(onOpen)
  const onCloseRef = useRef(onClose)
  const onErrorRef = useRef(onError)

  onMessageRef.current = onMessage
  onOpenRef.current = onOpen
  onCloseRef.current = onClose
  onErrorRef.current = onError

  useEffect(() => {
    const handleMessage = (msg: WsMessage) => onMessageRef.current?.(msg)
    const handleOpen = () => { setConnected(true); onOpenRef.current?.() }
    const handleClose = () => { setConnected(false); onCloseRef.current?.() }
    const handleError = (err: Event) => onErrorRef.current?.(err)

    wsListeners.message.push(handleMessage)
    wsListeners.open.push(handleOpen)
    wsListeners.close.push(handleClose)
    wsListeners.error.push(handleError)

    if (wsConnected) setConnected(true)

    // 每次组件挂载时都尝试连接
    const token = localStorage.getItem('aim_token')
    if (token) {
      // 只有当没有连接或者连接关闭时才连接
      if (!wsInstance || wsInstance.readyState === WebSocket.CLOSED || wsInstance.readyState === WebSocket.CLOSING) {
        wsManualClose = false
        wsRetryCount = 0
        // 添加延迟避免快速重连
        setTimeout(() => {
          if (!wsManualClose) {
            connectWebSocket(url, reconnectInterval, maxRetries)
          }
        }, 300)
      }
    }

    return () => {
      wsListeners.message = wsListeners.message.filter((l) => l !== handleMessage)
      wsListeners.open = wsListeners.open.filter((l) => l !== handleOpen)
      wsListeners.close = wsListeners.close.filter((l) => l !== handleClose)
      wsListeners.error = wsListeners.error.filter((l) => l !== handleError)
    }
  }, [url, reconnectInterval, maxRetries])

  const send = useCallback((data: WsMessage) => {
    sendWebSocketMessage(data)
  }, [])

  const disconnect = useCallback(() => {
    disconnectWebSocket()
    setConnected(false)
  }, [])

  const reconnect = useCallback(() => {
    wsManualClose = false
    wsRetryCount = 0
    connectWebSocket(url, reconnectInterval, maxRetries)
  }, [url, reconnectInterval, maxRetries])

  return { connected, send, disconnect, reconnect }
}
