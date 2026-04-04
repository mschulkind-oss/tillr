import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getFeature, getFeatureDeps, getQAResults, patchFeature, getDiscussions, getFeaturePRs, approveFeature, rejectFeature } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp, cn } from '../lib/utils'
import { useState, useMemo } from 'react'
import type { FeatureStatus, QAResult } from '../api/types'
import { MarkdownContent } from '../components/MarkdownContent'
import { AttachmentPanel } from '../components/AttachmentPanel'
import { useStore } from '../store'

export function FeatureDetail() {
  const { id } = useParams<{ id: string }>()
  const queryClient = useQueryClient()
  const addToast = useStore((s) => s.addToast)

  const feature = useQuery({
    queryKey: ['feature', id],
    queryFn: () => getFeature(id!),
    enabled: !!id,
  })

  const deps = useQuery({
    queryKey: ['feature-deps', id],
    queryFn: () => getFeatureDeps(id!),
    enabled: !!id,
  })

  const qaResults = useQuery({
    queryKey: ['qa-results', id],
    queryFn: () => getQAResults(id!),
    enabled: !!id,
  })

  const discussions = useQuery({
    queryKey: ['discussions'],
    queryFn: getDiscussions,
  })

  const prs = useQuery({
    queryKey: ['feature-prs', id],
    queryFn: () => getFeaturePRs(id!),
    enabled: !!id,
  })

  const [editing, setEditing] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')

  const patchMutation = useMutation({
    mutationFn: (data: Partial<{ name: string; description: string; priority: number }>) =>
      patchFeature(id!, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['feature', id] })
      queryClient.invalidateQueries({ queryKey: ['features'] })
      setEditing(null)
      addToast('Feature updated', 'success')
    },
  })

  if (feature.isLoading) return <PageSkeleton />
  if (!feature.data?.feature) {
    return (
      <div className="text-center py-12 text-text-muted">
        Feature not found
      </div>
    )
  }

  const f = feature.data.feature
  const featureCycles = feature.data.cycles || []
  const featureDiscussions = (discussions.data || []).filter((d) => d.feature_id === id)

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/features" className="hover:text-accent transition-colors">Features</Link>
        <span>/</span>
        <span className="text-text-secondary">{f.name}</span>
      </nav>

      {/* State Machine */}
      <FeatureStateMachine
        status={f.status}
        previousStatus={f.previous_status}
        qaResults={qaResults.data}
      />

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          {editing === 'name' ? (
            <input
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onBlur={() => {
                if (editValue !== f.name) patchMutation.mutate({ name: editValue })
                else setEditing(null)
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  if (editValue !== f.name) patchMutation.mutate({ name: editValue })
                  else setEditing(null)
                }
                if (e.key === 'Escape') setEditing(null)
              }}
              className="text-2xl font-bold bg-bg-input border border-accent rounded px-2 py-1 text-text-primary w-full"
              autoFocus
            />
          ) : (
            <h1
              className="text-2xl font-bold text-text-primary cursor-pointer hover:text-accent transition-colors"
              onClick={() => { setEditing('name'); setEditValue(f.name) }}
              title="Click to edit"
            >
              {f.name}
            </h1>
          )}

          {editing === 'description' ? (
            <textarea
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onBlur={() => {
                if (editValue !== (f.description || '')) patchMutation.mutate({ description: editValue })
                else setEditing(null)
              }}
              className="mt-2 w-full bg-bg-input border border-accent rounded px-2 py-1 text-sm text-text-secondary resize-y min-h-[60px]"
              autoFocus
            />
          ) : (
            f.description && (
              <p
                className="text-sm text-text-secondary mt-2 cursor-pointer hover:text-text-primary transition-colors"
                onClick={() => { setEditing('description'); setEditValue(f.description || '') }}
                title="Click to edit"
              >
                {f.description}
              </p>
            )
          )}
        </div>
        <StatusBadge status={f.status} />
      </div>

      {/* Inline QA Review — shown when feature is awaiting human QA */}
      {f.status === 'human-qa' && (
        <FeatureQAActions featureId={f.id} />
      )}

      {/* Metadata grid */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <MetaItem label="Priority" value={
          <span className={cn(
            'font-mono font-bold',
            f.priority >= 8 ? 'text-danger' : f.priority >= 5 ? 'text-warning' : 'text-text-primary'
          )}>
            {f.priority}
          </span>
        } />
        <MetaItem label="Milestone" value={
          f.milestone_id
            ? <EntityLink type="milestone" id={f.milestone_id} name={f.milestone_name || f.milestone_id} />
            : '—'
        } />
        <MetaItem label="Created" value={formatTimestamp(f.created_at)} />
        <MetaItem label="Updated" value={formatTimestamp(f.updated_at)} />
        {f.estimate_size && <MetaItem label="Estimate" value={f.estimate_size.toUpperCase()} />}
        {f.assigned_cycle && <MetaItem label="Cycle" value={
          <EntityLink type="cycle" id={f.assigned_cycle} name={f.assigned_cycle} />
        } />}
        {f.roadmap_item_id && <MetaItem label="Roadmap" value={
          <EntityLink type="roadmap" id={f.roadmap_item_id} name="View Roadmap Item" showIcon />
        } />}
      </div>

      {/* Tags */}
      {f.tags && f.tags.length > 0 && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-text-muted">Tags:</span>
          {f.tags.map((tag) => (
            <span key={tag} className="text-xs bg-accent/10 text-accent px-2 py-0.5 rounded-full">
              {tag}
            </span>
          ))}
        </div>
      )}

      {/* Spec */}
      {f.spec && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Feature Spec</h2>
          <MarkdownContent className="prose prose-sm prose-invert max-w-none text-text-secondary">
            {f.spec}
          </MarkdownContent>
        </div>
      )}

      {/* Attachments */}
      <AttachmentPanel entityType="feature" entityId={id!} />

      {/* Dependencies */}
      {deps.data && (deps.data.depends_on?.length > 0 || deps.data.depended_by?.length > 0) && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Dependencies</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {deps.data.depends_on?.length > 0 && (
              <div>
                <h3 className="text-xs text-text-muted uppercase tracking-wider mb-2">Depends on</h3>
                <div className="space-y-1">
                  {deps.data.depends_on.map((dep) => (
                    <div key={dep.id} className="flex items-center gap-2">
                      <Link
                        to={`/features/${dep.id}`}
                        className="text-sm text-accent hover:underline"
                      >
                        {dep.name}
                      </Link>
                      <StatusBadge status={dep.status} />
                    </div>
                  ))}
                </div>
              </div>
            )}
            {deps.data.depended_by?.length > 0 && (
              <div>
                <h3 className="text-xs text-text-muted uppercase tracking-wider mb-2">Blocks</h3>
                <div className="space-y-1">
                  {deps.data.depended_by.map((dep) => (
                    <div key={dep.id} className="flex items-center gap-2">
                      <Link
                        to={`/features/${dep.id}`}
                        className="text-sm text-warning hover:underline"
                      >
                        {dep.name}
                      </Link>
                      <StatusBadge status={dep.status} />
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      {/* QA History */}
      {qaResults.data && qaResults.data.length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">QA History</h2>
          <div className="space-y-2">
            {qaResults.data.map((r) => (
              <div
                key={r.id}
                className={cn(
                  'flex items-center gap-3 p-3 rounded border text-sm',
                  r.passed
                    ? 'bg-success/5 border-success/20'
                    : 'bg-danger/5 border-danger/20'
                )}
              >
                <span>{r.passed ? '✅' : '❌'}</span>
                <span className={r.passed ? 'text-success' : 'text-danger'}>
                  {r.qa_type === 'human' ? 'Human' : 'Agent'} QA — {r.passed ? 'Passed' : 'Failed'}
                </span>
                {r.notes && <span className="text-text-secondary">— {r.notes}</span>}
                <span className="ml-auto text-xs text-text-muted">{formatTimestamp(r.created_at)}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Pull Requests */}
      {prs.data && prs.data.length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Pull Requests</h2>
          <div className="space-y-2">
            {prs.data.map((pr) => (
              <a
                key={pr.pr_url}
                href={pr.pr_url}
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-3 p-3 rounded border border-border-light hover:border-accent/30 transition-colors text-sm"
              >
                <span className={cn(
                  'text-xs font-medium px-2 py-0.5 rounded',
                  pr.status === 'merged' ? 'bg-purple/10 text-purple' :
                  pr.status === 'open' ? 'bg-success/10 text-success' :
                  'bg-danger/10 text-danger'
                )}>
                  {pr.status}
                </span>
                <span className="font-mono text-text-muted">#{pr.pr_number}</span>
                <span className="text-text-secondary">{pr.repo}</span>
                <span className="ml-auto text-xs text-accent">View</span>
              </a>
            ))}
          </div>
        </div>
      )}

      {/* Related */}
      {(featureCycles.length > 0 || featureDiscussions.length > 0) && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Related</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {featureCycles.length > 0 && (
              <div>
                <h3 className="text-xs text-text-muted uppercase tracking-wider mb-2">Cycles</h3>
                <div className="space-y-1">
                  {featureCycles.map((c) => (
                    <div key={c.id}>
                      <EntityLink
                        type="cycle"
                        id={c.id}
                        name={`${c.cycle_type} #${c.iteration}`}
                        showIcon
                      />
                      <span className="ml-2 text-xs text-text-muted">({c.status})</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
            {featureDiscussions.length > 0 && (
              <div>
                <h3 className="text-xs text-text-muted uppercase tracking-wider mb-2">Discussions</h3>
                <div className="space-y-1">
                  {featureDiscussions.map((d) => (
                    <div key={d.id}>
                      <EntityLink
                        type="discussion"
                        id={d.id}
                        name={d.title}
                        showIcon
                      />
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

const STATE_MACHINE_STEPS: Exclude<FeatureStatus, 'blocked'>[] = [
  'draft', 'planning', 'implementing', 'agent-qa', 'human-qa', 'done',
]

const STEP_LABELS: Record<string, string> = {
  'draft': 'Draft',
  'planning': 'Planning',
  'implementing': 'Implementing',
  'agent-qa': 'Agent QA',
  'human-qa': 'Human QA',
  'done': 'Done',
}

type LifecycleStep = typeof STATE_MACHINE_STEPS[number]

function FeatureStateMachine({
  status,
  previousStatus,
  qaResults,
}: {
  status: FeatureStatus
  previousStatus?: string
  qaResults?: QAResult[]
}) {
  const isBlocked = status === 'blocked'
  const effectiveStep = (isBlocked ? (previousStatus || 'draft') : status) as LifecycleStep
  const currentIndex = STATE_MACHINE_STEPS.indexOf(effectiveStep)

  const rejectionCount = useMemo(() => {
    if (!qaResults || !Array.isArray(qaResults)) return 0
    return qaResults.filter((r) => r.qa_type === 'human' && !r.passed).length
  }, [qaResults])

  return (
    <div className="relative">
      {isBlocked && (
        <div className="absolute inset-0 flex items-center justify-center z-10 pointer-events-none">
          <span className="text-[10px] font-bold uppercase tracking-wider text-danger bg-danger/10 border border-danger/30 px-2 py-0.5 rounded">
            Blocked
          </span>
        </div>
      )}
      <div className={cn('flex gap-0.5 items-start', isBlocked && 'opacity-50')}>
        {STATE_MACHINE_STEPS.map((step, i) => {
          const isCurrent = i === currentIndex
          const isDone = i < currentIndex
          const isFuture = i > currentIndex
          const isHumanQa = step === 'human-qa'

          return (
            <div key={step} className="flex-1 flex flex-col items-center gap-1">
              <div
                className={cn(
                  'h-1.5 w-full rounded-sm',
                  isDone && 'bg-success',
                  isCurrent && 'bg-accent',
                  isFuture && 'bg-bg-tertiary',
                )}
              />
              <span className={cn(
                'text-[10px] whitespace-nowrap',
                isCurrent ? 'text-text-primary font-semibold' : 'text-text-muted font-normal',
              )}>
                {STEP_LABELS[step]}
              </span>
              {isHumanQa && rejectionCount > 0 && (
                <span className="text-[9px] text-danger font-medium -mt-0.5">
                  rejected x{rejectionCount}
                </span>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function FeatureQAActions({ featureId }: { featureId: string }) {
  const queryClient = useQueryClient()
  const addToast = useStore((s) => s.addToast)
  const [showRejectForm, setShowRejectForm] = useState(false)
  const [rejectNotes, setRejectNotes] = useState('')
  const [showApproveNotes, setShowApproveNotes] = useState(false)
  const [approveNotes, setApproveNotes] = useState('')

  const invalidateAll = () => {
    queryClient.invalidateQueries({ queryKey: ['feature', featureId] })
    queryClient.invalidateQueries({ queryKey: ['features'] })
    queryClient.invalidateQueries({ queryKey: ['qa-pending'] })
    queryClient.invalidateQueries({ queryKey: ['qa-results', featureId] })
    queryClient.invalidateQueries({ queryKey: ['status'] })
  }

  const approveMut = useMutation({
    mutationFn: (n?: string) => approveFeature(featureId, n),
    onSuccess: () => {
      invalidateAll()
      addToast('Feature approved', 'success')
    },
    onError: (err) => addToast(`Approve failed: ${err.message}`, 'error'),
  })

  const rejectMut = useMutation({
    mutationFn: (n?: string) => rejectFeature(featureId, n),
    onSuccess: () => {
      invalidateAll()
      addToast('Feature rejected — sent back to development', 'info')
    },
    onError: (err) => addToast(`Reject failed: ${err.message}`, 'error'),
  })

  return (
    <div className="bg-warning/5 border border-warning/20 rounded-lg p-5 space-y-4"
      style={{ borderLeft: '3px solid rgb(245, 158, 11)' }}>
      <div className="text-sm font-semibold text-warning">Awaiting QA Review</div>

      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <button
            onClick={() => {
              if (showApproveNotes) {
                approveMut.mutate(approveNotes || undefined)
                setApproveNotes('')
                setShowApproveNotes(false)
              } else {
                approveMut.mutate(undefined)
              }
            }}
            disabled={approveMut.isPending}
            className="px-5 py-2 bg-success/20 text-success border border-success/30 rounded-md text-sm font-medium hover:bg-success/30 transition-colors disabled:opacity-50"
          >
            {approveMut.isPending ? 'Approving...' : 'Approve'}
          </button>
          {!showApproveNotes && !showRejectForm && (
            <button
              onClick={() => setShowApproveNotes(true)}
              className="text-xs text-text-muted hover:text-text-secondary transition-colors"
              title="Add a note with approval"
            >
              + note
            </button>
          )}
          <button
            onClick={() => { setShowRejectForm(!showRejectForm); setShowApproveNotes(false) }}
            className={cn(
              'px-5 py-2 rounded-md text-sm font-medium transition-colors',
              showRejectForm
                ? 'bg-danger/20 text-danger border border-danger/30'
                : 'bg-danger/10 text-danger border border-danger/20 hover:bg-danger/20'
            )}
          >
            Reject
          </button>
        </div>

        {/* Optional approve notes — small inline input */}
        {showApproveNotes && !showRejectForm && (
          <div className="flex items-center gap-2">
            <input
              type="text"
              value={approveNotes}
              onChange={(e) => setApproveNotes(e.target.value)}
              placeholder="Quick note (optional)"
              className="flex-1 bg-bg-input border border-border rounded-md px-3 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:border-accent focus:outline-none"
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  approveMut.mutate(approveNotes || undefined)
                  setApproveNotes('')
                  setShowApproveNotes(false)
                }
                if (e.key === 'Escape') setShowApproveNotes(false)
              }}
              autoFocus
            />
            <button
              onClick={() => setShowApproveNotes(false)}
              className="text-xs text-text-muted hover:text-text-secondary"
            >
              cancel
            </button>
          </div>
        )}

        {/* Reject form — large textarea for detailed feedback */}
        {showRejectForm && (
          <div className="bg-danger/5 border border-danger/20 rounded-lg p-4 space-y-3">
            <label className="block text-xs font-semibold text-danger uppercase tracking-wider">
              Rejection feedback
            </label>
            <textarea
              value={rejectNotes}
              onChange={(e) => setRejectNotes(e.target.value)}
              placeholder="Describe what needs to change..."
              rows={5}
              className="w-full bg-bg-input border border-border rounded-md px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-danger/50 focus:outline-none resize-y leading-relaxed"
              style={{ minHeight: 120 }}
              autoFocus
            />
            <div className="flex items-center gap-2">
              <button
                onClick={() => { rejectMut.mutate(rejectNotes || undefined); setRejectNotes(''); setShowRejectForm(false) }}
                disabled={rejectMut.isPending}
                className="px-4 py-2 bg-danger text-white rounded-md text-sm font-medium hover:bg-danger/80 transition-colors disabled:opacity-50"
              >
                {rejectMut.isPending ? 'Rejecting...' : 'Confirm Reject'}
              </button>
              <button
                onClick={() => { setShowRejectForm(false); setRejectNotes('') }}
                className="px-4 py-2 bg-bg-tertiary text-text-secondary rounded-md text-sm hover:bg-bg-hover transition-colors"
              >
                Cancel
              </button>
            </div>
          </div>
        )}
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
