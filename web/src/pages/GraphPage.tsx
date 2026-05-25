import { useState, useEffect, useCallback, useRef } from 'react'
import {
  Search, Plus, RefreshCw, ZoomIn, ZoomOut, Maximize2,
  X, Edit3, Trash2, ChevronDown, ChevronUp,
  Navigation, Network, Route, Share2, Loader,
} from 'lucide-react'
import type {
  GraphEntity, GraphRelation, GraphStats, GraphNode, GraphEdge, EntityPath,
} from '@/api/graph'
import {
  getGraphStats, searchEntities, listEntities, getEntity,
  createEntity, updateEntity, deleteEntity, clearEntities,
  listRelations, createRelation, updateRelation, deleteRelation,
  getRelatedEntities, getSubgraph, getEntityPaths, getGraphData,
} from '@/api/graph'
import { listCollections } from '@/api/knowledge'
import type { VectorCollection } from '@/api/knowledge'
import Modal from '@/components/Modal'
import './GraphPage.css'

const TYPE_COLORS: Record<string, string> = {
  person: '#6366f1',
  organization: '#ec4899',
  product: '#10b981',
  document: '#f59e0b',
  concept: '#8b5cf6',
  event: '#06b6d4',
  technology: '#f97316',
  location: '#14b8a6',
}

function getTypeColor(type: string, entityColor?: string) {
  if (entityColor) return entityColor
  return TYPE_COLORS[type] || '#64748b'
}

function getTypeClass(type: string) {
  const known = ['person', 'organization', 'product', 'document', 'concept']
  return known.includes(type) ? `graph-detail-type-${type}` : 'graph-detail-type-default'
}

interface EdgeGroup {
  source: string
  target: string
  edges: GraphEdge[]
  isBidirectional: boolean
}

function groupEdges(edges: GraphEdge[]): EdgeGroup[] {
  const groups: EdgeGroup[] = []
  const visited = new Set<string>()

  for (const edge of edges) {
    const key = `${edge.source}-${edge.target}`
    const reverseKey = `${edge.target}-${edge.source}`

    if (visited.has(key)) continue

    const forward = edges.filter((e) => e.source === edge.source && e.target === edge.target)
    const reverse = edges.filter((e) => e.source === edge.target && e.target === edge.source)

    if (reverse.length > 0) {
      visited.add(key)
      visited.add(reverseKey)
      groups.push({
        source: edge.source,
        target: edge.target,
        edges: [...forward, ...reverse],
        isBidirectional: true,
      })
    } else {
      visited.add(key)
      groups.push({
        source: edge.source,
        target: edge.target,
        edges: forward,
        isBidirectional: false,
      })
    }
  }

  return groups
}

function calcArrowPos(sx: number, sy: number, tx: number, ty: number, nodeRadius: number) {
  const dx = tx - sx
  const dy = ty - sy
  const dist = Math.sqrt(dx * dx + dy * dy)
  if (dist === 0) return { ax: sx, ay: sy, angle: 0 }

  const ratio = (dist - nodeRadius - 2) / dist
  const ax = sx + dx * ratio
  const ay = sy + dy * ratio
  const angle = Math.atan2(dy, dx) * (180 / Math.PI)
  return { ax, ay, angle }
}

function calcCurveOffset(index: number, total: number, isBidirectional: boolean) {
  if (total === 1) return 0
  const spacing = 12
  const mid = (total - 1) / 2
  return (index - mid) * spacing
}

interface SimNode extends GraphNode {
  vx: number
  vy: number
  fx: number
  fy: number
  targetX?: number
  targetY?: number
}

