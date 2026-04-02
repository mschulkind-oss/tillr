import { useQuery } from '@tanstack/react-query'
import { getHistory } from '../api/client'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { formatTimeAgo, formatTimestamp, cn } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { Event } from '../api/types'

const LIMIT_OPTIONS = [50, 100, 200] as const

const EVENT_STYLES: Record<string, { color: string; dot: string; icon: string }> = {
  'feature.created':        { color: 'text-blue-400',   dot: 'bg-blue-400',   icon: '➕' },
  'feature_created':        { color: 'text-blue-400',   dot: 'bg-blue-400',   icon: '➕' },
  'feature.status_changed': { color: 'text-yellow-400', dot: 'bg-yellow-400', icon: '🔀' },
  'feature_status_changed': { color: 'text-yellow-400', dot: 'bg-yellow-400', icon: '🔀' },
  'qa_approved':            { color: 'text-green-400',  dot: 'bg-green-400',  icon: '✅' },
  'qa.approved':            { color: 'text-green-400',  dot: 'bg-green-400',  icon: '✅' },
  'qa_rejected':            { color: 'text-red-400',    dot: 'bg-red-400',    icon: '❌' },
  'qa.rejected':            { color: 'text-red-400',    dot: 'bg-red-400',    icon: '❌' },
  'cycle_started':          { color: 'text-purple-400', dot: 'bg-purple-400', icon: '🔄' },
  'cycle.started':          { color: 'text-purple-400', dot: 'bg-purple-400', icon: '🔄' },
  'cycle_completed':        { color: 'text-purple-400', dot: 'bg-purple-400', icon: '🏁' },
  'cycle.completed':        { color: 'text-purple-400', dot: 'bg-purple-400', icon: '🏁' },
}

const DEFAULT_STYLE = { color: 'text-text-muted', dot: 'bg-text-muted', icon: '📌' }

function getEventStyle(eventType: string) {
  return EVENT_STYLES[eventType] ?? DEFAULT_STYLE
}

function formatEventLabel(eventType: string): string {
  return eventType
    .replace(/^feature\./, '')
    .replace(/^qa\./, 'qa_')
    .replace(/^cycle\./, 'cycle_')
    .replace(/_/g, ' ')
}

function formatEventDescription(event: Event): string {
  try {
    const data = JSON.parse(event.data || '{}')
    const type = event.event_type

    if (type === 'feature.status_changed' || type === 'feature_status_changed') {
      const from = data.old_status || data.from
      const to = data.new_status || data.to
      if (from && to) return `Status changed from ${from} to ${to}`
      if (to) return `Status changed to ${to}`
      return 'Status changed'
    }
    if (type === 'feature.created' || type === 'feature_created') {
      return `Feature created: ${data.name || data.feature_name || event.feature_id || ''}`
    }
    if (type === 'qa_approved' || type === 'qa.approved') {
      return `QA approved${data.notes ? ': ' + data.notes : ''}`
    }
    if (type === 'qa_rejected' || type === 'qa.rejected') {
      return `QA rejected${data.notes ? ': ' + data.notes : ''}`
    }
    if (type === 'cycle_started' || type === 'cycle.started') {
      return `Cycle started${data.cycle_type ? ': ' + data.cycle_type : ''}`
    }
    if (type === 'cycle_completed' || type === 'cycle.completed') {
      return `Cycle completed${data.cycle_type ? ': ' + data.cycle_type : ''}`
    }

    return formatEventLabel(type)
  } catch {
    return formatEventLabel(event.event_type)
  }
}

