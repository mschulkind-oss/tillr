import { useQuery } from '@tanstack/react-query'
import { getHistory } from '../api/client'
import { EntityLink } from '../components/EntityLink'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { formatTimeAgo, formatTimestamp, cn, groupBy } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { Event } from '../api/types'

const LIMIT_OPTIONS = [50, 100, 200] as const

/* ── Event type visual config ─────────────────────────────────── */

const EVENT_STYLES: Record<string, { accent: string; dot: string; icon: string; label: string }> = {
  'feature.created':        { accent: 'text-green-400',  dot: 'bg-green-400',  icon: '+',  label: 'Created' },
  'feature_created':        { accent: 'text-green-400',  dot: 'bg-green-400',  icon: '+',  label: 'Created' },
  'feature.status_changed': { accent: 'text-yellow-400', dot: 'bg-yellow-400', icon: '~',  label: 'Status Changed' },
  'feature_status_changed': { accent: 'text-yellow-400', dot: 'bg-yellow-400', icon: '~',  label: 'Status Changed' },
  'qa_approved':            { accent: 'text-green-400',  dot: 'bg-green-400',  icon: 'ok', label: 'QA Approved' },
  'qa.approved':            { accent: 'text-green-400',  dot: 'bg-green-400',  icon: 'ok', label: 'QA Approved' },
  'qa_rejected':            { accent: 'text-red-400',    dot: 'bg-red-400',    icon: 'x',  label: 'QA Rejected' },
  'qa.rejected':            { accent: 'text-red-400',    dot: 'bg-red-400',    icon: 'x',  label: 'QA Rejected' },
  'cycle_started':          { accent: 'text-purple-400', dot: 'bg-purple-400', icon: '>',  label: 'Cycle Started' },
  'cycle.started':          { accent: 'text-purple-400', dot: 'bg-purple-400', icon: '>',  label: 'Cycle Started' },
  'cycle_completed':        { accent: 'text-purple-400', dot: 'bg-purple-400', icon: '#',  label: 'Cycle Completed' },
  'cycle.completed':        { accent: 'text-purple-400', dot: 'bg-purple-400', icon: '#',  label: 'Cycle Completed' },
  'workstream.created':     { accent: 'text-teal-400',   dot: 'bg-teal-400',   icon: '+',  label: 'Workstream Created' },
  'workstream_created':     { accent: 'text-teal-400',   dot: 'bg-teal-400',   icon: '+',  label: 'Workstream Created' },
}

const DEFAULT_STYLE = { accent: 'text-text-muted', dot: 'bg-text-muted', icon: '*', label: '' }

function getEventStyle(eventType: string) {
  return EVENT_STYLES[eventType] ?? DEFAULT_STYLE
}

function fallbackLabel(eventType: string): string {
  return eventType
    .replace(/^feature\./, '')
    .replace(/^qa\./, 'qa_')
    .replace(/^cycle\./, 'cycle_')
    .replace(/_/g, ' ')
}

/* ── Data parsing ─────────────────────────────────────────────── */

interface ParsedEventData {
  from?: string
  to?: string
  name?: string
  notes?: string
  cycleType?: string
  milestoneId?: string
  score?: string
  iteration?: string
  raw: Record<string, unknown>
}

function parseEventData(event: Event): ParsedEventData {
  const empty: ParsedEventData = { raw: {} }
  if (!event.data) return empty

  try {
    const data = JSON.parse(event.data)
    return {
      from: data.old_status || data.from,
      to: data.new_status || data.to,
      name: data.name || data.feature_name,
      notes: data.notes,
      cycleType: data.cycle_type,
      milestoneId: data.milestone_id,
      score: data.score !== undefined ? String(data.score) : undefined,
      iteration: data.iteration !== undefined ? String(data.iteration) : undefined,
      raw: data,
    }
  } catch {
    return empty
  }
}

/* ── Day grouping ─────────────────────────────────────────────── */

