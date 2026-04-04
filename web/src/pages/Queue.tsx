import { useQuery, keepPreviousData } from '@tanstack/react-query'
import { getImplementationQueue, getWorkstreams } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { Link } from 'react-router-dom'
import { formatTimeAgo, cn } from '../lib/utils'
import { useMemo, useState } from 'react'
import type { ImplementationQueueFeature } from '../api/types'

function PriorityBadge({ priority }: { priority: number }) {
  const classes =
    priority >= 8
      ? 'bg-danger/20 text-danger'
      : priority >= 5
        ? 'bg-warning/20 text-warning'
        : 'bg-bg-tertiary text-text-muted'
  return (
    <span className={cn('inline-flex items-center px-2 py-0.5 rounded-full text-xs font-semibold', classes)}>
      P{priority}
    </span>
  )
}

function FeatureCard({ feature, variant }: { feature: ImplementationQueueFeature; variant: 'queued' | 'in-progress' | 'done' | 'review' }) {
  const borderColor =
    variant === 'in-progress'
      ? 'border-l-accent'
      : variant === 'review'
        ? 'border-l-purple'
        : variant === 'done'
          ? 'border-l-success'
          : feature.ready
            ? 'border-l-success'
            : 'border-l-orange'

  return (
    <div className={cn('bg-bg-secondary border border-border rounded-lg p-4 border-l-4', borderColor)}>
      <div className="flex items-start justify-between gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap mb-1">
            <PriorityBadge priority={feature.priority} />
            <Link
              to={`/features/${feature.id}`}
              className="font-medium text-text-primary hover:text-accent transition-colors"
            >
              {feature.name}
            </Link>
            <StatusBadge status={feature.status} />
          </div>

          {feature.description && (
            <p className="text-sm text-text-secondary mt-1">
              {feature.description}
            </p>
          )}

          {/* Readiness indicator */}
          {variant === 'queued' && (
            <div className="mt-2">
              {feature.ready ? (
                <span className="text-xs text-success font-medium">Ready</span>
              ) : (
                <span className="text-xs text-orange font-medium">
                  Blocked by: {feature.blocked_by?.join(', ')}
                </span>
              )}
            </div>
          )}

          {/* Agent info for in-progress */}
          {variant === 'in-progress' && feature.agent_id && (
            <div className="mt-2 text-xs text-text-muted">
              Agent: <span className="font-mono text-text-secondary">{feature.agent_id}</span>
              {feature.claimed_at && (
                <span className="ml-2">claimed {formatTimeAgo(feature.claimed_at)}</span>
              )}
            </div>
          )}

          {/* Done time */}
          {variant === 'done' && feature.done_at && (
            <div className="mt-2 text-xs text-text-muted">
              Completed {formatTimeAgo(feature.done_at)}
            </div>
          )}

          {/* QA rejection notes */}
          {feature.qa_notes && (
            <div className="mt-2 p-2 rounded bg-danger/10 border border-danger/30">
              <span className="text-xs font-semibold text-danger">QA Rejection:</span>
              <p className="text-xs text-danger/90 mt-0.5">{feature.qa_notes}</p>
            </div>
          )}

          {/* Tags */}
          {feature.tags && feature.tags.length > 0 && (
            <div className="flex gap-1 mt-2 flex-wrap">
              {feature.tags.map((tag) => (
                <span
                  key={tag}
                  className="px-1.5 py-0.5 rounded text-[10px] bg-bg-tertiary text-text-muted"
                >
                  {tag}
                </span>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/** Group completed features by date label (Today, Yesterday, or formatted date). */
function groupByDay(features: ImplementationQueueFeature[]): { label: string; items: ImplementationQueueFeature[] }[] {
  const now = new Date()
  const todayStr = now.toDateString()
  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  const yesterdayStr = yesterday.toDateString()

  const groups = new Map<string, { label: string; sortKey: string; items: ImplementationQueueFeature[] }>()

  for (const f of features) {
    const d = new Date(f.done_at ?? f.updated_at)
    const dateStr = d.toDateString()
    let label: string
    if (dateStr === todayStr) {
      label = 'Today'
    } else if (dateStr === yesterdayStr) {
      label = 'Yesterday'
    } else {
      label = d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
    }
    const key = dateStr
    if (!groups.has(key)) {
      groups.set(key, { label, sortKey: d.toISOString().slice(0, 10), items: [] })
    }
    groups.get(key)!.items.push(f)
  }

  return Array.from(groups.values()).sort((a, b) => b.sortKey.localeCompare(a.sortKey))
}

export function Queue() {
  const [workstream, setWorkstream] = useState('')
  const [minPriority, setMinPriority] = useState('')
  const [doneExpanded, setDoneExpanded] = useState(false)
  const [doneDays, setDoneDays] = useState(0)
  const [collapsedDays, setCollapsedDays] = useState<Set<string>>(new Set())

  const queue = useQuery({
    queryKey: ['implementation-queue', workstream, minPriority, doneDays],
    queryFn: () =>
      getImplementationQueue({
        workstream: workstream || undefined,
        min_priority: minPriority || undefined,
        days: doneDays,
      }),
    refetchInterval: 10_000,
    placeholderData: keepPreviousData,
  })

  const workstreams = useQuery({
    queryKey: ['workstreams'],
    queryFn: () => getWorkstreams('active'),
    placeholderData: keepPreviousData,
  })

  const data = queue.data
  const claimable = data?.claimable ?? []
  const inProgress = data?.in_progress ?? []
  const inReview = data?.in_review ?? []
  const completedToday = data?.completed_today ?? []

  const doneGroups = useMemo(() => groupByDay(completedToday), [completedToday])

  const toggleDayCollapsed = (label: string) => {
    setCollapsedDays((prev) => {
      const next = new Set(prev)
      if (next.has(label)) {
        next.delete(label)
      } else {
        next.add(label)
      }
      return next
    })
  }

  // No PageSkeleton — render real layout immediately with empty arrays.
  // All data vars above default to [] so the page is always valid.

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-bold text-text-primary">Implementation Queue</h1>
          <p className="text-sm text-text-muted mt-1">
            What agents will pick up, in order.
          </p>
        </div>
      </div>

      {/* Summary bar */}
      <div className="flex gap-4 flex-wrap">
        <div className="bg-bg-secondary border border-border rounded-lg px-4 py-2 flex items-center gap-2">
          <span className="text-sm font-semibold text-text-primary">{claimable.length}</span>
          <span className="text-xs text-text-muted">queued</span>
        </div>
        <div className="bg-bg-secondary border border-border rounded-lg px-4 py-2 flex items-center gap-2">
          <span className="text-sm font-semibold text-accent">{inProgress.length}</span>
          <span className="text-xs text-text-muted">in progress</span>
        </div>
        <div className="bg-bg-secondary border border-border rounded-lg px-4 py-2 flex items-center gap-2">
          <span className="text-sm font-semibold text-purple">{inReview.length}</span>
          <span className="text-xs text-text-muted">in review</span>
        </div>
        <div className="bg-bg-secondary border border-border rounded-lg px-4 py-2 flex items-center gap-2">
          <span className="text-sm font-semibold text-success">{completedToday.length}</span>
          <span className="text-xs text-text-muted">done{doneDays > 0 ? ` (${doneDays + 1}d)` : ' today'}</span>
        </div>
      </div>

      {/* Filters */}
      <div className="flex gap-3 flex-wrap">
        <select
          value={workstream}
          onChange={(e) => setWorkstream(e.target.value)}
          className="px-3 py-1.5 rounded-md bg-bg-secondary border border-border text-text-primary text-sm"
        >
          <option value="">All workstreams</option>
          {(workstreams.data ?? []).map((ws) => (
            <option key={ws.id} value={ws.id}>
              {ws.name}
            </option>
          ))}
        </select>
        <select
          value={minPriority}
          onChange={(e) => setMinPriority(e.target.value)}
          className="px-3 py-1.5 rounded-md bg-bg-secondary border border-border text-text-primary text-sm"
        >
          <option value="">Any priority</option>
          <option value="8">P8+ (Critical)</option>
          <option value="5">P5+ (Medium+)</option>
          <option value="3">P3+ (Low+)</option>
        </select>
      </div>

      {/* Queued section */}
      <section>
        <h2 className="text-sm font-semibold uppercase tracking-wider text-text-muted mb-3">
          Queued ({claimable.length})
        </h2>
        {claimable.length === 0 ? (
          <p className="text-sm text-text-muted italic">No features in queue.</p>
        ) : (
          <div className="space-y-2">
            {claimable.map((f) => (
              <FeatureCard key={f.id} feature={f} variant="queued" />
            ))}
          </div>
        )}
      </section>

      {/* In Progress section */}
      <section>
        <h2 className="text-sm font-semibold uppercase tracking-wider text-text-muted mb-3">
          In Progress ({inProgress.length})
        </h2>
        {inProgress.length === 0 ? (
          <p className="text-sm text-text-muted italic">No features being worked on.</p>
        ) : (
          <div className="space-y-2">
            {inProgress.map((f) => (
              <FeatureCard key={f.id} feature={f} variant="in-progress" />
            ))}
          </div>
        )}
      </section>

      {/* In Review section */}
      <section>
        <h2 className="text-sm font-semibold uppercase tracking-wider text-text-muted mb-3">
          In Review ({inReview.length})
        </h2>
        {inReview.length === 0 ? (
          <p className="text-sm text-text-muted italic">No features in review.</p>
        ) : (
          <div className="space-y-2">
            {inReview.map((f) => (
              <FeatureCard key={f.id} feature={f} variant="review" />
            ))}
          </div>
        )}
      </section>

      {/* Done section (collapsed by default, with day grouping) */}
      <section>
        <button
          onClick={() => setDoneExpanded(!doneExpanded)}
          className="flex items-center gap-2 text-sm font-semibold uppercase tracking-wider text-text-muted mb-3 hover:text-text-secondary transition-colors"
        >
          <span className={cn('transition-transform', doneExpanded ? 'rotate-90' : '')}>
            &#9654;
          </span>
          Done ({completedToday.length})
        </button>
        {doneExpanded && (
          doneGroups.length === 0 ? (
            <p className="text-sm text-text-muted italic">No completed features.</p>
          ) : (
            <div className="space-y-4">
              {doneGroups.map((group) => {
                const isCollapsed = collapsedDays.has(group.label)
                return (
                  <div key={group.label}>
                    <button
                      onClick={() => toggleDayCollapsed(group.label)}
                      className="flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-text-muted mb-2 hover:text-text-secondary transition-colors"
                    >
                      <span className={cn('transition-transform text-[10px]', isCollapsed ? '' : 'rotate-90')}>
                        &#9654;
                      </span>
                      {group.label} ({group.items.length})
                    </button>
                    {!isCollapsed && (
                      <div className="space-y-2">
                        {group.items.map((f) => (
                          <FeatureCard key={f.id} feature={f} variant="done" />
                        ))}
                      </div>
                    )}
                  </div>
                )
              })}
              <button
                onClick={() => setDoneDays((d) => d + 7)}
                className="w-full py-2 text-sm text-text-muted hover:text-text-secondary border border-border border-dashed rounded-lg transition-colors"
              >
                Load more days
              </button>
            </div>
          )
        )}
      </section>
    </div>
  )
}
