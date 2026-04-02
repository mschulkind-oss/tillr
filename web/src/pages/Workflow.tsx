import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getQueue, getAgentDashboard, reclaimStaleItems } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { formatTimeAgo, cn } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { AgentHeartbeatInfo } from '../api/types'

type StatusFilter = 'all' | 'pending' | 'claimed' | 'completed'
type SortField = 'priority' | 'created'

function formatDuration(secs: number): string {
  if (secs < 60) return `${Math.round(secs)}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${Math.round(secs % 60)}s`
  const h = Math.floor(secs / 3600)
  const m = Math.floor((secs % 3600) / 60)
  return `${h}h ${m}m`
}

function QueueStatCard({ label, value, icon, bg }: {
  label: string
  value: number
  icon: string
  bg: string
}) {
  return (
    <div className={cn('rounded-lg p-4 text-center', bg)}>
      <span className="text-2xl block mb-1">{icon}</span>
      <div className="text-2xl font-bold text-text-primary">{value}</div>
      <div className="text-xs text-text-secondary">{label}</div>
    </div>
  )
}

function ActiveAgentCard({ agent }: { agent: AgentHeartbeatInfo }) {
  const s = agent.session
  const isActive = agent.heartbeat_status === 'active'
  const isStale = agent.heartbeat_status === 'stale'

  return (
    <div className="bg-bg-secondary rounded-lg p-3">
      <div className="flex items-center justify-between mb-2">
        <div className="flex items-center gap-2 min-w-0">
          <span className={cn(
            'inline-block w-2 h-2 rounded-full shrink-0',
            isActive && 'bg-success animate-pulse',
            isStale && 'bg-warning',
            !isActive && !isStale && 'bg-danger',
          )} />
          <EntityLink type="agent" id={s.id} name={s.name} className="text-sm font-semibold truncate" />
        </div>
        <StatusBadge status={s.status} />
      </div>

      {/* Progress bar */}
      {s.progress_pct > 0 && (
        <div className="mb-2">
          <div className="flex items-center justify-between text-[10px] text-text-muted mb-1">
            <span>{s.current_phase || 'Working'}</span>
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
      )}

      {s.feature_id && (
        <div className="flex items-center gap-1.5 text-xs text-text-secondary mb-1">
          <span className="text-text-muted">Feature:</span>
          <EntityLink type="feature" id={s.feature_id} name={agent.feature_name} showIcon />
        </div>
      )}

      <div className="flex items-center justify-between text-[10px] text-text-muted mt-2 pt-2 border-t border-border-light">
        <span>⏱ {formatDuration(agent.session_duration_secs)}</span>
        <span className={cn(
          'px-1.5 py-0.5 rounded-full text-[10px]',
          isActive && 'bg-success/10 text-success',
          isStale && 'bg-warning/10 text-warning',
          !isActive && !isStale && 'bg-danger/10 text-danger',
        )}>
          {agent.heartbeat_status}
        </span>
      </div>
    </div>
  )
}