function dayKey(dateStr: string): string {
  const d = new Date(dateStr.includes('T') ? dateStr : dateStr.replace(' ', 'T') + 'Z')
  if (isNaN(d.getTime())) return 'Unknown'
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  const eventDay = new Date(d.getFullYear(), d.getMonth(), d.getDate())
  const diffDays = Math.floor((today.getTime() - eventDay.getTime()) / (1000 * 60 * 60 * 24))
  if (diffDays === 0) return 'Today'
  if (diffDays === 1) return 'Yesterday'
  if (diffDays < 7) return d.toLocaleDateString('en-US', { weekday: 'long' })
  return d.toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: now.getFullYear() !== d.getFullYear() ? 'numeric' : undefined })
}

/* ── Accent bar color per event type ──────────────────────────── */

function accentBorderClass(eventType: string): string {
  const t = eventType
  if (t.includes('created') && t.startsWith('feature')) return 'border-l-green-400'
  if (t.includes('status_changed')) return 'border-l-yellow-400'
  if (t.includes('qa') && t.includes('approved')) return 'border-l-green-400'
  if (t.includes('qa') && t.includes('rejected')) return 'border-l-red-400'
  if (t.includes('cycle')) return 'border-l-purple-400'
  if (t.includes('workstream')) return 'border-l-teal-400'
  return 'border-l-border'
}

