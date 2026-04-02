import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getQAPending, approveFeature, rejectFeature, getQAResults } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { useStore } from '../store'
import { formatTimestamp, cn } from '../lib/utils'
import { useState } from 'react'
import type { Feature, QAResult } from '../api/types'
import { MarkdownContent } from '../components/MarkdownContent'

export function QA() {
  const queryClient = useQueryClient()
  const addToast = useStore((s) => s.addToast)
  const pending = useQuery({ queryKey: ['qa-pending'], queryFn: getQAPending })

  const approveMutation = useMutation({
    mutationFn: ({ id, notes }: { id: string; notes?: string }) => approveFeature(id, notes),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['qa-pending'] })
      queryClient.invalidateQueries({ queryKey: ['features'] })
      queryClient.invalidateQueries({ queryKey: ['status'] })
      addToast('Feature approved', 'success')
    },
    onError: (err) => addToast(`Approve failed: ${err.message}`, 'error'),
  })

  const rejectMutation = useMutation({
    mutationFn: ({ id, notes }: { id: string; notes?: string }) => rejectFeature(id, notes),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['qa-pending'] })
      queryClient.invalidateQueries({ queryKey: ['features'] })
      addToast('Feature rejected — sent back to development', 'info')
    },
    onError: (err) => addToast(`Reject failed: ${err.message}`, 'error'),
  })

  if (pending.isLoading) return <PageSkeleton />

  const features = pending.data || []
  const needsReview = features.filter((f) => f.status === 'human-qa')
  const otherQA = features.filter((f) => f.status !== 'human-qa')

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-text-primary">QA Review</h1>
        <p className="text-sm text-text-secondary mt-1">
          {needsReview.length} feature{needsReview.length !== 1 ? 's' : ''} awaiting human review
        </p>
      </div>

      {needsReview.length === 0 ? (
        <div className="bg-bg-card border border-border rounded-lg p-12 text-center">
          <span className="text-5xl mb-4 block">🎉</span>
          <h2 className="text-lg font-semibold text-text-primary">All clear!</h2>
          <p className="text-sm text-text-secondary mt-2">No features need review right now.</p>
        </div>
      ) : (
        <div className="space-y-4">
          {needsReview.map((feature) => (
            <QACard
              key={feature.id}
              feature={feature}
              onApprove={(notes) => approveMutation.mutate({ id: feature.id, notes })}
              onReject={(notes) => rejectMutation.mutate({ id: feature.id, notes })}
              isApproving={approveMutation.isPending}
              isRejecting={rejectMutation.isPending}
            />
          ))}
        </div>
      )}

      {otherQA.length > 0 && (
        <div>
          <h2 className="text-lg font-semibold text-text-primary mb-3">Agent QA</h2>
          <div className="space-y-3">
            {otherQA.map((f) => (
              <div key={f.id} className="bg-bg-card border border-border rounded-lg p-4 flex items-center justify-between">
                <div>
                  <span className="text-sm font-medium text-text-primary">
                    <EntityLink type="feature" id={f.id} name={f.name} />
                  </span>
                  <span className="ml-2"><StatusBadge status={f.status} /></span>
                </div>
                <span className="text-xs text-text-muted">{formatTimestamp(f.updated_at)}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function QACard({ feature, onApprove, onReject, isApproving, isRejecting }: {
  feature: Feature
  onApprove: (notes?: string) => void
  onReject: (notes?: string) => void
  isApproving: boolean
  isRejecting: boolean
}) {
  const [expanded, setExpanded] = useState(false)
  const [notes, setNotes] = useState('')
  const [showRejectConfirm, setShowRejectConfirm] = useState(false)

  const qaResults = useQuery({
    queryKey: ['qa-results', feature.id],
    queryFn: () => getQAResults(feature.id),
    enabled: expanded,
  })

  const reviewHistory = (qaResults.data || []) as QAResult[]
  const reviewRound = reviewHistory.length + 1

  return (
    <div className="bg-bg-card border border-border rounded-lg overflow-hidden">
      {/* Header */}
      <div
        className="flex items-center justify-between p-4 cursor-pointer hover:bg-bg-hover/30 transition-colors"
        onClick={() => setExpanded(!expanded)}
      >
        <div className="flex items-center gap-3 min-w-0">
          <span className="text-lg">
            {reviewRound > 1 ? '🔁' : '🆕'}
          </span>
          <div className="min-w-0">
            <h3 className="text-sm font-semibold text-text-primary truncate">
              <EntityLink type="feature" id={feature.id} name={feature.name} />
            </h3>
            <p className="text-xs text-text-secondary mt-0.5 truncate">{feature.description}</p>
          </div>
        </div>
        <div className="flex items-center gap-3 shrink-0">
          {feature.milestone_name && feature.milestone_id && (
            <EntityLink type="milestone" id={feature.milestone_id} name={feature.milestone_name} className="text-xs" />
          )}
          {feature.milestone_name && !feature.milestone_id && (
            <span className="text-xs text-text-muted">{feature.milestone_name}</span>
          )}
          <span className={cn(
            'text-xs font-mono px-1.5 py-0.5 rounded',
            feature.priority >= 8 ? 'bg-danger/10 text-danger' :
            feature.priority >= 5 ? 'bg-warning/10 text-warning' :
            'bg-bg-tertiary text-text-muted'
          )}>
            P{feature.priority}
          </span>
          <span className="text-text-muted text-sm">{expanded ? '▲' : '▼'}</span>
        </div>
      </div>

      {/* Expanded content */}
      {expanded && (
        <div className="border-t border-border p-4 space-y-4">
          {/* Review round indicator */}
          {reviewRound > 1 && (
            <div className="flex items-center gap-2 text-xs text-warning bg-warning/5 border border-warning/20 rounded-md px-3 py-2">
              <span>⚠️</span>
              <span>Review round #{reviewRound} — previously reviewed {reviewRound - 1} time{reviewRound > 2 ? 's' : ''}</span>
            </div>
          )}

          {/* Feature spec */}
          {feature.spec && (
            <div className="bg-bg-secondary rounded-lg p-4 border border-border-light">
              <h4 className="text-xs font-semibold text-text-muted uppercase tracking-wider mb-2">
                Feature Spec & QA Instructions
              </h4>
              <div className="prose prose-sm prose-invert max-w-none text-sm text-text-secondary">
                <MarkdownContent>{feature.spec}</MarkdownContent>
              </div>
            </div>
          )}

          {/* Review history */}
          {reviewHistory.length > 0 && (
            <div>
              <h4 className="text-xs font-semibold text-text-muted uppercase tracking-wider mb-2">
                Review History
              </h4>
              <div className="space-y-2">
                {reviewHistory.map((r) => (
                  <div
                    key={r.id}
                    className={cn(
                      'text-xs p-2.5 rounded border',
                      r.passed
                        ? 'bg-success/5 border-success/20 text-success'
                        : 'bg-danger/5 border-danger/20 text-danger'
                    )}
                  >
                    <span className="font-medium">{r.passed ? '✅ Approved' : '❌ Rejected'}</span>
                    {r.notes && <span className="ml-2 text-text-secondary">— {r.notes}</span>}
                    <span className="ml-2 text-text-muted">{formatTimestamp(r.created_at)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Notes + actions */}
          <div className="flex flex-col sm:flex-row gap-3">
            <input
              type="text"
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder="Review notes (optional)"
              className="flex-1 bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-accent focus:outline-none"
            />
            <div className="flex gap-2 shrink-0">
              <button
                onClick={() => {
                  onApprove(notes || undefined)
                  setNotes('')
                }}
                disabled={isApproving}
                className="px-4 py-2 bg-success/20 text-success border border-success/30 rounded-md text-sm font-medium hover:bg-success/30 transition-colors disabled:opacity-50"
              >
                {isApproving ? 'Approving...' : '✅ Approve'}
              </button>
              {showRejectConfirm ? (
                <div className="flex gap-1">
                  <button
                    onClick={() => {
                      onReject(notes || undefined)
                      setNotes('')
                      setShowRejectConfirm(false)
                    }}
                    disabled={isRejecting}
                    className="px-3 py-2 bg-danger text-white rounded-md text-sm font-medium hover:bg-danger/80 transition-colors disabled:opacity-50"
                  >
                    Confirm
                  </button>
                  <button
                    onClick={() => setShowRejectConfirm(false)}
                    className="px-3 py-2 bg-bg-tertiary text-text-secondary rounded-md text-sm hover:bg-bg-hover transition-colors"
                  >
                    Cancel
                  </button>
                </div>
              ) : (
                <button
                  onClick={() => setShowRejectConfirm(true)}
                  className="px-4 py-2 bg-danger/10 text-danger border border-danger/20 rounded-md text-sm font-medium hover:bg-danger/20 transition-colors"
                >
                  ❌ Reject
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
