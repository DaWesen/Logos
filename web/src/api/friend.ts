import client from './client'

function extractData<T>(res: { data: unknown }, fallback: T): T {
  const d = res.data as Record<string, unknown>
  if (!d) return fallback
  if (d.data !== undefined && d.data !== null) return d.data as T
  if (typeof d === 'object' && !Array.isArray(d) && !('code' in d)) return d as T
  return fallback
}

function extractArray<T>(res: { data: unknown }, key: string = 'data'): T[] {
  const d = res.data as Record<string, unknown>
  if (!d) return []
  if (Array.isArray((d as any)[key])) return (d as any)[key] as T[]
  if (Array.isArray(d.data)) return d.data as T[]
  if (Array.isArray(d)) return d as T[]
  return []
}

export async function searchUsers(keyword: string) {
  try {
    const res = await client.get('/users/search', { params: { keyword, page: 1, page_size: 20 } })
    return extractArray<{ id: string; username: string; nickname: string; avatar: string; email: string; status: string }>(res)
  } catch {
    return []
  }
}

export async function getUser(id: string) {
  try {
    const res = await client.get(`/users/${id}`)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function getOnlineStatus(userIds: string[]): Promise<Record<string, string>> {
  try {
    const res = await client.get('/im/online-status', { params: { user_ids: userIds.join(',') } })
    return extractData(res, {})
  } catch {
    return {}
  }
}

export async function setOnlineStatus(status: string): Promise<void> {
  try {
    await client.put('/im/online-status', { status })
  } catch {
  }
}

export async function addFriend(userId: string, remark: string = '', message: string = '') {
  const res = await client.post('/contact/add', { user_id: userId, remark, message })
  return res.data
}

export async function handleFriendRequest(requestId: string, status: 'accepted' | 'rejected') {
  const res = await client.post('/contact/handle', { request_id: requestId, status })
  return res.data
}

export async function getFriendRequests() {
  try {
    const res = await client.get('/contact/requests')
    return extractArray<any>(res, 'requests')
  } catch {
    return []
  }
}

export async function getFriendList() {
  try {
    const res = await client.get('/contact/list')
    return extractArray<any>(res, 'friends')
  } catch {
    return []
  }
}

export async function deleteFriend(friendId: string) {
  const res = await client.delete('/contact/delete', { data: { friend_id: friendId } })
  return res.data
}

export async function checkFriendship(friendId: string): Promise<{ is_friend: boolean; is_blocked: boolean }> {
  try {
    const res = await client.get('/contact/check', { params: { friend_id: friendId } })
    return extractData(res, { is_friend: false, is_blocked: false })
  } catch {
    return { is_friend: false, is_blocked: false }
  }
}

export async function updateFriendRemark(friendId: string, remark: string) {
  const res = await client.put('/contact/remark', { friend_id: friendId, remark })
  return res.data
}

export async function getFriendGroups() {
  try {
    const res = await client.get('/contact/groups')
    return extractArray<any>(res, 'groups')
  } catch {
    return []
  }
}

export async function createFriendGroup(name: string, sortOrder: number = 0) {
  const res = await client.post('/contact/group', { name, sort_order: sortOrder })
  return res.data
}

export async function updateFriendGroup(groupId: string, name: string, sortOrder: number) {
  const res = await client.put('/contact/group', { group_id: groupId, name, sort_order: sortOrder })
  return res.data
}

export async function deleteFriendGroup(groupId: string) {
  const res = await client.delete('/contact/group', { data: { group_id: groupId } })
  return res.data
}

export async function moveFriendToGroup(friendId: string, groupId: string) {
  const res = await client.post('/contact/group/move', { friend_id: friendId, group_id: groupId })
  return res.data
}

export async function blockUser(userId: string) {
  const res = await client.post('/contact/block', { user_id: userId })
  return res.data
}

export async function unblockUser(userId: string) {
  const res = await client.delete('/contact/block', { data: { user_id: userId } })
  return res.data
}

export async function getBlacklist() {
  try {
    const res = await client.get('/contact/blacklist')
    return extractArray<any>(res, 'records')
  } catch {
    return []
  }
}
