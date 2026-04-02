import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getQAPending, approveFeature, rejectFeature, getQAResults, getCycleTypes, getWorkstreams, getWorkstreamFeatures } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { useStore } from '../store'
import { formatTimestamp, cn } from '../lib/utils'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { Feature, QAResult, CycleType, Workstream, WorkstreamFeature } from '../api/types'
import { MarkdownContent } from '../components/MarkdownContent'

type GroupMode = 'workstream' | 'milestone'

const UNGROUPED = '__ungrouped__'

interface FeatureGroup {
  key: string
  label: string
  features: Feature[]
  relationship?: 'owned' | 'dependency'
}

function groupByMilestone(features: Feature[]): FeatureGroup[] {
  const map = new Map<string, Feature[]>()
  const labels = new Map<string, string>()

  for (const f of features) {
    const key = f.milestone_id || UNGROUPED
    const label = f.milestone_name || 'Ungrouped'
    if (!map.has(key)) {
      map.set(key, [])
      labels.set(key, label)
    }
    map.get(key)!.push(f)
  }

  const groups: FeatureGroup[] = []
  for (const [key, feats] of map) {
    groups.push({ key, label: labels.get(key)!, features: feats })
  }
  groups.sort((a, b) => {
    if (a.key === UNGROUPED) return 1
    if (b.key === UNGROUPED) return -1
    return a.label.localeCompare(b.label)
  })
  return groups
}

function groupByWorkstream(
  qaFeatures: Feature[],
  wsFeatures: WorkstreamFeature[],
): FeatureGroup[] {
  const qaSet = new Set(qaFeatures.map((f) => f.id))
  const owned: Feature[] = []
  const deps: Feature[] = []

  for (const wf of wsFeatures) {
    if (!qaSet.has(wf.feature.id)) continue
    const full = qaFeatures.find((f) => f.id === wf.feature.id) || wf.feature
    if (wf.relationship === 'owned') owned.push(full)
    else deps.push(full)
  }

  const groups: FeatureGroup[] = []
  if (owned.length > 0) {
    groups.push({ key: 'owned', label: 'Owned Features', features: owned, relationship: 'owned' })
  }
  if (deps.length > 0) {
    groups.push({ key: 'dependency', label: 'Dependencies', features: deps, relationship: 'dependency' })
  }
  return groups
}

/** Count how many QA features belong to a workstream (needs prefetched wsFeatures map) */
function countQAFeatures(qaIds: Set<string>, wsFeatures: WorkstreamFeature[]): { owned: number; deps: number } {
  let owned = 0, deps = 0
  for (const wf of wsFeatures) {
    if (!qaIds.has(wf.feature.id)) continue
    if (wf.relationship === 'owned') owned++
    else deps++
  }
  return { owned, deps }
}

