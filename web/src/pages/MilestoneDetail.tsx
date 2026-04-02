import { useQuery } from '@tanstack/react-query'
import { getMilestone, getFeatures, getCycles } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp, cn } from '../lib/utils'

export function MilestoneDetail() {
  const { id } = useParams<{ id: string }>()

  const milestone = useQuery({
    queryKey: ['milestone', id],
    queryFn: () => getMilestone(id!),
    enabled: !!id,
  })

  const features = useQuery({
    queryKey: ['features'],
    queryFn: () => getFeatures(),
  })

  const cycles = useQuery({
    queryKey: ['cycles'],
    queryFn: () => getCycles(),
  })

  if (milestone.isLoading || features.isLoading) return <PageSkeleton />
  if (!milestone.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Milestone not found
      </div>
    )
  }

  const m = milestone.data
  const milestoneFeatures = (features.data || [])
    .filter((f) => f.milestone_id === id)
    .sort((a, b) => b.priority - a.priority)

  const milestoneCycles = (cycles.data || []).filter((c) =>
    milestoneFeatures.some((f) => f.id === c.entity_id),
  )

  const totalFeatures = milestoneFeatures.length
  const doneFeatures = milestoneFeatures.filter((f) => f.status === 'done').length
  const inProgressFeatures = milestoneFeatures.filter((f) => f.status === 'implementing').length
  const blockedFeatures = milestoneFeatures.filter((f) => f.status === 'blocked').length
  const progressPct = totalFeatures > 0 ? Math.round((doneFeatures / totalFeatures) * 100) : 0

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/dashboard" className="hover:text-accent transition-colors">Milestones</Link>
        <span>/</span>
        <span className="text-text-secondary">{m.name}</span>
      </nav>

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-text-primary">{m.name}</h1>
          {m.description && (
            <p className="text-sm text-text-secondary mt-2">{m.description}</p>
          )}
        </div>
        <StatusBadge status={m.status} />
      </div>

      {/* Progress bar */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <div className="flex items-center justify-between mb-2">
          <h2 className="text-sm font-semibold text-text-primary">Progress</h2>
          <span className="text-sm text-text-secondary">
            {doneFeatures} / {totalFeatures} features ({progressPct}%)
          </span>
        </div>
        <div className="w-full h-3 bg-bg-tertiary rounded-full overflow-hidden">
          <div
            className="h-full bg-success rounded-full transition-all duration-500"
            style={{ width: `${progressPct}%` }}
          />
        </div>
      </div>

      {/* Stats row */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <MetaItem label="Total Features" value={totalFeatures} />
        <MetaItem label="Done" value={
          <span className="text-success font-bold">{doneFeatures}</span>
        } />
        <MetaItem label="In Progress" value={
          <span className="text-accent font-bold">{inProgressFeatures}</span>
        } />
        <MetaItem label="Blocked" value={
          <span className={cn(blockedFeatures > 0 ? 'text-danger font-bold' : 'text-text-primary')}>
            {blockedFeatures}
          </span>
        } />
      </div>

      {/* Features table */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-3">
          Features ({totalFeatures})
        </h2>
        {milestoneFeatures.length === 0 ? (
          <p className="text-sm text-text-muted">No features in this milestone yet.</p>
        ) : (
          <div className="space-y-1">
            {/* Header */}
            <div className="grid grid-cols-[3rem_1fr_6rem_8rem] gap-3 px-3 py-2 text-[10px] text-text-muted uppercase tracking-wider">
              <span>Pri</span>
              <span>Name</span>
              <span>Status</span>
              <span>Updated</span>
            </div>
            {/* Rows */}
            {milestoneFeatures.map((f) => (
              <div
                key={f.id}
                className="grid grid-cols-[3rem_1fr_6rem_8rem] gap-3 px-3 py-2 rounded hover:bg-bg-secondary transition-colors items-center"
              >
                <span className={cn(
                  'font-mono text-sm font-bold',
                  f.priority >= 8 ? 'text-danger' : f.priority >= 5 ? 'text-warning' : 'text-text-primary',
                )}>
                  {f.priority}
                </span>
                <EntityLink type="feature" id={f.id} name={f.name} />
                <StatusBadge status={f.status} />
                <span className="text-xs text-text-muted truncate">{formatTimestamp(f.updated_at)}</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Active Cycles */}
      {milestoneCycles.length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">
            Cycles ({milestoneCycles.length})
          </h2>
          <div className="space-y-1">
            {milestoneCycles.map((c) => (
              <div
                key={c.id}
                className="flex items-center gap-3 px-3 py-2 rounded hover:bg-bg-secondary transition-colors"
              >
                <EntityLink type="cycle" id={c.id} name={`${c.cycle_type} #${c.iteration}`} />
                <span className="text-sm text-text-secondary">→</span>
                <EntityLink type="feature" id={c.entity_id} name={c.entity_id} />
                <span className="ml-auto">
                  <StatusBadge status={c.status} />
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Timestamps */}
      <div className="grid grid-cols-2 gap-4">
        <MetaItem label="Created" value={formatTimestamp(m.created_at)} />
        <MetaItem label="Updated" value={formatTimestamp(m.updated_at)} />
      </div>
    </div>
  )
}

function MetaItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="bg-bg-secondary border border-border-light rounded-lg p-3">
      <div className="text-[10px] text-text-muted uppercase tracking-wider mb-1">{label}</div>
      <div className="text-sm text-text-primary">{value}</div>
    </div>
  )
}
