import client from './client'

function extractData<T>(res: { data: unknown }, fallback: T): T {
  const d = res.data as Record<string, unknown>
  if (!d) return fallback
  if (d.data !== undefined && d.data !== null) return d.data as T
  if (d.success === true && d.data !== undefined) return d.data as T
  if (typeof d === 'object' && !Array.isArray(d) && !('data' in d)) return d as T
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
    if (Array.isArray(inner.entites)) return inner.entites as T[]
    if (Array.isArray(inner.entities)) return inner.entities as T[]
    if (Array.isArray(inner.relations)) return inner.relations as T[]
    if (Array.isArray(inner.items)) return inner.items as T[]
  }
  return []
}

export interface GraphEntity {
  id: string
  type: string
  name: string
  properties: Record<string, string>
  description?: string
  collection_id?: string
  color?: string
  created_at: string
  updated_at: string
}

export interface GraphRelation {
  id: string
  type: string
  source_id: string
  target_id: string
  properties: Record<string, string>
  description?: string
  collection_id?: string
  created_at: string
  updated_at: string
}

export interface GraphStats {
  entity_count: number
  relation_count: number
  entity_type_count: Record<string, number>
  relation_type_count: Record<string, number>
}

export interface GraphNode {
  id: string
  label: string
  type: string
  properties: Record<string, string>
  collection_id?: string
  color?: string
  x?: number
  y?: number
}

export interface GraphEdge {
  id: string
  source: string
  target: string
  label: string
  properties: Record<string, string>
}

export interface GraphData {
  nodes: GraphNode[]
  edges: GraphEdge[]
}

export interface SubgraphData {
  nodes: GraphEntity[]
  edges: GraphRelation[]
  nodeCount: number
  edgeCount: number
}

export interface EntityPath {
  entities: GraphEntity[]
  relations: GraphRelation[]
  length: number
}

export async function getGraphStats(collectionId?: string): Promise<GraphStats | null> {
  try {
    const params: Record<string, unknown> = {}
    if (collectionId) params.collectionId = collectionId
    const res = await client.get('/knowledge/stats', { params })
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function searchEntities(keyword: string, type?: string, collectionId?: string, page = 1, pageSize = 20): Promise<GraphEntity[]> {
  try {
    const params: Record<string, unknown> = { keyword, page, pageSize: pageSize }
    if (type) params.type = type
    if (collectionId) params.collectionId = collectionId
    const res = await client.get('/knowledge/entities/search', { params })
    return extractArray<GraphEntity>(res)
  } catch {
    return []
  }
}

export async function listEntities(type?: string, collectionId?: string, page = 1, pageSize = 20): Promise<GraphEntity[]> {
  try {
    const params: Record<string, unknown> = { page, pageSize: pageSize }
    if (type) params.type = type
    if (collectionId) params.collectionId = collectionId
    const res = await client.get('/knowledge/entities', { params })
    return extractArray<GraphEntity>(res)
  } catch {
    return []
  }
}

export async function getEntity(id: string): Promise<GraphEntity | null> {
  try {
    const res = await client.get(`/knowledge/entities/${id}`)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function createEntity(data: {
  type: string
  name: string
  properties?: Record<string, string>
  description?: string
  collection_id?: string
}): Promise<GraphEntity | null> {
  try {
    const res = await client.post('/knowledge/entities', data)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function updateEntity(
  id: string,
  data: {
    type?: string
    name?: string
    properties?: Record<string, string>
    description?: string
  }
): Promise<GraphEntity | null> {
  try {
    const res = await client.put(`/knowledge/entities/${id}`, data)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function deleteEntity(id: string): Promise<void> {
  await client.delete(`/knowledge/entities/${id}`)
}

export async function clearEntities(collectionId?: string): Promise<{ deleted_count: number } | null> {
  try {
    const params: Record<string, unknown> = {}
    if (collectionId) params.collectionId = collectionId
    const res = await client.delete('/knowledge/entities/clear', { params })
    const d = res.data as Record<string, unknown>
    if (d && d.data) return d.data as { deleted_count: number }
    return null
  } catch {
    return null
  }
}

export async function listRelations(
  type?: string,
  sourceId?: string,
  targetId?: string,
  collectionId?: string,
  page = 1,
  pageSize = 20
): Promise<GraphRelation[]> {
  try {
    const params: Record<string, unknown> = { page, pageSize: pageSize }
    if (type) params.type = type
    if (sourceId) params.source_id = sourceId
    if (targetId) params.target_id = targetId
    if (collectionId) params.collectionId = collectionId
    const res = await client.get('/knowledge/relations', { params })
    return extractArray<GraphRelation>(res)
  } catch {
    return []
  }
}

export async function getRelation(id: string): Promise<GraphRelation | null> {
  try {
    const res = await client.get(`/knowledge/relations/${id}`)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function createRelation(data: {
  type: string
  source_id: string
  target_id: string
  properties?: Record<string, string>
  description?: string
  collection_id?: string
}): Promise<GraphRelation | null> {
  try {
    const res = await client.post('/knowledge/relations', data)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function updateRelation(
  id: string,
  data: {
    type?: string
    source_id?: string
    target_id?: string
    properties?: Record<string, string>
    description?: string
  }
): Promise<GraphRelation | null> {
  try {
    const res = await client.put(`/knowledge/relations/${id}`, data)
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function deleteRelation(id: string): Promise<void> {
  await client.delete(`/knowledge/relations/${id}`)
}

export async function getRelatedEntities(entityId: string, relationType?: string): Promise<GraphEntity[]> {
  try {
    const params: Record<string, unknown> = {}
    if (relationType) params.relation_type = relationType
    const res = await client.get(`/knowledge/entities/${entityId}/related`, { params })
    return extractArray<GraphEntity>(res)
  } catch {
    return []
  }
}

export async function getSubgraph(entityId: string, depth = 2, collectionId?: string): Promise<SubgraphData | null> {
  try {
    const params: Record<string, unknown> = { depth }
    if (collectionId) params.collectionId = collectionId
    const res = await client.get(`/knowledge/entities/${entityId}/subgraph`, { params })
    return extractData(res, null)
  } catch {
    return null
  }
}

export async function getEntityPaths(sourceId: string, targetId: string, maxDepth = 4, collectionId?: string): Promise<EntityPath[]> {
  try {
    const params: Record<string, unknown> = { sourceId, targetId, maxDepth }
    if (collectionId) params.collectionId = collectionId
    const res = await client.get('/knowledge/paths', { params })
    return extractArray<EntityPath>(res)
  } catch {
    return []
  }
}

export async function getGraphData(collectionId?: string): Promise<GraphData> {
  try {
    const [entities, relations] = await Promise.all([
      listEntities(undefined, collectionId, 1, 0),
      listRelations(undefined, undefined, undefined, collectionId, 1, 0)
    ])
    
    const nodes: GraphNode[] = entities.map((e) => ({
      id: e.id,
      label: e.name,
      type: e.type,
      properties: e.properties,
      collection_id: e.collection_id,
      color: e.color,
    }))
    
    const edges: GraphEdge[] = relations.map((r) => ({
      id: r.id,
      source: r.source_id,
      target: r.target_id,
      label: r.type,
      properties: r.properties,
    }))
    
    return { nodes, edges }
  } catch {
    return { nodes: [], edges: [] }
  }
}
