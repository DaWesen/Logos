import client from './client'

function extractData<T>(res: { data: unknown }, fallback: T): T {
  const d = res.data as Record<string, unknown>
  if (!d) return fallback
  if (d.success === true && d.data !== undefined) return d.data as T
  if (d.data !== undefined && d.data !== null) return d.data as T
  if (typeof d === 'object' && !Array.isArray(d) && !('code' in d) && !('success' in d)) return d as T
  return fallback
}

function extractArray<T>(res: { data: unknown }): T[] {
  const d = res.data as Record<string, unknown>
  if (!d) return []
  if (d.success === true && Array.isArray(d.data)) return d.data as T[]
  if (Array.isArray(d.data)) return d.data as T[]
  if (Array.isArray(d)) return d as T[]
  if (d.data && typeof d.data === 'object') {
    const inner = d.data as Record<string, unknown>
    if (Array.isArray(inner.collections)) return inner.collections as T[]
    if (Array.isArray(inner.items)) return inner.items as T[]
    if (Array.isArray(inner.documents)) return inner.documents as T[]
    if (Array.isArray(inner.list)) return inner.list as T[]
  }
  return []
}

export interface ProcessDocument {
  id: string
  vector_collection_id?: string
  name: string
  source_type: string
  source_url: string
  status: string
  chunk_count: number
  created_at: string
  updated_at: string
}

export interface VectorCollection {
  id: string
  name: string
  model_type: number
  index_type: number
  dimension: number
  size: number
  parameters: Record<string, string>
  vlm_model?: string
  vlm_base_url?: string
  vlm_api_key?: string
  llm_model?: string
  llm_base_url?: string
  llm_api_key?: string
  asr_model?: string
  asr_base_url?: string
  asr_api_key?: string
  embedding_model?: string
  embedding_base_url?: string
  embedding_api_key?: string
  created_at: string
  updated_at: string
}

export async function listDocuments(page = 1, pageSize = 20): Promise<{ items: ProcessDocument[]; total: number }> {
  try {
    const res = await client.get('/process/documents', {
      params: { page, page_size: pageSize },
    })
    const d = res.data as Record<string, unknown>
    if (!d) return { items: [], total: 0 }
    if (d.success === true) {
      const data = d.data
      if (Array.isArray(data)) return { items: data as ProcessDocument[], total: (d.total as number) || data.length }
      if (data && typeof data === 'object') {
        const inner = data as Record<string, unknown>
        return {
          items: (inner.items || inner.documents || []) as ProcessDocument[],
          total: (inner.total as number) || (d.total as number) || 0,
        }
      }
    }
    const data = extractData<Record<string, unknown>>(res, {})
    if (Array.isArray(data)) return { items: data as ProcessDocument[], total: data.length }
    const inner = data as Record<string, unknown>
    return {
      items: (inner.items || inner.documents || []) as ProcessDocument[],
      total: (inner.total as number) || 0,
    }
  } catch {
    return { items: [], total: 0 }
  }
}

export async function getDocument(id: string): Promise<ProcessDocument | null> {
  try {
    const res = await client.get(`/process/documents/${id}`)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function processFile(file: File, collectionId: string): Promise<ProcessDocument | null> {
  try {
    const form = new FormData()
    form.append('file', file)
    if (collectionId) form.append('collection_id', collectionId)
    const res = await client.post('/process/file', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function processUrl(url: string, collectionId: string): Promise<ProcessDocument | null> {
  try {
    const res = await client.post('/process/url', { url, collection_id: collectionId })
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function deleteDocument(id: string): Promise<void> {
  await client.delete(`/process/documents/${id}`)
}

export async function reprocessDocument(id: string): Promise<ProcessDocument | null> {
  try {
    const res = await client.post(`/process/documents/${id}/reprocess`)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function getDocumentChunks(id: string) {
  try {
    const res = await client.get(`/process/documents/${id}/chunks`)
    return extractArray(res)
  } catch {
    return []
  }
}

export async function vectorSearch(collectionId: string, query: string, topK = 5) {
  try {
    const res = await client.post('/vector/text-search', {
      collection_id: collectionId,
      text: query,
      top_k: topK,
    })
    return extractData(res, { ids: [], scores: [] })
  } catch {
    return { ids: [], scores: [] }
  }
}

export async function listCollections(): Promise<VectorCollection[]> {
  try {
    const res = await client.get('/vector/collections')
    return extractArray<VectorCollection>(res)
  } catch {
    return []
  }
}

export async function createCollection(
  name: string,
  modelType = 4,
  indexType = 3,
  dimension = 768,
  parameters?: Record<string, string>,
  modelConfigs?: {
    vlm_model?: string
    vlm_base_url?: string
    vlm_api_key?: string
    llm_model?: string
    llm_base_url?: string
    llm_api_key?: string
    asr_model?: string
    asr_base_url?: string
    asr_api_key?: string
    embedding_model?: string
    embedding_base_url?: string
    embedding_api_key?: string
  },
) {
  try {
    const res = await client.post('/vector/collections', {
      name,
      model_type: modelType,
      index_type: indexType,
      dimension,
      parameters: parameters || {},
      vlm_model: modelConfigs?.vlm_model || '',
      vlm_base_url: modelConfigs?.vlm_base_url || '',
      vlm_api_key: modelConfigs?.vlm_api_key || '',
      llm_model: modelConfigs?.llm_model || '',
      llm_base_url: modelConfigs?.llm_base_url || '',
      llm_api_key: modelConfigs?.llm_api_key || '',
      asr_model: modelConfigs?.asr_model || '',
      asr_base_url: modelConfigs?.asr_base_url || '',
      asr_api_key: modelConfigs?.asr_api_key || '',
      embedding_model: modelConfigs?.embedding_model || '',
      embedding_base_url: modelConfigs?.embedding_base_url || '',
      embedding_api_key: modelConfigs?.embedding_api_key || '',
    })
    return extractData<VectorCollection | null>(res, null)
  } catch {
    return null
  }
}

export async function deleteCollection(id: string): Promise<void> {
  await client.delete(`/vector/collections/${id}`)
}

export interface VectorPreviewItem {
  id: string
  content: string
  metadata: Record<string, string>
  created_at: string
}

export async function listVectors(
  collectionId: string,
  page = 1,
  pageSize = 20,
): Promise<{ vectors: VectorPreviewItem[]; total: number; page: number; page_size: number }> {
  const res = await client.get(`/vector/collections/${collectionId}/vectors`, {
    params: { page, page_size: pageSize },
  })
  const d = res.data as Record<string, unknown>
  if (!d) return { vectors: [], total: 0, page, page_size: pageSize }
  if (d.code && d.code !== 200) {
    throw new Error((d.message as string) || '未知错误')
  }
  const data = d.data as Record<string, unknown> | undefined
  if (data) {
    return {
      vectors: (data.vectors as VectorPreviewItem[]) || [],
      total: (data.total as number) || 0,
      page: (data.page as number) || page,
      page_size: (data.page_size as number) || pageSize,
    }
  }
  return { vectors: [], total: 0, page, page_size: pageSize }
}
