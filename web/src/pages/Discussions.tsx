import { useQuery } from '@tanstack/react-query'
import { getDiscussions } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { Link } from 'react-router-dom'
import { formatTimeAgo } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { Discussion } from '../api/types'

const STATUS_TABS = ['all', 'open', 'resolved', 'closed'] as const
type StatusTab = (typeof STATUS_TABS)[number]

export function Discussions() {
  const discussions = useQuery({ queryKey: ['discussions'], queryFn: getDiscussions })
  const [statusFilter, setStatusFilter] = useState<StatusTab>('all')

  const allDiscussions = discussions.data || []

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const d of allDiscussions) {
      counts[d.status] = (counts[d.status] || 0) + 1
    }
    return counts
  }, [allDiscussions])

  const filtered = useMemo(() => {
    if (statusFilter === 'all') return allDiscussions
    return allDiscussions.filter((d) => d.status === statusFilter)
  }, [allDiscussions, statusFilter])

  if (discussions.isLoading) return <PageSkeleton />

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Discussions</h1>
        <p className="text-sm text-text-secondary mt-1">
          {allDiscussions.length} total
          {statusCounts['open'] ? ` · ${statusCounts['open']} open` : ''}
          {statusCounts['resolved'] ? ` · ${statusCounts['resolved']} resolved` : ''}
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
              ? ` (${allDiscussions.length})`
              : statusCounts[tab]
                ? ` (${statusCounts[tab]})`
                : ''}
          </button>
        ))}
      </div>

      {/* Discussion rows */}
      <div className="space-y-2">
        {filtered.map((discussion) => (
          <DiscussionRow key={discussion.id} discussion={discussion} />
        ))}
        {filtered.length === 0 && (
          <div className="text-center py-12 text-text-muted text-sm">
            No discussions match your filters
          </div>
        )}
      </div>
    </div>
  )
}

function DiscussionRow({ discussion }: { discussion: Discussion }) {
  const d = discussion

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 hover:border-accent/30 transition-colors">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <Link
            to={`/discussions/${d.id}`}
            className="text-sm font-medium text-text-primary hover:text-accent transition-colors"
          >
            {d.title}
          </Link>

          <div className="flex items-center gap-3 mt-2 flex-wrap">
            <span className="text-xs text-text-muted">by {d.author}</span>

            {d.comment_count != null && d.comment_count > 0 && (
              <span className="text-xs text-text-secondary">
                💬 {d.comment_count} comment{d.comment_count !== 1 ? 's' : ''}
              </span>
            )}

            {d.feature_id && (
              <EntityLink type="feature" id={d.feature_id} className="text-xs" />
            )}

            {d.votes && Object.keys(d.votes).length > 0 && (
              <span className="text-xs text-text-secondary">
                {Object.entries(d.votes).map(([emoji, count]) => (
                  <span key={emoji} className="mr-1">{emoji} {count}</span>
                ))}
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
