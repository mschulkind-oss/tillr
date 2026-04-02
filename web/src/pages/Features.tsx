import { useQuery } from '@tanstack/react-query'
import { getFeatures, getMilestones, getTags } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { Link } from 'react-router-dom'
import { formatTimeAgo, cn, truncate } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { Feature, FeatureStatus } from '../api/types'

const ALL_STATUSES: FeatureStatus[] = ['draft', 'planning', 'implementing', 'agent-qa', 'human-qa', 'done', 'blocked']

export function Features() {
  const features = useQuery({ queryKey: ['features'], queryFn: getFeatures })
  const milestones = useQuery({ queryKey: ['milestones'], queryFn: getMilestones })
  const tags = useQuery({ queryKey: ['tags'], queryFn: getTags })
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState<FeatureStatus | 'all'>('all')
  const [milestoneFilter, setMilestoneFilter] = useState<string>('all')
  const [tagFilter, setTagFilter] = useState<string>('all')
  const [sortBy, setSortBy] = useState<'priority' | 'name' | 'updated'>('priority')

  const filtered = useMemo(() => {
    let items = features.data || []

    if (search) {
      const q = search.toLowerCase()
      items = items.filter((f) =>
        f.name.toLowerCase().includes(q) ||
        f.description?.toLowerCase().includes(q) ||
        f.id.toLowerCase().includes(q)
      )
    }

    if (statusFilter !== 'all') {
      items = items.filter((f) => f.status === statusFilter)
    }

    if (milestoneFilter !== 'all') {
      items = items.filter((f) => f.milestone_id === milestoneFilter)
    }

    if (tagFilter !== 'all') {
      items = items.filter((f) => f.tags?.includes(tagFilter))
    }

    items = [...items].sort((a, b) => {
      if (sortBy === 'priority') return b.priority - a.priority
      if (sortBy === 'name') return a.name.localeCompare(b.name)
      return b.updated_at.localeCompare(a.updated_at)
    })

    return items
  }, [features.data, search, statusFilter, milestoneFilter, tagFilter, sortBy])

  if (features.isLoading) return <PageSkeleton />

  const allFeatures = features.data || []
  const statusCounts = ALL_STATUSES.reduce((acc, s) => {
    acc[s] = allFeatures.filter((f) => f.status === s).length
    return acc
  }, {} as Record<string, number>)

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-text-primary">Features</h1>
          <p className="text-sm text-text-secondary mt-1">{allFeatures.length} total features</p>
        </div>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        {/* Search */}
        <div className="relative flex-1 min-w-[200px] max-w-sm">
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search features..."
            className="w-full bg-bg-input border border-border rounded-md pl-8 pr-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-accent focus:outline-none"
          />
          <span className="absolute left-2.5 top-1/2 -translate-y-1/2 text-text-muted text-sm">🔍</span>
        </div>

        {/* Status filter */}
        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as FeatureStatus | 'all')}
          className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
        >
          <option value="all">All statuses ({allFeatures.length})</option>
          {ALL_STATUSES.map((s) => (
            <option key={s} value={s}>
              {s} ({statusCounts[s] || 0})
            </option>
          ))}
        </select>

        {/* Milestone filter */}
        <select
          value={milestoneFilter}
          onChange={(e) => setMilestoneFilter(e.target.value)}
          className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
        >
          <option value="all">All milestones</option>
          {(milestones.data || []).map((m) => (
            <option key={m.id} value={m.id}>{m.name}</option>
          ))}
        </select>

        {/* Tag filter */}
        <select
          value={tagFilter}
          onChange={(e) => setTagFilter(e.target.value)}
          className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
        >
          <option value="all">All tags</option>
          {(tags.data || []).map((t) => (
            <option key={t.tag} value={t.tag}>{t.tag} ({t.count})</option>
          ))}
        </select>

        {/* Sort */}
        <select
          value={sortBy}
          onChange={(e) => setSortBy(e.target.value as 'priority' | 'name' | 'updated')}
          className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
        >
          <option value="priority">Sort by priority</option>
          <option value="name">Sort by name</option>
          <option value="updated">Sort by updated</option>
        </select>
      </div>

      {/* Status bar */}
      <StatusBar counts={statusCounts} total={allFeatures.length} />

      {/* Results count */}
      {search && (
        <p className="text-xs text-text-muted">
          {filtered.length} result{filtered.length !== 1 ? 's' : ''} matching "{search}"
        </p>
      )}

      {/* Feature list */}
      <div className="space-y-2">
        {filtered.map((feature) => (
          <FeatureRow key={feature.id} feature={feature} />
        ))}
        {filtered.length === 0 && (
          <div className="text-center py-12 text-text-muted text-sm">
            No features match your filters
          </div>
        )}
      </div>
    </div>
  )
}

