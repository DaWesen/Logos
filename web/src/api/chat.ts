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

export async function sendMessage(chatId: string, content: string, messageType = 'text', replyTo?: string, chatType = 'private') {
  const res = await client.post('/chat/message', {
    chat_id: chatId,
    chat_type: chatTypeMap[chatType] || 1,
    content,
    message_type: messageTypeMap[messageType] || 1,
    reply_to_message_id: replyTo || '',
  })
  return extractData(res, null)
}

export async function sendMediaMessage(chatId: string, content: string, mediaUrl: string, messageType: string, mediaMeta?: string, chatType = 'private') {
  const res = await client.post('/chat/media', {
    chat_id: chatId,
    chat_type: chatTypeMap[chatType] || 1,
    content,
    media_url: mediaUrl,
    message_type: messageTypeMap[messageType] || 3,
    media_meta: mediaMeta ? JSON.parse(mediaMeta) : {},
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

export async function getChatHistory(chatId: string, limit = 50, before?: string) {
  try {
    const res = await client.get('/chat/history', {
      params: { chat_id: chatId, limit, before },
    })
    return extractArray<Record<string, unknown>>(res)
  } catch {
    return []
  }
}

export async function searchChatMessages(chatId: string, query: string, startTime?: string, endTime?: string) {
  try {
    const res = await client.post('/chat/search', {
      chat_id: chatId,
      query,
      start_time: startTime || '',
      end_time: endTime || '',
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

export async function withdrawMessage(messageId: string): Promise<void> {
  await client.post('/chat/withdraw', { message_id: messageId })
}

export async function editMessage(messageId: string, content: string): Promise<void> {
  await client.put('/chat/edit', { message_id: messageId, content })
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
