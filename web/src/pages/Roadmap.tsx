import { useQuery } from '@tanstack/react-query'
import { getRoadmap } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { formatTimeAgo, cn, groupBy } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { RoadmapItem } from '../api/types'

const PRIORITY_ORDER = ['critical', 'high', 'medium', 'low', 'nice-to-have']
const PRIORITY_COLORS: Record<string, string> = {
  critical: 'text-danger bg-danger/10 border-danger/20',
  high: 'text-orange bg-orange/10 border-orange/20',
  medium: 'text-warning bg-warning/10 border-warning/20',
  low: 'text-accent bg-accent/10 border-accent/20',
  'nice-to-have': 'text-text-muted bg-bg-tertiary border-border',
}
const EFFORT_LABELS: Record<string, string> = {
  xs: 'XS', s: 'S', m: 'M', l: 'L', xl: 'XL',
}

export function Roadmap() {
  const roadmap = useQuery({ queryKey: ['roadmap'], queryFn: getRoadmap })
  const [categoryFilter, setCategoryFilter] = useState<string>('all')
  const [priorityFilter, setPriorityFilter] = useState<string>('all')
  const [statusFilter, setStatusFilter] = useState<string>('all')
  const [viewMode, setViewMode] = useState<'priority' | 'category' | 'status'>('priority')

  const items = roadmap.data || []

  const categories = useMemo(() => [...new Set(items.map((i) => i.category).filter(Boolean))].sort(), [items])
  const statuses = useMemo(() => [...new Set(items.map((i) => i.status))].sort(), [items])

  const filtered = useMemo(() => {
    let result = items
    if (categoryFilter !== 'all') result = result.filter((i) => i.category === categoryFilter)
    if (priorityFilter !== 'all') result = result.filter((i) => i.priority === priorityFilter)
    if (statusFilter !== 'all') result = result.filter((i) => i.status === statusFilter)
    return result
  }, [items, categoryFilter, priorityFilter, statusFilter])

  const grouped = useMemo(() => {
    if (viewMode === 'priority') return groupBy(filtered, (i) => i.priority)
    if (viewMode === 'category') return groupBy(filtered, (i) => i.category || 'uncategorized')
    return groupBy(filtered, (i) => i.status)
  }, [filtered, viewMode])

  const groupOrder = viewMode === 'priority' ? PRIORITY_ORDER : Object.keys(grouped).sort()

  if (roadmap.isLoading) return <PageSkeleton />

  // Stats for hero banner
  const totalItems = items.length
  const doneCount = items.filter((i) => i.status === 'done').length
  const inProgressCount = items.filter((i) => i.status === 'in-progress').length
  const pct = totalItems ? Math.round((doneCount / totalItems) * 100) : 0

  return (
    <div className="space-y-6">
      {/* Hero banner */}
      <div className="bg-gradient-to-r from-accent/10 via-purple/10 to-pink/10 border border-accent/20 rounded-xl p-6">
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold text-text-primary">Roadmap</h1>
            <p className="text-sm text-text-secondary mt-1">
              {totalItems} items · {doneCount} completed · {inProgressCount} in progress
            </p>
          </div>
          <div className="flex items-center gap-4">
            <div className="text-right">
              <div className="text-3xl font-bold text-accent">{pct}%</div>
              <div className="text-xs text-text-muted">complete</div>
            </div>
            <div className="w-32 h-3 bg-bg-tertiary rounded-full overflow-hidden">
              <div
                className="h-full bg-accent rounded-full transition-all duration-700"
                style={{ width: `${pct}%` }}
              />
            </div>
          </div>
        </div>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <select
          value={categoryFilter}
          onChange={(e) => setCategoryFilter(e.target.value)}
          className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
        >
          <option value="all">All categories</option>
          {categories.map((c) => (
            <option key={c} value={c}>{c}</option>
          ))}
        </select>

        <select
          value={priorityFilter}
          onChange={(e) => setPriorityFilter(e.target.value)}
          className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
        >
          <option value="all">All priorities</option>
          {PRIORITY_ORDER.map((p) => (
            <option key={p} value={p}>{p}</option>
          ))}
        </select>

        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
        >
          <option value="all">All statuses</option>
          {statuses.map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>

        <div className="ml-auto flex bg-bg-secondary border border-border rounded-md overflow-hidden">
          {(['priority', 'category', 'status'] as const).map((mode) => (
            <button
              key={mode}
              onClick={() => setViewMode(mode)}
              className={cn(
                'px-3 py-1.5 text-xs font-medium transition-colors capitalize',
                viewMode === mode
                  ? 'bg-accent text-white'
                  : 'text-text-secondary hover:text-text-primary hover:bg-bg-hover'
              )}
            >
              {mode}
            </button>
          ))}
        </div>
      </div>

      {/* Grouped items */}
      <div className="space-y-8">
        {groupOrder
          .filter((group) => grouped[group]?.length > 0)
          .map((group) => (
            <div key={group}>
              <div className="flex items-center gap-3 mb-4">
                <h2 className="text-lg font-semibold text-text-primary capitalize">{group}</h2>
                <span className="text-xs text-text-muted font-mono bg-bg-tertiary px-2 py-0.5 rounded">
                  {grouped[group].length}
                </span>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                {grouped[group].map((item) => (
                  <RoadmapCard key={item.id} item={item} />
                ))}
              </div>
            </div>
          ))}
      </div>

      {filtered.length === 0 && (
        <div className="text-center py-12 text-text-muted text-sm">
          No roadmap items match your filters
        </div>
      )}
    </div>
  )
}

function RoadmapCard({ item }: { item: RoadmapItem }) {
  const priorityClass = PRIORITY_COLORS[item.priority] || PRIORITY_COLORS['medium']

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 space-y-3">
      {/* Title + status */}
      <div className="flex items-start justify-between gap-2">
        <h3 className="text-sm font-semibold text-text-primary leading-snug">{item.title}</h3>
        <StatusBadge status={item.status} />
      </div>

      {/* Description */}
      {item.description && (
        <p className="text-xs text-text-secondary line-clamp-2">{item.description}</p>
      )}

      {/* Meta row */}
      <div className="flex items-center gap-2 flex-wrap">
        <span className={cn('text-[10px] font-medium px-2 py-0.5 rounded-full border', priorityClass)}>
          {item.priority}
        </span>
        {item.effort && (
          <span className="text-[10px] font-mono bg-bg-tertiary text-text-muted px-1.5 py-0.5 rounded">
            {EFFORT_LABELS[item.effort] || item.effort}
          </span>
        )}
        {item.category && (
          <span className="text-[10px] text-text-muted">{item.category}</span>
        )}
        <span className="text-[10px] text-text-muted ml-auto">{formatTimeAgo(item.updated_at)}</span>
      </div>
    </div>
  )
}
