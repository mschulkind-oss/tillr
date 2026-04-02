import { useQuery } from '@tanstack/react-query'
import { getStats, getBurndown, getActivityHeatmap, getAnalyticsHeatmap } from '../api/client'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { cn } from '../lib/utils'
import { useMemo, useState } from 'react'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  BarChart,
  Bar,
} from 'recharts'
import type { StatsResponse, BurndownPoint, WeekVelocity, ActivityDayCount, HeatmapGrid } from '../api/types'

const STATUS_COLORS: Record<string, string> = {
  done: 'bg-success',
  'human-qa': 'bg-warning',
  'agent-qa': 'bg-orange',
  implementing: 'bg-accent',
  planning: 'bg-purple',
  draft: 'bg-bg-tertiary',
  blocked: 'bg-danger',
}

const STATUS_ORDER = ['done', 'implementing', 'agent-qa', 'human-qa', 'planning', 'draft', 'blocked']

const PRIORITY_COLORS: Record<string, string> = {
  critical: 'bg-danger',
  high: 'bg-warning',
  medium: 'bg-accent',
  low: 'bg-bg-tertiary',
  'nice-to-have': 'bg-purple',
}

const PRIORITY_ORDER = ['critical', 'high', 'medium', 'low', 'nice-to-have']

const ROADMAP_STATUS_COLORS: Record<string, string> = {
  done: 'bg-success',
  'in-progress': 'bg-accent',
  accepted: 'bg-warning',
  proposed: 'bg-bg-tertiary',
  deferred: 'bg-purple',
  rejected: 'bg-danger',
}

const ROADMAP_STATUS_ORDER = ['done', 'in-progress', 'accepted', 'proposed', 'deferred', 'rejected']

export function Stats() {
  const stats = useQuery({ queryKey: ['stats'], queryFn: getStats })
  const burndown = useQuery({ queryKey: ['burndown'], queryFn: getBurndown })
  const heatmap = useQuery({ queryKey: ['activity-heatmap'], queryFn: () => getActivityHeatmap(365) })
  const heatGrid = useQuery({ queryKey: ['analytics-heatmap'], queryFn: getAnalyticsHeatmap })

  if (stats.isLoading) return <PageSkeleton />

  const data = stats.data
  if (!data) return <div className="text-text-muted text-center py-12">No stats available</div>

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Stats</h1>
        <p className="text-sm text-text-secondary mt-1">
          Project analytics and insights
        </p>
      </div>

      {/* KPI Row */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <CompletionCard rate={data.feature_stats.completion_rate} />
        <KPICard
          label="Total Features"
          value={data.feature_stats.total}
          icon="📦"
        />
        <KPICard
          label="Total Cycles"
          value={data.cycle_stats.total_cycles}
          icon="🔄"
          accent="text-accent"
        />
        <ScoreCard score={data.cycle_stats.avg_score} />
      </div>

      {/* Feature Distribution + Milestone Progress */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <FeatureDistribution stats={data} />
        <MilestoneProgress milestones={data.milestone_stats} />
      </div>

      {/* Activity Heatmap */}
      <ActivityHeatmap data={heatmap.data} isLoading={heatmap.isLoading} />

      {/* Day/Hour Heatmap */}
      <DayHourHeatmap data={heatGrid.data} isLoading={heatGrid.isLoading} />

      {/* Burndown + Velocity */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <BurndownChart data={burndown.data?.points} isLoading={burndown.isLoading} />
        <VelocityChart data={burndown.data?.velocity} isLoading={burndown.isLoading} />
      </div>

      {/* Roadmap Distribution */}
      <RoadmapDistribution stats={data} />

      {/* Activity Summary */}
      <ActivitySummary activity={data.activity} />
    </div>
  )
}

/* ── KPI Cards ─────────────────────────────────────────── */

function CompletionCard({ rate }: { rate: number }) {
  const pct = Math.round(rate)
  const radius = 36
  const circumference = 2 * Math.PI * radius
  const offset = circumference - (pct / 100) * circumference
  const color = pct >= 75 ? 'text-success' : pct >= 40 ? 'text-warning' : 'text-danger'
  const strokeColor = pct >= 75 ? 'var(--color-success)' : pct >= 40 ? 'var(--color-warning)' : 'var(--color-danger)'

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 flex items-center gap-4">
      <div className="relative w-20 h-20 shrink-0">
        <svg className="w-full h-full -rotate-90" viewBox="0 0 80 80">
          <circle
            cx="40" cy="40" r={radius}
            fill="none"
            stroke="var(--color-border)"
            strokeWidth="6"
          />
          <circle
            cx="40" cy="40" r={radius}
            fill="none"
            stroke={strokeColor}
            strokeWidth="6"
            strokeLinecap="round"
            strokeDasharray={circumference}
            strokeDashoffset={offset}
            className="transition-all duration-700"
          />
        </svg>
        <div className="absolute inset-0 flex items-center justify-center">
          <span className={cn('text-lg font-bold', color)}>{pct}%</span>
        </div>
      </div>
      <div>
        <div className="text-xs text-text-secondary">Completion Rate</div>
        <div className="text-[10px] text-text-muted mt-0.5">Features done</div>
      </div>
    </div>
  )
}

function KPICard({ label, value, icon, accent }: {
  label: string; value: number; icon: string; accent?: string
}) {
  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 flex items-center gap-3">
      <span className="text-2xl">{icon}</span>
      <div>
        <div className={cn('text-2xl font-bold', accent || 'text-text-primary')}>{value}</div>
        <div className="text-xs text-text-secondary">{label}</div>
      </div>
    </div>
  )
}

