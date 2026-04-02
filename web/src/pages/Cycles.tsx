import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getCycles, getCycleTypes } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { Link } from 'react-router-dom'
import { cn } from '../lib/utils'
import type { CycleInstance, CycleType } from '../api/types'

function StatCard({ label, value, icon, accent }: {
  label: string
  value: number
  icon: string
  accent?: string
}) {
  return (
    <div className="bg-bg-card border border-border rounded-md px-3 py-2 flex items-center gap-2">
      <span className="text-lg">{icon}</span>
      <div>
        <div className={cn('text-lg font-bold leading-tight', accent || 'text-text-primary')}>
          {value}
        </div>
        <div className="text-[10px] text-text-secondary">{label}</div>
      </div>
    </div>
  )
}

function CompactCycleCard({ cycle, cycleTypes }: {
  cycle: CycleInstance
  cycleTypes: Record<string, CycleType>
}) {
  const ct = cycleTypes[cycle.cycle_type]
  const steps = ct?.steps || []
  const totalSteps = steps.length || 1
  const completedSteps = cycle.status === 'completed' ? totalSteps : cycle.current_step
  const progressPct = Math.round((completedSteps / totalSteps) * 100)

  return (
    <Link
      to={`/cycles/${cycle.id}`}
      className="block bg-bg-card border border-border rounded-md px-3 py-2 hover:border-accent/40 transition-colors"
    >
      <div className="flex items-center justify-between gap-2 mb-1">
        <span className="text-xs font-semibold text-text-primary truncate">
          {cycle.cycle_type}
        </span>
        <StatusBadge status={cycle.status} />
      </div>
      <div className="flex items-center justify-between gap-2">
        <EntityLink type="feature" id={cycle.entity_id} showIcon className="text-[10px]" />
        <div className="flex items-center gap-2 text-[10px] text-text-muted shrink-0">
          <span className="font-mono">{progressPct}%</span>
          <span>#{cycle.iteration}</span>
        </div>
      </div>
      {/* Thin progress bar */}
      <div className="mt-1.5 h-1 bg-bg-tertiary rounded-full overflow-hidden">
        <div
          className={cn(
            'h-full rounded-full transition-all',
            cycle.status === 'completed' ? 'bg-success' :
            cycle.status === 'failed' ? 'bg-danger' : 'bg-accent',
          )}
          style={{ width: `${progressPct}%` }}
        />
      </div>
    </Link>
  )
}

export function Cycles() {
  const [showCompleted, setShowCompleted] = useState(false)
  const [expandAll, setExpandAll] = useState(false)

  const cycles = useQuery({
    queryKey: ['cycles'],
    queryFn: getCycles,
  })

  const cycleTypesQuery = useQuery({
    queryKey: ['cycle-types'],
    queryFn: getCycleTypes,
  })

  if (cycles.isLoading) return <PageSkeleton />

  const allCycles = cycles.data || []
  const cycleTypeMap: Record<string, CycleType> = {}
  for (const ct of cycleTypesQuery.data || []) {
    cycleTypeMap[ct.name] = ct
  }

  const activeCycles = allCycles.filter((c) => c.status === 'active')
  const completedCycles = allCycles.filter((c) => c.status === 'completed')
  const failedCycles = allCycles.filter((c) => c.status === 'failed')

  // When expandAll is toggled on, force completed section open
  const completedVisible = expandAll || showCompleted

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-text-primary">Iteration Cycles</h1>
          <p className="text-xs text-text-secondary mt-0.5">Structured iteration workflows</p>
        </div>
        <button
          onClick={() => {
            const next = !expandAll
            setExpandAll(next)
            if (next) setShowCompleted(true)
            else setShowCompleted(false)
          }}
          className="text-[10px] px-2 py-1 rounded border border-border text-text-secondary hover:bg-bg-tertiary transition-colors"
        >
          {expandAll ? 'Collapse All' : 'Expand All'}
        </button>
      </div>

      {/* Stats row */}
      <div className="grid grid-cols-4 gap-2">
        <StatCard label="Total" value={allCycles.length} icon="O" />
        <StatCard label="Active" value={activeCycles.length} icon="*" accent="text-accent" />
        <StatCard label="Completed" value={completedCycles.length} icon="+" accent="text-success" />
        <StatCard label="Failed" value={failedCycles.length} icon="x" accent="text-danger" />
      </div>

      {/* Active cycles */}
      {activeCycles.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold text-text-primary mb-2">Active</h2>
          <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-2">
            {activeCycles.map((c) => (
              <CompactCycleCard key={c.id} cycle={c} cycleTypes={cycleTypeMap} />
            ))}
          </div>
        </div>
      )}

      {/* Failed cycles */}
      {failedCycles.length > 0 && (
        <div>
          <h2 className="text-sm font-semibold text-text-primary mb-2">Failed</h2>
          <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-2">
            {failedCycles.map((c) => (
              <CompactCycleCard key={c.id} cycle={c} cycleTypes={cycleTypeMap} />
            ))}
          </div>
        </div>
      )}

      {/* Completed cycles - collapsed by default */}
      {completedCycles.length > 0 && (
        <div>
          <button
            onClick={() => setShowCompleted(!completedVisible)}
            className="flex items-center gap-1.5 text-sm font-semibold text-text-primary mb-2 hover:text-accent transition-colors"
          >
            <span className={cn(
              'text-[10px] transition-transform',
              completedVisible ? 'rotate-90' : '',
            )}>&#9654;</span>
            Completed ({completedCycles.length})
          </button>
          {completedVisible && (
            <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-2">
              {completedCycles.map((c) => (
                <CompactCycleCard key={c.id} cycle={c} cycleTypes={cycleTypeMap} />
              ))}
            </div>
          )}
        </div>
      )}

      {/* Empty state */}
      {allCycles.length === 0 && (
        <div className="text-center py-12 text-text-muted">
          <p className="text-sm">No iteration cycles.</p>
          <p className="text-xs mt-1">
            Use <code className="bg-bg-tertiary px-1 py-0.5 rounded text-text-secondary text-[10px]">tillr cycle start &lt;type&gt; &lt;feature&gt;</code> to begin one.
          </p>
        </div>
      )}
    </div>
  )
}
