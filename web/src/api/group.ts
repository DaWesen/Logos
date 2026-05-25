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
    if (Array.isArray(inner.members)) return inner.members as T[]
    if (Array.isArray(inner.items)) return inner.items as T[]
    if (Array.isArray(inner.list)) return inner.list as T[]
  }
  return []
}

export interface ChatGroup {
  id: string
  name: string
  avatar: string
  description: string
  announcement: string
  owner_id: string
  member_ids: string[]
  member_count: number
  created_at: string
}

function mapGroup(raw: Record<string, unknown>): ChatGroup {
  const memberIds = Array.isArray(raw.member_ids) ? raw.member_ids as string[] : []
  return {
    id: String(raw.id || ''),
    name: String(raw.name || ''),
    avatar: String(raw.avatar || ''),
    description: String(raw.description || ''),
    announcement: String(raw.announcement || ''),
    owner_id: String(raw.owner_id || ''),
    member_ids: memberIds,
    member_count: memberIds.length || Number(raw.member_count) || 0,
    created_at: String(raw.created_at || ''),
  }
}

const roleMap: Record<number, 'owner' | 'admin' | 'member'> = {
  1: 'owner',
  2: 'admin',
  3: 'member',
}

export interface RawGroupMember {
  user_id?: string
  username?: string
  avatar?: string
  role?: number | string
  mute_type?: number
  joined_at?: string
}

function mapMember(raw: RawGroupMember): { userId: string; username: string; avatar: string; role: 'owner' | 'admin' | 'member'; joinedAt: string } {
  let role: 'owner' | 'admin' | 'member' = 'member'
  if (typeof raw.role === 'number') {
    role = roleMap[raw.role] || 'member'
  } else if (typeof raw.role === 'string') {
    const lower = raw.role.toLowerCase()
    if (lower === 'owner' || lower === '1') role = 'owner'
    else if (lower === 'admin' || lower === '2') role = 'admin'
  }
  return {
    userId: String(raw.user_id || ''),
    username: String(raw.username || ''),
    avatar: String(raw.avatar || ''),
    role,
    joinedAt: String(raw.joined_at || ''),
  }
}

export async function createGroup(name: string, description?: string): Promise<ChatGroup> {
  const res = await client.post('/chat/group', { name, description })
  const d = res.data as Record<string, unknown>
  if (d && d.code && d.code !== 200) {
    throw new Error(String(d.message || '创建群组失败'))
  }
  const raw = extractData<Record<string, unknown>>(res, null)
  if (!raw) throw new Error('服务器未返回有效数据')
  return mapGroup(raw)
}

export async function getGroup(groupId: string): Promise<ChatGroup | null> {
  try {
    const res = await client.get('/chat/group', {
      params: { group_id: groupId },
    })
    const raw = extractData<Record<string, unknown>>(res, null)
    return raw ? mapGroup(raw) : null
  } catch {
    return null
  }
}

export async function getGroupMembers(groupId: string) {
  try {
    const res = await client.get('/chat/group/members', {
      params: { group_id: groupId },
    })
    const rawMembers = extractArray<RawGroupMember>(res)
    return rawMembers.map(mapMember)
  } catch {
    return []
  }
}

export async function inviteGroupMember(groupId: string, userIds: string[]): Promise<void> {
  await client.post('/chat/group/invite', { group_id: groupId, user_ids: userIds })
}

export async function kickGroupMember(groupId: string, userId: string): Promise<void> {
  await client.post('/chat/group/kick', { group_id: groupId, user_id: userId })
}

export async function muteGroupMember(groupId: string, userId: string, duration = 0): Promise<void> {
  await client.put('/chat/group/mute', { group_id: groupId, user_id: userId, duration })
}

export async function transferGroupOwner(groupId: string, newOwnerId: string): Promise<void> {
  await client.put('/chat/group/owner', { group_id: groupId, new_owner_id: newOwnerId })
}

export async function updateGroupAnnouncement(groupId: string, announcement: string): Promise<void> {
  await client.post('/chat/group/announcement', { group_id: groupId, announcement })
}

export async function setGroupAdmin(groupId: string, userId: string, isAdmin: boolean): Promise<void> {
  await client.put('/chat/group/admin', { group_id: groupId, user_id: userId, is_admin: isAdmin })
}

export async function inviteBotToGroup(groupId: string, botId: string): Promise<void> {
  await client.post('/chat/group/invite', { group_id: groupId, user_ids: [`bot_${botId}`] })
}

export async function joinGroup(groupId: string): Promise<void> {
  await client.post('/chat/group/join', { group_id: groupId })
}

export async function leaveGroup(groupId: string): Promise<void> {
  await client.post('/chat/group/leave', { group_id: groupId })
}

export async function updateGroupAvatar(groupId: string, file: File): Promise<string | null> {
  const form = new FormData()
  form.append('group_id', groupId)
  form.append('avatar', file)
  const res = await client.post('/chat/group/avatar', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
  const d = res.data as Record<string, unknown>
  if (d && d.data) {
    const inner = d.data as Record<string, unknown>
    return String(inner.url || '')
  }
  return null
}