function ScoreCard({ score }: { score: number }) {
  const display = score > 0 ? score.toFixed(1) : '—'
  const color = score >= 7 ? 'text-success' : score >= 5 ? 'text-warning' : score > 0 ? 'text-danger' : 'text-text-muted'

  return (
    <div className="bg-bg-card border border-border rounded-lg p-4 flex items-center gap-3">
      <span className="text-2xl">⭐</span>
      <div>
        <div className={cn('text-2xl font-bold', color)}>{display}</div>
        <div className="text-xs text-text-secondary">Avg Cycle Score</div>
      </div>
    </div>
  )
}

/* ── Feature Distribution ──────────────────────────────── */

function FeatureDistribution({ stats }: { stats: StatsResponse }) {
  const byStatus = stats.feature_stats.by_status
  const total = stats.feature_stats.total

  if (total === 0) {
    return (
      <div className="bg-bg-card border border-border rounded-lg p-6">
        <h2 className="text-sm font-semibold text-text-primary mb-4">Feature Distribution</h2>
        <p className="text-sm text-text-muted">No features yet</p>
      </div>
    )
  }

  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Feature Distribution</h2>

      {/* Stacked bar */}
      <div className="flex h-6 rounded-lg overflow-hidden bg-bg-tertiary mb-4">
        {STATUS_ORDER.map((status) => {
          const count = byStatus[status] || 0
          if (count === 0) return null
          const pct = (count / total) * 100
          return (
            <div
              key={status}
              className={cn(STATUS_COLORS[status], 'transition-all duration-500 relative group')}
              style={{ width: `${pct}%` }}
              title={`${status}: ${count} (${pct.toFixed(0)}%)`}
            />
          )
        })}
      </div>

      {/* Legend */}
      <div className="grid grid-cols-2 sm:grid-cols-3 gap-2">
        {STATUS_ORDER.map((status) => {
          const count = byStatus[status] || 0
          if (count === 0) return null
          const pct = ((count / total) * 100).toFixed(0)
          return (
            <div key={status} className="flex items-center gap-2 text-xs">
              <div className={cn('w-2.5 h-2.5 rounded-sm shrink-0', STATUS_COLORS[status])} />
              <span className="text-text-secondary capitalize">{status}</span>
              <span className="text-text-muted ml-auto font-mono">{count}</span>
              <span className="text-text-muted font-mono">({pct}%)</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

/* ── Milestone Progress ────────────────────────────────── */

function MilestoneProgress({ milestones }: { milestones: StatsResponse['milestone_stats'] }) {
  if (milestones.length === 0) {
    return (
      <div className="bg-bg-card border border-border rounded-lg p-6">
        <h2 className="text-sm font-semibold text-text-primary mb-4">Milestone Progress</h2>
        <p className="text-sm text-text-muted">No milestones yet</p>
      </div>
    )
  }

  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Milestone Progress</h2>
      <div className="space-y-4">
        {milestones.map((m) => {
          const pct = Math.round(m.progress)
          return (
            <div key={m.name}>
              <div className="flex items-center justify-between mb-1.5">
                <EntityLink type="milestone" id={m.name} name={m.name} />
                <span className={cn(
                  'text-xs font-mono font-semibold',
                  pct === 100 ? 'text-success' : pct >= 50 ? 'text-accent' : 'text-text-muted'
                )}>
                  {pct}%
                </span>
              </div>
              <div className="h-2 bg-bg-tertiary rounded-full overflow-hidden">
                <div
                  className={cn(
                    'h-full rounded-full transition-all duration-500',
                    pct === 100 ? 'bg-success' : 'bg-accent'
                  )}
                  style={{ width: `${pct}%` }}
                />
              </div>
              <div className="text-[10px] text-text-muted mt-0.5">
                {m.done} / {m.total} features
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

/* ── Activity Heatmap ──────────────────────────────────── */

function ActivityHeatmap({ data, isLoading }: { data?: ActivityDayCount[]; isLoading: boolean }) {
  const [tooltip, setTooltip] = useState<{ date: string; count: number; x: number; y: number } | null>(null)

  const { grid, months, maxCount } = useMemo(() => {
    if (!data || data.length === 0) {
      return { grid: [], months: [], maxCount: 0 }
    }

    // Build a map of date → count
    const countMap = new Map<string, number>()
    let max = 0
    for (const d of data) {
      countMap.set(d.date, d.count)
      if (d.count > max) max = d.count
    }

    // Build 52 weeks × 7 days grid ending today
    const today = new Date()
    const weeks: Array<Array<{ date: string; count: number }>> = []
    const monthLabels: Array<{ label: string; col: number }> = []

    // Find the Sunday of the week 52 weeks ago
    const startDate = new Date(today)
    startDate.setDate(startDate.getDate() - startDate.getDay() - 52 * 7)

    let lastMonth = -1
    for (let week = 0; week < 53; week++) {
      const weekData: Array<{ date: string; count: number }> = []
      for (let day = 0; day < 7; day++) {
        const d = new Date(startDate)
        d.setDate(d.getDate() + week * 7 + day)
        if (d > today) {
          weekData.push({ date: '', count: -1 })
          continue
        }
        const dateStr = d.toISOString().split('T')[0]
        const month = d.getMonth()
        if (month !== lastMonth && day === 0) {
          const monthNames = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
          monthLabels.push({ label: monthNames[month], col: week })
          lastMonth = month
        }
        weekData.push({ date: dateStr, count: countMap.get(dateStr) || 0 })
      }
      weeks.push(weekData)
    }

    return { grid: weeks, months: monthLabels, maxCount: max }
  }, [data])

  function intensityClass(count: number): string {
    if (count < 0) return 'bg-transparent'
    if (count === 0) return 'bg-bg-tertiary'
    if (maxCount === 0) return 'bg-bg-tertiary'
    const ratio = count / maxCount
    if (ratio <= 0.25) return 'bg-green-900/40'
    if (ratio <= 0.5) return 'bg-green-700/60'
    if (ratio <= 0.75) return 'bg-green-500/80'
    return 'bg-green-400'
  }

  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Activity</h2>

      {isLoading ? (
        <div className="h-[120px] bg-bg-secondary rounded animate-pulse" />
      ) : grid.length === 0 ? (
        <p className="text-sm text-text-muted">No activity data</p>
      ) : (
        <div className="overflow-x-auto">
          {/* Month labels */}
          <div className="flex ml-8 mb-1">
            {months.map((m, i) => (
              <div
                key={i}
                className="text-[10px] text-text-muted"
                style={{ position: 'relative', left: `${m.col * 14}px` }}
              >
                {m.label}
              </div>
            ))}
          </div>

          <div className="flex gap-0.5 relative" onMouseLeave={() => setTooltip(null)}>
            {/* Day labels */}
            <div className="flex flex-col gap-0.5 mr-1 shrink-0">
              {['', 'Mon', '', 'Wed', '', 'Fri', ''].map((d, i) => (
                <div key={i} className="w-6 h-[12px] text-[10px] text-text-muted leading-[12px] text-right pr-1">
                  {d}
                </div>
              ))}
            </div>

            {/* Grid */}
            {grid.map((week, wi) => (
              <div key={wi} className="flex flex-col gap-0.5">
                {week.map((day, di) => (
                  <div
                    key={di}
                    className={cn(
                      'w-[12px] h-[12px] rounded-sm cursor-default transition-colors',
                      intensityClass(day.count)
                    )}
                    onMouseEnter={(e) => {
                      if (day.count >= 0) {
                        const rect = e.currentTarget.getBoundingClientRect()
                        setTooltip({
                          date: day.date,
                          count: day.count,
                          x: rect.left + rect.width / 2,
                          y: rect.top,
                        })
                      }
                    }}
                  />
                ))}
              </div>
            ))}
          </div>

          {/* Tooltip */}
          {tooltip && (
            <div
              className="fixed z-50 px-2 py-1 bg-bg-primary border border-border rounded text-xs text-text-primary shadow-lg pointer-events-none"
              style={{ left: tooltip.x, top: tooltip.y - 30, transform: 'translateX(-50%)' }}
            >
              <span className="font-mono">{tooltip.count}</span>
              <span className="text-text-muted ml-1">
                {tooltip.count === 1 ? 'event' : 'events'} on {tooltip.date}
              </span>
            </div>
          )}

          {/* Legend */}
          <div className="flex items-center gap-1 mt-3 justify-end text-[10px] text-text-muted">
            <span>Less</span>
            <div className="w-[12px] h-[12px] rounded-sm bg-bg-tertiary" />
            <div className="w-[12px] h-[12px] rounded-sm bg-green-900/40" />
            <div className="w-[12px] h-[12px] rounded-sm bg-green-700/60" />
            <div className="w-[12px] h-[12px] rounded-sm bg-green-500/80" />
            <div className="w-[12px] h-[12px] rounded-sm bg-green-400" />
            <span>More</span>
          </div>
        </div>
      )}
    </div>
  )
}

/* ── Burndown Chart ────────────────────────────────────── */

function BurndownChart({ data, isLoading }: { data?: BurndownPoint[]; isLoading: boolean }) {
  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Burndown</h2>

      {isLoading ? (
        <div className="h-[200px] bg-bg-secondary rounded animate-pulse" />
      ) : !data || data.length === 0 ? (
        <p className="text-sm text-text-muted">No burndown data yet</p>
      ) : (
        <ResponsiveContainer width="100%" height={220}>
          <LineChart data={data} margin={{ top: 5, right: 10, left: -10, bottom: 5 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" />
            <XAxis
              dataKey="date"
              tick={{ fontSize: 10, fill: 'var(--color-text-muted)' }}
              tickFormatter={(v: string) => v.slice(5)}
            />
            <YAxis tick={{ fontSize: 10, fill: 'var(--color-text-muted)' }} />
            <Tooltip
              contentStyle={{
                backgroundColor: 'var(--color-bg-card)',
                border: '1px solid var(--color-border)',
                borderRadius: '8px',
                fontSize: '12px',
              }}
            />
            <Line
              type="monotone"
              dataKey="remaining"
              stroke="var(--color-warning)"
              strokeWidth={2}
              dot={false}
              name="Remaining"
            />
            <Line
              type="monotone"
              dataKey="completed"
              stroke="var(--color-success)"
              strokeWidth={2}
              dot={false}
              name="Completed"
            />
          </LineChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}

/* ── Velocity Chart ────────────────────────────────────── */

function VelocityChart({ data, isLoading }: { data?: WeekVelocity[]; isLoading: boolean }) {
  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Weekly Velocity</h2>

      {isLoading ? (
        <div className="h-[200px] bg-bg-secondary rounded animate-pulse" />
      ) : !data || data.length === 0 ? (
        <p className="text-sm text-text-muted">No velocity data yet</p>
      ) : (
        <ResponsiveContainer width="100%" height={220}>
          <BarChart data={data} margin={{ top: 5, right: 10, left: -10, bottom: 5 }}>
            <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border)" />
            <XAxis
              dataKey="week"
              tick={{ fontSize: 10, fill: 'var(--color-text-muted)' }}
              tickFormatter={(v: string) => v.slice(5)}
            />
            <YAxis tick={{ fontSize: 10, fill: 'var(--color-text-muted)' }} allowDecimals={false} />
            <Tooltip
              contentStyle={{
                backgroundColor: 'var(--color-bg-card)',
                border: '1px solid var(--color-border)',
                borderRadius: '8px',
                fontSize: '12px',
              }}
            />
            <Bar
              dataKey="completed_count"
              fill="var(--color-accent)"
              radius={[4, 4, 0, 0]}
              name="Completed"
            />
          </BarChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}

/* ── Roadmap Distribution ──────────────────────────────── */

function RoadmapDistribution({ stats }: { stats: StatsResponse }) {
  const { by_priority, by_status, total } = stats.roadmap_stats

  if (total === 0) {
    return (
      <div className="bg-bg-card border border-border rounded-lg p-6">
        <h2 className="text-sm font-semibold text-text-primary mb-4">Roadmap Distribution</h2>
        <p className="text-sm text-text-muted">No roadmap items yet</p>
      </div>
    )
  }

  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Roadmap Distribution</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {/* By Priority */}
        <div>
          <h3 className="text-xs text-text-muted mb-2 uppercase tracking-wider">By Priority</h3>
          <DistributionBar entries={by_priority} total={total} order={PRIORITY_ORDER} colors={PRIORITY_COLORS} />
          <DistributionLegend entries={by_priority} total={total} order={PRIORITY_ORDER} colors={PRIORITY_COLORS} />
        </div>

        {/* By Status */}
        <div>
          <h3 className="text-xs text-text-muted mb-2 uppercase tracking-wider">By Status</h3>
          <DistributionBar entries={by_status} total={total} order={ROADMAP_STATUS_ORDER} colors={ROADMAP_STATUS_COLORS} />
          <DistributionLegend entries={by_status} total={total} order={ROADMAP_STATUS_ORDER} colors={ROADMAP_STATUS_COLORS} />
        </div>
      </div>
    </div>
  )
}

function DistributionBar({ entries, total, order, colors }: {
  entries: Record<string, number>
  total: number
  order: string[]
  colors: Record<string, string>
}) {
  if (total === 0) return null

  return (
    <div className="flex h-4 rounded-lg overflow-hidden bg-bg-tertiary mb-3">
      {order.map((key) => {
        const count = entries[key] || 0
        if (count === 0) return null
        const pct = (count / total) * 100
        return (
          <div
            key={key}
            className={cn(colors[key], 'transition-all duration-500')}
            style={{ width: `${pct}%` }}
            title={`${key}: ${count} (${pct.toFixed(0)}%)`}
          />
        )
      })}
    </div>
  )
}

function DistributionLegend({ entries, total, order, colors }: {
  entries: Record<string, number>
  total: number
  order: string[]
  colors: Record<string, string>
}) {
  return (
    <div className="flex flex-wrap gap-x-4 gap-y-1">
      {order.map((key) => {
        const count = entries[key] || 0
        if (count === 0) return null
        const pct = ((count / total) * 100).toFixed(0)
        return (
          <div key={key} className="flex items-center gap-1.5 text-xs">
            <div className={cn('w-2.5 h-2.5 rounded-sm shrink-0', colors[key])} />
            <span className="text-text-secondary capitalize">{key}</span>
            <span className="text-text-muted font-mono">{count} ({pct}%)</span>
          </div>
        )
      })}
    </div>
  )
}

/* ── Activity Summary ──────────────────────────────────── */

function ActivitySummary({ activity }: { activity: StatsResponse['activity'] }) {
  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Activity Summary</h2>
      <div className="grid grid-cols-3 gap-4">
        <div className="text-center">
          <div className="text-2xl font-bold text-text-primary">{activity.total_events}</div>
          <div className="text-xs text-text-muted">Total Events</div>
        </div>
        <div className="text-center">
          <div className="text-2xl font-bold text-accent">{activity.events_last_7_days}</div>
          <div className="text-xs text-text-muted">Last 7 Days</div>
        </div>
        <div className="text-center">
          <div className="text-2xl font-bold text-text-secondary">{activity.events_last_30_days}</div>
          <div className="text-xs text-text-muted">Last 30 Days</div>
        </div>
      </div>
    </div>
  )
}

/* -- Day/Hour Heatmap -- */

const DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const HOUR_LABELS = Array.from({ length: 24 }, (_, i) => `${i}`)

function DayHourHeatmap({ data, isLoading }: { data?: HeatmapGrid; isLoading: boolean }) {
  const [tooltip, setTooltip] = useState<{ day: number; hour: number; count: number; x: number; y: number } | null>(null)

  const cellMap = useMemo(() => {
    if (!data) return new Map<string, number>()
    const m = new Map<string, number>()
    for (const c of data.cells) {
      m.set(`${c.day}-${c.hour}`, c.count)
    }
    return m
  }, [data])

  function intensityClass(count: number): string {
    if (!data || data.max_count === 0) return 'bg-bg-tertiary'
    if (count === 0) return 'bg-bg-tertiary'
    const ratio = count / data.max_count
    if (ratio <= 0.25) return 'bg-accent/20'
    if (ratio <= 0.5) return 'bg-accent/40'
    if (ratio <= 0.75) return 'bg-accent/60'
    return 'bg-accent/90'
  }

  return (
    <div className="bg-bg-card border border-border rounded-lg p-6">
      <h2 className="text-sm font-semibold text-text-primary mb-4">Activity by Hour & Day</h2>

      {isLoading ? (
        <div className="h-[200px] bg-bg-secondary rounded animate-pulse" />
      ) : !data ? (
        <p className="text-sm text-text-muted">No heatmap data</p>
      ) : (
        <div className="overflow-x-auto">
          {/* Hour labels */}
          <div className="flex ml-10 mb-1">
            {HOUR_LABELS.map((h) => (
              <div key={h} className="w-[20px] text-[10px] text-text-muted text-center shrink-0">
                {Number(h) % 3 === 0 ? h : ''}
              </div>
            ))}
          </div>

          <div className="relative" onMouseLeave={() => setTooltip(null)}>
            {DAY_NAMES.map((dayName, dayIdx) => (
              <div key={dayIdx} className="flex items-center gap-1 mb-0.5">
                <div className="w-8 text-[10px] text-text-muted text-right shrink-0">{dayName}</div>
                {HOUR_LABELS.map((_, hourIdx) => {
                  const count = cellMap.get(`${dayIdx}-${hourIdx}`) || 0
                  return (
                    <div
                      key={hourIdx}
                      className={cn(
                        'w-[20px] h-[20px] rounded-sm cursor-default transition-colors shrink-0',
                        intensityClass(count)
                      )}
                      onMouseEnter={(e) => {
                        const rect = e.currentTarget.getBoundingClientRect()
                        setTooltip({
                          day: dayIdx,
                          hour: hourIdx,
                          count,
                          x: rect.left + rect.width / 2,
                          y: rect.top,
                        })
                      }}
                    />
                  )
                })}
              </div>
            ))}
          </div>

          {/* Tooltip */}
          {tooltip && (
            <div
              className="fixed z-50 px-2 py-1 bg-bg-primary border border-border rounded text-xs text-text-primary shadow-lg pointer-events-none"
              style={{ left: tooltip.x, top: tooltip.y - 30, transform: 'translateX(-50%)' }}
            >
              <span className="font-mono">{tooltip.count}</span>
              <span className="text-text-muted ml-1">
                {tooltip.count === 1 ? 'event' : 'events'} -- {DAY_NAMES[tooltip.day]} {tooltip.hour}:00
              </span>
            </div>
          )}

          {/* Legend */}
          <div className="flex items-center gap-1 mt-3 justify-end text-[10px] text-text-muted">
            <span>Less</span>
            <div className="w-[14px] h-[14px] rounded-sm bg-bg-tertiary" />
            <div className="w-[14px] h-[14px] rounded-sm bg-accent/20" />
            <div className="w-[14px] h-[14px] rounded-sm bg-accent/40" />
            <div className="w-[14px] h-[14px] rounded-sm bg-accent/60" />
            <div className="w-[14px] h-[14px] rounded-sm bg-accent/90" />
            <span>More</span>
          </div>
        </div>
      )}
    </div>
  )
}
