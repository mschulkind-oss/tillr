import { useQuery } from '@tanstack/react-query'
import { getDependencies, getFeatures } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { useState, useMemo } from 'react'
import type { DependencyGraph } from '../api/types'

type FilterMode = 'all' | 'with-deps' | 'blocked'

const statusBorderColor: Record<string, string> = {
  done: 'border-success',
  implementing: 'border-accent',
  blocked: 'border-danger',
  'agent-qa': 'border-orange',
  'human-qa': 'border-warning',
  planning: 'border-purple',
  draft: 'border-border',
}

interface NodeInfo {
  id: string
  name: string
  status: string
  dependsOn: string[]
  blocks: string[]
}

function buildNodeMap(graph: DependencyGraph): Map<string, NodeInfo> {
  const map = new Map<string, NodeInfo>()
  for (const node of graph.nodes) {
    map.set(node.id, { ...node, dependsOn: [], blocks: [] })
  }
  for (const edge of graph.edges) {
    // edge.from depends on edge.to
    map.get(edge.from)?.dependsOn.push(edge.to)
    map.get(edge.to)?.blocks.push(edge.from)
  }
  return map
}

function computeLongestChain(nodeMap: Map<string, NodeInfo>): number {
  const memo = new Map<string, number>()
  function depth(id: string, visited: Set<string>): number {
    if (memo.has(id)) return memo.get(id)!
    if (visited.has(id)) return 0 // cycle guard
    visited.add(id)
    const node = nodeMap.get(id)
    if (!node || node.dependsOn.length === 0) {
      memo.set(id, 1)
      return 1
    }
    let max = 0
    for (const dep of node.dependsOn) {
      max = Math.max(max, depth(dep, visited))
    }
    const result = max + 1
    memo.set(id, result)
    return result
  }
  let longest = 0
  for (const id of nodeMap.keys()) {
    longest = Math.max(longest, depth(id, new Set()))
  }
  return longest
}

function categorizeNodes(nodeMap: Map<string, NodeInfo>) {
  const criticalPath: NodeInfo[] = []
  const blockedChain: NodeInfo[] = []
  const leafNodes: NodeInfo[] = []
  const other: NodeInfo[] = []

  // Sort critical path by number of features blocked (descending)
  const sorted = [...nodeMap.values()].sort((a, b) => b.blocks.length - a.blocks.length)

  for (const node of sorted) {
    if (node.blocks.length >= 2) {
      criticalPath.push(node)
    } else if (node.status === 'blocked' || node.dependsOn.some((d) => nodeMap.get(d)?.status === 'blocked')) {
      blockedChain.push(node)
    } else if (node.blocks.length === 0 && node.dependsOn.length > 0) {
      leafNodes.push(node)
    } else if (node.dependsOn.length > 0 || node.blocks.length > 0) {
      other.push(node)
    }
  }

  return { criticalPath, blockedChain, leafNodes, other }
}

function NodeCard({ node, nodeMap }: { node: NodeInfo; nodeMap: Map<string, NodeInfo> }) {
  const borderClass = statusBorderColor[node.status] || 'border-border'

  return (
    <div className={`bg-bg-secondary border-l-4 ${borderClass} rounded-lg p-4 space-y-2`}>
      <div className="flex items-center gap-2 flex-wrap">
        <EntityLink type="feature" id={node.id} name={node.name} showIcon />
        <StatusBadge status={node.status} />
      </div>

      {node.dependsOn.length > 0 && (
        <div className="text-sm">
          <span className="text-text-muted">Depends on: </span>
          <span className="inline-flex flex-wrap gap-1">
            {node.dependsOn.map((depId) => {
              const dep = nodeMap.get(depId)
              return (
                <span key={depId} className="inline-flex items-center gap-1">
                  <span className="text-text-muted">←</span>
                  <EntityLink type="feature" id={depId} name={dep?.name || depId} />
                  {dep && <StatusBadge status={dep.status} />}
                </span>
              )
            })}
          </span>
        </div>
      )}

      {node.blocks.length > 0 && (
        <div className="text-sm">
          <span className="text-text-muted">Blocks: </span>
          <span className="inline-flex flex-wrap gap-1">
            {node.blocks.map((blockId) => {
              const blocked = nodeMap.get(blockId)
              return (
                <span key={blockId} className="inline-flex items-center gap-1">
                  <span className="text-text-muted">→</span>
                  <EntityLink type="feature" id={blockId} name={blocked?.name || blockId} />
                  {blocked && <StatusBadge status={blocked.status} />}
                </span>
              )
            })}
          </span>
        </div>
      )}
    </div>
  )
}

function GroupSection({
  title,
  icon,
  nodes,
  nodeMap,
  emptyMessage,
}: {
  title: string
  icon: string
  nodes: NodeInfo[]
  nodeMap: Map<string, NodeInfo>
  emptyMessage: string
}) {
  if (nodes.length === 0) {
    return (
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-3">
          {icon} {title}
        </h2>
        <p className="text-sm text-text-muted">{emptyMessage}</p>
      </div>
    )
  }

  return (
    <div className="bg-bg-card border border-border rounded-lg p-5">
      <h2 className="text-sm font-semibold text-text-primary mb-3">
        {icon} {title}{' '}
        <span className="text-text-muted font-normal">({nodes.length})</span>
      </h2>
      <div className="space-y-3">
        {nodes.map((node) => (
          <NodeCard key={node.id} node={node} nodeMap={nodeMap} />
        ))}
      </div>
    </div>
  )
}