export function Workflow() {
  const queryClient = useQueryClient()
  const queue = useQuery({ queryKey: ['queue'], queryFn: getQueue })
  const dashboard = useQuery({ queryKey: ['agent-dashboard'], queryFn: getAgentDashboard })

  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [workTypeFilter, setWorkTypeFilter] = useState<string>('all')
  const [sortBy, setSortBy] = useState<SortField>('priority')
  const [reclaimMessage, setReclaimMessage] = useState<string | null>(null)

  const reclaim = useMutation({
    mutationFn: reclaimStaleItems,
    onSuccess: (data) => {
      setReclaimMessage(`Reclaimed ${data.reclaimed} stale item${data.reclaimed !== 1 ? 's' : ''}`)
      queryClient.invalidateQueries({ queryKey: ['queue'] })
      queryClient.invalidateQueries({ queryKey: ['agent-dashboard'] })
      setTimeout(() => setReclaimMessage(null), 4000)
    },
    onError: () => {
      setReclaimMessage('Failed to reclaim stale items')
      setTimeout(() => setReclaimMessage(null), 4000)
    },
  })

  const allEntries = queue.data?.queue || []
  const workTypes = useMemo(
    () => [...new Set(allEntries.map((e) => e.work_type))].sort(),
    [allEntries],
  )

  const filtered = useMemo(() => {
    let items = allEntries

    if (statusFilter !== 'all') {
      items = items.filter((e) => {
        if (statusFilter === 'pending') return e.status === 'pending'
        if (statusFilter === 'claimed') return e.status === 'active' || e.status === 'claimed'
        if (statusFilter === 'completed') return e.status === 'done' || e.status === 'completed'
        return true
      })
    }

    if (workTypeFilter !== 'all') {
      items = items.filter((e) => e.work_type === workTypeFilter)
    }

    items = [...items].sort((a, b) => {
      if (sortBy === 'priority') return b.priority - a.priority
      return b.created_at.localeCompare(a.created_at)
    })

    return items
  }, [allEntries, statusFilter, workTypeFilter, sortBy])

  if (queue.isLoading) return <PageSkeleton />

  const stats = queue.data?.stats
  const agents = dashboard.data?.agents || []
  const activeAgents = agents.filter(
    (a) => a.heartbeat_status === 'active' || a.heartbeat_status === 'stale',
  )

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-text-primary">Workflow Queue</h1>
          <p className="text-sm text-text-secondary mt-1">Work items and agent assignments</p>
        </div>
      </div>

      {/* Stats row */}
      <div className="flex flex-wrap items-start gap-4">
        <div className="flex-1 grid grid-cols-3 gap-4 min-w-[300px]">
          <QueueStatCard label="Pending" value={stats?.total_pending ?? 0} icon="⏳" bg="bg-warning/10" />
          <QueueStatCard label="Claimed" value={stats?.total_claimed ?? 0} icon="🔧" bg="bg-accent/10" />
          <QueueStatCard label="Completed Today" value={stats?.total_completed_today ?? 0} icon="✅" bg="bg-success/10" />
        </div>
        <div className="flex flex-col items-end gap-2">
          <button
            onClick={() => reclaim.mutate()}
            disabled={reclaim.isPending}
            className="bg-accent text-white px-4 py-2 rounded hover:bg-accent/80 disabled:opacity-50 text-sm whitespace-nowrap"
          >
            {reclaim.isPending ? 'Reclaiming…' : 'Reclaim Stale Items'}
          </button>
          {reclaimMessage && (
            <span className={cn(
              'text-xs px-2 py-1 rounded',
              reclaimMessage.includes('Failed') ? 'bg-danger/10 text-danger' : 'bg-success/10 text-success',
            )}>
              {reclaimMessage}
            </span>
          )}
        </div>
      </div>

      {/* Main content: table + agents sidebar */}
      <div className="flex flex-col xl:flex-row gap-6">
        {/* Work Queue Table */}
        <div className="flex-1 min-w-0">
          {/* Filters */}
          <div className="flex flex-wrap items-center gap-3 mb-4">
            <select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
              className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
            >
              <option value="all">All statuses ({allEntries.length})</option>
              <option value="pending">Pending</option>
              <option value="claimed">Claimed</option>
              <option value="completed">Completed</option>
            </select>

            <select
              value={workTypeFilter}
              onChange={(e) => setWorkTypeFilter(e.target.value)}
              className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
            >
              <option value="all">All work types</option>
              {workTypes.map((wt) => (
                <option key={wt} value={wt}>{wt}</option>
              ))}
            </select>

            <select
              value={sortBy}
              onChange={(e) => setSortBy(e.target.value as SortField)}
              className="bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary"
            >
              <option value="priority">Sort by priority</option>
              <option value="created">Sort by created</option>
            </select>
          </div>

          {/* Table */}
          {filtered.length > 0 ? (
            <div className="overflow-x-auto border border-border rounded-lg">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-border bg-bg-secondary">
                    <th className="text-left px-4 py-3 text-text-secondary font-medium">Feature</th>
                    <th className="text-left px-4 py-3 text-text-secondary font-medium">Work Type</th>
                    <th className="text-left px-4 py-3 text-text-secondary font-medium">Priority</th>
                    <th className="text-left px-4 py-3 text-text-secondary font-medium">Cycle</th>
                    <th className="text-left px-4 py-3 text-text-secondary font-medium">Agent</th>
                    <th className="text-left px-4 py-3 text-text-secondary font-medium">Status</th>
                    <th className="text-left px-4 py-3 text-text-secondary font-medium">Created</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((entry) => (
                    <tr key={entry.work_item_id} className="border-b border-border-light hover:bg-bg-secondary/50 transition-colors">
                      <td className="px-4 py-3">
                        <EntityLink type="feature" id={entry.feature_id} name={entry.feature_name} showIcon />
                      </td>
                      <td className="px-4 py-3">
                        <span className="inline-block bg-bg-tertiary text-text-secondary text-xs px-2 py-0.5 rounded">
                          {entry.work_type}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        <span className={cn(
                          'text-sm font-mono w-8 text-center rounded py-0.5 inline-block',
                          entry.priority >= 8 ? 'bg-danger/10 text-danger' :
                          entry.priority >= 5 ? 'bg-warning/10 text-warning' :
                          'bg-bg-tertiary text-text-muted',
                        )}>
                          {entry.priority}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-text-secondary text-xs">{entry.cycle_type}</td>
                      <td className="px-4 py-3">
                        {entry.assigned_agent ? (
                          <EntityLink type="agent" id={entry.assigned_agent} name={entry.assigned_agent} showIcon />
                        ) : (
                          <span className="text-text-muted text-xs">Unassigned</span>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge status={entry.status} />
                      </td>
                      <td className="px-4 py-3 text-text-muted text-xs whitespace-nowrap">
                        {formatTimeAgo(entry.created_at)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : allEntries.length === 0 ? (
            <div className="text-center py-16 text-text-muted">
              <span className="text-4xl block mb-3">⚡</span>
              <p className="text-sm">No work items in queue.</p>
              <p className="text-xs mt-1">
                Use <code className="bg-bg-tertiary px-1.5 py-0.5 rounded text-text-secondary">tillr cycle start</code> to begin a cycle.
              </p>
            </div>
          ) : (
            <div className="text-center py-12 text-text-muted text-sm">
              No items match your filters
            </div>
          )}
        </div>

        {/* Active Agents Sidebar */}
        <div className="xl:w-80 shrink-0">
          <h2 className="text-lg font-semibold text-text-primary mb-3">
            Active Agents
            {activeAgents.length > 0 && (
              <span className="text-xs text-text-muted font-normal ml-2">({activeAgents.length})</span>
            )}
          </h2>

          {dashboard.isLoading ? (
            <div className="text-sm text-text-muted">Loading agents…</div>
          ) : activeAgents.length > 0 ? (
            <div className="space-y-3">
              {activeAgents.map((a) => (
                <ActiveAgentCard key={a.session.id} agent={a} />
              ))}
            </div>
          ) : (
            <div className="text-center py-8 text-text-muted bg-bg-secondary rounded-lg">
              <span className="text-2xl block mb-2">🤖</span>
              <p className="text-xs">No active agents</p>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
