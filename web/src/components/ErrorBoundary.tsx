import { Component } from 'react'
import { AlertTriangle, RefreshCw } from 'lucide-react'

interface Props {
  children: React.ReactNode
}

interface State {
  hasError: boolean
  error?: Error
}

export default class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  handleReload = () => {
    this.setState({ hasError: false, error: undefined })
    window.location.reload()
  }

  render() {
    if (this.state.hasError) {
      return (
        <div style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          height: '100vh',
          gap: 16,
          color: '#64748b',
          fontFamily: 'system-ui, sans-serif',
        }}>
          <AlertTriangle size={48} color="#FF6B8A" />
          <h2 style={{ fontSize: 20, fontWeight: 700, color: '#1e293b' }}>页面出错了</h2>
          <p style={{ fontSize: 14, maxWidth: 400, textAlign: 'center' }}>
            {this.state.error?.message || '发生了未知错误'}
          </p>
          <button
            onClick={this.handleReload}
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: '10px 20px',
              borderRadius: 10,
              border: 'none',
              background: 'linear-gradient(135deg, #4A90D9, #5BB5F0)',
              color: 'white',
              fontSize: 14,
              fontWeight: 600,
              cursor: 'pointer',
            }}
          >
            <RefreshCw size={16} /> 重新加载
          </button>
        </div>
      )
    }
    return this.props.children
  }
}
