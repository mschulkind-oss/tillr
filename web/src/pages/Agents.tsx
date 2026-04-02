import { useQuery } from '@tanstack/react-query'
import { getAgentDashboard } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { Link } from 'react-router-dom'
import { formatTimeAgo, cn } from '../lib/utils'
import type { AgentHeartbeatInfo } from '../api/types'

function formatDuration(secs: number): string {
  if (secs < 60) return `${Math.round(secs)}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s`
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  return `${h}h ${m}m`
}

function StatCard({ label, value, icon, accent }: {
  label: string
  value: number
  icon: string
  accent?: string
}) {
  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 flex items-center gap-3">
      <span className="text-2xl">{icon}</span>
      <div>
        <div className={cn('text-2xl font-bold', accent || 'text-text-primary')}>
          {value}
        </div>
        <div className="text-xs text-text-secondary">{label}</div>
      </div>
    </div>
  )
}

function AgentCard({ agent }: { agent: AgentHeartbeatInfo }) {
  const s = agent.session
  const isActive = agent.heartbeat_status === 'active'
  const isStale = agent.heartbeat_status === 'stale'

  return (
    <Link
      to={`/agents/${s.id}`}
      className="block bg-bg-card border border-border rounded-lg p-4 hover:border-accent/40 transition-colors"
    >
      {/* Header row */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 min-w-0">
          <span className={cn(
            'inline-block w-2 h-2 rounded-full shrink-0',
            isActive && 'bg-success animate-pulse',
            isStale && 'bg-warning',
            !isActive && !isStale && 'bg-danger',
          )} />
          <span className="text-sm font-semibold text-text-primary truncate">{s.name}</span>
        </div>
        <StatusBadge status={s.status} />
      </div>

      {/* Progress bar */}
      <div className="mb-3">
        <div className="flex items-center justify-between text-[10px] text-text-muted mb-1">
          <span>{agent.session.current_phase || 'Working'}</span>
          <span>{s.progress_pct}%</span>
        </div>
        <div className="h-1.5 bg-bg-tertiary rounded-full overflow-hidden">
          <div
            className={cn(
              'h-full rounded-full transition-all duration-500',
              isActive ? 'bg-accent' : isStale ? 'bg-warning' : 'bg-danger',
            )}
            style={{ width: `${Math.min(s.progress_pct, 100)}%` }}
          />
        </div>
      </div>

      {/* Feature & work item */}
      {s.feature_id && (
        <div className="flex items-center gap-1.5 text-xs text-text-secondary mb-2">
          <span className="text-text-muted">Feature:</span>
          <EntityLink type="feature" id={s.feature_id} name={agent.feature_name} showIcon />
        </div>
      )}

      {agent.current_work_item && (
        <div className="text-xs text-text-muted mb-2">
          Work: <span className="text-text-secondary">{agent.current_work_item.work_type}</span>
        </div>
      )}

      {/* Footer stats */}
      <div className="flex items-center justify-between text-[10px] text-text-muted mt-2 pt-2 border-t border-border-light">
        <span>⏱ {formatDuration(agent.session_duration_secs)}</span>
        {s.eta && <span>ETA: {formatTimeAgo(s.eta)}</span>}
        <div className="flex items-center gap-2">
          {agent.completed_count > 0 && (
            <span className="text-success">✓ {agent.completed_count}</span>
          )}
          {agent.failed_count > 0 && (
            <span className="text-danger">✗ {agent.failed_count}</span>
          )}
        </div>
      </div>
    </Link>
  )
}

export function Agents() {
  const dashboard = useQuery({
    queryKey: ['agent-dashboard'],
    queryFn: getAgentDashboard,
  })

  if (dashboard.isLoading) return <PageSkeleton />

  const data = dashboard.data
  const agents = data?.agents || []
  const active = agents.filter((a) => a.heartbeat_status === 'active')
  const stale = agents.filter((a) => a.heartbeat_status === 'stale')
  const recent = agents.filter((a) =>
    a.session.status === 'completed' || a.session.status === 'failed'
  )

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Agents</h1>
        <p className="text-sm text-text-secondary mt-1">Agent sessions and activity</p>
      </div>

      {/* Stats row */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard label="Active" value={data?.active_count ?? 0} icon="🟢" accent="text-success" />
        <StatCard label="Stale" value={data?.stale_count ?? 0} icon="🟡" accent="text-warning" />
        <StatCard label="Failed" value={data?.failed_count ?? 0} icon="🔴" accent="text-danger" />
        <StatCard label="Completed" value={data?.completed_count ?? 0} icon="✅" accent="text-success" />
      </div>

      {/* Active agents */}
      {active.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-text-primary mb-3">Active Agents</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {active.map((a) => (
              <AgentCard key={a.session.id} agent={a} />
            ))}
          </div>
        </div>
      )}

      {/* Stale agents */}
      {stale.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-text-primary mb-3 flex items-center gap-2">
            Stale Agents
            <span className="text-xs text-text-muted font-normal">(no recent heartbeat)</span>
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {stale.map((a) => (
              <AgentCard key={a.session.id} agent={a} />
            ))}
          </div>
        </div>
      )}

      {/* Recent completed/failed */}
      {recent.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-text-primary mb-3">Recent Completed</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {recent.map((a) => (
              <AgentCard key={a.session.id} agent={a} />
            ))}
          </div>
        </div>
      )}

      {/* Empty state */}
      {agents.length === 0 && (
        <div className="text-center py-16 text-text-muted">
          <span className="text-4xl block mb-3">🤖</span>
          <p className="text-sm">No active agents.</p>
          <p className="text-xs mt-1">
            Use <code className="bg-bg-tertiary px-1.5 py-0.5 rounded text-text-secondary">tillr agent start</code> to begin a session.
          </p>
        </div>
      )}
    </div>
  )
}
