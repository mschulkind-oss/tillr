import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getCycleDetail, getFeature, advanceCycle } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { EntityLink } from '../components/EntityLink'
import { PageSkeleton } from '../components/Skeleton'
import { MarkdownContent } from '../components/MarkdownContent'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp, cn } from '../lib/utils'
import type { CycleScore } from '../api/types'
import { useState } from 'react'

function MetaItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="bg-bg-secondary border border-border-light rounded-lg p-3">
      <div className="text-[10px] text-text-muted uppercase tracking-wider mb-1">{label}</div>
      <div className="text-sm text-text-primary">{value}</div>
    </div>
  )
}

function scoreColor(score: number): string {
  if (score >= 8) return 'bg-success/20 text-success'
  if (score >= 6) return 'bg-accent/20 text-accent'
  if (score >= 4) return 'bg-warning/20 text-warning'
  return 'bg-danger/20 text-danger'
}

function ScoreCard({ score }: { score: CycleScore }) {
  return (
    <div className="flex items-center gap-3 p-3 rounded border border-border-light bg-bg-secondary text-sm">
      <div className={cn(
        'w-10 h-10 rounded-lg flex items-center justify-center font-bold text-lg',
        scoreColor(score.score),
      )}>
        {score.score}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 text-xs text-text-muted">
          <span>Step {score.step}</span>
          <span>·</span>
          <span>Iteration #{score.iteration}</span>
        </div>
        {score.notes && (
          <p className="text-sm text-text-secondary mt-0.5 line-clamp-2">{score.notes}</p>
        )}
      </div>
      <span className="text-[10px] text-text-muted shrink-0">
        {formatTimestamp(score.created_at)}
      </span>
    </div>
  )
}