export function QA() {
  const queryClient = useQueryClient()
  const addToast = useStore((s) => s.addToast)
  const pending = useQuery({ queryKey: ['qa-pending'], queryFn: getQAPending })
  const cycleTypesQuery = useQuery({ queryKey: ['cycle-types'], queryFn: getCycleTypes })
  const workstreamsQuery = useQuery({ queryKey: ['workstreams'], queryFn: () => getWorkstreams('active') })
  const cycleTypes = cycleTypesQuery.data || []
  const workstreams = workstreamsQuery.data || []

  const [groupMode, setGroupMode] = useState<GroupMode>('workstream')
  const [activeMilestone, setActiveMilestone] = useState<string | null>(null)
  const [activeWorkstream, setActiveWorkstream] = useState<string | null>(null)

  // Fetch features for the active workstream only
  const activeWsFeatures = useQuery({
    queryKey: ['workstream-features', activeWorkstream],
    queryFn: () => getWorkstreamFeatures(activeWorkstream!),
    enabled: !!activeWorkstream,
  })

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
  const qaIds = new Set(needsReview.map((f) => f.id))

  // Groups for the active drilldown
  const workstreamGroups = activeWorkstream && activeWsFeatures.data
    ? groupByWorkstream(needsReview, activeWsFeatures.data)
    : []

  const milestoneGroups = groupByMilestone(needsReview)

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
        <>
          {/* Mode toggle */}
          <div className="flex items-center gap-4">
            <div className="flex rounded-lg border border-border overflow-hidden">
              <button
                onClick={() => { setGroupMode('workstream'); setActiveMilestone(null); setActiveWorkstream(null) }}
                className={cn(
                  'px-3 py-1.5 text-xs font-medium transition-colors',
                  groupMode === 'workstream'
                    ? 'bg-accent/20 text-accent'
                    : 'bg-bg-card text-text-secondary hover:text-text-primary'
                )}
              >
                By Workstream
              </button>
              <button
                onClick={() => { setGroupMode('milestone'); setActiveWorkstream(null) }}
                className={cn(
                  'px-3 py-1.5 text-xs font-medium border-l border-border transition-colors',
                  groupMode === 'milestone'
                    ? 'bg-accent/20 text-accent'
                    : 'bg-bg-card text-text-secondary hover:text-text-primary'
                )}
              >
                By Milestone
              </button>
            </div>
          </div>

          {/* ===== Workstream mode ===== */}
          {groupMode === 'workstream' && !activeWorkstream && (
            <div className="grid gap-3 md:grid-cols-2">
              {workstreams.map((ws) => (
                <WorkstreamCard
                  key={ws.id}
                  ws={ws}
                  qaIds={qaIds}
                  onSelect={() => setActiveWorkstream(ws.id)}
                />
              ))}
            </div>
          )}

          {groupMode === 'workstream' && activeWorkstream && (
            <>
              {/* Workstream header */}
              <div className="bg-accent/5 border border-accent/20 rounded-lg px-4 py-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <button
                      onClick={() => setActiveWorkstream(null)}
                      className="text-xs text-text-muted hover:text-accent transition-colors"
                    >
                      ← All workstreams
                    </button>
                    <span className="text-border">|</span>
                    <span className="text-sm font-medium text-accent">
                      {workstreams.find((ws) => ws.id === activeWorkstream)?.name}
                    </span>
                  </div>
                  <Link
                    to={`/workstreams/${activeWorkstream}`}
                    className="text-xs text-accent hover:text-accent/80 transition-colors"
                  >
                    View full workstream →
                  </Link>
                </div>
                {workstreams.find((ws) => ws.id === activeWorkstream)?.description && (
                  <p className="text-xs text-text-muted mt-1">
                    {workstreams.find((ws) => ws.id === activeWorkstream)?.description}
                  </p>
                )}
              </div>

              {activeWsFeatures.isLoading && <PageSkeleton />}

              {workstreamGroups.length === 0 && !activeWsFeatures.isLoading && (
                <div className="bg-bg-card border border-border rounded-lg p-8 text-center">
                  <p className="text-sm text-text-secondary">No features in this workstream need QA right now</p>
                </div>
              )}

              <div className="space-y-6">
                {workstreamGroups.map((group) => (
                  <GroupSection
                    key={group.key}
                    group={group}
                    cycleTypes={cycleTypes}
                    onApprove={(id, notes) => approveMutation.mutate({ id, notes })}
                    onReject={(id, notes) => rejectMutation.mutate({ id, notes })}
                    isApproving={approveMutation.isPending}
                    isRejecting={rejectMutation.isPending}
                    defaultExpanded={true}
                  />
                ))}
              </div>
            </>
          )}

          {/* ===== Milestone mode ===== */}
          {groupMode === 'milestone' && (
            <>
              {milestoneGroups.length > 1 && (
                <div className="flex flex-wrap gap-2">
                  <button
                    onClick={() => setActiveMilestone(null)}
                    className={cn(
                      'px-3 py-1.5 rounded-full text-xs font-medium border transition-colors',
                      !activeMilestone
                        ? 'bg-accent/20 text-accent border-accent/30'
                        : 'bg-bg-card text-text-secondary border-border hover:border-accent/30'
                    )}
                  >
                    All ({needsReview.length})
                  </button>
                  {milestoneGroups.map((g) => (
                    <button
                      key={g.key}
                      onClick={() => setActiveMilestone(activeMilestone === g.key ? null : g.key)}
                      className={cn(
                        'px-3 py-1.5 rounded-full text-xs font-medium border transition-colors',
                        activeMilestone === g.key
                          ? 'bg-accent/20 text-accent border-accent/30'
                          : 'bg-bg-card text-text-secondary border-border hover:border-accent/30'
                      )}
                    >
                      {g.label} ({g.features.length})
                    </button>
                  ))}
                </div>
              )}

              <div className="space-y-6">
                {(activeMilestone
                  ? milestoneGroups.filter((g) => g.key === activeMilestone)
                  : milestoneGroups
                ).map((group) => (
                  <GroupSection
                    key={group.key}
                    group={group}
                    cycleTypes={cycleTypes}
                    onApprove={(id, notes) => approveMutation.mutate({ id, notes })}
                    onReject={(id, notes) => rejectMutation.mutate({ id, notes })}
                    isApproving={approveMutation.isPending}
                    isRejecting={rejectMutation.isPending}
                    defaultExpanded={milestoneGroups.length === 1 || !!activeMilestone}
                  />
                ))}
              </div>
            </>
          )}
        </>
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

function WorkstreamCard({ ws, qaIds, onSelect }: {
  ws: Workstream
  qaIds: Set<string>
  onSelect: () => void
}) {
  const wsFeatures = useQuery({
    queryKey: ['workstream-features', ws.id],
    queryFn: () => getWorkstreamFeatures(ws.id),
  })

  const counts = wsFeatures.data ? countQAFeatures(qaIds, wsFeatures.data) : { owned: 0, deps: 0 }
  const qaOwned = counts.owned
  const qaDeps = counts.deps
  const total = qaOwned + qaDeps

  return (
    <div
      onClick={onSelect}
      className="bg-bg-card border border-border rounded-lg p-4 cursor-pointer hover:border-accent/40 hover:bg-bg-hover/30 transition-colors"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold text-text-primary">{ws.name}</h3>
          {ws.description && (
            <p className="text-xs text-text-secondary mt-1 line-clamp-2">{ws.description}</p>
          )}
        </div>
        <div className="shrink-0 text-right">
          {total > 0 ? (
            <span className="text-lg font-bold text-accent">{total}</span>
          ) : (
            <span className="text-lg font-bold text-success">✓</span>
          )}
        </div>
      </div>

      {total > 0 && (
        <div className="flex items-center gap-3 mt-3 text-xs text-text-muted">
          {qaOwned > 0 && (
            <span>{qaOwned} owned</span>
          )}
          {qaDeps > 0 && (
            <span className="text-warning">{qaDeps} prerequisite{qaDeps !== 1 ? 's' : ''}</span>
          )}
        </div>
      )}

      {ws.tags && (
        <div className="flex flex-wrap gap-1 mt-2">
          {ws.tags.split(',').map((tag) => (
            <span key={tag} className="text-[10px] px-1.5 py-0.5 rounded bg-bg-tertiary text-text-muted">
              {tag.trim()}
            </span>
          ))}
        </div>
      )}

      <div className="flex items-center justify-between mt-3">
        <Link
          to={`/workstreams/${ws.id}`}
          onClick={(e) => e.stopPropagation()}
          className="text-[10px] text-accent hover:text-accent/80 transition-colors"
        >
          Full details →
        </Link>
        {total > 0 && (
          <span className="text-[10px] text-accent font-medium">
            Review QA →
          </span>
        )}
      </div>
    </div>
  )
}

function GroupSection({ group, cycleTypes, onApprove, onReject, isApproving, isRejecting, defaultExpanded }: {
  group: FeatureGroup
  cycleTypes: CycleType[]
  onApprove: (id: string, notes?: string) => void
  onReject: (id: string, notes?: string) => void
  isApproving: boolean
  isRejecting: boolean
  defaultExpanded: boolean
}) {
  const [collapsed, setCollapsed] = useState(!defaultExpanded)
  const isMilestone = group.key !== UNGROUPED && !group.relationship

  return (
    <div>
      <button
        onClick={() => setCollapsed(!collapsed)}
        className="flex items-center gap-2 mb-2 group w-full text-left"
      >
        <span className="text-text-muted text-xs">{collapsed ? '▶' : '▼'}</span>
        <h2 className="text-sm font-semibold text-text-primary group-hover:text-accent transition-colors">
          {isMilestone ? (
            <EntityLink type="milestone" id={group.key} name={group.label} />
          ) : (
            group.label
          )}
        </h2>
        <span className="text-xs text-text-muted font-mono">
          {group.features.length}
        </span>
        {group.relationship === 'dependency' && (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-warning/10 text-warning border border-warning/20">
            prerequisite
          </span>
        )}
      </button>

      {!collapsed && (
        <div className="space-y-3 ml-4 border-l-2 border-border pl-4">
          {group.features.map((feature) => (
            <QACard
              key={feature.id}
              feature={feature}
              cycleTypes={cycleTypes}
              onApprove={(notes) => onApprove(feature.id, notes)}
              onReject={(notes) => onReject(feature.id, notes)}
              isApproving={isApproving}
              isRejecting={isRejecting}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function QACard({ feature, cycleTypes, onApprove, onReject, isApproving, isRejecting }: {
  feature: Feature
  cycleTypes: CycleType[]
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

  const cycleType = cycleTypes.find((ct) => ct.name === feature.assigned_cycle)
  const humanStep = cycleType?.steps?.find((s) => s.human && s.instructions)
  const testPlanInstructions = humanStep?.instructions

  return (
    <div className="bg-bg-card border border-border rounded-lg overflow-hidden">
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

      {expanded && (
        <div className="border-t border-border p-4 space-y-4">
          {reviewRound > 1 && (
            <div className="flex items-center gap-2 text-xs text-warning bg-warning/5 border border-warning/20 rounded-md px-3 py-2">
              <span>⚠️</span>
              <span>Review round #{reviewRound} — previously reviewed {reviewRound - 1} time{reviewRound > 2 ? 's' : ''}</span>
            </div>
          )}

          {testPlanInstructions && (
            <div className="bg-warning/5 rounded-lg p-4 border border-warning/20">
              <h4 className="text-xs font-semibold text-warning uppercase tracking-wider mb-2">
                Test Plan
              </h4>
              <div className="prose prose-sm prose-invert max-w-none text-sm text-text-secondary">
                <MarkdownContent>{testPlanInstructions}</MarkdownContent>
              </div>
            </div>
          )}

          {feature.spec && (
            <div className="bg-bg-secondary rounded-lg p-4 border border-border-light">
              <h4 className="text-xs font-semibold text-text-muted uppercase tracking-wider mb-2">
                Feature Spec
              </h4>
              <div className="prose prose-sm prose-invert max-w-none text-sm text-text-secondary">
                <MarkdownContent>{feature.spec}</MarkdownContent>
              </div>
            </div>
          )}

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