export function Timeline() {
  const graphQuery = useQuery({ queryKey: ['dependencies'], queryFn: getDependencies })
  const featuresQuery = useQuery({ queryKey: ['features'], queryFn: getFeatures })

  const [filter, setFilter] = useState<FilterMode>('all')
  const [search, setSearch] = useState('')

  const nodeMap = useMemo(() => {
    if (!graphQuery.data) return new Map<string, NodeInfo>()
    return buildNodeMap(graphQuery.data)
  }, [graphQuery.data])

  const stats = useMemo(() => {
    const totalFeatures = featuresQuery.data?.length ?? 0
    const withDeps = [...nodeMap.values()].filter((n) => n.dependsOn.length > 0 || n.blocks.length > 0).length
    const blockedCount = [...nodeMap.values()].filter((n) => n.status === 'blocked').length
    const independent = totalFeatures - withDeps
    const longestChain = nodeMap.size > 0 ? computeLongestChain(nodeMap) : 0

    return { withDeps, blockedCount, independent, longestChain }
  }, [nodeMap, featuresQuery.data])

  const filteredNodeMap = useMemo(() => {
    let nodes = [...nodeMap.values()]

    if (search) {
      const q = search.toLowerCase()
      nodes = nodes.filter(
        (n) => n.name.toLowerCase().includes(q) || n.id.toLowerCase().includes(q),
      )
    }

    if (filter === 'with-deps') {
      nodes = nodes.filter((n) => n.dependsOn.length > 0 || n.blocks.length > 0)
    } else if (filter === 'blocked') {
      nodes = nodes.filter(
        (n) => n.status === 'blocked' || n.dependsOn.some((d) => nodeMap.get(d)?.status === 'blocked'),
      )
    }

    const filtered = new Map<string, NodeInfo>()
    for (const n of nodes) filtered.set(n.id, n)
    return filtered
  }, [nodeMap, search, filter])

  const categories = useMemo(() => categorizeNodes(filteredNodeMap), [filteredNodeMap])

  if (graphQuery.isLoading || featuresQuery.isLoading) return <PageSkeleton />

  if (graphQuery.isError) {
    return (
      <div className="space-y-6">
        <h1 className="text-2xl font-bold text-text-primary">📅 Timeline & Dependencies</h1>
        <div className="bg-danger/10 border border-danger/30 rounded-lg p-4 text-danger text-sm">
          Failed to load dependency graph. Check that the project has features with dependencies.
        </div>
      </div>
    )
  }

  const totalNodes = nodeMap.size

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">📅 Timeline & Dependencies</h1>
        <p className="text-sm text-text-muted mt-1">
          Dependency graph across {totalNodes} connected feature{totalNodes !== 1 ? 's' : ''}
        </p>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="bg-bg-card border border-border rounded-lg p-4 text-center">
          <div className="text-2xl font-bold text-accent">{stats.withDeps}</div>
          <div className="text-xs text-text-muted mt-1">With Dependencies</div>
        </div>
        <div className="bg-bg-card border border-border rounded-lg p-4 text-center">
          <div className="text-2xl font-bold text-warning">{stats.longestChain}</div>
          <div className="text-xs text-text-muted mt-1">Longest Chain</div>
        </div>
        <div className="bg-bg-card border border-border rounded-lg p-4 text-center">
          <div className="text-2xl font-bold text-danger">{stats.blockedCount}</div>
          <div className="text-xs text-text-muted mt-1">Blocked</div>
        </div>
        <div className="bg-bg-card border border-border rounded-lg p-4 text-center">
          <div className="text-2xl font-bold text-success">{stats.independent}</div>
          <div className="text-xs text-text-muted mt-1">Independent</div>
        </div>
      </div>

      {/* Filters */}
      <div className="flex flex-col sm:flex-row gap-3">
        <div className="relative flex-1">
          <span className="absolute left-3 top-1/2 -translate-y-1/2 text-text-muted">🔍</span>
          <input
            type="text"
            placeholder="Search features..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full pl-9 pr-3 py-2 bg-bg-secondary border border-border rounded-lg text-sm text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent"
          />
        </div>
        <div className="flex gap-1 bg-bg-secondary rounded-lg p-1">
          {([
            ['all', 'All'],
            ['with-deps', 'With Deps'],
            ['blocked', 'Blocked'],
          ] as [FilterMode, string][]).map(([mode, label]) => (
            <button
              key={mode}
              onClick={() => setFilter(mode)}
              className={`px-3 py-1.5 text-xs font-medium rounded-md transition-colors ${
                filter === mode
                  ? 'bg-accent text-white'
                  : 'text-text-secondary hover:text-text-primary'
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {/* Dependency Groups */}
      {filteredNodeMap.size === 0 ? (
        <div className="bg-bg-card border border-border rounded-lg p-8 text-center">
          <p className="text-text-muted">
            {search ? 'No features match your search.' : 'No dependency data found.'}
          </p>
        </div>
      ) : (
        <div className="space-y-6">
          <GroupSection
            title="Critical Path"
            icon="🔥"
            nodes={categories.criticalPath}
            nodeMap={nodeMap}
            emptyMessage="No features on the critical path."
          />

          {categories.blockedChain.length > 0 && (
            <GroupSection
              title="Blocked Chain"
              icon="🚫"
              nodes={categories.blockedChain}
              nodeMap={nodeMap}
              emptyMessage="No blocked chains."
            />
          )}

          {categories.other.length > 0 && (
            <GroupSection
              title="Other Dependencies"
              icon="🔗"
              nodes={categories.other}
              nodeMap={nodeMap}
              emptyMessage="No other dependencies."
            />
          )}

          <GroupSection
            title="Leaf Nodes"
            icon="🍃"
            nodes={categories.leafNodes}
            nodeMap={nodeMap}
            emptyMessage="No leaf nodes — all features block something."
          />
        </div>
      )}
    </div>
  )
}
