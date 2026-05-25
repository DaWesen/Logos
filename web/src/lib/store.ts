const PREFIX = 'aim_'

const UNSCOPED_KEYS = new Set(['token', 'user'])

function getUserId(): string {
  try {
    const raw = localStorage.getItem(PREFIX + 'user')
    if (raw) {
      const u = JSON.parse(raw)
      return u?.id ? String(u.id) : ''
    }
  } catch { /* */ }
  return ''
}

function scopedKey(key: string): string {
  if (UNSCOPED_KEYS.has(key)) return PREFIX + key
  const uid = getUserId()
  if (uid) return `${PREFIX}${uid}_${key}`
  return PREFIX + key
}

export function load<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(scopedKey(key))
    if (raw) return JSON.parse(raw)
  } catch {
    // ignore
  }
  return fallback
}

export function save(key: string, value: unknown): void {
  try {
    localStorage.setItem(scopedKey(key), JSON.stringify(value))
  } catch {
    // ignore
  }
}

export function remove(key: string): void {
  try {
    localStorage.removeItem(scopedKey(key))
  } catch {
    // ignore
  }
}
