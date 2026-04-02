import { useQuery } from '@tanstack/react-query'
import { getIdeas } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { Link } from 'react-router-dom'
import { formatTimeAgo, truncate } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { Idea } from '../api/types'

const STATUS_TABS = ['all', 'pending', 'approved', 'rejected', 'implemented'] as const
type StatusTab = (typeof STATUS_TABS)[number]

export function Ideas() {
  const ideas = useQuery({ queryKey: ['ideas'], queryFn: getIdeas })
  const [statusFilter, setStatusFilter] = useState<StatusTab>('all')

  const allIdeas = ideas.data || []

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const idea of allIdeas) {
      counts[idea.status] = (counts[idea.status] || 0) + 1
    }
    return counts
  }, [allIdeas])

  const filtered = useMemo(() => {
    if (statusFilter === 'all') return allIdeas
    return allIdeas.filter((i) => i.status === statusFilter)
  }, [allIdeas, statusFilter])

  if (ideas.isLoading) return <PageSkeleton />

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Idea Queue</h1>
        <p className="text-sm text-text-secondary mt-1">
          {allIdeas.length} total
          {statusCounts['pending'] ? ` · ${statusCounts['pending']} pending` : ''}
          {statusCounts['approved'] ? ` · ${statusCounts['approved']} approved` : ''}
          {statusCounts['implemented'] ? ` · ${statusCounts['implemented']} implemented` : ''}
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
              ? ` (${allIdeas.length})`
              : statusCounts[tab]
                ? ` (${statusCounts[tab]})`
                : ''}
          </button>
        ))}
      </div>

      {/* Idea cards */}
      <div className="space-y-2">
        {filtered.map((idea) => (
          <IdeaCard key={idea.id} idea={idea} />
        ))}
        {filtered.length === 0 && (
          <div className="text-center py-12 text-text-muted text-sm">
            No ideas match your filters
          </div>
        )}
      </div>
    </div>
  )
}

function IdeaCard({ idea }: { idea: Idea }) {
  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 hover:border-accent/30 transition-colors">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <Link
              to={`/ideas/${idea.id}`}
              className="text-sm font-medium text-text-primary hover:text-accent transition-colors truncate"
            >
              {idea.title}
            </Link>
            <span className="text-[10px] bg-bg-tertiary text-text-muted px-1.5 py-0.5 rounded shrink-0">
              {idea.idea_type}
            </span>
          </div>

          <p className="text-xs text-text-secondary mt-1">
            {truncate(idea.raw_input, 100)}
          </p>

          <div className="flex items-center gap-3 mt-2 flex-wrap">
            <span className="text-xs text-text-muted">by {idea.submitted_by}</span>

            {idea.feature_id && (
              <span className="text-xs text-text-secondary flex items-center gap-1">
                Created feature: <EntityLink type="feature" id={idea.feature_id} />
              </span>
            )}

            {idea.assigned_agent && (
              <span className="text-[10px] bg-purple/10 text-purple px-1.5 py-0.5 rounded">
                🤖 {idea.assigned_agent}
              </span>
            )}
          </div>
        </div>

        <div className="flex items-center gap-2 shrink-0">
          <StatusBadge status={idea.status} />
          <span className="text-xs text-text-muted hidden lg:inline">
            {formatTimeAgo(idea.created_at)}
          </span>
        </div>
      </div>
    </div>
  )
}