/* ── Main component ───────────────────────────────────────────── */

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

  const dayGroups = useMemo(() => {
    if (!events) return []
    const grouped = groupBy(events, (e) => dayKey(e.created_at))
    return Object.entries(grouped)
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
              {uniqueTypes.map((t) => {
                const s = EVENT_STYLES[t]
                return (
                  <option key={t} value={t}>
                    {s?.label || fallbackLabel(t)}
                  </option>
                )
              })}
            </select>
          </div>

          {/* Feature filter */}
          <div className="flex flex-col gap-1">
            <label className="text-xs text-text-muted">Feature ID</label>
            <input
              type="text"
              value={featureFilter}
              onChange={(e) => setFeatureFilter(e.target.value)}
              placeholder="Filter by feature..."
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

      {/* Timeline grouped by day */}
      {displayEvents.length === 0 ? (
        <div className="bg-bg-card border border-border rounded-lg p-12 text-center">
          <p className="text-text-secondary text-sm">No events found</p>
          <p className="text-text-muted text-xs mt-1">
            {typeFilter || featureFilter
              ? 'Try adjusting your filters'
              : 'Events will appear here as you work on the project'}
          </p>
        </div>
      ) : (
        <div className="space-y-6">
          {dayGroups.map(([day, dayEvents]) => (
            <div key={day}>
              {/* Day header */}
              <div className="flex items-center gap-3 mb-3">
                <h2 className="text-sm font-semibold text-text-secondary whitespace-nowrap">{day}</h2>
                <div className="flex-1 h-px bg-border" />
                <span className="text-xs text-text-muted">{dayEvents.length} event{dayEvents.length !== 1 ? 's' : ''}</span>
              </div>

              {/* Events for this day */}
              <div className="space-y-2">
                {dayEvents.map((event) => (
                  <EventCard key={event.id} event={event} />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Load more */}
      {displayEvents.length >= limit && (
        <div className="text-center">
          <button
            onClick={() => setLimit((prev) => prev + 50)}
            className="px-4 py-2 rounded text-sm bg-bg-secondary text-text-secondary border border-border hover:border-accent/50 hover:text-accent transition-colors"
          >
            Load more events...
          </button>
        </div>
      )}
    </div>
  )
}

/* ── Event card ───────────────────────────────────────────────── */

function EventCard({ event }: { event: Event }) {
  const style = getEventStyle(event.event_type)
  const parsed = parseEventData(event)
  const eventType = event.event_type
  const isStatusChange = eventType === 'feature.status_changed' || eventType === 'feature_status_changed'
  const isFeatureCreated = eventType === 'feature.created' || eventType === 'feature_created'
  const isWorkstreamCreated = eventType === 'workstream.created' || eventType === 'workstream_created'

  return (
    <div
      className={cn(
        'bg-bg-card border border-border rounded-lg pl-3 pr-4 py-3 border-l-4 transition-colors hover:border-border/80',
        accentBorderClass(eventType)
      )}
    >
      <div className="flex items-start gap-3">
        {/* Timeline dot */}
        <div className="shrink-0 mt-1">
          <div className={cn('w-2.5 h-2.5 rounded-full', style.dot)} />
        </div>

        {/* Main content */}
        <div className="flex-1 min-w-0">
          {/* First row: type label + feature link + timestamp */}
          <div className="flex items-center gap-2 flex-wrap">
            <span className={cn('text-xs font-semibold uppercase tracking-wide', style.accent)}>
              {style.label || fallbackLabel(eventType)}
            </span>

            {event.feature_id && (
              <EntityLink
                type="feature"
                id={event.feature_id}
                name={isFeatureCreated && parsed.name ? parsed.name : undefined}
                showIcon
                className="text-sm font-medium"
              />
            )}

            <span className="ml-auto text-xs text-text-muted shrink-0" title={formatTimestamp(event.created_at)}>
              {formatTimeAgo(event.created_at)}
            </span>
          </div>

          {/* Second row: event-specific content */}
          <div className="mt-1.5">
            {isStatusChange && (parsed.from || parsed.to) ? (
              <div className="flex items-center gap-2 flex-wrap">
                {parsed.from && <StatusBadge status={parsed.from} />}
                {parsed.from && parsed.to && (
                  <span className="text-text-muted text-sm">&rarr;</span>
                )}
                {parsed.to && <StatusBadge status={parsed.to} />}
              </div>
            ) : isFeatureCreated ? (
              <p className="text-sm text-text-secondary">
                {parsed.name
                  ? <>New feature: <span className="text-text-primary font-medium">{parsed.name}</span></>
                  : 'New feature created'}
              </p>
            ) : isWorkstreamCreated ? (
              <p className="text-sm text-text-secondary">
                {parsed.name
                  ? <>New workstream: <span className="text-text-primary font-medium">{parsed.name}</span></>
                  : 'New workstream created'}
              </p>
            ) : (
              <EventDescription event={event} parsed={parsed} />
            )}
          </div>

          {/* Metadata chips */}
          <EventMetadata parsed={parsed} />
        </div>
      </div>
    </div>
  )
}

/* ── Fallback description for other event types ───────────────── */

function EventDescription({ event, parsed }: { event: Event; parsed: ParsedEventData }) {
  const t = event.event_type

  if ((t === 'qa_approved' || t === 'qa.approved') && parsed.notes) {
    return <p className="text-sm text-text-secondary">{parsed.notes}</p>
  }
  if ((t === 'qa_rejected' || t === 'qa.rejected') && parsed.notes) {
    return <p className="text-sm text-text-secondary">{parsed.notes}</p>
  }
  if ((t === 'cycle_started' || t === 'cycle.started') && parsed.cycleType) {
    return <p className="text-sm text-text-secondary">Cycle type: <span className="text-text-primary">{parsed.cycleType}</span></p>
  }
  if ((t === 'cycle_completed' || t === 'cycle.completed') && parsed.cycleType) {
    return <p className="text-sm text-text-secondary">Cycle type: <span className="text-text-primary">{parsed.cycleType}</span></p>
  }

  return null
}

/* ── Extra metadata chips ─────────────────────────────────────── */

function EventMetadata({ parsed }: { parsed: ParsedEventData }) {
  const chips: { label: string; value: string }[] = []
  if (parsed.cycleType) chips.push({ label: 'Cycle', value: parsed.cycleType })
  if (parsed.milestoneId) chips.push({ label: 'Milestone', value: parsed.milestoneId })
  if (parsed.score) chips.push({ label: 'Score', value: parsed.score })
  if (parsed.iteration) chips.push({ label: 'Iteration', value: parsed.iteration })

  if (chips.length === 0) return null

  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1 mt-2">
      {chips.map((c, i) => (
        <span key={i} className="text-xs text-text-muted">
          <span className="text-text-muted/70">{c.label}:</span>{' '}
          <span className="text-text-secondary">{c.value}</span>
        </span>
      ))}
    </div>
  )
}