function StatusBar({ counts, total }: { counts: Record<string, number>; total: number }) {
  if (total === 0) return null

  const colors: Record<string, string> = {
    done: 'bg-success',
    'human-qa': 'bg-warning',
    'agent-qa': 'bg-orange',
    implementing: 'bg-accent',
    planning: 'bg-purple',
    draft: 'bg-bg-tertiary',
    blocked: 'bg-danger',
  }

  return (
    <div className="flex h-2 rounded-full overflow-hidden bg-bg-tertiary">
      {ALL_STATUSES.map((status) => {
        const count = counts[status] || 0
        if (count === 0) return null
        const pct = (count / total) * 100
        return (
          <div
            key={status}
            className={cn(colors[status], 'transition-all duration-500')}
            style={{ width: `${pct}%` }}
            title={`${status}: ${count}`}
          />
        )
      })}
    </div>
  )
}

function FeatureRow({ feature }: { feature: Feature }) {
  return (
    <Link
      to={`/features/${feature.id}`}
      className="block bg-bg-card border border-border rounded-lg p-4 hover:border-accent/30 transition-colors"
    >
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <span className={cn(
            'text-sm font-mono shrink-0 w-8 text-center rounded py-0.5',
            feature.priority >= 8 ? 'bg-danger/10 text-danger' :
            feature.priority >= 5 ? 'bg-warning/10 text-warning' :
            'bg-bg-tertiary text-text-muted'
          )}>
            {feature.priority}
          </span>
          <div className="min-w-0">
            <h3 className="text-sm font-medium text-text-primary truncate">{feature.name}</h3>
            {feature.description && (
              <p className="text-xs text-text-secondary mt-0.5 truncate">
                {truncate(feature.description, 120)}
              </p>
            )}
          </div>
        </div>
        <div className="flex items-center gap-3 shrink-0">
          {feature.tags?.slice(0, 3).map((tag) => (
            <TagPill key={tag} tag={tag} />
          ))}
          {feature.milestone_name && feature.milestone_id && (
            <EntityLink type="milestone" id={feature.milestone_id} name={feature.milestone_name} className="text-xs hidden md:inline" />
          )}
          {feature.milestone_name && !feature.milestone_id && (
            <span className="text-xs text-text-muted hidden md:inline">{feature.milestone_name}</span>
          )}
          <StatusBadge status={feature.status} />
          <span className="text-xs text-text-muted hidden lg:inline w-16 text-right">
            {formatTimeAgo(feature.updated_at)}
          </span>
        </div>
      </div>
    </Link>
  )
}

const TAG_COLORS = [
  'bg-accent/15 text-accent',
  'bg-purple/15 text-purple',
  'bg-success/15 text-success',
  'bg-warning/15 text-warning',
  'bg-pink/15 text-pink',
  'bg-cyan/15 text-cyan',
  'bg-orange/15 text-orange',
]

function tagColor(tag: string): string {
  let hash = 0
  for (let i = 0; i < tag.length; i++) {
    hash = tag.charCodeAt(i) + ((hash << 5) - hash)
  }
  return TAG_COLORS[Math.abs(hash) % TAG_COLORS.length]
}

function TagPill({ tag }: { tag: string }) {
  return (
    <span className={cn('text-[10px] font-medium px-1.5 py-0.5 rounded-full', tagColor(tag))}>
      {tag}
    </span>
  )
}