export default function GraphPage() {
  const [stats, setStats] = useState<GraphStats | null>(null)
  const [entities, setEntities] = useState<GraphEntity[]>([])
  const [selectedEntity, setSelectedEntity] = useState<GraphEntity | null>(null)
  const [relatedEntities, setRelatedEntities] = useState<GraphEntity[]>([])
  const [graphData, setGraphData] = useState<{ nodes: SimNode[]; edges: GraphEdge[] } | null>(null)
  const [searchKeyword, setSearchKeyword] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [collectionFilter, setCollectionFilter] = useState('')
  const [pathMode, setPathMode] = useState(false)
  const [pathSource, setPathSource] = useState<string | null>(null)
  const [pathTarget, setPathTarget] = useState<string | null>(null)
  const [paths, setPaths] = useState<EntityPath[]>([])
  const [subgraphMode, setSubgraphMode] = useState(false)
  const [subgraphCenter, setSubgraphCenter] = useState<string | null>(null)
  const [showCreateEntity, setShowCreateEntity] = useState(false)
  const [showEditEntity, setShowEditEntity] = useState(false)
  const [showCreateRelation, setShowCreateRelation] = useState(false)
  const [showEditRelation, setShowEditRelation] = useState(false)
  const [showClearConfirm, setShowClearConfirm] = useState(false)
  const [hoveredNode, setHoveredNode] = useState<string | null>(null)
  const [autoRotate, setAutoRotate] = useState(true)
  const rotateAngleRef = useRef(0)
  const [editingRelation, setEditingRelation] = useState<GraphRelation | null>(null)
  const [entityTypes, setEntityTypes] = useState<string[]>([])
  const [collections, setCollections] = useState<VectorCollection[]>([])
  const [statsOpen, setStatsOpen] = useState(true)
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [dragging, setDragging] = useState<string | null>(null)
  const [isPanning, setIsPanning] = useState(false)
  const [panStart, setPanStart] = useState({ x: 0, y: 0 })
  const svgRef = useRef<SVGSVGElement>(null)
  const animRef = useRef<number>(0)
  const simNodesRef = useRef<SimNode[]>([])
  const draggingRef = useRef<string | null>(null)
  const panningRef = useRef(false)
  const panRef = useRef({ x: 0, y: 0 })
  const centerAnimRef = useRef(0)

  const [newEntityName, setNewEntityName] = useState('')
  const [newEntityType, setNewEntityType] = useState('')
  const [newEntityDesc, setNewEntityDesc] = useState('')
  const [newEntityProps, setNewEntityProps] = useState<Record<string, string>>({})
  const [editEntityName, setEditEntityName] = useState('')
  const [editEntityType, setEditEntityType] = useState('')
  const [editEntityDesc, setEditEntityDesc] = useState('')
  const [editEntityProps, setEditEntityProps] = useState<Record<string, string>>({})
  const [newRelType, setNewRelType] = useState('')
  const [newRelSource, setNewRelSource] = useState('')
  const [newRelTarget, setNewRelTarget] = useState('')
  const [editRelType, setEditRelType] = useState('')
  const [propKey, setPropKey] = useState('')
  const [propVal, setPropVal] = useState('')
  const [editPropKey, setEditPropKey] = useState('')
  const [editPropVal, setEditPropVal] = useState('')
  const [relSourceSearch, setRelSourceSearch] = useState('')
  const [relTargetSearch, setRelTargetSearch] = useState('')
  const [showSourceDropdown, setShowSourceDropdown] = useState(false)
  const [showTargetDropdown, setShowTargetDropdown] = useState(false)

  const loadStats = useCallback(async () => {
    const s = await getGraphStats(collectionFilter || undefined)
    if (s) {
      setStats(s)
      setEntityTypes(Object.keys(s.entity_type_count || {}))
    }
  }, [collectionFilter])

  const loadEntities = useCallback(async () => {
    let list: GraphEntity[]
    if (searchKeyword.trim()) {
      list = await searchEntities(searchKeyword.trim(), typeFilter || undefined, collectionFilter || undefined)
    } else {
      list = await listEntities(typeFilter || undefined, collectionFilter || undefined, 1, 0)
    }
    setEntities(list)
  }, [searchKeyword, typeFilter, collectionFilter])

  const loadGraph = useCallback(async () => {
    const data = await getGraphData(collectionFilter || undefined)
    const simNodes: SimNode[] = data.nodes.map((n, i) => {
      const angle = (2 * Math.PI * i) / Math.max(data.nodes.length, 1)
      const radius = 165 + Math.random() * 110
      const flyAngle = Math.random() * Math.PI * 2
      const flyDist = 800 + Math.random() * 400
      return {
        ...n,
        x: 400 + flyDist * Math.cos(flyAngle),
        y: 300 + flyDist * Math.sin(flyAngle),
        targetX: 400 + radius * Math.cos(angle),
        targetY: 300 + radius * Math.sin(angle),
        vx: 0,
        vy: 0,
        fx: 0,
        fy: 0,
      }
    })
    simNodesRef.current = simNodes
    setGraphData({ nodes: simNodes, edges: data.edges })
  }, [collectionFilter])

  useEffect(() => { loadStats() }, [loadStats])
  useEffect(() => { loadEntities() }, [loadEntities])
  useEffect(() => { loadGraph() }, [loadGraph])
  useEffect(() => { listCollections().then(setCollections) }, [])

  const runSimulation = useCallback(() => {
    const nodes = simNodesRef.current
    if (!nodes.length) return
    const edges = graphData?.edges || []
    const nodeMap = new Map(nodes.map((n) => [n.id, n]))
    let frame = 0

    const tick = () => {
      frame++
      const isDragging = draggingRef.current
      const isPanningNow = panningRef.current

      for (const node of nodes) {
        node.fx = 0
        node.fy = 0
      }

      if (frame < 60) {
        for (const node of nodes) {
          if (node.targetX !== undefined && node.targetY !== undefined) {
            const progress = Math.min(frame / 60, 1)
            const eased = 1 - Math.pow(1 - progress, 3)
            node.x = (node.x || 0) + ((node.targetX - (node.x || 0)) * eased * 0.08)
            node.y = (node.y || 0) + ((node.targetY - (node.y || 0)) * eased * 0.08)
          }
        }
      } else {
        for (const node of nodes) {
          const dx = 400 - (node.x || 0)
          const dy = 300 - (node.y || 0)
          const d = Math.sqrt(dx * dx + dy * dy) || 1
          if (d > 500) {
            node.fx += dx * 0.005
            node.fy += dy * 0.005
          }
        }

        for (let i = 0; i < nodes.length; i++) {
          for (let j = i + 1; j < nodes.length; j++) {
            const a = nodes[i]
            const b = nodes[j]
            const dx = (b.x || 0) - (a.x || 0)
            const dy = (b.y || 0) - (a.y || 0)
            const dist = Math.sqrt(dx * dx + dy * dy) || 1
            const minDist = 80
            if (dist < minDist) {
              const force = (minDist - dist) * 1.5
              const fx = (dx / dist) * force
              const fy = (dy / dist) * force
              a.fx -= fx
              a.fy -= fy
              b.fx += fx
              b.fy += fy
            }
          }
        }

        for (const edge of edges) {
          const source = nodeMap.get(edge.source)
          const target = nodeMap.get(edge.target)
          if (!source || !target) continue
          const dx = (target.x || 0) - (source.x || 0)
          const dy = (target.y || 0) - (source.y || 0)
          const dist = Math.sqrt(dx * dx + dy * dy) || 1
          const force = (dist - 130) * 0.04
          const fx = (dx / dist) * force
          const fy = (dy / dist) * force
          source.fx += fx
          source.fy += fy
          target.fx -= fx
          target.fy -= fy
        }

        for (const node of nodes) {
          if (isDragging === node.id) continue
          node.vx = (node.vx + node.fx) * 0.7
          node.vy = (node.vy + node.fy) * 0.7
          node.x = (node.x || 0) + node.vx
          node.y = (node.y || 0) + node.vy
        }
      }

      if (autoRotate && !isDragging && !isPanningNow) {
        rotateAngleRef.current += 0.001
      }

      setGraphData({ nodes: [...nodes], edges })
      animRef.current = requestAnimationFrame(tick)
    }

    animRef.current = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(animRef.current)
  }, [graphData?.edges, autoRotate])

  useEffect(() => {
    if (!graphData) return
    const cleanup = runSimulation()
    return cleanup
  }, [runSimulation])

  useEffect(() => {
    return () => cancelAnimationFrame(animRef.current)
  }, [])

  const handleSelectEntity = useCallback(async (entity: GraphEntity) => {
    if (pathMode) {
      if (!pathSource) {
        setPathSource(entity.id)
      } else if (!pathTarget && entity.id !== pathSource) {
        setPathTarget(entity.id)
        const result = await getEntityPaths(pathSource, entity.id, 4, collectionFilter || undefined)
        setPaths(result)
      }
      return
    }
    setSelectedEntity(entity)
    const related = await getRelatedEntities(entity.id)
    setRelatedEntities(related)
  }, [pathMode, pathSource, pathTarget, collectionFilter])

  const handleDoubleClickNode = useCallback(async (nodeId: string) => {
    setSubgraphMode(true)
    setSubgraphCenter(nodeId)
    const sub = await getSubgraph(nodeId, 2, collectionFilter || undefined)
    if (sub) {
      const simNodes: SimNode[] = sub.nodes.map((e, i) => {
        const angle = (2 * Math.PI * i) / Math.max(sub.nodes.length, 1)
        const radius = 120 + Math.random() * 80
        return {
          id: e.id,
          label: e.name,
          type: e.type,
          properties: e.properties,
          collection_id: e.collection_id,
          x: 400 + radius * Math.cos(angle),
          y: 300 + radius * Math.sin(angle),
          vx: 0,
          vy: 0,
          fx: 0,
          fy: 0,
        }
      })
      const edges: GraphEdge[] = sub.edges.map((r) => ({
        id: r.id,
        source: r.source_id,
        target: r.target_id,
        label: r.type,
        properties: r.properties,
      }))
      simNodesRef.current = simNodes
      setGraphData({ nodes: simNodes, edges })
    }
  }, [collectionFilter])

  const handleExitSubgraph = useCallback(() => {
    setSubgraphMode(false)
    setSubgraphCenter(null)
    loadGraph()
  }, [loadGraph])

  const handleExitPathMode = useCallback(() => {
    setPathMode(false)
    setPathSource(null)
    setPathTarget(null)
    setPaths([])
  }, [])

  const handleDeleteEntity = useCallback(async (id: string) => {
    try {
      await deleteEntity(id)
      setSelectedEntity(null)
      setRelatedEntities([])
      loadEntities()
      loadGraph()
      loadStats()
    } catch { /* ignore */ }
  }, [loadEntities, loadGraph, loadStats])

  const handleClearEntities = useCallback(async () => {
    try {
      await clearEntities(collectionFilter || undefined)
      setSelectedEntity(null)
      setRelatedEntities([])
      setShowClearConfirm(false)
      loadEntities()
      loadGraph()
      loadStats()
    } catch { /* ignore */ }
  }, [collectionFilter, loadEntities, loadGraph, loadStats])

  const handleCreateEntity = async () => {
    if (!newEntityName.trim() || !newEntityType.trim()) return
    const result = await createEntity({
      name: newEntityName.trim(),
      type: newEntityType.trim(),
      description: newEntityDesc.trim() || undefined,
      properties: Object.keys(newEntityProps).length > 0 ? newEntityProps : undefined,
      collection_id: collectionFilter.trim() || undefined,
    })
    if (result) {
      setShowCreateEntity(false)
      resetEntityForm()
      setSelectedEntity(result)
      loadEntities()
      loadGraph()
      loadStats()
    }
  }

  const handleUpdateEntity = async () => {
    if (!selectedEntity) return
    const result = await updateEntity(selectedEntity.id, {
      name: editEntityName?.trim() || undefined,
      type: editEntityType?.trim() || undefined,
      description: editEntityDesc?.trim() || undefined,
      properties: Object.keys(editEntityProps).length > 0 ? editEntityProps : undefined,
    })
    if (result) {
      setSelectedEntity(result)
      setShowEditEntity(false)
      loadEntities()
      loadGraph()
    }
  }

  const handleCreateRelation = async () => {
    if (!newRelType.trim() || !newRelSource.trim() || !newRelTarget.trim()) return
    const result = await createRelation({
      type: newRelType.trim(),
      source_id: newRelSource.trim(),
      target_id: newRelTarget.trim(),
      collection_id: collectionFilter.trim() || undefined,
    })
    if (result) {
      setShowCreateRelation(false)
      setNewRelType('')
      setNewRelSource('')
      setNewRelTarget('')
      setRelSourceSearch('')
      setRelTargetSearch('')
      setShowSourceDropdown(false)
      setShowTargetDropdown(false)
      loadGraph()
      loadStats()
      if (selectedEntity) {
        const related = await getRelatedEntities(selectedEntity.id)
        setRelatedEntities(related)
      }
    }
  }

  const handleUpdateRelation = async () => {
    if (!editingRelation) return
    const result = await updateRelation(editingRelation.id, {
      type: editRelType.trim() || undefined,
    })
    if (result) {
      setEditingRelation(null)
      setShowEditRelation(false)
      loadGraph()
    }
  }

  const handleDeleteRelation = async (id: string) => {
    try {
      await deleteRelation(id)
      loadGraph()
      loadStats()
      if (selectedEntity) {
        const related = await getRelatedEntities(selectedEntity.id)
        setRelatedEntities(related)
      }
    } catch { /* ignore */ }
  }

  const resetEntityForm = () => {
    setNewEntityName('')
    setNewEntityType('')
    setNewEntityDesc('')
    setNewEntityProps({})
    setPropKey('')
    setPropVal('')
  }

  const openEditEntity = () => {
    if (!selectedEntity) return
    setEditEntityName(selectedEntity.name || '')
    setEditEntityType(selectedEntity.type || '')
    setEditEntityDesc(selectedEntity.description || '')
    setEditEntityProps(selectedEntity.properties ? { ...selectedEntity.properties } : {})
    setEditPropKey('')
    setEditPropVal('')
    setShowEditEntity(true)
  }

  const openEditRelation = (rel: GraphRelation) => {
    setEditingRelation(rel)
    setEditRelType(rel.type)
    setShowEditRelation(true)
  }

  const handleNodeMouseDown = (nodeId: string, e: React.MouseEvent) => {
    e.stopPropagation()
    setDragging(nodeId)
    draggingRef.current = nodeId
    const node = simNodesRef.current.find((n) => n.id === nodeId)
    if (node) {
      node.vx = 0
      node.vy = 0
    }
  }

  const handleNodeMouseUp = () => {
    setDragging(null)
    draggingRef.current = null
  }

  const handleSvgMouseDown = (e: React.MouseEvent) => {
    if (e.button === 0 && !dragging) {
      cancelAnimationFrame(centerAnimRef.current)
      setIsPanning(true)
      panningRef.current = true
      setPanStart({ x: e.clientX - panRef.current.x, y: e.clientY - panRef.current.y })
    }
  }

  const handleSvgMouseMove = useCallback((e: React.MouseEvent) => {
    if (draggingRef.current) {
      const svg = svgRef.current
      if (!svg) return
      const rect = svg.getBoundingClientRect()
      let x = (e.clientX - rect.left - pan.x) / zoom
      let y = (e.clientY - rect.top - pan.y) / zoom
      const angle = rotateAngleRef.current
      if (angle !== 0) {
        const cosA = Math.cos(-angle)
        const sinA = Math.sin(-angle)
        const dx = x - 400
        const dy = y - 300
        x = dx * cosA - dy * sinA + 400
        y = dx * sinA + dy * cosA + 300
      }
      const node = simNodesRef.current.find((n) => n.id === draggingRef.current)
      if (node) {
        node.x = x
        node.y = y
        node.vx = 0
        node.vy = 0
      }
    } else if (panningRef.current) {
      const newPan = {
        x: e.clientX - panStart.x,
        y: e.clientY - panStart.y,
      }
      panRef.current = newPan
      setPan(newPan)
    }
  }, [panStart, zoom])

  const handleSvgMouseUp = () => {
    setDragging(null)
    draggingRef.current = null
    setIsPanning(false)
    panningRef.current = false
  }

  const handleZoomIn = () => setZoom((z) => Math.min(z * 1.2, 3))
  const handleZoomOut = () => setZoom((z) => Math.max(z / 1.2, 0.3))
  const handleResetView = () => {
    setZoom(1)
    setPan({ x: 0, y: 0 })
    panRef.current = { x: 0, y: 0 }
  }

  const centerOnNode = useCallback((nodeId: string) => {
    const node = simNodesRef.current.find((n) => n.id === nodeId)
    if (!node || !svgRef.current) return

    cancelAnimationFrame(centerAnimRef.current)

    const svg = svgRef.current
    const svgWidth = svg.clientWidth
    const svgHeight = svg.clientHeight

    const angle = rotateAngleRef.current
    const dx = (node.x || 0) - 400
    const dy = (node.y || 0) - 300
    const rx = dx * Math.cos(angle) - dy * Math.sin(angle) + 400
    const ry = dx * Math.sin(angle) + dy * Math.cos(angle) + 300

    const targetPanX = svgWidth / 2 - rx * zoom
    const targetPanY = svgHeight / 2 - ry * zoom

    const startPanX = panRef.current.x
    const startPanY = panRef.current.y
    const duration = 400
    const startTime = performance.now()

    const animate = (currentTime: number) => {
      const elapsed = currentTime - startTime
      const progress = Math.min(elapsed / duration, 1)
      const eased = 1 - Math.pow(1 - progress, 3)

      const newPanX = startPanX + (targetPanX - startPanX) * eased
      const newPanY = startPanY + (targetPanY - startPanY) * eased

      panRef.current = { x: newPanX, y: newPanY }
      setPan({ x: newPanX, y: newPanY })

      if (progress < 1) {
        centerAnimRef.current = requestAnimationFrame(animate)
      }
    }

    centerAnimRef.current = requestAnimationFrame(animate)
  }, [zoom])

  const handleSidebarEntityClick = useCallback((entity: GraphEntity) => {
    handleSelectEntity(entity)
    centerOnNode(entity.id)
  }, [handleSelectEntity, centerOnNode])

  const handleNodeClick = (nodeId: string) => {
    const entity = entities.find((e) => e.id === nodeId)
    if (entity) {
      handleSelectEntity(entity)
    } else {
      getEntity(nodeId).then((e) => {
        if (e) handleSelectEntity(e)
      })
    }
  }

  const nodeMap = new Map((graphData?.nodes || []).map((n) => [n.id, n]))
  const pathNodeIds = new Set<string>()
  if (paths.length > 0) {
    for (const p of paths) {
      for (const e of p.entities) pathNodeIds.add(e.id)
    }
  }

  return (
    <div className="graph-page">
      <div className="graph-header">
        <div>
          <h2>知识图谱</h2>
          <p className="graph-subtitle">实体关系可视化与探索</p>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="ba-btn ba-btn-secondary" onClick={() => { loadStats(); loadEntities(); loadGraph() }}>
            <RefreshCw size={16} /> 刷新
          </button>
          <button className="ba-btn ba-btn-secondary" onClick={() => setShowCreateRelation(true)}>
            <Share2 size={16} /> 新建关系
          </button>
          <button className="ba-btn ba-btn-primary" onClick={() => setShowCreateEntity(true)}>
            <Plus size={16} /> 新建实体
          </button>
        </div>
      </div>

      <div className="graph-content">
        <div className="graph-sidebar">
          <div className="graph-sidebar-header">
            <h3>实体列表</h3>
          </div>

          <div className="graph-sidebar-search">
            <div className="graph-search-row">
              <input
                className="ba-input"
                placeholder="搜索实体..."
                value={searchKeyword}
                onChange={(e) => setSearchKeyword(e.target.value)}
              />
              <button className="ba-btn ba-btn-secondary" style={{ padding: '0 10px' }}>
                <Search size={16} />
              </button>
            </div>
            <select
              className="ba-input"
              value={typeFilter}
              onChange={(e) => setTypeFilter(e.target.value)}
            >
              <option value="">全部类型</option>
              {entityTypes.map((t) => (
                <option key={t} value={t}>{t} ({stats?.entity_type_count[t] || 0})</option>
              ))}
            </select>
            <select
              className="ba-input"
              value={collectionFilter}
              onChange={(e) => setCollectionFilter(e.target.value)}
            >
              <option value="">全部集合</option>
              {collections.map((c) => (
                <option key={c.id} value={c.id}>{c.name}</option>
              ))}
            </select>
          </div>

          <div className="graph-section">
            <div className="graph-section-header">
              <span className="graph-section-title">实体</span>
              <span className="graph-count">{entities.length}</span>
              {entities.length > 0 && (
                <button
                  className="ba-btn ba-btn-danger"
                  style={{ padding: '2px 8px', fontSize: 11, marginLeft: 'auto' }}
                  onClick={() => setShowClearConfirm(true)}
                >
                  <Trash2 size={12} /> 清空
                </button>
              )}
            </div>
            <div className="graph-entity-list">
              {entities.map((entity) => (
                <div
                  key={entity.id}
                  className={`graph-entity-item${selectedEntity?.id === entity.id ? ' active' : ''}${pathSource === entity.id || pathTarget === entity.id ? ' path-selected' : ''}`}
                  onClick={() => handleSidebarEntityClick(entity)}
                >
                  <div className="graph-entity-name">{entity.name}</div>
                  <div className="graph-entity-type">{entity.type}</div>
                </div>
              ))}
              {entities.length === 0 && (
                <div style={{ padding: '16px 0', textAlign: 'center', color: 'var(--ba-text-light)', fontSize: 13 }}>
                  暂无实体
                </div>
              )}
            </div>
          </div>

          <div className="graph-section">
            <div className="graph-section-header">
              <span className="graph-section-title">统计</span>
              <button className="graph-toggle-btn" onClick={() => setStatsOpen(!statsOpen)}>
                {statsOpen ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
              </button>
            </div>
            {statsOpen && (
              <div className="graph-stats">
                <div className="graph-stat-item">
                  <span className="graph-stat-label">实体总数</span>
                  <span className="graph-stat-value">{stats?.entity_count || 0}</span>
                </div>
                <div className="graph-stat-item">
                  <span className="graph-stat-label">关系总数</span>
                  <span className="graph-stat-value">{stats?.relation_count || 0}</span>
                </div>
                <div className="graph-stat-item">
                  <span className="graph-stat-label">实体类型</span>
                  <span className="graph-stat-value">{entityTypes.length}</span>
                </div>
                {Object.entries(stats?.entity_type_count || {}).map(([type, count]) => (
                  <div key={type} className="graph-stat-item">
                    <span className="graph-stat-label">{type}</span>
                    <span className="graph-stat-value">{count}</span>
                  </div>
                ))}
              </div>
            )}
          </div>

          {pathMode && (
            <div className="graph-section graph-path-section">
              <div className="graph-section-header">
                <span className="graph-section-title">路径查找</span>
              </div>
              <div className="graph-path-info">
                <div className={`graph-path-step${pathSource ? ' completed' : ''}`}>
                  <span className="graph-path-step-num">1</span>
                  <span>{pathSource ? `已选择源节点` : '点击选择源节点'}</span>
                </div>
                <div className={`graph-path-step${pathTarget ? ' completed' : ''}`}>
                  <span className="graph-path-step-num">2</span>
                  <span>{pathTarget ? '已选择目标节点' : '点击选择目标节点'}</span>
                </div>
              </div>
              {paths.length > 0 && (
                <div className="graph-path-result">
                  <div className="graph-path-result-header">
                    <Route size={14} /> 找到 {paths.length} 条路径
                  </div>
                  {paths.map((p, pi) => (
                    <div key={pi} style={{ marginBottom: 8 }}>
                      <div className="graph-path-nodes">
                        {p.entities.map((e, ei) => (
                          <span key={e.id}>
                            <span
                              className="graph-path-node-chip"
                              style={{ background: `${getTypeColor(e.type, e.color)}20`, color: getTypeColor(e.type, e.color) }}
                            >
                              {e.name}
                            </span>
                            {ei < p.entities.length - 1 && (
                              <span className="graph-path-arrow">→</span>
                            )}
                          </span>
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              )}
              {pathSource && pathTarget && paths.length === 0 && (
                <div className="graph-path-no-result">未找到路径</div>
              )}
            </div>
          )}
        </div>

        <div className="graph-canvas-container">
          {subgraphMode && (
            <div className="graph-mode-banner graph-mode-banner-subgraph">
              <Network size={14} />
              子图探索模式
              <button
                className="ba-btn ba-btn-secondary"
                style={{ padding: '2px 10px', fontSize: 12, marginLeft: 4 }}
                onClick={handleExitSubgraph}
              >
                退出
              </button>
            </div>
          )}

          {pathMode && (
            <div className="graph-mode-banner">
              <Navigation size={14} />
              路径查找模式
              <button
                className="ba-btn ba-btn-secondary"
                style={{ padding: '2px 10px', fontSize: 12, marginLeft: 4 }}
                onClick={handleExitPathMode}
              >
                退出
              </button>
            </div>
          )}

          {graphData && graphData.nodes.length > 0 ? (
            <svg
              ref={svgRef}
              className="graph-canvas"
              onMouseDown={handleSvgMouseDown}
              onMouseMove={handleSvgMouseMove}
              onMouseUp={handleSvgMouseUp}
              onMouseLeave={handleSvgMouseUp}
            >
              <defs>
                <filter id="glow-node" x="-50%" y="-50%" width="200%" height="200%">
                  <feGaussianBlur stdDeviation="4" result="blur" />
                  <feMerge>
                    <feMergeNode in="blur" />
                    <feMergeNode in="SourceGraphic" />
                  </feMerge>
                </filter>
                <filter id="glow-edge" x="-50%" y="-50%" width="200%" height="200%">
                  <feGaussianBlur stdDeviation="2" result="blur" />
                  <feMerge>
                    <feMergeNode in="blur" />
                    <feMergeNode in="SourceGraphic" />
                  </feMerge>
                </filter>
                <filter id="glow-strong" x="-80%" y="-80%" width="260%" height="260%">
                  <feGaussianBlur stdDeviation="8" result="blur" />
                  <feMerge>
                    <feMergeNode in="blur" />
                    <feMergeNode in="blur" />
                    <feMergeNode in="SourceGraphic" />
                  </feMerge>
                </filter>
                {graphData.nodes.map((node) => {
                  const color = getTypeColor(node.type || '', node.color)
                  return (
                    <radialGradient key={`rg-${node.id}`} id={`nodeGlow-${node.id}`}>
                      <stop offset="0%" stopColor={color} stopOpacity="0.6" />
                      <stop offset="40%" stopColor={color} stopOpacity="0.2" />
                      <stop offset="100%" stopColor={color} stopOpacity="0" />
                    </radialGradient>
                  )
                })}
              </defs>
              <g transform={`translate(${pan.x},${pan.y}) scale(${zoom}) rotate(${rotateAngleRef.current * 180 / Math.PI}, 400, 300)`}>
                {(() => {
                  const adjacentNodes = new Set<string>()
                  if (hoveredNode) {
                    for (const edge of graphData.edges) {
                      if (edge.source === hoveredNode) adjacentNodes.add(edge.target)
                      if (edge.target === hoveredNode) adjacentNodes.add(edge.source)
                    }
                  }

                  const edgeGroups = groupEdges(graphData.edges)
                  return edgeGroups.map((group, gi) => {
                    const source = nodeMap.get(group.source)
                    const target = nodeMap.get(group.target)
                    if (!source || !target) return null
                    const sx = source.x || 0
                    const sy = source.y || 0
                    const tx = target.x || 0
                    const ty = target.y || 0
                    const isPathEdge = pathNodeIds.has(group.source) && pathNodeIds.has(group.target)
                    const isHighlighted = hoveredNode === group.source || hoveredNode === group.target
                    const isDimmed = hoveredNode !== null && !isHighlighted

                    const dx = tx - sx
                    const dy = ty - sy
                    const dist = Math.sqrt(dx * dx + dy * dy)
                    const nx = dist > 0 ? -dy / dist : 0
                    const ny = dist > 0 ? dx / dist : 0

                    const forwardEdges = group.edges.filter((e) => e.source === group.source && e.target === group.target)
                    const reverseEdges = group.edges.filter((e) => e.source === group.target && e.target === group.source)

                    const forwardArrow = calcArrowPos(sx, sy, tx, ty, 16)
                    const reverseArrow = calcArrowPos(tx, ty, sx, sy, 16)

                    const edgeColor = isPathEdge ? '#f59e0b' : isHighlighted ? '#22d3ee' : '#60a5fa'
                    const edgeOpacity = isDimmed ? 0.15 : isHighlighted ? 1 : 0.5
                    const edgeWidth = isHighlighted ? 2 : isPathEdge ? 2 : 1

                    return (
                      <g key={`${group.source}-${group.target}`} style={{ animationDelay: `${gi * 40}ms` }}>
                        <defs>
                          <marker
                            id={`arrow-fwd-${group.source}-${group.target}`}
                            markerWidth="10"
                            markerHeight="7"
                            refX="9"
                            refY="3.5"
                            orient="auto"
                          >
                            <polygon points="0 0, 10 3.5, 0 7" fill={edgeColor} opacity={edgeOpacity} />
                          </marker>
                          {group.isBidirectional && (
                            <marker
                              id={`arrow-rev-${group.source}-${group.target}`}
                              markerWidth="10"
                              markerHeight="7"
                              refX="9"
                              refY="3.5"
                              orient="auto"
                            >
                              <polygon points="0 0, 10 3.5, 0 7" fill={edgeColor} opacity={edgeOpacity} />
                            </marker>
                          )}
                        </defs>

                        {group.isBidirectional ? (
                          <>
                            <path
                              d={`M ${sx + nx * 6} ${sy + ny * 6} L ${forwardArrow.ax + nx * 6} ${forwardArrow.ay + ny * 6}`}
                              stroke={edgeColor}
                              strokeWidth={edgeWidth}
                              fill="none"
                              opacity={edgeOpacity}
                              markerEnd={`url(#arrow-fwd-${group.source}-${group.target})`}
                              filter={isHighlighted ? 'url(#glow-edge)' : undefined}
                              strokeDasharray={isDimmed ? '4 4' : undefined}
                            />
                            <path
                              d={`M ${tx - nx * 6} ${ty - ny * 6} L ${reverseArrow.ax - nx * 6} ${reverseArrow.ay - ny * 6}`}
                              stroke={edgeColor}
                              strokeWidth={edgeWidth}
                              fill="none"
                              opacity={edgeOpacity}
                              markerEnd={`url(#arrow-rev-${group.source}-${group.target})`}
                              filter={isHighlighted ? 'url(#glow-edge)' : undefined}
                              strokeDasharray={isDimmed ? '4 4' : undefined}
                            />
                            <text
                              x={(sx + tx) / 2 + nx * 18}
                              y={(sy + ty) / 2 + ny * 18 - 2}
                              textAnchor="middle"
                              fontSize={9}
                              fill={isHighlighted ? '#22d3ee' : isPathEdge ? '#f59e0b' : '#94a3b8'}
                              opacity={isDimmed ? 0.2 : 1}
                              style={{ pointerEvents: 'none', userSelect: 'none' }}
                            >
                              {forwardEdges.map((e) => e.label).join(' / ')}
                            </text>
                            <text
                              x={(sx + tx) / 2 - nx * 18}
                              y={(sy + ty) / 2 - ny * 18 + 10}
                              textAnchor="middle"
                              fontSize={9}
                              fill={isHighlighted ? '#22d3ee' : isPathEdge ? '#f59e0b' : '#94a3b8'}
                              opacity={isDimmed ? 0.2 : 1}
                              style={{ pointerEvents: 'none', userSelect: 'none' }}
                            >
                              {reverseEdges.map((e) => e.label).join(' / ')}
                            </text>
                          </>
                        ) : (
                          forwardEdges.map((edge, idx) => {
                            const offset = calcCurveOffset(idx, forwardEdges.length, false)
                            const cpx = (sx + tx) / 2 + nx * offset
                            const cpy = (sy + ty) / 2 + ny * offset

                            const arrow = calcArrowPos(sx, sy, tx, ty, 16)
                            const pathD = offset === 0
                              ? `M ${sx} ${sy} L ${arrow.ax} ${arrow.ay}`
                              : `M ${sx} ${sy} Q ${cpx} ${cpy} ${arrow.ax} ${arrow.ay}`

                            const lx = offset === 0
                              ? (sx + tx) / 2 + nx * offset * 0.3
                              : (1 - 0.5) * (1 - 0.5) * sx + 2 * (1 - 0.5) * 0.5 * cpx + 0.5 * 0.5 * tx
                            const ly = offset === 0
                              ? (sy + ty) / 2 + ny * offset * 0.3 - 4
                              : (1 - 0.5) * (1 - 0.5) * sy + 2 * (1 - 0.5) * 0.5 * cpy + 0.5 * 0.5 * ty - 4

                            return (
                              <g key={edge.id}>
                                <path
                                  d={pathD}
                                  stroke={edgeColor}
                                  strokeWidth={edgeWidth}
                                  fill="none"
                                  opacity={edgeOpacity}
                                  markerEnd={`url(#arrow-fwd-${group.source}-${group.target})`}
                                  filter={isHighlighted ? 'url(#glow-edge)' : undefined}
                                  strokeDasharray={isDimmed ? '4 4' : undefined}
                                />
                                <text
                                  x={lx}
                                  y={ly}
                                  textAnchor="middle"
                                  fontSize={9}
                                  fill={isHighlighted ? '#22d3ee' : isPathEdge ? '#f59e0b' : '#94a3b8'}
                                  opacity={isDimmed ? 0.2 : 1}
                                  style={{ pointerEvents: 'none', userSelect: 'none' }}
                                >
                                  {edge.label}
                                </text>
                              </g>
                            )
                          })
                        )}
                      </g>
                    )
                  })
                })()}
                {graphData.nodes.map((node, ni) => {
                  const isSelected = selectedEntity?.id === node.id
                  const isPathNode = pathNodeIds.has(node.id)
                  const isSubgraphCenter = subgraphMode && subgraphCenter === node.id
                  const isHovered = hoveredNode === node.id
                  const isAdjacent = hoveredNode !== null && (() => {
                    for (const edge of graphData.edges) {
                      if ((edge.source === hoveredNode && edge.target === node.id) ||
                          (edge.target === hoveredNode && edge.source === node.id)) return true
                    }
                    return false
                  })()
                  const isDimmed = hoveredNode !== null && !isHovered && !isAdjacent

                  const baseRadius = isSubgraphCenter ? 22 : isSelected ? 20 : isPathNode ? 18 : 16
                  const radius = isHovered ? baseRadius + 4 : baseRadius
                  const color = getTypeColor(node.type || '', node.color)
                  const firstChar = node.label ? node.label.charAt(0).toUpperCase() : '?'

                  return (
                    <g
                      key={node.id}
                      transform={`translate(${node.x || 0},${node.y || 0})`}
                      onMouseDown={(e) => handleNodeMouseDown(node.id, e)}
                      onMouseUp={handleNodeMouseUp}
                      onClick={() => handleNodeClick(node.id)}
                      onDoubleClick={() => handleDoubleClickNode(node.id)}
                      onMouseEnter={() => setHoveredNode(node.id)}
                      onMouseLeave={() => setHoveredNode(null)}
                      style={{
                        cursor: dragging === node.id ? 'grabbing' : 'pointer',
                        opacity: isDimmed ? 0.2 : 1,
                        transition: 'opacity 0.3s ease',
                      }}
                    >
                      <circle
                        r={radius + 18}
                        fill={`url(#nodeGlow-${node.id})`}
                        opacity={isHovered ? 0.8 : 0.3}
                        style={{ transition: 'opacity 0.3s ease' }}
                      />
                      <circle
                        r={radius + 8}
                        fill={color}
                        opacity={isHovered ? 0.25 : 0.1}
                        style={{ transition: 'opacity 0.3s ease' }}
                      />
                      <circle
                        r={radius}
                        fill="#1e3a8a"
                        stroke={isHovered ? '#22d3ee' : isSelected ? '#fff' : isPathNode ? '#f59e0b' : color}
                        strokeWidth={isHovered || isSelected || isPathNode ? 2.5 : 1.5}
                        filter={isHovered ? 'url(#glow-strong)' : 'url(#glow-node)'}
                        style={{ transition: 'r 0.2s ease, stroke 0.2s ease' }}
                      />
                      <text
                        textAnchor="middle"
                        dominantBaseline="central"
                        fontSize={radius * 0.7}
                        fontWeight={700}
                        fill="white"
                        style={{ pointerEvents: 'none', userSelect: 'none' }}
                      >
                        {firstChar}
                      </text>
                      <text
                        y={radius + 14}
                        textAnchor="middle"
                        fontSize={10}
                        fontWeight={500}
                        fill={isDimmed ? '#475569' : '#94a3b8'}
                        style={{ pointerEvents: 'none', userSelect: 'none' }}
                      >
                        {node.label.length > 5 ? node.label.slice(0, 5) + '...' : node.label}
                      </text>
                    </g>
                  )
                })}
              </g>
            </svg>
          ) : (
            <div className="graph-canvas-empty">
              <Network size={48} />
              <p>暂无图谱数据</p>
              <p style={{ fontSize: 13 }}>创建实体和关系开始构建知识图谱</p>
            </div>
          )}

          <div className="graph-controls">
            <button className="graph-control-btn" onClick={handleZoomIn} title="放大">
              <ZoomIn size={18} />
            </button>
            <button className="graph-control-btn" onClick={handleZoomOut} title="缩小">
              <ZoomOut size={18} />
            </button>
            <button className="graph-control-btn" onClick={handleResetView} title="重置视图">
              <Maximize2 size={18} />
            </button>
            <button
              className={`graph-control-btn${autoRotate ? ' graph-control-btn-accent' : ''}`}
              onClick={() => setAutoRotate(!autoRotate)}
              title={autoRotate ? '停止旋转' : '自动旋转'}
            >
              <RefreshCw size={18} />
            </button>
            <button
              className={`graph-control-btn${pathMode ? ' graph-control-btn-accent' : ''}`}
              onClick={() => pathMode ? handleExitPathMode() : setPathMode(true)}
              title="路径查找"
            >
              <Navigation size={18} />
            </button>
          </div>

          {entityTypes.length > 0 && (
            <div className="graph-legend">
              {entityTypes.map((type) => (
                <div key={type} className="graph-legend-item">
                  <div className="graph-legend-dot" style={{ background: getTypeColor(type) }} />
                  <span>{type}</span>
                </div>
              ))}
            </div>
          )}

          {selectedEntity && (
            <div className="graph-detail-panel">
              <div className="graph-detail-header">
                <div>
                  <h3>{selectedEntity.name}</h3>
                  <span className={`graph-detail-type ${getTypeClass(selectedEntity.type)}`}>
                    {selectedEntity.type}
                  </span>
                </div>
                <button className="graph-detail-close" onClick={() => { setSelectedEntity(null); setRelatedEntities([]) }}>
                  <X size={18} />
                </button>
              </div>
              <div className="graph-detail-content">
                {selectedEntity.description && (
                  <div className="graph-detail-section">
                    <h4>描述</h4>
                    <p style={{ fontSize: 13, color: 'var(--ba-text)', lineHeight: 1.6, margin: 0 }}>
                      {selectedEntity.description}
                    </p>
                  </div>
                )}

                <div className="graph-detail-section">
                  <h4>属性</h4>
                  <div className="graph-properties">
                    {Object.entries(selectedEntity.properties || {}).map(([key, value]) => (
                      <div key={key} className="graph-property">
                        <span className="graph-property-key">{key}</span>
                        <span className="graph-property-value">{value}</span>
                      </div>
                    ))}
                    {Object.keys(selectedEntity.properties || {}).length === 0 && (
                      <div style={{ fontSize: 13, color: 'var(--ba-text-light)' }}>暂无属性</div>
                    )}
                  </div>
                </div>

                <div className="graph-detail-section">
                  <h4>关联实体 ({relatedEntities.length})</h4>
                  <div className="graph-related-list">
                    {relatedEntities.map((re) => (
                      <div
                        key={re.id}
                        className="graph-related-item"
                        onClick={() => handleSelectEntity(re)}
                      >
                        <div className="graph-related-name">{re.name}</div>
                        <div className="graph-related-type">{re.type}</div>
                      </div>
                    ))}
                    {relatedEntities.length === 0 && (
                      <div style={{ fontSize: 13, color: 'var(--ba-text-light)' }}>暂无关联实体</div>
                    )}
                  </div>
                </div>

                <div className="graph-detail-actions">
                  <button className="ba-btn ba-btn-secondary" onClick={openEditEntity}>
                    <Edit3 size={14} /> 编辑
                  </button>
                  <button className="ba-btn ba-btn-danger" onClick={() => handleDeleteEntity(selectedEntity.id)}>
                    <Trash2 size={14} /> 删除
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>
      </div>

      <Modal open={showCreateEntity} onClose={() => { setShowCreateEntity(false); resetEntityForm() }} title="新建实体">
        <div className="new-chat-form">
          <div className="login-field">
            <label>名称</label>
            <input className="ba-input" placeholder="实体名称" value={newEntityName} onChange={(e) => setNewEntityName(e.target.value)} />
          </div>
          <div className="login-field">
            <label>类型</label>
            <input
              className="ba-input"
              placeholder="如 person, organization, concept..."
              value={newEntityType}
              onChange={(e) => setNewEntityType(e.target.value)}
              list="entity-type-list"
            />
            <datalist id="entity-type-list">
              {entityTypes.map((t) => <option key={t} value={t} />)}
            </datalist>
          </div>
          <div className="login-field">
            <label>描述</label>
            <input className="ba-input" placeholder="可选描述" value={newEntityDesc} onChange={(e) => setNewEntityDesc(e.target.value)} />
          </div>
          <div className="login-field">
            <label>属性</label>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <input className="ba-input" placeholder="键" value={propKey} onChange={(e) => setPropKey(e.target.value)} style={{ flex: 1 }} />
              <input className="ba-input" placeholder="值" value={propVal} onChange={(e) => setPropVal(e.target.value)} style={{ flex: 1 }} />
              <button
                className="ba-btn ba-btn-secondary"
                style={{ padding: '0 10px' }}
                onClick={() => {
                  if (propKey.trim()) {
                    setNewEntityProps({ ...newEntityProps, [propKey.trim()]: propVal })
                    setPropKey('')
                    setPropVal('')
                  }
                }}
              >
                <Plus size={14} />
              </button>
            </div>
            {Object.entries(newEntityProps).map(([k, v]) => (
              <div key={k} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4, fontSize: 13 }}>
                <span style={{ color: 'var(--ba-text-light)' }}>{k}:</span>
                <span style={{ color: 'var(--ba-text)' }}>{v}</span>
                <button
                  style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--ba-accent)', padding: 0 }}
                  onClick={() => {
                    const next = { ...newEntityProps }
                    delete next[k]
                    setNewEntityProps(next)
                  }}
                >
                  <X size={12} />
                </button>
              </div>
            ))}
          </div>
          <button className="ba-btn ba-btn-primary" onClick={handleCreateEntity} style={{ marginTop: 16, width: '100%' }}>创建</button>
        </div>
      </Modal>

      <Modal open={showEditEntity} onClose={() => setShowEditEntity(false)} title="编辑实体">
        <div className="new-chat-form">
          <div className="login-field">
            <label>名称</label>
            <input className="ba-input" value={editEntityName} onChange={(e) => setEditEntityName(e.target.value)} />
          </div>
          <div className="login-field">
            <label>类型</label>
            <input className="ba-input" value={editEntityType} onChange={(e) => setEditEntityType(e.target.value)} />
          </div>
          <div className="login-field">
            <label>描述</label>
            <input className="ba-input" value={editEntityDesc} onChange={(e) => setEditEntityDesc(e.target.value)} />
          </div>
          <div className="login-field">
            <label>属性</label>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <input className="ba-input" placeholder="键" value={editPropKey} onChange={(e) => setEditPropKey(e.target.value)} style={{ flex: 1 }} />
              <input className="ba-input" placeholder="值" value={editPropVal} onChange={(e) => setEditPropVal(e.target.value)} style={{ flex: 1 }} />
              <button
                className="ba-btn ba-btn-secondary"
                style={{ padding: '0 10px' }}
                onClick={() => {
                  if (editPropKey.trim()) {
                    setEditEntityProps({ ...editEntityProps, [editPropKey.trim()]: editPropVal })
                    setEditPropKey('')
                    setEditPropVal('')
                  }
                }}
              >
                <Plus size={14} />
              </button>
            </div>
            {Object.entries(editEntityProps).map(([k, v]) => (
              <div key={k} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4, fontSize: 13 }}>
                <span style={{ color: 'var(--ba-text-light)' }}>{k}:</span>
                <span style={{ color: 'var(--ba-text)' }}>{v}</span>
                <button
                  style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'var(--ba-accent)', padding: 0 }}
                  onClick={() => {
                    const next = { ...editEntityProps }
                    delete next[k]
                    setEditEntityProps(next)
                  }}
                >
                  <X size={12} />
                </button>
              </div>
            ))}
          </div>
          <button className="ba-btn ba-btn-primary" onClick={handleUpdateEntity} style={{ marginTop: 16, width: '100%' }}>保存</button>
        </div>
      </Modal>

      <Modal open={showCreateRelation} onClose={() => { setShowCreateRelation(false); setNewRelType(''); setNewRelSource(''); setNewRelTarget(''); setRelSourceSearch(''); setRelTargetSearch(''); setShowSourceDropdown(false); setShowTargetDropdown(false) }} title="新建关系">
        <div className="new-chat-form">
          <div className="login-field">
            <label>关系类型</label>
            <input className="ba-input" placeholder="如 works_for, created, located_in..." value={newRelType} onChange={(e) => setNewRelType(e.target.value)} />
          </div>
          <div className="login-field" style={{ position: 'relative' }}>
            <label>源实体</label>
            <input
              className="ba-input"
              placeholder="输入关键词搜索实体..."
              value={newRelSource ? (entities.find((e) => e.id === newRelSource)?.name || newRelSource) : relSourceSearch}
              onChange={(e) => {
                setRelSourceSearch(e.target.value)
                setNewRelSource('')
                setShowSourceDropdown(true)
              }}
              onFocus={() => {
                if (newRelSource) {
                  setRelSourceSearch(entities.find((e) => e.id === newRelSource)?.name || '')
                  setNewRelSource('')
                }
                setShowSourceDropdown(true)
              }}
              onBlur={() => setTimeout(() => setShowSourceDropdown(false), 200)}
            />
            {showSourceDropdown && !newRelSource && (
              <div className="entity-search-dropdown">
                {entities
                  .filter((e) => !relSourceSearch || e.name.toLowerCase().includes(relSourceSearch.toLowerCase()) || e.type.toLowerCase().includes(relSourceSearch.toLowerCase()))
                  .slice(0, 30)
                  .map((e) => (
                    <div
                      key={e.id}
                      className="entity-search-item"
                      onMouseDown={(ev) => ev.preventDefault()}
                      onClick={() => {
                        setNewRelSource(e.id)
                        setRelSourceSearch('')
                        setShowSourceDropdown(false)
                      }}
                    >
                      <span className="entity-search-item-name">{e.name}</span>
                      <span className="entity-search-item-type" style={{ color: getTypeColor(e.type, e.color) }}>{e.type}</span>
                    </div>
                  ))}
                {entities.filter((e) => !relSourceSearch || e.name.toLowerCase().includes(relSourceSearch.toLowerCase()) || e.type.toLowerCase().includes(relSourceSearch.toLowerCase())).length === 0 && (
                  <div className="entity-search-empty">无匹配实体</div>
                )}
              </div>
            )}
          </div>
          <div className="login-field" style={{ position: 'relative' }}>
            <label>目标实体</label>
            <input
              className="ba-input"
              placeholder="输入关键词搜索实体..."
              value={newRelTarget ? (entities.find((e) => e.id === newRelTarget)?.name || newRelTarget) : relTargetSearch}
              onChange={(e) => {
                setRelTargetSearch(e.target.value)
                setNewRelTarget('')
                setShowTargetDropdown(true)
              }}
              onFocus={() => {
                if (newRelTarget) {
                  setRelTargetSearch(entities.find((e) => e.id === newRelTarget)?.name || '')
                  setNewRelTarget('')
                }
                setShowTargetDropdown(true)
              }}
              onBlur={() => setTimeout(() => setShowTargetDropdown(false), 200)}
            />
            {showTargetDropdown && !newRelTarget && (
              <div className="entity-search-dropdown">
                {entities
                  .filter((e) => !relTargetSearch || e.name.toLowerCase().includes(relTargetSearch.toLowerCase()) || e.type.toLowerCase().includes(relTargetSearch.toLowerCase()))
                  .slice(0, 30)
                  .map((e) => (
                    <div
                      key={e.id}
                      className="entity-search-item"
                      onMouseDown={(ev) => ev.preventDefault()}
                      onClick={() => {
                        setNewRelTarget(e.id)
                        setRelTargetSearch('')
                        setShowTargetDropdown(false)
                      }}
                    >
                      <span className="entity-search-item-name">{e.name}</span>
                      <span className="entity-search-item-type" style={{ color: getTypeColor(e.type, e.color) }}>{e.type}</span>
                    </div>
                  ))}
                {entities.filter((e) => !relTargetSearch || e.name.toLowerCase().includes(relTargetSearch.toLowerCase()) || e.type.toLowerCase().includes(relTargetSearch.toLowerCase())).length === 0 && (
                  <div className="entity-search-empty">无匹配实体</div>
                )}
              </div>
            )}
          </div>
          <button className="ba-btn ba-btn-primary" onClick={handleCreateRelation} style={{ marginTop: 16, width: '100%' }}>创建</button>
        </div>
      </Modal>

      <Modal open={showEditRelation} onClose={() => { setShowEditRelation(false); setEditingRelation(null) }} title="编辑关系">
        <div className="new-chat-form">
          <div className="login-field">
            <label>关系类型</label>
            <input className="ba-input" value={editRelType} onChange={(e) => setEditRelType(e.target.value)} />
          </div>
          {editingRelation && (
            <div style={{ fontSize: 13, color: 'var(--ba-text-light)', marginBottom: 12 }}>
              <div>源: {editingRelation.source_id}</div>
              <div>目标: {editingRelation.target_id}</div>
            </div>
          )}
          <div style={{ display: 'flex', gap: 8 }}>
            <button className="ba-btn ba-btn-primary" onClick={handleUpdateRelation} style={{ flex: 1 }}>保存</button>
            <button
              className="ba-btn ba-btn-danger"
              onClick={() => {
                if (editingRelation) {
                  handleDeleteRelation(editingRelation.id)
                  setShowEditRelation(false)
                  setEditingRelation(null)
                }
              }}
            >
              <Trash2 size={14} /> 删除
            </button>
          </div>
        </div>
      </Modal>

      <Modal open={showClearConfirm} onClose={() => setShowClearConfirm(false)} title="确认清空实体">
        <div className="new-chat-form">
          <p style={{ fontSize: 14, color: 'var(--ba-text)', lineHeight: 1.6 }}>
            {collectionFilter
              ? '确定要清空当前集合下的所有实体吗？该操作不可撤销。'
              : '确定要清空所有实体吗？该操作不可撤销。'}
          </p>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button className="ba-btn ba-btn-secondary" onClick={() => setShowClearConfirm(false)} style={{ flex: 1 }}>取消</button>
            <button className="ba-btn ba-btn-danger" onClick={handleClearEntities} style={{ flex: 1 }}>确认清空</button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
