import { useQuery } from '@tanstack/react-query'
import { getRoadmap, getFeatures, getMilestones } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { formatTimeAgo, cn, groupBy } from '../lib/utils'
import { useState, useMemo } from 'react'
import { Link } from 'react-router-dom'
import type { RoadmapItem, Feature, Milestone } from '../api/types'

const EFFORT_LABELS: Record<string, string> = {
  xs: 'XS', s: 'S', m: 'M', l: 'L', xl: 'XL',
}

const STATUS_ORDER = ['proposed', 'accepted', 'in-progress', 'done', 'deferred', 'rejected']

export function Roadmap() {
  const roadmap = useQuery({ queryKey: ['roadmap'], queryFn: getRoadmap })
  const featuresQuery = useQuery({ queryKey: ['features'], queryFn: getFeatures })
  const milestonesQuery = useQuery({ queryKey: ['milestones'], queryFn: getMilestones })

  const [categoryFilter, setCategoryFilter] = useState<string>('all')
  const [statusFilter, setStatusFilter] = useState<string>('all')
  const [viewMode, setViewMode] = useState<'milestone' | 'status' | 'category'>('milestone')
  const [showDone, setShowDone] = useState(false)

  const items = roadmap.data || []
  const features = featuresQuery.data || []
  const milestones = milestonesQuery.data || []

  // Build lookup maps
  const featureByRoadmapId = useMemo(() => {
    const map: Record<string, Feature> = {}
    for (const f of features) {
      if (f.roadmap_item_id) map[f.roadmap_item_id] = f
    }
    return map
  }, [features])

  const milestoneById = useMemo(() => {
    const map: Record<string, Milestone> = {}
    for (const m of milestones) map[m.id] = m
    return map
  }, [milestones])

  // Map roadmap item -> milestone via linked feature
  const itemMilestoneId = useMemo(() => {
    const map: Record<string, string> = {}
    for (const item of items) {
      const feat = featureByRoadmapId[item.id]
      if (feat?.milestone_id) map[item.id] = feat.milestone_id
    }
    return map
  }, [items, featureByRoadmapId])

  const categories = useMemo(() => [...new Set(items.map((i) => i.category).filter(Boolean))].sort(), [items])
  const statuses = useMemo(() => [...new Set(items.map((i) => i.status))].sort(), [items])

  const filtered = useMemo(() => {
    let result = items
    // Hide done by default
    if (!showDone) result = result.filter((i) => i.status !== 'done')
    if (categoryFilter !== 'all') result = result.filter((i) => i.category === categoryFilter)
    if (statusFilter !== 'all') result = result.filter((i) => i.status === statusFilter)
    return result
  }, [items, categoryFilter, statusFilter, showDone])

  const grouped = useMemo(() => {
    if (viewMode === 'milestone') {
      return groupBy(filtered, (i) => {
        const msId = itemMilestoneId[i.id]
        return msId || 'no-milestone'
      })
    }
    if (viewMode === 'status') return groupBy(filtered, (i) => i.status)
    return groupBy(filtered, (i) => i.category || 'uncategorized')
  }, [filtered, viewMode, itemMilestoneId])

  // Sort milestones by completion (100% last, then by progress)
  const milestoneOrder = useMemo(() => {
    return [...milestones].sort((a, b) => {
      const aPct = (a.total_features || 0) > 0 ? (a.done_features || 0) / (a.total_features || 1) : 0
      const bPct = (b.total_features || 0) > 0 ? (b.done_features || 0) / (b.total_features || 1) : 0
      // Completed milestones go last
      if (aPct >= 1 && bPct < 1) return 1
      if (bPct >= 1 && aPct < 1) return -1
      // Otherwise sort by progress ascending (least done first = most work remaining)
      return aPct - bPct
    })
  }, [milestones])

  const groupOrder = useMemo(() => {
    if (viewMode === 'status') return STATUS_ORDER.filter((s) => grouped[s]?.length > 0)
    if (viewMode === 'milestone') {
      const ordered = milestoneOrder.map((m) => m.id).filter((id) => grouped[id]?.length > 0)
      if (grouped['no-milestone']?.length > 0) ordered.push('no-milestone')
      return ordered
    }
    return Object.keys(grouped).sort((a, b) => {
      if (a === 'uncategorized') return 1
      if (b === 'uncategorized') return -1
      return a.localeCompare(b)
    })
  }, [grouped, viewMode, milestoneOrder])

  const isLoading = roadmap.isLoading || featuresQuery.isLoading || milestonesQuery.isLoading
  if (isLoading) return <PageSkeleton />

  // Stats (always from all items, not filtered)
  const totalItems = items.length
  const doneCount = items.filter((i) => i.status === 'done').length
  const inProgressCount = items.filter((i) => i.status === 'in-progress').length
  const proposedCount = items.filter((i) => i.status === 'proposed' || i.status === 'accepted').length
  const deferredCount = items.filter((i) => i.status === 'deferred').length
  const remainingCount = totalItems - doneCount

  const donePct = totalItems ? (doneCount / totalItems) * 100 : 0
  const inProgressPct = totalItems ? (inProgressCount / totalItems) * 100 : 0
  const proposedPct = totalItems ? (proposedCount / totalItems) * 100 : 0

  const groupLabel = (key: string): string => {
    if (viewMode === 'milestone') {
      if (key === 'no-milestone') return 'No Milestone'
      return milestoneById[key]?.name || key
    }
    return key
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="bg-gradient-to-r from-accent/10 via-purple/10 to-pink/10 border border-accent/20 rounded-xl p-6 space-y-5">
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold text-text-primary">Roadmap</h1>
            <p className="text-sm text-text-secondary mt-1">
              {remainingCount} items remaining &middot; {inProgressCount} in progress
            </p>
          </div>
          <div className="text-right">
            <div className="text-3xl font-bold text-accent">{Math.round(donePct)}%</div>
            <div className="text-xs text-text-muted">overall complete</div>
          </div>
        </div>

        {/* Stacked progress bar */}
        <div>
          <div className="w-full h-3 bg-bg-tertiary rounded-full overflow-hidden flex">
            {donePct > 0 && (
              <div className="h-full bg-success transition-all duration-700" style={{ width: `${donePct}%` }} />
            )}
            {inProgressPct > 0 && (
              <div className="h-full bg-accent transition-all duration-700" style={{ width: `${inProgressPct}%` }} />
            )}
            {proposedPct > 0 && (
              <div className="h-full bg-bg-hover transition-all duration-700" style={{ width: `${proposedPct}%` }} />
            )}
          </div>
          <div className="flex items-center gap-4 mt-2 text-[10px] text-text-muted">
            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-success inline-block" /> {doneCount} Done</span>
            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-accent inline-block" /> {inProgressCount} In Progress</span>
            <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-bg-hover inline-block" /> {proposedCount} Proposed</span>
            {deferredCount > 0 && <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-warning inline-block" /> {deferredCount} Deferred</span>}
          </div>
        </div>

        {/* Milestone progress cards */}
        {milestones.length > 0 && (
          <div>
            <h3 className="text-xs font-semibold text-text-secondary uppercase tracking-wider mb-3">Milestone Progress</h3>
            <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-2">
              {milestoneOrder.map((ms) => {
                const total = ms.total_features || 0
                const done = ms.done_features || 0
                const msPct = total > 0 ? Math.round((done / total) * 100) : 0
                return (
                  <Link
                    key={ms.id}
                    to={`/milestones/${ms.id}`}
                    className="bg-bg-card/60 border border-border rounded-lg px-3 py-2 hover:border-accent/40 transition-colors group"
                  >
                    <div className="flex items-center justify-between mb-1.5">
                      <span className="text-xs font-medium text-text-primary group-hover:text-accent transition-colors truncate mr-2">
                        {ms.name}
                      </span>
                      <span className="text-[10px] font-mono text-text-muted shrink-0">{msPct}%</span>
                    </div>
                    <div className="w-full h-1 bg-bg-tertiary rounded-full overflow-hidden">
                      <div
                        className={cn('h-full rounded-full transition-all duration-500', msPct >= 100 ? 'bg-success' : 'bg-accent')}
                        style={{ width: `${msPct}%` }}
                      />
                    </div>
                    <div className="text-[10px] text-text-muted mt-1">{done}/{total}</div>
                  </Link>
                )
              })}
            </div>
          </div>
        )}
      </div>

      {/* Filters and controls */}
      <div className="flex flex-wrap items-center gap-3">
        <select
          value={categoryFilter}
          onChange={(e) => setCategoryFilter(e.target.value)}
          className="bg-bg-input border border-border rounded-md px-3 py-1.5 text-sm text-text-primary"
        >
          <option value="all">All categories</option>
          {categories.map((c) => <option key={c} value={c}>{c}</option>)}
        </select>

        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          className="bg-bg-input border border-border rounded-md px-3 py-1.5 text-sm text-text-primary"
        >
          <option value="all">All statuses</option>
          {statuses.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>

        <label className="flex items-center gap-1.5 text-xs text-text-secondary cursor-pointer select-none">
          <input
            type="checkbox"
            checked={showDone}
            onChange={(e) => setShowDone(e.target.checked)}
            className="rounded border-border"
          />
          Show completed ({doneCount})
        </label>

        <div className="ml-auto flex bg-bg-secondary border border-border rounded-md overflow-hidden">
          {(['milestone', 'status', 'category'] as const).map((mode) => (
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

      {/* Grouped list */}
      <div className="space-y-8">
        {groupOrder
          .filter((group) => grouped[group]?.length > 0)
          .map((group) => {
            const ms = viewMode === 'milestone' && group !== 'no-milestone' ? milestoneById[group] : undefined
            const msTotal = ms?.total_features || 0
            const msDone = ms?.done_features || 0
            const msPct = msTotal > 0 ? Math.round((msDone / msTotal) * 100) : 0
            const groupItems = grouped[group]

            return (
              <div key={group}>
                {/* Group header */}
                <div className="flex items-center gap-3 mb-3">
                  {ms ? (
                    <Link to={`/milestones/${ms.id}`} className="text-base font-semibold text-text-primary hover:text-accent transition-colors">
                      {ms.name}
                    </Link>
                  ) : (
                    <h2 className="text-base font-semibold text-text-primary capitalize">{groupLabel(group)}</h2>
                  )}
                  <span className="text-xs text-text-muted font-mono bg-bg-tertiary px-1.5 py-0.5 rounded">
                    {groupItems.length}
                  </span>
                  {ms && (
                    <div className="flex items-center gap-2">
                      <div className="w-20 h-1.5 bg-bg-tertiary rounded-full overflow-hidden">
                        <div className={cn('h-full rounded-full', msPct >= 100 ? 'bg-success' : 'bg-accent')} style={{ width: `${msPct}%` }} />
                      </div>
                      <span className="text-xs text-text-muted">{msPct}%</span>
                    </div>
                  )}
                </div>

                {/* List rows */}
                <div className="border border-border rounded-lg overflow-hidden divide-y divide-border">
                  {groupItems.map((item) => (
                    <RoadmapRow
                      key={item.id}
                      item={item}
                      linkedFeature={featureByRoadmapId[item.id]}
                    />
                  ))}
                </div>
              </div>
            )
          })}
      </div>

      {filtered.length === 0 && (
        <div className="text-center py-12 text-text-muted text-sm">
          {showDone ? 'No roadmap items match your filters' : 'All items are done! Toggle "Show completed" to see them.'}
        </div>
      )}
    </div>
  )
}

function RoadmapRow({ item, linkedFeature }: { item: RoadmapItem; linkedFeature?: Feature }) {
  return (
    <Link
      to={`/roadmap/${item.id}`}
      className="flex items-center gap-3 px-4 py-3 bg-bg-card hover:bg-bg-hover transition-colors group"
      data-list-item
    >
      {/* Status indicator dot */}
      <span className={cn(
        'w-2 h-2 rounded-full shrink-0',
        item.status === 'done' ? 'bg-success' :
        item.status === 'in-progress' ? 'bg-accent' :
        item.status === 'deferred' ? 'bg-warning' :
        'bg-text-muted',
      )} />

      {/* Title */}
      <span className="text-sm text-text-primary group-hover:text-accent transition-colors min-w-0 truncate flex-1">
        {item.title}
      </span>

      {/* Category */}
      {item.category && (
        <span className="text-[10px] font-medium px-2 py-0.5 rounded-full border bg-purple/10 text-purple border-purple/20 shrink-0 hidden sm:inline">
          {item.category}
        </span>
      )}

      {/* Effort */}
      {item.effort && (
        <span className="text-[10px] font-mono bg-bg-tertiary text-text-muted px-1.5 py-0.5 rounded shrink-0 hidden md:inline">
          {EFFORT_LABELS[item.effort] || item.effort}
        </span>
      )}

      {/* Feature status if linked and differs */}
      {linkedFeature && (
        <span className="shrink-0 hidden lg:inline">
          <StatusBadge status={linkedFeature.status} />
        </span>
      )}

      {/* Roadmap status */}
      <span className="shrink-0">
        <StatusBadge status={item.status} />
      </span>

      {/* Updated */}
      <span className="text-[10px] text-text-muted shrink-0 w-16 text-right hidden md:inline">
        {formatTimeAgo(item.updated_at)}
      </span>
    </Link>
  )
}
