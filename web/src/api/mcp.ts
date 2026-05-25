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
    if (Array.isArray(inner.services)) return inner.services as T[]
    if (Array.isArray(inner.items)) return inner.items as T[]
    if (Array.isArray(inner.list)) return inner.list as T[]
  }
  return []
}

export interface MCPService {
  id: string
  name: string
  description: string
  enabled: boolean
  transport_type: 'sse' | 'http-streamable'
  url: string
  headers: Record<string, string>
  auth_config: Record<string, string>
  advanced_config: Record<string, string>
  created_at: string
  updated_at: string
}

export interface MCPTool {
  name: string
  description: string
  inputSchema: Record<string, unknown>
  service_id?: string
  service_name?: string
}

export async function listMCPServices(): Promise<MCPService[]> {
  try {
    const res = await client.post('/mcp/services/list')
    return extractArray<MCPService>(res)
  } catch {
    return []
  }
}

export async function getMCPService(id: string): Promise<MCPService | null> {
  try {
    const res = await client.get(`/mcp/services/${id}`)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function createMCPService(data: Partial<MCPService>): Promise<MCPService | null> {
  try {
    const res = await client.post('/mcp/services', data)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function updateMCPService(id: string, data: Partial<MCPService>): Promise<MCPService | null> {
  try {
    const res = await client.put(`/mcp/services/${id}`, data)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function deleteMCPService(id: string): Promise<void> {
  await client.delete(`/mcp/services/${id}`)
}

export async function testMCPService(data: { url: string; transport_type: string; headers?: Record<string, string>; auth_config?: Record<string, string> }): Promise<{ success: boolean; message: string }> {
  try {
    const res = await client.post('/mcp/services/test', data)
    return extractData(res, { success: false, message: '' })
  } catch {
    return { success: false, message: '连接失败' }
  }
}

export async function listMCPTools(): Promise<MCPTool[]> {
  try {
    const res = await client.post('/mcp/list')
    return extractArray<MCPTool>(res)
  } catch {
    return []
  }
}

export async function callMCPTool(data: { tool_id?: string; tool_name: string; parameters: Record<string, string> }): Promise<unknown> {
  try {
    const res = await client.post('/mcp/call', data)
    return extractData(res, null)
  } catch {
    return null
  }
}