export function History() {
  const [limit, setLimit] = useState<number>(50)
  const [typeFilter, setTypeFilter] = useState<string>('')
  const [featureFilter, setFeatureFilter] = useState<string>('')

  const { data: events, isLoading } = useQuery({
    queryKey: ['history', { limit, type: typeFilter || undefined, feature: featureFilter || undefined }],
    queryFn: () =>
      getHistory({
        limit,
        type: typeFilter || undefined,
        feature: featureFilter || undefined,
      }),
  })

  const uniqueTypes = useMemo(() => {
    if (!events) return []
    const types = [...new Set(events.map((e) => e.event_type))]
    types.sort()
    return types
  }, [events])

  if (isLoading) return <PageSkeleton />

  const displayEvents = events || []

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Activity History</h1>
        <p className="text-sm text-text-secondary mt-1">Full timeline of project events</p>
      </div>

      {/* Filters */}
      <div className="bg-bg-card border border-border rounded-lg p-4">
        <div className="flex flex-wrap items-center gap-4">
          {/* Event type filter */}
          <div className="flex flex-col gap-1">
            <label className="text-xs text-text-muted">Event Type</label>
            <select
              value={typeFilter}
              onChange={(e) => setTypeFilter(e.target.value)}
              className="bg-bg-secondary border border-border rounded px-3 py-1.5 text-sm text-text-primary focus:outline-none focus:ring-1 focus:ring-accent"
            >
              <option value="">All types</option>
              {uniqueTypes.map((t) => (
                <option key={t} value={t}>
                  {formatEventLabel(t)}
                </option>
              ))}
            </select>
          </div>

          {/* Feature filter */}
          <div className="flex flex-col gap-1">
            <label className="text-xs text-text-muted">Feature ID</label>
            <input
              type="text"
              value={featureFilter}
              onChange={(e) => setFeatureFilter(e.target.value)}
              placeholder="Filter by feature…"
              className="bg-bg-secondary border border-border rounded px-3 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent w-48"
            />
          </div>

          {/* Limit selector */}
          <div className="flex flex-col gap-1">
            <label className="text-xs text-text-muted">Show</label>
            <div className="flex gap-1">
              {LIMIT_OPTIONS.map((n) => (
                <button
                  key={n}
                  onClick={() => setLimit(n)}
                  className={cn(
                    'px-3 py-1.5 rounded text-sm border transition-colors',
                    limit === n
                      ? 'bg-accent text-white border-accent'
                      : 'bg-bg-secondary text-text-secondary border-border hover:border-accent/50'
                  )}
                >
                  {n}
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* Timeline */}
      {displayEvents.length === 0 ? (
        <div className="bg-bg-card border border-border rounded-lg p-12 text-center">
          <span className="text-4xl block mb-3">📜</span>
          <p className="text-text-secondary text-sm">No events found</p>
          <p className="text-text-muted text-xs mt-1">
            {typeFilter || featureFilter
              ? 'Try adjusting your filters'
              : 'Events will appear here as you work on the project'}
          </p>
        </div>
      ) : (
        <div className="bg-bg-card border border-border rounded-lg p-4">
          <div className="relative">
            {/* Vertical timeline line */}
            <div className="absolute left-[7.5rem] top-0 bottom-0 w-px bg-border" />

            <div className="space-y-0">
              {displayEvents.map((event, idx) => (
                <EventRow key={event.id} event={event} isLast={idx === displayEvents.length - 1} />
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Load more */}
      {displayEvents.length >= limit && (
        <div className="text-center">
          <button
            onClick={() => setLimit((prev) => prev + 50)}
            className="px-4 py-2 rounded text-sm bg-bg-secondary text-text-secondary border border-border hover:border-accent/50 hover:text-accent transition-colors"
          >
            Load more events…
          </button>
        </div>
      )}
    </div>
  )
}

function EventRow({ event, isLast }: { event: Event; isLast: boolean }) {
  const style = getEventStyle(event.event_type)
  const description = formatEventDescription(event)

  return (
    <div className={cn('flex items-start gap-4 relative', !isLast && 'pb-4')}>
      {/* Timestamp (left) */}
      <div className="w-24 shrink-0 text-right pt-1">
        <span className="text-xs text-text-muted" title={formatTimestamp(event.created_at)}>
          {formatTimeAgo(event.created_at)}
        </span>
      </div>

      {/* Dot (center) */}
      <div className="relative z-10 shrink-0 mt-1.5">
        <div className={cn('w-3 h-3 rounded-full border-2 border-bg-card', style.dot)} />
      </div>

      {/* Content card (right) */}
      <div className="flex-1 min-w-0 pb-4 border-b border-border/50 last:border-0">
        <div className="flex items-start gap-2 flex-wrap">
          {/* Type badge */}
          <span
            className={cn(
              'inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-bg-secondary',
              style.color
            )}
          >
            <span>{style.icon}</span>
            {formatEventLabel(event.event_type)}
          </span>

          {/* Feature link */}
          {event.feature_id && (
            <EntityLink type="feature" id={event.feature_id} showIcon className="text-xs" />
          )}
        </div>

        <p className="text-sm text-text-secondary mt-1">{description}</p>

        {/* Extra data from parsed JSON */}
        <EventDataDetails event={event} />
      </div>
    </div>
  )
}

function EventDataDetails({ event }: { event: Event }) {
  if (!event.data) return null

  try {
    const data = JSON.parse(event.data)
    const details: { label: string; value: string; entityType?: 'feature' | 'cycle'; entityId?: string }[] = []

    if (data.cycle_type) details.push({ label: 'Cycle', value: data.cycle_type })
    if (data.milestone_id) details.push({ label: 'Milestone', value: data.milestone_id })
    if (data.score !== undefined) details.push({ label: 'Score', value: String(data.score) })
    if (data.iteration !== undefined) details.push({ label: 'Iteration', value: String(data.iteration) })

    if (details.length === 0) return null

    return (
      <div className="flex flex-wrap gap-x-4 gap-y-1 mt-1.5">
        {details.map((d, i) => (
          <span key={i} className="text-xs text-text-muted">
            <span className="text-text-muted/70">{d.label}:</span>{' '}
            <span className="text-text-secondary">{d.value}</span>
          </span>
        ))}
      </div>
    )
  } catch {
    return null
  }
}
