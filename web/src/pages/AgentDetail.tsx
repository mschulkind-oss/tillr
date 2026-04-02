import { useQuery } from '@tanstack/react-query'
import { getAgentDetail } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp, cn } from '../lib/utils'

function formatDuration(secs: number): string {
  if (secs < 60) return `${Math.round(secs)}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s`
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  return `${h}h ${m}m`
}

function MetaItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="bg-bg-secondary border border-border-light rounded-lg p-3">
      <div className="text-[10px] text-text-muted uppercase tracking-wider mb-1">{label}</div>
      <div className="text-sm text-text-primary">{value}</div>
    </div>
  )
}

export function AgentDetail() {
  const { id } = useParams<{ id: string }>()

  const detail = useQuery({
    queryKey: ['agent-detail', id],
    queryFn: () => getAgentDetail(id!),
    enabled: !!id,
  })

  if (detail.isLoading) return <PageSkeleton />
  if (!detail.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Agent session not found
      </div>
    )
  }

  const { session: s, updates } = detail.data

  // Compute duration from created_at to now (or updated_at if completed)
  const startTime = new Date(s.created_at.includes('T') ? s.created_at : s.created_at.replace(' ', 'T') + 'Z')
  const endTime = s.status === 'completed' || s.status === 'failed'
    ? new Date(s.updated_at.includes('T') ? s.updated_at : s.updated_at.replace(' ', 'T') + 'Z')
    : new Date()
  const durationSecs = Math.max(0, (endTime.getTime() - startTime.getTime()) / 1000)

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/agents" className="hover:text-accent transition-colors">Agents</Link>
        <span>/</span>
        <span className="text-text-secondary">{s.name}</span>
      </nav>

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-text-primary">{s.name}</h1>
          {s.task_description && (
            <p className="text-sm text-text-secondary mt-2">{s.task_description}</p>
          )}
        </div>
        <StatusBadge status={s.status} />
      </div>

      {/* Progress bar */}
      <div>
        <div className="flex items-center justify-between text-xs text-text-muted mb-1">
          <span>{s.current_phase || 'Working'}</span>
          <span>{s.progress_pct}%</span>
        </div>
        <div className="h-2 bg-bg-tertiary rounded-full overflow-hidden">
          <div
            className={cn(
              'h-full rounded-full transition-all duration-500',
              s.status === 'active' ? 'bg-accent' :
                s.status === 'completed' ? 'bg-success' :
                  s.status === 'failed' ? 'bg-danger' : 'bg-warning',
            )}
            style={{ width: `${Math.min(s.progress_pct, 100)}%` }}
          />
        </div>
      </div>

      {/* Info grid */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {s.current_phase && <MetaItem label="Phase" value={s.current_phase} />}
        <MetaItem label="Duration" value={formatDuration(durationSecs)} />
        {s.eta && <MetaItem label="ETA" value={formatTimestamp(s.eta)} />}
        {s.feature_id && (
          <MetaItem label="Feature" value={
            <EntityLink type="feature" id={s.feature_id} showIcon />
          } />
        )}
        <MetaItem label="Created" value={formatTimestamp(s.created_at)} />
        <MetaItem label="Updated" value={formatTimestamp(s.updated_at)} />
      </div>

      {/* Status Updates timeline */}
      {updates && updates.length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-4">Status Updates</h2>
          <div className="space-y-0">
            {[...updates].reverse().map((update, i) => (
              <div key={update.id} className="relative flex gap-3 pb-4 last:pb-0">
                {/* Timeline connector */}
                {i < updates.length - 1 && (
                  <div className="absolute left-[7px] top-4 bottom-0 w-px bg-border" />
                )}
                {/* Dot */}
                <div className={cn(
                  'w-[15px] h-[15px] rounded-full border-2 shrink-0 mt-0.5',
                  update.phase ? 'bg-accent/20 border-accent' : 'bg-bg-tertiary border-border',
                )} />
                {/* Content */}
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2 flex-wrap mb-1">
                    {update.phase && (
                      <span className="text-xs bg-accent/10 text-accent px-2 py-0.5 rounded-full font-medium">
                        {update.phase}
                      </span>
                    )}
                    {update.progress_pct !== undefined && update.progress_pct !== null && (
                      <span className="text-[10px] text-text-muted font-mono">{update.progress_pct}%</span>
                    )}
                    <span className="text-[10px] text-text-muted ml-auto">
                      {formatTimestamp(update.created_at)}
                    </span>
                  </div>
                  <div className="text-sm text-text-secondary whitespace-pre-wrap">
                    {update.message_md}
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Empty updates state */}
      {(!updates || updates.length === 0) && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-2">Status Updates</h2>
          <p className="text-sm text-text-muted">No status updates yet.</p>
        </div>
      )}
    </div>
  )
}
