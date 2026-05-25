import { useState, useCallback } from 'react'
import type { User } from '@/types'
import * as userApi from '@/api/user'

export function useAuth() {
  const [user, setUser] = useState<User | null>(() => {
    try {
      const saved = localStorage.getItem('aim_user')
      return saved ? JSON.parse(saved) : null
    } catch {
      return null
    }
  })
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('aim_token'))
  const [loading, setLoading] = useState(false)

  const isAuthenticated = !!token

  const saveAuth = (newToken: string, newUser: User) => {
    localStorage.setItem('aim_token', newToken)
    localStorage.setItem('aim_user', JSON.stringify(newUser))
    setToken(newToken)
    setUser(newUser)
    // 触发自定义事件，通知 WebSocket 连接
    window.dispatchEvent(new CustomEvent('aim_login_success'))
  }

  const login = useCallback(async (username: string, password: string) => {
    setLoading(true)
    try {
      const res = await userApi.login({ username, password })
      const t = res.token || ''
      const u = res.user || { id: '', username, nickname: username, avatar: '', email: '', status: 'online' as const }
      if (t) {
        saveAuth(t, u)
      }
      return res
    } finally {
      setLoading(false)
    }
  }, [])

  const register = useCallback(async (username: string, password: string, nickname: string, email: string) => {
    setLoading(true)
    try {
      const res = await userApi.register({ username, password, nickname, email })
      const t = res.token || ''
      const u = res.user || { id: '', username, nickname, avatar: '', email, status: 'online' as const }
      if (t) {
        saveAuth(t, u)
      }
      return res
    } finally {
      setLoading(false)
    }
  }, [])

  const logout = useCallback(() => {
    localStorage.removeItem('aim_token')
    localStorage.removeItem('aim_user')
    setToken(null)
    setUser(null)
  }, [])

  const refreshProfile = useCallback(async () => {
    try {
      const profile = await userApi.getProfile()
      if (profile) {
        setUser(profile)
        localStorage.setItem('aim_user', JSON.stringify(profile))
      }
    } catch {
      // ignore
    }
  }, [])

  return { user, token, isAuthenticated, loading, login, register, logout, refreshProfile }
}
