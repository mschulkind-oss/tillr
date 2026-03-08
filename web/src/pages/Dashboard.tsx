import { useQuery } from '@tanstack/react-query'
import { getStatus, getFeatures, getMilestones, getRoadmap, getHistory } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { Link } from 'react-router-dom'
import { formatTimeAgo, groupBy, cn } from '../lib/utils'
import type { Feature, RoadmapItem, Milestone, Event } from '../api/types'

const STATUS_ORDER = ['implementing', 'agent-qa', 'human-qa', 'draft', 'planning', 'blocked', 'done']

export function Dashboard() {
  const status = useQuery({ queryKey: ['status'], queryFn: getStatus })
  const features = useQuery({ queryKey: ['features'], queryFn: getFeatures })
  const milestones = useQuery({ queryKey: ['milestones'], queryFn: getMilestones })
  const roadmap = useQuery({ queryKey: ['roadmap'], queryFn: getRoadmap })
  const history = useQuery({ queryKey: ['history', { limit: 15 }], queryFn: () => getHistory({ limit: 15 }) })

  if (status.isLoading) return <PageSkeleton />

  const featureCounts = status.data?.feature_counts || {}
  const totalFeatures = Object.values(featureCounts).reduce((a, b) => a + b, 0)
  const featuresByStatus = groupBy(features.data || [], (f) => f.status)

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Dashboard</h1>
        <p className="text-sm text-text-secondary mt-1">
          {status.data?.project?.name || 'Project'} overview
        </p>
      </div>

      {/* Stats row */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard
          label="Total Features"
          value={totalFeatures}
          icon="✨"
        />
        <StatCard
          label="Done"
          value={featureCounts['done'] || 0}
          icon="✅"
          accent="text-success"
        />
        <StatCard
          label="In QA"
          value={(featureCounts['human-qa'] || 0) + (featureCounts['agent-qa'] || 0)}
          icon="🔍"
          accent="text-warning"
        />
        <StatCard
          label="Active Cycles"
          value={status.data?.active_cycles || 0}
          icon="🔄"
          accent="text-accent"
        />
      </div>

      {/* Kanban + sidebar */}
      <div className="grid grid-cols-1 xl:grid-cols-[1fr_340px] gap-6">
        {/* Kanban board */}
        <div>
          <h2 className="text-lg font-semibold text-text-primary mb-4">Feature Board</h2>
          <div className="grid grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
            {STATUS_ORDER.filter((s) => (featuresByStatus[s]?.length || 0) > 0).map((status) => (
              <KanbanColumn
                key={status}
                status={status}
                features={featuresByStatus[status] || []}
              />
            ))}
          </div>
        </div>

        {/* Right sidebar */}
        <div className="space-y-6">
          {/* Milestone progress */}
          <MilestonePanel milestones={milestones.data || []} />

          {/* Roadmap highlights */}
          <RoadmapHighlights items={roadmap.data || []} />

          {/* Recent activity */}
          <ActivityFeed events={history.data || []} />
        </div>
      </div>
    </div>
  )
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

function KanbanColumn({ status, features }: { status: string; features: Feature[] }) {
  return (
    <div className="bg-bg-secondary border border-border rounded-lg p-3 min-w-0">
      <div className="flex items-center justify-between mb-3">
        <StatusBadge status={status} />
        <span className="text-xs text-text-muted font-mono">{features.length}</span>
      </div>
      <div className="space-y-2 max-h-[400px] overflow-y-auto">
        {features.slice(0, 10).map((f) => (
          <Link
            key={f.id}
            to={`/features/${f.id}`}
            className="block bg-bg-card border border-border-light rounded-md p-2.5 hover:border-accent/40 transition-colors"
          >
            <div className="text-sm font-medium text-text-primary truncate">{f.name}</div>
            {f.milestone_name && (
              <div className="text-[10px] text-text-muted mt-1 truncate">
                {f.milestone_name}
              </div>
            )}
            <div className="flex items-center gap-2 mt-1.5">
              {f.priority > 0 && (
                <span className={cn(
                  'text-[10px] font-mono',
                  f.priority >= 8 ? 'text-danger' : f.priority >= 5 ? 'text-warning' : 'text-text-muted'
                )}>
                  P{f.priority}
                </span>
              )}
              {f.tags?.map((tag) => (
                <span key={tag} className="text-[10px] bg-bg-tertiary text-text-muted px-1 rounded">
                  {tag}
                </span>
              ))}
            </div>
          </Link>
        ))}
        {features.length > 10 && (
          <div className="text-xs text-text-muted text-center py-1">
            +{features.length - 10} more
          </div>
        )}
      </div>
    </div>
  )
}

function MilestonePanel({ milestones }: { milestones: Milestone[] }) {
  if (milestones.length === 0) return null

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4">
      <h3 className="text-sm font-semibold text-text-primary mb-3">Milestones</h3>
      <div className="space-y-3">
        {milestones.map((m) => {
          const pct = m.total_features ? Math.round((m.done_features || 0) / m.total_features * 100) : 0
          return (
            <div key={m.id}>
              <div className="flex items-center justify-between mb-1">
                <span className="text-sm text-text-primary">{m.name}</span>
                <span className="text-xs text-text-muted">{pct}%</span>
              </div>
              <div className="h-1.5 bg-bg-tertiary rounded-full overflow-hidden">
                <div
                  className="h-full bg-accent rounded-full transition-all duration-500"
                  style={{ width: `${pct}%` }}
                />
              </div>
              <div className="text-[10px] text-text-muted mt-0.5">
                {m.done_features || 0} / {m.total_features || 0} features
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function RoadmapHighlights({ items }: { items: RoadmapItem[] }) {
  const active = items.filter((i) => i.status === 'in-progress' || i.status === 'accepted').slice(0, 5)
  if (active.length === 0) return null

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-text-primary">Active Roadmap</h3>
        <Link to="/roadmap" className="text-xs text-accent hover:underline">View all →</Link>
      </div>
      <div className="space-y-2">
        {active.map((item) => (
          <Link
            key={item.id}
            to="/roadmap"
            className="block text-sm text-text-secondary hover:text-text-primary transition-colors"
          >
            <span className="mr-2">
              {item.priority === 'critical' ? '🔴' : item.priority === 'high' ? '🟠' : '🔵'}
            </span>
            {item.title}
          </Link>
        ))}
      </div>
    </div>
  )
}

function ActivityFeed({ events }: { events: Event[] }) {
  if (events.length === 0) return null

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-text-primary">Recent Activity</h3>
        <Link to="/history" className="text-xs text-accent hover:underline">View all →</Link>
      </div>
      <div className="space-y-2">
        {events.slice(0, 8).map((event) => (
          <div key={event.id} className="flex items-start gap-2 text-xs">
            <span className="text-text-muted shrink-0">{eventIcon(event.event_type)}</span>
            <div className="min-w-0 flex-1">
              <span className="text-text-secondary">
                {formatEventText(event)}
              </span>
              <span className="text-text-muted ml-1.5">
                {formatTimeAgo(event.created_at)}
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

function eventIcon(type: string): string {
  if (type.includes('status_changed')) return '🔀'
  if (type.includes('created')) return '➕'
  if (type.includes('cycle')) return '🔄'
  if (type.includes('qa')) return '✅'
  if (type.includes('idea')) return '💡'
  return '📌'
}

function formatEventText(event: Event): string {
  const type = event.event_type
  const feature = event.feature_id || ''

  if (type === 'feature.status_changed') {
    try {
      const data = JSON.parse(event.data || '{}')
      return `${feature} → ${data.to}`
    } catch {
      return `${feature} status changed`
    }
  }
  if (type === 'feature.created') return `${feature} created`
  if (type.includes('cycle')) return `Cycle event: ${feature}`
  return `${type.replace('feature.', '')}: ${feature}`
}
