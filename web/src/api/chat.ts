import client from './client'
import type { Message } from '@/types'

export function toMediaUrl(url: string): string {
  if (!url) return ''
  if (url.startsWith('/api/') || url.startsWith('blob:') || url.startsWith('http')) return url
  if (url.startsWith('avatars/') || url.startsWith('group-avatars/')) return '/api/v1/file/minio/' + url
  const chatMediaMatch = url.match(/\/(chat-media\/[^?]+)/)
  if (chatMediaMatch) return '/api/v1/file/minio/' + chatMediaMatch[1]
  const bucketMatch = url.match(/\/(?:logos[^/?]*)\/([^?]+)/)
  if (bucketMatch) return '/api/v1/file/minio/' + bucketMatch[1]
  return url
}

function extractData<T>(res: { data: unknown }, fallback: T): T {
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

const messageTypeMap: Record<string, number> = {
  text: 1,
  image: 2,
  file: 3,
  voice: 4,
  video: 5,
  location: 6,
  system: 7,
}

const messageTypeReverseMap: Record<number, string> = {
  1: 'text',
  2: 'image',
  3: 'file',
  4: 'voice',
  5: 'video',
  6: 'location',
  7: 'system',
}

export function resolveMessageType(raw: unknown): Message['messageType'] {
  if (raw === undefined || raw === null) return 'text'
  if (typeof raw === 'number') {
    return (messageTypeReverseMap[raw] || 'text') as Message['messageType']
  }
  const str = String(raw).toLowerCase().replace('message_type_', '')
  if (str === 'system' || str === '7') return 'system'
  if (str === 'text' || str === '1') return 'text'
  if (str === 'image' || str === '2') return 'image'
  if (str === 'file' || str === '3') return 'file'
  if (str === 'voice' || str === '4') return 'voice'
  if (str === 'video' || str === '5') return 'video'
  if (str === 'location' || str === '6') return 'location'
  return (str || 'text') as Message['messageType']
}

const chatTypeMap: Record<string, number> = {
  private: 1,
  group: 2,
  bot: 1,
  broadcast: 3,
}

// newRequestId 生成发送幂等键：同一 request_id 重复提交（超时重试、用户连点）
// 服务端只会落一条消息，并回放首次结果。
export function newRequestId(): string {
  const randomUUID = (globalThis.crypto as Crypto & { randomUUID?: () => string } | undefined)?.randomUUID
  if (typeof randomUUID === 'function') {
    return randomUUID.call(globalThis.crypto)
  }
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

export async function sendMessage(chatId: string, content: string, messageType = 'text', replyTo?: string, chatType = 'private', requestId: string = newRequestId()) {
  const res = await client.post('/chat/message', {
    chat_id: chatId,
    chat_type: chatTypeMap[chatType] || 1,
    content,
    message_type: messageTypeMap[messageType] || 1,
    reply_to_message_id: replyTo || '',
    request_id: requestId,
  })
  return extractData(res, null)
}

export async function sendMediaMessage(chatId: string, content: string, mediaUrl: string, messageType: string, mediaMeta?: string, chatType = 'private', requestId: string = newRequestId()) {
  const res = await client.post('/chat/media', {
    chat_id: chatId,
    chat_type: chatTypeMap[chatType] || 1,
    content,
    media_url: mediaUrl,
    message_type: messageTypeMap[messageType] || 3,
    media_meta: mediaMeta ? JSON.parse(mediaMeta) : {},
    request_id: requestId,
  })
  return extractData(res, null)
}

export async function uploadChatMedia(chatId: string, file: File, onProgress?: (progress: number) => void): Promise<{ url: string; media_type: string; media_meta: string }> {
  const form = new FormData()
  form.append('file', file)
  form.append('chat_id', chatId)
  try {
    // 模拟进度：先快速到 90%，等上传完成后跳到 100%
    let fakeProgress = 0
    let fakeTimer: ReturnType<typeof setInterval> | null = null
    if (onProgress) {
      onProgress(0)
      fakeTimer = setInterval(() => {
        fakeProgress += Math.random() * 15 + 5
        if (fakeProgress >= 90) { fakeProgress = 90; if (fakeTimer) clearInterval(fakeTimer) }
        onProgress(Math.round(fakeProgress))
      }, 200)
    }

    const cleanup = () => { if (fakeTimer) clearInterval(fakeTimer) }

    const res = await client.post('/chat/upload', form, {
      transformRequest: [(data) => data], // 不让 axios 序列化 FormData
      headers: {}, // 清空 headers，让浏览器自动设置 Content-Type
    })
    cleanup()
    if (onProgress) onProgress(100)

    const data = extractData(res, { url: '', media_type: 'file', media_meta: '' })
    return {
      url: data.url || '',
      media_type: data.media_type || 'file',
      media_meta: typeof data.media_meta === 'string' ? data.media_meta : JSON.stringify(data.media_meta || {}),
    }
  } catch {
    return { url: '', media_type: 'file', media_meta: '' }
  }
}

export async function getChatHistory(chatId: string, limit = 50, before?: string, afterSeq?: number) {
  try {
    const params: Record<string, unknown> = { chat_id: chatId, limit, before }
    // after_seq > 0 时走增量补拉：只取 seq 大于该游标的消息（升序），用于 WS 重连后补齐缺口
    if (afterSeq && afterSeq > 0) params.after_seq = afterSeq
    const res = await client.get('/chat/history', { params })
    return extractArray<Record<string, unknown>>(res)
  } catch {
    return []
  }
}

// toRFC3339 把 <input type="date"> 的日期（YYYY-MM-DD）补成后端要求的 RFC3339 时间。
// 结束日期取当天 23:59:59，避免把结束当天的消息排除在外。
function toRFC3339(value?: string, endOfDay = false): string {
  if (!value) return ''
  const dateOnly = /^\d{4}-\d{2}-\d{2}$/.test(value)
  const d = new Date(dateOnly ? `${value}T${endOfDay ? '23:59:59.999' : '00:00:00'}` : value)
  if (Number.isNaN(d.getTime())) return ''
  return d.toISOString()
}

export async function searchChatMessages(chatId: string, keyword: string, startTime?: string, endTime?: string) {
  try {
    const res = await client.post('/chat/search', {
      chat_id: chatId,
      keyword,
      start_time: toRFC3339(startTime),
      end_time: toRFC3339(endTime, true),
    })
    return extractArray(res)
  } catch {
    return []
  }
}

export async function translateMessage(content: string, targetLang = 'zh', messageId?: string, modelConfig?: { provider?: string; model?: string; apiKey?: string; baseUrl?: string }): Promise<string> {
  try {
    const body: Record<string, unknown> = {
      content,
      target_lang: targetLang,
      message_id: messageId || '',
    }
    if (modelConfig && modelConfig.apiKey) {
      body.model_config = {
        provider: modelConfig.provider || 'openai',
        model: modelConfig.model || 'gpt-4o-mini',
        api_key: modelConfig.apiKey,
        base_url: modelConfig.baseUrl || '',
      }
    }
    const res = await client.post('/chat/translate', body)
    const data = extractData<{ translated_content?: string; translated?: string }>(res, {})
    return data.translated_content || data.translated || ''
  } catch {
    return ''
  }
}

export async function markMessagesRead(chatId: string, chatType = 1): Promise<void> {
  try {
    await client.post('/chat/mark-read', { chat_id: chatId, chat_type: chatType })
  } catch {
    // ignore
  }
}

// ChatSyncChange 原位变更（编辑/撤回）：消息 seq 不变、内容被改写，
// 因此不能靠 lastSeq 补拉，必须用独立的 updatedSeq 游标。
export interface ChatSyncChange {
  messageId: string
  chatId: string
  // edit / withdraw
  changeType: string
  content: string
  // 变更后的消息版本，客户端只在比本地新时应用，乱序/重复不会回退内容
  version: number
  // 被变更消息的会话内 seq，用于跳过本人已清空的历史
  messageSeq: number
  // 变更日志自增 ID，即 updated_seq
  updatedSeq: number
  changedAt: number
}

export interface ChatSyncItem {
  chatId: string
  messages: Record<string, unknown>[]
  latestSeq: number
  clearedSeq: number
  hasMore: boolean
  // 自 deletedSeq 游标以来被本人删除的消息 ID（删除无法由消息 seq 表达，必须独立游标）
  deletedMessageIds: string[]
  deletedSeq: number
  deletedHasMore: boolean
  // 自 updatedSeq 游标以来发生的编辑/撤回（同样不改变消息 seq）
  changes: ChatSyncChange[]
  updatedSeq: number
  updatedHasMore: boolean
}

// syncChatMessages 断线重连后批量补齐增量：每个会话带三个独立游标
// （lastSeq 表达新增、deletedSeq 表达删除、updatedSeq 表达编辑/撤回），
// 服务端一次请求返回多个会话的增量。
export async function syncChatMessages(
  cursors: { chatId: string; lastSeq: number; deletedSeq?: number; updatedSeq?: number }[]
): Promise<ChatSyncItem[]> {
  if (cursors.length === 0) return []
  try {
    const res = await client.post('/chat/sync', {
      cursors: cursors.slice(0, 100).map((c) => ({
        chat_id: c.chatId,
        last_seq: c.lastSeq,
        deleted_seq: c.deletedSeq || 0,
        updated_seq: c.updatedSeq || 0,
      })),
      limit: 100,
    })
    const chats = ((res.data as { data?: { chats?: any[] } })?.data?.chats) || []
    return chats.map((c) => ({
      chatId: String(c.chat_id || c.chatId || ''),
      messages: Array.isArray(c.messages) ? c.messages : [],
      latestSeq: Number(c.latest_seq ?? c.latestSeq ?? 0),
      clearedSeq: Number(c.cleared_seq ?? c.clearedSeq ?? 0),
      hasMore: Boolean(c.has_more ?? c.hasMore),
      deletedMessageIds: Array.isArray(c.deleted_message_ids) ? c.deleted_message_ids.map(String) : [],
      deletedSeq: Number(c.deleted_seq ?? c.deletedSeq ?? 0),
      deletedHasMore: Boolean(c.deleted_has_more ?? c.deletedHasMore),
      changes: (Array.isArray(c.changes) ? c.changes : []).map((ch: Record<string, unknown>) => ({
        messageId: String(ch.message_id ?? ch.messageId ?? ''),
        chatId: String(ch.chat_id ?? ch.chatId ?? ''),
        changeType: String(ch.change_type ?? ch.changeType ?? ''),
        content: String(ch.content ?? ''),
        version: Number(ch.version ?? 0),
        messageSeq: Number(ch.message_seq ?? ch.messageSeq ?? 0),
        updatedSeq: Number(ch.updated_seq ?? ch.updatedSeq ?? 0),
        changedAt: Number(ch.changed_at ?? ch.changedAt ?? 0),
      })).filter((ch: ChatSyncChange) => ch.messageId),
      updatedSeq: Number(c.updated_seq ?? c.updatedSeq ?? 0),
      updatedHasMore: Boolean(c.updated_has_more ?? c.updatedHasMore),
    })).filter((c) => c.chatId)
  } catch (err) {
    // 不吞错误：断线重连后的批量补拉是全链路可靠性唯一兜底，调用方需要重试。
    throw err
  }
}

export interface MessageReadReceipt {
  messageId: string
  seq: number
  readCount: number
  memberCount: number
  allRead: boolean
}

// getMessageReadReceipts 查询自己发送的消息的已读回执（服务端按已读位点聚合）。
// 单次最多 50 条，超出部分由服务端拒绝，因此在调用侧截断。
export async function getMessageReadReceipts(chatId: string, messageIds: string[]): Promise<Record<string, MessageReadReceipt>> {
  if (!chatId || messageIds.length === 0) return {}
  const ids = messageIds.filter((id) => id && !id.startsWith('local-')).slice(0, 50)
  if (ids.length === 0) return {}
  try {
    const res = await client.post('/chat/read-receipts', { chat_id: chatId, message_ids: ids })
    const raw = (res.data as { data?: { receipts?: any[] } })?.data?.receipts || []
    const result: Record<string, MessageReadReceipt> = {}
    for (const item of raw) {
      const messageId: string = item.message_id || item.messageId || ''
      if (!messageId) continue
      result[messageId] = {
        messageId,
        seq: Number(item.seq || 0),
        readCount: Number(item.read_count ?? item.readCount ?? 0),
        memberCount: Number(item.member_count ?? item.memberCount ?? 0),
        allRead: Boolean(item.all_read ?? item.allRead),
      }
    }
    return result
  } catch {
    return {}
  }
}

export async function withdrawMessage(messageId: string): Promise<void> {
  await client.post('/chat/withdraw', { message_id: messageId })
}

// deleteMessage 删除单条消息：只隐藏本人视角，不撤回、不影响对方/其他群成员。
// 服务端幂等，重复调用不会报错；其他设备通过 /chat/sync 的 deleted_seq 游标同步该删除。
export async function deleteMessage(messageId: string): Promise<void> {
  await client.post('/chat/delete-message', { message_id: messageId })
}

// editMessage 编辑消息：version 为客户端当前持有的消息版本（乐观锁）。
// 服务端版本不一致时返回 409，调用方应刷新最新内容后再次提交，避免覆盖他人编辑。
export async function editMessage(messageId: string, content: string, version = 0): Promise<void> {
  await client.put('/chat/edit', { message_id: messageId, content, version })
}

export async function getConversationList(page = 1, pageSize = 100) {
  try {
    const res = await client.get("/chat/conversations", {
      params: { page, page_size: pageSize },
    });
    const data = extractData<{ conversations?: any[] }>(res, { conversations: [] });
    return data.conversations || [];
  } catch {
    return [];
  }
}

export async function forwardMessage(messageId: string, targetChatIds: string[], targetChatType = 1) {
  const res = await client.post("/chat/forward", {
    message_id: messageId,
    target_chat_ids: targetChatIds,
    target_chat_type: targetChatType,
  });
  return extractData(res, null);
}

export async function deleteChat(chatId: string, chatType = 1): Promise<void> {
  await client.post("/chat/delete", { chat_id: chatId, chat_type: chatType });
}

export async function deleteChatHistory(chatId: string): Promise<void> {
  await client.post("/chat/delete-history", { chat_id: chatId });
}
