import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '@/hooks/useAuth'
import { MessageCircle } from 'lucide-react'
import './LoginPage.css'

export default function LoginPage() {
  const [isRegister, setIsRegister] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [nickname, setNickname] = useState('')
  const [email, setEmail] = useState('')
  const [error, setError] = useState('')
  const { login, register, loading } = useAuth()
  const navigate = useNavigate()

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    try {
      if (isRegister) {
        if (!nickname.trim()) { setError('请输入昵称'); return }
        await register(username, password, nickname, email)
      } else {
        await login(username, password)
      }
      navigate('/chat')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '操作失败'
      setError(msg)
    }
  }

  return (
    <div className="login-page">
      <div className="login-bg-decoration">
        <div className="login-circle login-circle-1" />
        <div className="login-circle login-circle-2" />
        <div className="login-circle login-circle-3" />
      </div>

      <div className="login-card ba-card fade-in">
        <div className="login-header">
          <div className="login-logo">
            <div className="login-logo-icon">
              <MessageCircle size={28} />
            </div>
            <h1 className="login-title">AIM</h1>
          </div>
          <p className="login-subtitle">AI 驱动的即时通讯</p>
        </div>

        <form className="login-form" onSubmit={handleSubmit}>
          {error && <div className="login-error">{error}</div>}

          <div className="login-field">
            <label>用户名</label>
            <input
              className="ba-input"
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="请输入用户名"
              required
              autoFocus
            />
          </div>

          <div className="login-field">
            <label>密码</label>
            <input
              className="ba-input"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="请输入密码"
              required
            />
          </div>

          {isRegister && (
            <>
              <div className="login-field">
                <label>昵称</label>
                <input
                  className="ba-input"
                  type="text"
                  value={nickname}
                  onChange={(e) => setNickname(e.target.value)}
                  placeholder="请输入昵称"
                />
              </div>
              <div className="login-field">
                <label>邮箱</label>
                <input
                  className="ba-input"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="请输入邮箱（选填）"
                />
              </div>
            </>
          )}

          <button className="ba-btn ba-btn-primary login-submit" type="submit" disabled={loading}>
            {loading ? '处理中...' : isRegister ? '注册' : '登录'}
          </button>
        </form>

        <div className="login-footer">
          <span>{isRegister ? '已有账号？' : '没有账号？'}</span>
          <button className="login-switch" onClick={() => { setIsRegister(!isRegister); setError('') }}>
            {isRegister ? '去登录' : '去注册'}
          </button>
        </div>
      </div>
    </div>
  )
}
