import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useParams, Link } from 'react-router-dom'
import { getWorkstream, addWorkstreamNote, resolveWorkstreamNote, addWorkstreamLink, getConfig, getFeature, getCycleDetail, advanceCycle, getWorkstreamFeatures } from '../api/client'
import type { WorkstreamNote, WorkstreamLink, AppConfig, CycleStep, WorkstreamFeature, FeatureStatus } from '../api/types'
import { useState, useMemo, useCallback } from 'react'
import { StatusBadge } from '../components/StatusBadge'
import { cn, truncate } from '../lib/utils'

const NOTE_COLORS: Record<string, { bg: string; border: string; label: string }> = {
  note:     { bg: 'var(--color-bg-tertiary)', border: 'var(--color-border)', label: 'Note' },
  question: { bg: 'rgba(245, 158, 11, 0.08)', border: 'rgba(245, 158, 11, 0.3)', label: 'Question' },
  decision: { bg: 'rgba(34, 197, 94, 0.08)', border: 'rgba(34, 197, 94, 0.3)', label: 'Decision' },
  idea:     { bg: 'rgba(167, 139, 250, 0.08)', border: 'rgba(167, 139, 250, 0.3)', label: 'Idea' },
  import:   { bg: 'rgba(59, 130, 246, 0.08)', border: 'rgba(59, 130, 246, 0.3)', label: 'Import' },
}

function NoteCard({ note, onResolve }: { note: WorkstreamNote; onResolve: () => void }) {
  const style = NOTE_COLORS[note.note_type] || NOTE_COLORS.note
  const isQuestion = note.note_type === 'question'
  const isResolved = note.resolved === 1

  return (
    <div style={{
      padding: '12px 16px',
      background: style.bg,
      border: `1px solid ${style.border}`,
      borderRadius: 8,
      borderLeft: `3px solid ${style.border}`,
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 12 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 6 }}>
            <span style={{ fontSize: 11, fontWeight: 600, textTransform: 'uppercase', color: style.border, letterSpacing: '0.05em' }}>
              {style.label}
            </span>
            {isQuestion && (
              <span style={{
                fontSize: 10, padding: '1px 6px', borderRadius: 99,
                background: isResolved ? 'rgba(34,197,94,0.15)' : 'rgba(245,158,11,0.15)',
                color: isResolved ? 'rgb(34,197,94)' : 'rgb(245,158,11)',
                fontWeight: 600,
              }}>
                {isResolved ? 'Resolved' : 'Open'}
              </span>
            )}
            {note.source && (
              <span style={{ fontSize: 10, color: 'var(--color-text-muted)' }}>via {note.source}</span>
            )}
          </div>
          <div className="prose" style={{ fontSize: 14, lineHeight: 1.5 }} dangerouslySetInnerHTML={{ __html: simpleMarkdown(note.content) }} />
        </div>
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 4 }}>
          <span style={{ fontSize: 11, color: 'var(--color-text-muted)', whiteSpace: 'nowrap' }}>
            {formatTime(note.created_at)}
          </span>
          {isQuestion && !isResolved && (
            <button
              onClick={onResolve}
              style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: 'rgba(34,197,94,0.15)', color: 'rgb(34,197,94)', border: 'none', cursor: 'pointer', fontWeight: 600 }}
            >
              Resolve
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