function CycleApproveRejectCard({ cycleId, stepName, instructions }: { cycleId: number; stepName: string; instructions?: string }) {
  const [notes, setNotes] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const queryClient = useQueryClient()

  const handleAction = async (action: 'approve' | 'reject') => {
    setSubmitting(true)
    try {
      await advanceCycle(cycleId, action, notes)
      queryClient.invalidateQueries({ queryKey: ['cycle-detail', cycleId] })
      setNotes('')
    } catch (err) {
      alert(`Failed to ${action}: ${err instanceof Error ? err.message : err}`)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className={cn('bg-warning/5 border border-warning/30 rounded-lg p-5 space-y-4')}>
      <h2 className="text-sm font-semibold text-warning">
        Waiting for human input: {stepName}
      </h2>

      {instructions && (
        <div className="bg-bg-secondary rounded-lg p-4 border border-border-light">
          <div className="prose prose-sm prose-invert max-w-none text-sm text-text-secondary">
            <MarkdownContent>{instructions}</MarkdownContent>
          </div>
        </div>
      )}

      <textarea
        value={notes}
        onChange={e => setNotes(e.target.value)}
        placeholder="Review notes (optional)..."
        className="w-full min-h-[60px] p-2 rounded-md border border-border bg-bg-primary text-text-primary text-sm resize-y font-[inherit]"
      />
      <div className="flex gap-2">
        <button
          onClick={() => handleAction('approve')}
          disabled={submitting}
          className="px-4 py-1.5 rounded-md bg-success text-white font-semibold text-sm hover:bg-success/90 disabled:opacity-50"
        >
          Approve &amp; Advance
        </button>
        <button
          onClick={() => handleAction('reject')}
          disabled={submitting}
          className="px-4 py-1.5 rounded-md border border-warning/30 text-warning font-semibold text-sm hover:bg-warning/10 disabled:opacity-50"
        >
          Request Changes
        </button>
      </div>
    </div>
  )
}

export function CycleDetail() {
  const { id } = useParams<{ id: string }>()
  const cycleId = Number(id)

  const detail = useQuery({
    queryKey: ['cycle-detail', cycleId],
    queryFn: () => getCycleDetail(cycleId),
    enabled: !isNaN(cycleId),
  })

  const featureQuery = useQuery({
    queryKey: ['feature', detail.data?.cycle?.entity_id],
    queryFn: () => getFeature(detail.data!.cycle.entity_id),
    enabled: !!detail.data?.cycle?.entity_id,
  })

  if (detail.isLoading) return <PageSkeleton />
  if (!detail.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Cycle not found
      </div>
    )
  }

  const { cycle, scores, steps } = detail.data
  const featureName = featureQuery.data?.feature?.name

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/cycles" className="hover:text-accent transition-colors">Cycles</Link>
        <span>/</span>
        <span className="text-text-secondary">{cycle.cycle_type} #{cycle.id}</span>
      </nav>

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-text-primary">
            {cycle.cycle_type}
            <span className="text-text-muted font-normal ml-2">#{cycle.id}</span>
          </h1>
          <p className="text-sm text-text-secondary mt-1">
            Iteration <span className="font-mono">#{cycle.iteration}</span>
          </p>
        </div>
        <StatusBadge status={cycle.status} />
      </div>

      {/* Entity link */}
      <div className="bg-bg-card border border-border rounded-lg p-4 flex items-center gap-2">
        <span className="text-text-muted text-sm capitalize">{cycle.entity_type}:</span>
        <EntityLink
          type={cycle.entity_type as 'feature'}
          id={cycle.entity_id}
          name={featureName}
          showIcon
        />
      </div>

      {/* Step timeline */}
      {steps.length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-4">Steps</h2>
          <div className="flex items-stretch gap-0">
            {steps.map((step, i) => {
              const stepName = typeof step === 'string' ? step : step.name
              const isHuman = typeof step === 'object' && step.human
              const isCompleted = cycle.status === 'completed' ? true : i < cycle.current_step
              const isCurrent = cycle.status === 'active' && i === cycle.current_step
              const isFailed = cycle.status === 'failed' && i === cycle.current_step

              return (
                <div key={i} className="flex items-center flex-1 min-w-0">
                  <div className={cn(
                    'flex flex-col items-center gap-1.5 flex-1 p-3 rounded-lg border text-center',
                    isCompleted && 'bg-success/10 border-success/30',
                    isCurrent && (isHuman ? 'bg-warning/10 border-warning/40 ring-1 ring-warning/20' : 'bg-accent/10 border-accent/40 ring-1 ring-accent/20'),
                    isFailed && 'bg-danger/10 border-danger/30',
                    !isCompleted && !isCurrent && !isFailed && 'bg-bg-secondary border-border-light',
                  )}>
                    <div className={cn(
                      'w-6 h-6 rounded-full flex items-center justify-center text-[10px] font-bold',
                      isCompleted && 'bg-success text-white',
                      isCurrent && (isHuman ? 'bg-warning text-white' : 'bg-accent text-white'),
                      isFailed && 'bg-danger text-white',
                      !isCompleted && !isCurrent && !isFailed && 'bg-bg-tertiary text-text-muted',
                    )}>
                      {isCompleted ? '✓' : i + 1}
                    </div>
                    <span className={cn(
                      'text-[10px] font-medium truncate w-full',
                      isCompleted && 'text-success',
                      isCurrent && (isHuman ? 'text-warning' : 'text-accent'),
                      isFailed && 'text-danger',
                      !isCompleted && !isCurrent && !isFailed && 'text-text-muted',
                    )}>
                      {stepName}{isHuman ? ' *' : ''}
                    </span>
                  </div>
                  {i < steps.length - 1 && (
                    <span className={cn(
                      'text-xs px-1 shrink-0',
                      isCompleted ? 'text-success' : 'text-text-muted',
                    )}>→</span>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Human step: approve/reject */}
      {cycle.status === 'active' && (() => {
        const step = steps[cycle.current_step]
        const isHuman = typeof step === 'object' && step.human
        if (!isHuman) return null
        const stepName = typeof step === 'string' ? step : step.name
        const instructions = typeof step === 'object' ? step.instructions : undefined
        return <CycleApproveRejectCard cycleId={cycle.id} stepName={stepName} instructions={instructions} />
      })()}

      {/* Scores */}
      {scores.length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-4">
            Scores
            <span className="text-text-muted font-normal ml-2">({scores.length})</span>
          </h2>
          <div className="space-y-2">
            {scores.map((score) => (
              <ScoreCard key={score.id} score={score} />
            ))}
          </div>
        </div>
      )}

      {/* Metadata */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <MetaItem label="Status" value={<StatusBadge status={cycle.status} />} />
        <MetaItem label="Iteration" value={<span className="font-mono">#{cycle.iteration}</span>} />
        <MetaItem label="Created" value={formatTimestamp(cycle.created_at)} />
        <MetaItem label="Updated" value={formatTimestamp(cycle.updated_at)} />
      </div>
    </div>
  )
}
