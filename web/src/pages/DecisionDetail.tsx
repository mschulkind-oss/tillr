import { useQuery } from '@tanstack/react-query'
import { getDecision } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp } from '../lib/utils'

export function DecisionDetail() {
  const { id } = useParams<{ id: string }>()

  const decision = useQuery({
    queryKey: ['decision', id],
    queryFn: () => getDecision(id!),
    enabled: !!id,
  })

  if (decision.isLoading) return <PageSkeleton />
  if (!decision.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Decision not found
      </div>
    )
  }

  const d = decision.data

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/decisions" className="hover:text-accent transition-colors">Decisions</Link>
        <span>/</span>
        <span className="text-text-secondary">{d.title}</span>
      </nav>

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <h1 className="text-2xl font-bold text-text-primary min-w-0">{d.title}</h1>
        <StatusBadge status={d.status} />
      </div>

      {/* Feature link */}
      {d.feature_id && (
        <div className="flex items-center gap-2 text-sm">
          <span className="text-text-muted">Feature:</span>
          <EntityLink type="feature" id={d.feature_id} showIcon />
        </div>
      )}

      {/* Supersedes chain */}
      {d.superseded_by && (
        <div className="flex items-center gap-2 text-sm bg-bg-secondary border border-border rounded-lg p-3">
          <span className="text-text-muted">⚠️ Superseded by:</span>
          <EntityLink type="decision" id={d.superseded_by} showIcon />
        </div>
      )}

      {/* Context */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-3">Context</h2>
        <div className="text-sm text-text-secondary whitespace-pre-wrap">{d.context}</div>
      </div>

      {/* Decision */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-3">Decision</h2>
        <div className="text-sm text-text-secondary whitespace-pre-wrap">{d.decision}</div>
      </div>

      {/* Consequences */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-3">Consequences</h2>
        <div className="text-sm text-text-secondary whitespace-pre-wrap">{d.consequences}</div>
      </div>

      {/* Metadata */}
      <div className="grid grid-cols-2 gap-4">
        <MetaItem label="Created" value={formatTimestamp(d.created_at)} />
        <MetaItem label="Updated" value={formatTimestamp(d.updated_at)} />
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