function LinkCard({ link, config }: { link: WorkstreamLink; config?: AppConfig }) {
  const vantageUrl = config?.vantage_url
  const projectId = config?.project_id || 'tillr'

  let href = ''
  let label = link.label || ''
  let icon = ''

  switch (link.link_type) {
    case 'feature':
      href = `/features/${link.target_id}`
      label = label || link.target_id || 'Feature'
      icon = 'F'
      break
    case 'doc':
      if (vantageUrl && link.target_url) {
        href = `${vantageUrl}/${projectId}/${link.target_url}`
      }
      label = label || link.target_url || 'Document'
      icon = 'D'
      break
    case 'url':
      href = link.target_url || ''
      label = label || link.target_url || 'Link'
      icon = 'U'
      break
    case 'discussion':
      href = `/discussions/${link.target_id}`
      label = label || link.target_id || 'Discussion'
      icon = 'C'
      break
  }

  const isExternal = href.startsWith('http')
  const Wrapper = isExternal ? 'a' : Link
  const wrapperProps = isExternal
    ? { href, target: '_blank', rel: 'noopener noreferrer' }
    : { to: href }

  return (
    <Wrapper
      {...(wrapperProps as any)}
      style={{
        display: 'flex', alignItems: 'center', gap: 10,
        padding: '8px 12px', borderRadius: 6,
        background: 'var(--color-bg-tertiary)',
        border: '1px solid var(--color-border)',
        textDecoration: 'none', color: 'inherit', fontSize: 13,
      }}
    >
      <span style={{
        width: 22, height: 22, borderRadius: 4,
        background: 'var(--color-accent)', color: 'white',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontSize: 11, fontWeight: 700, flexShrink: 0,
      }}>
        {icon}
      </span>
      <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {label}
      </span>
      <span style={{ fontSize: 10, color: 'var(--color-text-muted)', textTransform: 'uppercase' }}>
        {link.link_type}
      </span>
      {isExternal && <span style={{ fontSize: 12 }}>&#8599;</span>}
    </Wrapper>
  )
}

function CycleApproveReject({ cycleId, stepName }: { cycleId: number; stepName: string }) {
  const [notes, setNotes] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const queryClient = useQueryClient()

  const handleAction = async (action: 'approve' | 'reject') => {
    setSubmitting(true)
    try {
      await advanceCycle(cycleId, action, notes)
      queryClient.invalidateQueries({ queryKey: ['cycle', cycleId] })
      queryClient.invalidateQueries({ queryKey: ['workstream'] })
      queryClient.invalidateQueries({ queryKey: ['feature'] })
      setNotes('')
    } catch (err) {
      alert(`Failed to ${action}: ${err instanceof Error ? err.message : err}`)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div style={{
      marginTop: 10, padding: '12px 16px', borderRadius: 6,
      background: 'rgba(245, 158, 11, 0.08)', border: '1px solid rgba(245, 158, 11, 0.2)',
    }}>
      <div style={{ fontSize: 13, color: 'rgb(245, 158, 11)', fontWeight: 600, marginBottom: 8 }}>
        Waiting for human input: {stepName}
      </div>
      <textarea
        value={notes}
        onChange={e => setNotes(e.target.value)}
        placeholder="Notes (optional)..."
        style={{
          width: '100%', minHeight: 60, padding: '8px 10px', borderRadius: 6,
          border: '1px solid var(--color-border)', background: 'var(--color-bg-primary)',
          color: 'var(--color-text-primary)', fontSize: 13, resize: 'vertical',
          fontFamily: 'inherit', boxSizing: 'border-box',
        }}
      />
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <button
          onClick={() => handleAction('approve')}
          disabled={submitting}
          style={{
            padding: '6px 16px', borderRadius: 6, border: 'none', cursor: 'pointer',
            background: 'var(--color-success, #22c55e)', color: '#fff', fontWeight: 600, fontSize: 13,
            opacity: submitting ? 0.6 : 1,
          }}
        >
          Approve &amp; Advance
        </button>
        <button
          onClick={() => handleAction('reject')}
          disabled={submitting}
          style={{
            padding: '6px 16px', borderRadius: 6, border: '1px solid rgba(245, 158, 11, 0.3)',
            cursor: 'pointer', background: 'transparent', color: 'rgb(245, 158, 11)',
            fontWeight: 600, fontSize: 13, opacity: submitting ? 0.6 : 1,
          }}
        >
          Request Changes
        </button>
      </div>
    </div>
  )
}

/* ── Progress Bar ── */

const PROGRESS_COLORS: Record<string, string> = {
  done: 'bg-success',
  'human-qa': 'bg-warning',
  'agent-qa': 'bg-orange',
  implementing: 'bg-accent',
  planning: 'bg-purple',
  draft: 'bg-bg-tertiary',
  blocked: 'bg-danger',
}

const PROGRESS_ORDER: FeatureStatus[] = ['done', 'human-qa', 'agent-qa', 'implementing', 'planning', 'draft', 'blocked']

function WorkstreamProgressBar({ counts, total, doneCount }: { counts: Record<string, number>; total: number; doneCount: number }) {
  if (total === 0) return null
  return (
    <div>
      <div className="flex h-2 rounded-full overflow-hidden bg-bg-tertiary">
        {PROGRESS_ORDER.map((status) => {
          const count = counts[status] || 0
          if (count === 0) return null
          const pct = (count / total) * 100
          return (
            <div
              key={status}
              className={cn(PROGRESS_COLORS[status], 'transition-all duration-500')}
              style={{ width: `${pct}%` }}
              title={`${status}: ${count}`}
            />
          )
        })}
      </div>
      <p className="text-xs text-text-muted mt-1.5">
        {doneCount} of {total} feature{total !== 1 ? 's' : ''} complete
      </p>
    </div>
  )
}

/* ── Feature List ── */

const GROUP_CONFIG = [
  { key: 'attention', label: 'Needs Attention', defaultOpen: true },
  { key: 'inProgress', label: 'In Progress', defaultOpen: true },
  { key: 'backlog', label: 'Backlog', defaultOpen: true },
  { key: 'completed', label: 'Completed', defaultOpen: false },
] as const

function WorkstreamFeatureList({ groups }: { groups: Record<string, WorkstreamFeature[]> }) {
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {}
    for (const g of GROUP_CONFIG) {
      init[g.key] = !g.defaultOpen
    }
    return init
  })

  const toggle = (key: string) => setCollapsed(prev => ({ ...prev, [key]: !prev[key] }))

  const hasAny = GROUP_CONFIG.some(g => (groups[g.key] || []).length > 0)
  if (!hasAny) return null

  return (
    <div className="mb-6">
      <h2 className="text-[15px] font-semibold text-text-secondary mb-3">Features</h2>
      <div className="space-y-4">
        {GROUP_CONFIG.map(({ key, label }) => {
          const items = groups[key] || []
          if (items.length === 0) return null
          const isCollapsed = collapsed[key]
          return (
            <div key={key}>
              <button
                onClick={() => toggle(key)}
                className="flex items-center gap-2 text-sm text-text-muted hover:text-text-secondary transition-colors w-full mb-2"
              >
                <span className="text-xs">{isCollapsed ? '\u25B6' : '\u25BC'}</span>
                <span>{label} ({items.length})</span>
                <div className="flex-1 border-t border-border ml-2" />
              </button>
              {!isCollapsed && (
                <div className="space-y-1.5">
                  {items.map(wf => (
                    <Link
                      key={wf.feature.id}
                      to={`/features/${wf.feature.id}`}
                      className={cn(
                        'block bg-bg-card border border-border rounded-lg p-3 hover:border-accent/30 transition-colors',
                        wf.relationship === 'dependency' && 'ml-6'
                      )}
                    >
                      <div className="flex items-center justify-between gap-3">
                        <div className="flex items-center gap-2.5 min-w-0 flex-1">
                          <span className={cn(
                            'text-xs font-mono shrink-0 w-7 text-center rounded py-0.5',
                            wf.feature.priority >= 8 ? 'bg-danger/10 text-danger' :
                            wf.feature.priority >= 5 ? 'bg-warning/10 text-warning' :
                            'bg-bg-tertiary text-text-muted'
                          )}>
                            {wf.feature.priority}
                          </span>
                          <div className="min-w-0">
                            <div className="flex items-center gap-2">
                              <h3 className="text-sm font-medium text-text-primary truncate">{wf.feature.name}</h3>
                              {wf.relationship === 'dependency' && (
                                <span className="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-bg-tertiary text-text-muted shrink-0">
                                  prerequisite
                                </span>
                              )}
                            </div>
                            {wf.feature.description && (
                              <p className="text-xs text-text-secondary mt-0.5 truncate">
                                {truncate(wf.feature.description, 100)}
                              </p>
                            )}
                          </div>
                        </div>
                        <StatusBadge status={wf.feature.status} />
                      </div>
                    </Link>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

/* ── Needs Attention Summary ── */

function NeedsAttentionSummary({ features, openQuestionCount }: {
  features: WorkstreamFeature[]
  openQuestionCount: number
}) {
  const qaFeatures = features.filter(wf => wf.feature.status === 'human-qa')
  const blockedFeatures = features.filter(wf => wf.feature.status === 'blocked')

  const hasItems = qaFeatures.length > 0 || openQuestionCount > 0 || blockedFeatures.length > 0

  const scrollTo = useCallback((targetId: string) => {
    document.getElementById(targetId)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [])

  if (!hasItems) {
    return (
      <div className="mb-5 rounded-lg px-4 py-2.5 text-sm text-[var(--color-text-muted)]"
        style={{ background: 'var(--color-bg-secondary)', border: '1px solid var(--color-border)' }}>
        All clear — nothing needs your attention
      </div>
    )
  }

  return (
    <div className="mb-5 rounded-lg overflow-hidden"
      style={{
        background: 'rgba(245, 158, 11, 0.08)',
        border: '1px solid rgba(245, 158, 11, 0.2)',
        borderLeft: '3px solid rgb(245, 158, 11)',
      }}>
      <div className="px-4 py-3 flex flex-col gap-1.5">
        <div className="text-xs font-semibold uppercase tracking-wide" style={{ color: 'rgb(245, 158, 11)' }}>
          Needs Attention
        </div>
        {qaFeatures.length > 0 && (
          <button
            onClick={() => scrollTo('qa-features')}
            className="text-left text-sm font-medium hover:underline cursor-pointer bg-transparent border-none p-0"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {qaFeatures.length} feature{qaFeatures.length > 1 ? 's' : ''} awaiting QA review
          </button>
        )}
        {openQuestionCount > 0 && (
          <button
            onClick={() => scrollTo('open-questions')}
            className="text-left text-sm font-medium hover:underline cursor-pointer bg-transparent border-none p-0"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {openQuestionCount} open question{openQuestionCount > 1 ? 's' : ''}
          </button>
        )}
        {blockedFeatures.length > 0 && (
          <button
            onClick={() => scrollTo('blocked-features')}
            className="text-left text-sm font-medium hover:underline cursor-pointer bg-transparent border-none p-0"
            style={{ color: 'var(--color-text-primary)' }}
          >
            {blockedFeatures.length} blocked feature{blockedFeatures.length > 1 ? 's' : ''}
          </button>
        )}
      </div>
    </div>
  )
}

/* ── Main Component ── */

export default function WorkstreamDetail() {
  const { id } = useParams<{ id: string }>()
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['workstream', id],
    queryFn: () => getWorkstream(id!),
    enabled: !!id,
  })

  const { data: config } = useQuery({
    queryKey: ['config'],
    queryFn: getConfig,
  })

  // Add note form state
  const [noteContent, setNoteContent] = useState('')
  const [noteType, setNoteType] = useState<string>('note')

  const addNoteMut = useMutation({
    mutationFn: () => addWorkstreamNote(id!, { content: noteContent, note_type: noteType }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workstream', id] })
      setNoteContent('')
      setNoteType('note')
    },
  })

  const resolveMut = useMutation({
    mutationFn: (noteId: number) => resolveWorkstreamNote(id!, noteId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['workstream', id] }),
  })

  // Add link form state
  const [showLinkForm, setShowLinkForm] = useState(false)
  const [linkType, setLinkType] = useState<string>('doc')
  const [linkTarget, setLinkTarget] = useState('')
  const [linkLabel, setLinkLabel] = useState('')

  const addLinkMut = useMutation({
    mutationFn: () => {
      const isIdType = linkType === 'feature' || linkType === 'discussion'
      return addWorkstreamLink(id!, {
        link_type: linkType,
        target_id: isIdType ? linkTarget : undefined,
        target_url: !isIdType ? linkTarget : undefined,
        label: linkLabel,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workstream', id] })
      setShowLinkForm(false)
      setLinkTarget('')
      setLinkLabel('')
    },
  })

  // Fetch linked feature details (for cycle status)
  const featureLinks = data?.links.filter(l => l.link_type === 'feature') ?? []
  const linkedFeatureId = featureLinks[0]?.target_id
  const { data: linkedFeature } = useQuery({
    queryKey: ['feature', linkedFeatureId],
    queryFn: () => getFeature(linkedFeatureId!),
    enabled: !!linkedFeatureId,
  })

  const activeCycleRef = linkedFeature?.cycles?.find((c: any) => c.status === 'active')
  const { data: cycleDetail } = useQuery({
    queryKey: ['cycle', activeCycleRef?.id],
    queryFn: () => getCycleDetail(activeCycleRef!.id),
    enabled: !!activeCycleRef?.id,
  })

  // Fetch workstream features
  const { data: wsFeatures } = useQuery({
    queryKey: ['workstream-features', id],
    queryFn: () => getWorkstreamFeatures(id!),
    enabled: !!id,
  })

  const featureGroups = useMemo(() => {
    const features = wsFeatures || []
    const groups: Record<string, WorkstreamFeature[]> = {
      attention: [],
      inProgress: [],
      backlog: [],
      completed: [],
    }
    for (const wf of features) {
      const s = wf.feature.status
      if (s === 'human-qa' || s === 'blocked') groups.attention.push(wf)
      else if (s === 'implementing' || s === 'agent-qa' || s === 'planning') groups.inProgress.push(wf)
      else if (s === 'draft') groups.backlog.push(wf)
      else if (s === 'done') groups.completed.push(wf)
    }
    // Sort each group by priority DESC
    for (const key of Object.keys(groups)) {
      groups[key].sort((a, b) => b.feature.priority - a.feature.priority)
    }
    return groups
  }, [wsFeatures])

  const progressStats = useMemo(() => {
    const owned = (wsFeatures || []).filter(wf => wf.relationship === 'owned')
    const total = owned.length
    const counts: Record<string, number> = {}
    for (const wf of owned) {
      counts[wf.feature.status] = (counts[wf.feature.status] || 0) + 1
    }
    const doneCount = counts['done'] || 0
    return { total, doneCount, counts }
  }, [wsFeatures])

  if (isLoading) return <div style={{ padding: 40, textAlign: 'center', color: 'var(--color-text-muted)' }}>Loading...</div>
  if (!data) return <div style={{ padding: 40, textAlign: 'center', color: 'var(--color-text-muted)' }}>Workstream not found</div>

  const { workstream: ws, notes, links, children } = data
  const activeCycle = cycleDetail ? { ...cycleDetail.cycle, steps: cycleDetail.steps } : null
  const openQuestions = notes.filter(n => n.note_type === 'question' && n.resolved === 0)
  const tags = ws.tags ? ws.tags.split(',').map(t => t.trim()).filter(Boolean) : []

  return (
    <div style={{ maxWidth: 800, margin: '0 auto' }}>
      {/* Breadcrumb */}
      <div style={{ fontSize: 13, color: 'var(--color-text-muted)', marginBottom: 12 }}>
        <Link to="/workstreams" style={{ color: 'var(--color-text-secondary)', textDecoration: 'none' }}>Workstreams</Link>
        {' / '}
        {ws.parent_id && (
          <>
            <Link to={`/workstreams/${ws.parent_id}`} style={{ color: 'var(--color-text-secondary)', textDecoration: 'none' }}>{ws.parent_id}</Link>
            {' / '}
          </>
        )}
        <span style={{ color: 'var(--color-text-primary)' }}>{ws.name}</span>
      </div>

      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 20 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 700, margin: 0 }}>{ws.name}</h1>
          {ws.description && (
            <div className="prose" style={{ fontSize: 14, color: 'var(--color-text-secondary)', marginTop: 6, lineHeight: 1.5 }}
              dangerouslySetInnerHTML={{ __html: simpleMarkdown(ws.description) }} />
          )}
        </div>
        <span style={{
          fontSize: 12, fontWeight: 600, padding: '4px 10px', borderRadius: 99,
          background: ws.status === 'active' ? 'rgba(34,197,94,0.15)' : 'var(--color-bg-tertiary)',
          color: ws.status === 'active' ? 'rgb(34,197,94)' : 'var(--color-text-muted)',
        }}>
          {ws.status}
        </span>
      </div>

      {tags.length > 0 && (
        <div style={{ display: 'flex', gap: 6, marginBottom: 20, flexWrap: 'wrap' }}>
          {tags.map(tag => (
            <span key={tag} style={{ fontSize: 11, padding: '2px 8px', borderRadius: 99, background: 'var(--color-bg-tertiary)', color: 'var(--color-text-secondary)' }}>
              {tag}
            </span>
          ))}
        </div>
      )}

      {/* Needs Attention Summary */}
      <NeedsAttentionSummary
        features={wsFeatures || []}
        openQuestionCount={openQuestions.length}
      />

      {/* Active Cycle */}
      {activeCycle && (
        <div style={{
          padding: '14px 18px', borderRadius: 8, marginBottom: 20,
          background: 'var(--color-bg-secondary)', border: '1px solid var(--color-border)',
        }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
            <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--color-text-secondary)' }}>
              Active Cycle: {activeCycle.cycle_type}
            </div>
            <Link to={`/cycles/${activeCycle.id}`} style={{ fontSize: 12, color: 'var(--color-accent)', textDecoration: 'none' }}>
              View cycle detail
            </Link>
          </div>
          {/* Step progress */}
          {(() => {
            const steps: CycleStep[] = activeCycle.steps || []
            const currentStep = activeCycle.current_step ?? 0
            if (steps.length === 0) return null
            return (
              <div style={{ display: 'flex', gap: 2, alignItems: 'center' }}>
                {steps.map((s: CycleStep, i: number) => {
                  const isCurrent = i === currentStep
                  const isDone = i < currentStep
                  const isHuman = s.human
                  return (
                    <div key={i} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4 }}>
                      <div style={{
                        height: 6, width: '100%', borderRadius: 3,
                        background: isDone ? 'var(--color-success)' : isCurrent
                          ? (isHuman ? 'rgb(245, 158, 11)' : 'var(--color-accent)')
                          : 'var(--color-bg-tertiary)',
                      }} />
                      <span style={{
                        fontSize: 10,
                        color: isCurrent ? 'var(--color-text-primary)' : 'var(--color-text-muted)',
                        fontWeight: isCurrent ? 600 : 400,
                        whiteSpace: 'nowrap',
                      }}>
                        {s.name}{isHuman ? ' *' : ''}
                      </span>
                    </div>
                  )
                })}
              </div>
            )
          })()}
          {/* Human step: approve/reject UI */}
          {(() => {
            const steps: CycleStep[] = activeCycle.steps || []
            const currentStep = activeCycle.current_step ?? 0
            const step = steps[currentStep]
            if (!step?.human) return null
            return <CycleApproveReject cycleId={activeCycle.id} stepName={step.name} />
          })()}
        </div>
      )}

      {/* Progress Bar */}
      {progressStats.total > 0 && (
        <div className="mb-5">
          <WorkstreamProgressBar counts={progressStats.counts} total={progressStats.total} doneCount={progressStats.doneCount} />
        </div>
      )}

      {/* Features */}
      {(wsFeatures || []).length > 0 && (
        <WorkstreamFeatureList groups={featureGroups} />
      )}

      {/* Children */}
      {children.length > 0 && (
        <div style={{ marginBottom: 24 }}>
          <h2 style={{ fontSize: 15, fontWeight: 600, marginBottom: 8, color: 'var(--color-text-secondary)' }}>Sub-workstreams</h2>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {children.map(child => (
              <Link key={child.id} to={`/workstreams/${child.id}`}
                style={{
                  display: 'block', padding: '10px 14px', borderRadius: 6,
                  background: 'var(--color-bg-secondary)', border: '1px solid var(--color-border)',
                  textDecoration: 'none', color: 'inherit', fontSize: 14,
                }}
              >
                <span style={{ fontWeight: 600 }}>{child.name}</span>
                {child.description && <span style={{ color: 'var(--color-text-muted)', marginLeft: 8 }}>{child.description.slice(0, 60)}</span>}
              </Link>
            ))}
          </div>
        </div>
      )}

      {/* Links */}
      <div style={{ marginBottom: 24 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <h2 style={{ fontSize: 15, fontWeight: 600, color: 'var(--color-text-secondary)', margin: 0 }}>Links</h2>
          <button onClick={() => setShowLinkForm(!showLinkForm)}
            style={{ fontSize: 12, padding: '3px 10px', borderRadius: 4, background: 'var(--color-bg-tertiary)', color: 'var(--color-text-secondary)', border: '1px solid var(--color-border)', cursor: 'pointer' }}>
            + Add link
          </button>
        </div>
        {showLinkForm && (
          <div style={{ padding: 12, background: 'var(--color-bg-secondary)', borderRadius: 8, border: '1px solid var(--color-border)', marginBottom: 8 }}>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <select value={linkType} onChange={e => setLinkType(e.target.value)}
                style={{ padding: '6px 10px', fontSize: 13, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 4 }}>
                <option value="doc">Document</option>
                <option value="feature">Feature</option>
                <option value="url">URL</option>
                <option value="discussion">Discussion</option>
              </select>
              <input placeholder={linkType === 'feature' || linkType === 'discussion' ? 'ID...' : 'Path or URL...'} value={linkTarget}
                onChange={e => setLinkTarget(e.target.value)}
                style={{ flex: 1, padding: '6px 10px', fontSize: 13, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 4 }} />
              <input placeholder="Label..." value={linkLabel} onChange={e => setLinkLabel(e.target.value)}
                style={{ width: 150, padding: '6px 10px', fontSize: 13, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 4 }} />
              <button onClick={() => linkTarget.trim() && addLinkMut.mutate()}
                style={{ padding: '6px 12px', fontSize: 13, fontWeight: 600, background: 'var(--color-accent)', color: 'white', border: 'none', borderRadius: 4, cursor: 'pointer' }}>
                Add
              </button>
            </div>
          </div>
        )}
        {links.length === 0 && !showLinkForm ? (
          <div style={{ fontSize: 13, color: 'var(--color-text-muted)', padding: '8px 0' }}>No links yet</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {links.map(link => <LinkCard key={link.id} link={link} config={config} />)}
          </div>
        )}
      </div>

      {/* Add note form */}
      <div style={{ marginBottom: 16 }}>
        <h2 style={{ fontSize: 15, fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 8 }}>Timeline</h2>
        <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
          <textarea
            placeholder="Add a note, question, decision, or idea..."
            value={noteContent}
            onChange={e => setNoteContent(e.target.value)}
            rows={2}
            style={{
              flex: 1, padding: '8px 12px', fontSize: 13, lineHeight: 1.5,
              background: 'var(--color-bg-secondary)', color: 'var(--color-text-primary)',
              border: '1px solid var(--color-border)', borderRadius: 6, resize: 'vertical',
            }}
            onKeyDown={e => {
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && noteContent.trim()) {
                addNoteMut.mutate()
              }
            }}
          />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <select value={noteType} onChange={e => setNoteType(e.target.value)}
              style={{ padding: '6px 8px', fontSize: 12, background: 'var(--color-bg-secondary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 4 }}>
              <option value="note">Note</option>
              <option value="question">Question</option>
              <option value="decision">Decision</option>
              <option value="idea">Idea</option>
              <option value="import">Import</option>
            </select>
            <button
              onClick={() => noteContent.trim() && addNoteMut.mutate()}
              disabled={!noteContent.trim() || addNoteMut.isPending}
              style={{ padding: '6px 12px', fontSize: 12, fontWeight: 600, background: 'var(--color-accent)', color: 'white', border: 'none', borderRadius: 4, cursor: 'pointer', opacity: !noteContent.trim() ? 0.5 : 1 }}
            >
              Add
            </button>
          </div>
        </div>
      </div>

      {/* Notes timeline */}
      <div id="open-questions" style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {notes.length === 0 ? (
          <div style={{ fontSize: 13, color: 'var(--color-text-muted)', padding: '8px 0' }}>No notes yet. Add one above to start tracking your thinking.</div>
        ) : notes.map(note => (
          <NoteCard key={note.id} note={note} onResolve={() => resolveMut.mutate(note.id)} />
        ))}
      </div>
    </div>
  )
}

// Simple markdown-to-HTML (handles bold, italic, code, links)
function simpleMarkdown(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\*(.+?)\*/g, '<em>$1</em>')
    .replace(/`(.+?)`/g, '<code>$1</code>')
    .replace(/\[(.+?)\]\((.+?)\)/g, '<a href="$2" style="color:var(--color-accent)">$1</a>')
    .replace(/\n/g, '<br>')
}

function formatTime(ts: string): string {
  if (!ts) return ''
  try {
    const d = new Date(ts + (ts.includes('Z') ? '' : 'Z'))
    const now = new Date()
    const diff = now.getTime() - d.getTime()
    if (diff < 60000) return 'just now'
    if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
    if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
    return d.toLocaleDateString()
  } catch {
    return ts
  }
}
