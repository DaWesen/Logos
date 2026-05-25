import client from './client'

interface BotItem {
  id: string
  name: string
  avatar: string
  description: string
  systemPrompt: string
  provider: string
  model: string
  baseUrl: string
  apiKey: string
  embeddingModel: string
  embeddingBaseUrl: string
  embeddingApiKey: string
  temperature: number
  enableMemory: boolean
  enableRag: boolean
  enableGraph: boolean
  autoSaveToKb: boolean
  knowledgeBaseIds: string[]
  createdAt: string
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
    if (Array.isArray(inner.bots)) return inner.bots as T[]
    if (Array.isArray(inner.items)) return inner.items as T[]
    if (Array.isArray(inner.list)) return inner.list as T[]
  }
  return []
}

export type { BotItem }

export async function getBotList(): Promise<BotItem[]> {
  try {
    const res = await client.get('/bot')
    return extractArray<BotItem>(res)
  } catch {
    return []
  }
}

export async function getBot(id: string): Promise<BotItem | null> {
  try {
    const res = await client.get(`/bot/${id}`)
    return extractData(res, null)
  } catch {
    return null
  }
}

const providerMap: Record<string, number> = {
  openai: 1,
  claude: 2,
  anthropic: 2,
  qianfan: 3,
  deepseek: 1,
  chatglm: 1,
  platform: 4,
}

function mapProvider(value?: string): number | undefined {
  if (!value) return undefined
  return providerMap[value] ?? undefined
}

export async function createBot(data: Partial<BotItem>): Promise<BotItem | null> {
  try {
    const res = await client.post('/bot', {
      name: data.name,
      description: data.description,
      avatar: data.avatar,
      type: 2,
      provider: mapProvider(data.provider),
      model: data.model,
      api_key: data.apiKey,
      base_url: data.baseUrl,
      embedding_model: data.embeddingModel,
      system_prompt: data.systemPrompt,
      config: {
        temperature: String(data.temperature || 0.7),
        enable_memory: String(data.enableMemory || false),
        enable_rag: String(data.enableRag || false),
        enable_graph: String(data.enableGraph || false),
        auto_save_to_kb: String(data.autoSaveToKb || false),
        collection_ids: (data.knowledgeBaseIds || []).join(','),
        embedding_base_url: data.embeddingBaseUrl || '',
        embedding_api_key: data.embeddingApiKey || '',
      },
    })
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function updateBot(id: string, data: Partial<BotItem>): Promise<BotItem | null> {
  try {
    const res = await client.put(`/bot/${id}`, {
      name: data.name,
      description: data.description,
      avatar: data.avatar,
      provider: mapProvider(data.provider),
      model: data.model,
      api_key: data.apiKey,
      base_url: data.baseUrl,
      embedding_model: data.embeddingModel,
      system_prompt: data.systemPrompt,
      config: {
        temperature: String(data.temperature || 0.7),
        enable_memory: String(data.enableMemory || false),
        enable_rag: String(data.enableRag || false),
        enable_graph: String(data.enableGraph || false),
        auto_save_to_kb: String(data.autoSaveToKb || false),
        collection_ids: (data.knowledgeBaseIds || []).join(','),
        embedding_base_url: data.embeddingBaseUrl || '',
        embedding_api_key: data.embeddingApiKey || '',
      },
    })
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function deleteBot(id: string): Promise<void> {
  await client.delete(`/bot/${id}`)
}

export async function sendBotMessage(botId: string, content: string, chatId?: string, autoSaveToKb?: boolean): Promise<{ content: string; chatId?: string; error?: string; cost?: number; tokens?: number }> {
  try {
    console.log('📡 发送请求到 /bot/message', { botId, content, chatId, autoSaveToKb })
    const res = await client.post('/bot/message', {
      bot_id: botId,
      content,
      chat_id: chatId,
      metadata: autoSaveToKb ? { auto_save_to_kb: 'true' } : {},
    })
    console.log('📡 /bot/message 响应:', res)
    const data = extractData<{ content?: string; chat_id?: string; cost?: number; tokens?: number }>(res, { content: '' })
    console.log('📡 extractData 结果:', data)
    return {
      content: data.content || '',
      chatId: data.chat_id,
      cost: data.cost,
      tokens: data.tokens,
    }
  } catch (err: unknown) {
    console.error('❌ sendBotMessage 出错:', err)
    const message = err instanceof Error ? err.message : 'Bot 响应失败'
    return { content: '', error: message }
  }
}

export async function getBotHistory(botId: string, limit = 50) {
  try {
    const res = await client.get('/bot/history', {
      params: { bot_id: botId, limit },
    })
    const d = res.data as Record<string, unknown>
    if (!d) return []
    if (d.data && typeof d.data === 'object') {
      const inner = d.data as Record<string, unknown>
      if (Array.isArray(inner.messages)) return inner.messages as Record<string, unknown>[]
    }
    if (Array.isArray(d.data)) return d.data as Record<string, unknown>[]
    return []
  } catch {
    return []
  }
}

export async function getBillingAccount() {
  const res = await client.get('/billing/account')
  const d = res.data as Record<string, unknown>
  if (!d) return null
  if (d.data !== undefined && d.data !== null) return d.data as { balance: number }
  if (typeof d === 'object' && !Array.isArray(d) && !('code' in d)) return d as { balance: number }
  return null
}

export interface UserMemory {
  id: string
  user_id: string
  bot_id: string
  key: string
  value: string
  category: string
  source: string
  confidence: number
  created_at: string
  updated_at: string
}

export async function getUserMemory(botId: string): Promise<UserMemory[]> {
  try {
    const res = await client.get('/bot/memory', { params: { bot_id: botId } })
    const d = res.data as Record<string, unknown>
    if (!d) return []
    if (Array.isArray(d.data)) return d.data as UserMemory[]
    return []
  } catch {
    return []
  }
}

export async function setUserMemory(botId: string, key: string, value: string, category?: string): Promise<void> {
  await client.post('/bot/memory', {
    bot_id: botId,
    key,
    value,
    category: category || 'fact',
  })
}

export async function deleteUserMemory(botId: string, key: string): Promise<void> {
  await client.delete('/bot/memory', { params: { bot_id: botId, key } })
}
