import { useQuery } from '@tanstack/react-query'
import { getRoadmapItem, getFeatures } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp, cn } from '../lib/utils'

const priorityColors: Record<string, string> = {
  critical: 'bg-danger/20 text-danger',
  high: 'bg-orange/20 text-orange',
  medium: 'bg-warning/20 text-warning',
  low: 'bg-accent/20 text-accent',
  'nice-to-have': 'bg-bg-tertiary text-text-secondary',
}

const effortLabels: Record<string, string> = {
  xs: 'XS',
  s: 'S',
  m: 'M',
  l: 'L',
  xl: 'XL',
}

export function RoadmapDetail() {
  const { id } = useParams<{ id: string }>()

  const roadmapItem = useQuery({
    queryKey: ['roadmap-item', id],
    queryFn: () => getRoadmapItem(id!),
    enabled: !!id,
  })

  const features = useQuery({
    queryKey: ['features'],
    queryFn: () => getFeatures(),
  })

  if (roadmapItem.isLoading || features.isLoading) return <PageSkeleton />
  if (!roadmapItem.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Roadmap item not found
      </div>
    )
  }

  const item = roadmapItem.data
  const linkedFeatures = (features.data || []).filter(
    (f) => f.roadmap_item_id === id,
  )

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/roadmap" className="hover:text-accent transition-colors">Roadmap</Link>
        <span>/</span>
        <span className="text-text-secondary">{item.title}</span>
      </nav>

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-text-primary">{item.title}</h1>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <PriorityBadge priority={item.priority} />
          <StatusBadge status={item.status} />
        </div>
      </div>

      {/* Badges row */}
      <div className="flex items-center gap-2 flex-wrap">
        {item.category && (
          <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-purple/20 text-purple">
            {item.category}
          </span>
        )}
        {item.effort && (
          <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-accent/10 text-accent">
            Effort: {effortLabels[item.effort] || item.effort}
          </span>
        )}
      </div>

      {/* Description */}
      {item.description && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Description</h2>
          <p className="text-sm text-text-secondary whitespace-pre-wrap">{item.description}</p>
        </div>
      )}

      {/* Metadata grid */}
      <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
        <MetaItem label="Priority" value={
          <span className={cn(
            'font-medium',
            item.priority === 'critical' ? 'text-danger' :
            item.priority === 'high' ? 'text-orange' :
            'text-text-primary',
          )}>
            {item.priority}
          </span>
        } />
        <MetaItem label="Effort" value={effortLabels[item.effort] || item.effort || '—'} />
        <MetaItem label="Category" value={item.category || '—'} />
        <MetaItem label="Status" value={<StatusBadge status={item.status} />} />
        <MetaItem label="Created" value={formatTimestamp(item.created_at)} />
        <MetaItem label="Updated" value={formatTimestamp(item.updated_at)} />
      </div>

      {/* Linked Features */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-3">
          Linked Features ({linkedFeatures.length})
        </h2>
        {linkedFeatures.length === 0 ? (
          <p className="text-sm text-text-muted">No features linked to this roadmap item yet.</p>
        ) : (
          <div className="space-y-1">
            {/* Header */}
            <div className="grid grid-cols-[1fr_6rem_8rem_3rem] gap-3 px-3 py-2 text-[10px] text-text-muted uppercase tracking-wider">
              <span>Name</span>
              <span>Status</span>
              <span>Milestone</span>
              <span>Pri</span>
            </div>
            {/* Rows */}
            {linkedFeatures.map((f) => (
              <div
                key={f.id}
                className="grid grid-cols-[1fr_6rem_8rem_3rem] gap-3 px-3 py-2 rounded hover:bg-bg-secondary transition-colors items-center"
              >
                <EntityLink type="feature" id={f.id} name={f.name} />
                <StatusBadge status={f.status} />
                <span className="text-sm">
                  {f.milestone_id ? (
                    <EntityLink type="milestone" id={f.milestone_id} name={f.milestone_name || f.milestone_id} />
                  ) : (
                    <span className="text-text-muted">—</span>
                  )}
                </span>
                <span className={cn(
                  'font-mono text-sm font-bold',
                  f.priority >= 8 ? 'text-danger' : f.priority >= 5 ? 'text-warning' : 'text-text-primary',
                )}>
                  {f.priority}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function PriorityBadge({ priority }: { priority: string }) {
  const classes = priorityColors[priority] || 'bg-bg-tertiary text-text-secondary'
  return (
    <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${classes}`}>
      {priority}
    </span>
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
