import client from './client'

function extractData<T>(res: { data: unknown }, fallback: T | null): T | null {
  const d = res.data as Record<string, unknown>
  if (!d) return fallback
  if (d.data !== undefined && d.data !== null) return d.data as T
  if (typeof d === 'object' && !Array.isArray(d) && !('code' in d)) return d as T
  return fallback
}

function extractArray<T>(res: { data: unknown }): T[] {
  const d = res.data as Record<string, unknown>
  if (!d) return []
  if (Array.isArray(d.data)) return d.data as T[]
  if (Array.isArray(d)) return d as T[]
  if (d.data && typeof d.data === 'object') {
    const inner = d.data as Record<string, unknown>
    if (Array.isArray(inner.messages)) return inner.messages as T[]
    if (Array.isArray(inner.users)) return inner.users as T[]
    if (Array.isArray(inner.items)) return inner.items as T[]
    if (Array.isArray(inner.list)) return inner.list as T[]
    if (Array.isArray(inner.bots)) return inner.bots as T[]
  }
  return []
}

function normalizeUser(raw: Record<string, unknown>) {
  return {
    id: String(raw.id || raw.user_id || ''),
    username: String(raw.username || ''),
    nickname: String(raw.nickname || raw.username || raw.name || ''),
    avatar: String(raw.avatar || ''),
    email: String(raw.email || ''),
    status: 'online' as const,
  }
}

export async function login(req: { username: string; password: string }) {
  const res = await client.post('/auth/login', { username: req.username, password: req.password })
  const data = extractData<Record<string, unknown>>(res, {}) || {}
  const token = String(data.token || '')
  const rawUser = (data.user || {}) as Record<string, unknown>
  const user = normalizeUser(rawUser)
  if (!user.nickname && req.username) user.nickname = req.username
  return { token, user }
}

export async function register(req: { username: string; password: string; nickname: string; email: string }) {
  const res = await client.post('/auth/register', req)
  const data = extractData<Record<string, unknown>>(res, {}) || {}
  const token = String(data.token || '')
  const rawUser = (data.user || {}) as Record<string, unknown>
  const user = normalizeUser(rawUser)
  if (!user.nickname && req.nickname) user.nickname = req.nickname
  return { token, user }
}

export async function getProfile() {
  const token = localStorage.getItem('aim_token')
  let userId = ''
  if (token) {
    try {
      const parts = token.split('.')
      if (parts.length === 3) {
        const payload = JSON.parse(atob(parts[1]))
        userId = String(payload.user_id || payload.sub || payload.id || '')
      }
    } catch {
      // ignore
    }
  }
  if (!userId) return null
  try {
    const res = await client.get(`/users/${userId}`)
    const raw = extractData<Record<string, unknown> | null>(res, null)
    if (!raw) return null
    return normalizeUser(raw)
  } catch {
    return null
  }
}

export async function getUser(id: string): Promise<Record<string, unknown> | null> {
  try {
    const res = await client.get(`/users/${id}`)
    return extractData<Record<string, unknown>>(res, null)
  } catch {
    return null
  }
}

export async function updateProfile(data: Record<string, unknown>) {
  const res = await client.put('/users', data)
  return extractData(res, null)
}

export async function updateAvatar(file: File) {
  const form = new FormData()
  form.append('avatar', file)
  const res = await client.post('/users/avatar', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  return extractData(res, null)
}

export async function searchUsers(keyword: string) {
  try {
    const res = await client.get('/users/search', { params: { keyword, page: 1, page_size: 20 } })
    const raw = extractArray<Record<string, unknown>>(res)
    return raw.map(normalizeUser)
  } catch {
    return []
  }
}

export async function batchGetUsers(ids: string[]) {
  const res = await client.post('/users/batch', { ids })
  return extractArray(res)
}

export async function checkUsername(username: string) {
  try {
    const res = await client.post('/users/check-username', { username })
    const data = extractData<{ available?: boolean }>(res, { available: true })
    return (data ?? { available: true }).available ?? true
  } catch {
    return true
  }
}
