import { useQuery } from '@tanstack/react-query'
import { getDecisions } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { Link } from 'react-router-dom'
import { formatTimeAgo, truncate } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { Decision } from '../api/types'

const STATUS_TABS = ['all', 'proposed', 'accepted', 'rejected', 'superseded'] as const
type StatusTab = (typeof STATUS_TABS)[number]

export function Decisions() {
  const decisions = useQuery({ queryKey: ['decisions'], queryFn: getDecisions })
  const [statusFilter, setStatusFilter] = useState<StatusTab>('all')

  const allDecisions = decisions.data || []

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const d of allDecisions) {
      counts[d.status] = (counts[d.status] || 0) + 1
    }
    return counts
  }, [allDecisions])

  const filtered = useMemo(() => {
    if (statusFilter === 'all') return allDecisions
    return allDecisions.filter((d) => d.status === statusFilter)
  }, [allDecisions, statusFilter])

  if (decisions.isLoading) return <PageSkeleton />

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Architecture Decisions</h1>
        <p className="text-sm text-text-secondary mt-1">
          {allDecisions.length} total
          {statusCounts['proposed'] ? ` · ${statusCounts['proposed']} proposed` : ''}
          {statusCounts['accepted'] ? ` · ${statusCounts['accepted']} accepted` : ''}
          {statusCounts['rejected'] ? ` · ${statusCounts['rejected']} rejected` : ''}
        </p>
      </div>

      {/* Filter tabs */}
      <div className="flex items-center gap-1 bg-bg-secondary rounded-lg p-1">
        {STATUS_TABS.map((tab) => (
          <button
            key={tab}
            onClick={() => setStatusFilter(tab)}
            className={`px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
              statusFilter === tab
                ? 'bg-bg-card text-text-primary shadow-sm'
                : 'text-text-muted hover:text-text-secondary'
            }`}
          >
            {tab === 'all' ? 'All' : tab.charAt(0).toUpperCase() + tab.slice(1)}
            {tab === 'all'
              ? ` (${allDecisions.length})`
              : statusCounts[tab]
                ? ` (${statusCounts[tab]})`
                : ''}
          </button>
        ))}
      </div>

      {/* Decision rows */}
      <div className="space-y-2">
        {filtered.map((decision) => (
          <DecisionRow key={decision.id} decision={decision} />
        ))}
        {filtered.length === 0 && (
          <div className="text-center py-12 text-text-muted text-sm">
            No decisions match your filters
          </div>
        )}
      </div>
    </div>
  )
}

function DecisionRow({ decision }: { decision: Decision }) {
  const d = decision

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 hover:border-accent/30 transition-colors">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <Link
            to={`/decisions/${d.id}`}
            className="text-sm font-medium text-text-primary hover:text-accent transition-colors"
          >
            {d.title}
          </Link>

          <p className="text-xs text-text-secondary mt-1">
            {truncate(d.context, 120)}
          </p>

          <div className="flex items-center gap-3 mt-2 flex-wrap">
            {d.feature_id && (
              <EntityLink type="feature" id={d.feature_id} className="text-xs" />
            )}

            {d.superseded_by && (
              <span className="text-xs text-text-secondary flex items-center gap-1">
                Superseded by: <EntityLink type="decision" id={d.superseded_by} />
              </span>
            )}
          </div>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          <StatusBadge status={d.status} />
          <span className="text-xs text-text-muted hidden lg:inline">
            {formatTimeAgo(d.created_at)}
          </span>
        </div>
      </div>
    </div>
  )
}
