import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useParams, Link } from 'react-router-dom'
import { getWorkstream, addWorkstreamNote, resolveWorkstreamNote, addWorkstreamLink, getConfig, getFeature, getCycleDetail, advanceCycle, getWorkstreamFeatures, approveFeature, rejectFeature, getQAResults, getCycleTypes } from '../api/client'
import type { WorkstreamNote, WorkstreamLink, AppConfig, CycleStep, WorkstreamFeature, FeatureStatus, QAResult, CycleType } from '../api/types'
import { useState, useMemo, useCallback } from 'react'
import { StatusBadge } from '../components/StatusBadge'
import { cn, truncate, formatTimestamp } from '../lib/utils'
import { MarkdownContent } from '../components/MarkdownContent'
import { AttachmentPanel } from '../components/AttachmentPanel'
import { useStore } from '../store'

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

/* ── Grouped Links ── */

interface LinkGroup {
  key: string
  label: string
  icon: string
  defaultOpen: boolean
  links: WorkstreamLink[]
}

function CompactLinkItem({ link, config, featureStatusMap }: {
  link: WorkstreamLink
  config?: AppConfig
  featureStatusMap?: Map<string, FeatureStatus>
}) {
  const vantageUrl = config?.vantage_url
  const projectId = config?.project_id || 'tillr'

  let href = ''
  let label = link.label || ''
  let icon = ''
  let isDep = false

  switch (link.link_type) {
    case 'feature':
    case 'feature-dependency':
      href = `/features/${link.target_id}`
      label = label || link.target_id || 'Feature'
      icon = 'F'
      isDep = link.link_type === 'feature-dependency'
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

  const featureStatus = (link.link_type === 'feature' || link.link_type === 'feature-dependency')
    ? featureStatusMap?.get(link.target_id || '')
    : undefined

  return (
    <Wrapper
      {...(wrapperProps as any)}
      style={{
        display: 'flex', alignItems: 'center', gap: 8,
        padding: '4px 6px', borderRadius: 4,
        textDecoration: 'none', color: 'inherit', fontSize: 13,
      }}
      className="hover:bg-bg-tertiary transition-colors"
    >
      <span style={{
        width: 18, height: 18, borderRadius: 3,
        background: 'var(--color-accent)', color: 'white',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontSize: 10, fontWeight: 700, flexShrink: 0,
      }}>
        {icon}
      </span>
      <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {label}
      </span>
      {isDep && (
        <span style={{
          fontSize: 9, padding: '1px 4px', borderRadius: 3,
          background: 'var(--color-bg-tertiary)', color: 'var(--color-text-muted)',
          fontWeight: 600, textTransform: 'uppercase', flexShrink: 0,
        }}>
          dep
        </span>
      )}
      {featureStatus && <StatusBadge status={featureStatus} />}
      {isExternal && <span style={{ fontSize: 11, color: 'var(--color-text-muted)', flexShrink: 0 }}>&#8599;</span>}
    </Wrapper>
  )
}

function GroupedLinks({ links, config, wsFeatures }: {
  links: WorkstreamLink[]
  config?: AppConfig
  wsFeatures?: WorkstreamFeature[]
}) {
  // Build a set of feature IDs already shown in the features section
  const featureIds = useMemo(() => {
    const ids = new Set<string>()
    if (Array.isArray(wsFeatures)) {
      for (const wf of wsFeatures) {
        ids.add(wf.feature.id)
      }
    }
    return ids
  }, [wsFeatures])

  // Build a map of feature ID -> status for inline badges
  const featureStatusMap = useMemo(() => {
    const m = new Map<string, FeatureStatus>()
    if (Array.isArray(wsFeatures)) {
      for (const wf of wsFeatures) {
        m.set(wf.feature.id, wf.feature.status)
      }
    }
    return m
  }, [wsFeatures])

  // Filter out feature links whose target_id is already in the features list
  const filteredLinks = useMemo(() =>
    links.filter(link => {
      if ((link.link_type === 'feature' || link.link_type === 'feature-dependency') && link.target_id) {
        return !featureIds.has(link.target_id)
      }
      return true
    }),
  [links, featureIds])

  // Group links by type
  const groups: LinkGroup[] = useMemo(() => {
    const featureLinks: WorkstreamLink[] = []
    const docLinks: WorkstreamLink[] = []
    const urlLinks: WorkstreamLink[] = []
    const discussionLinks: WorkstreamLink[] = []

    for (const link of filteredLinks) {
      switch (link.link_type) {
        case 'feature':
        case 'feature-dependency':
          featureLinks.push(link)
          break
        case 'doc':
          docLinks.push(link)
          break
        case 'url':
          urlLinks.push(link)
          break
        case 'discussion':
          discussionLinks.push(link)
          break
      }
    }

    const result: LinkGroup[] = []
    if (featureLinks.length > 0) result.push({ key: 'features', label: 'Features', icon: 'F', defaultOpen: true, links: featureLinks })
    if (docLinks.length > 0) result.push({ key: 'docs', label: 'Docs', icon: 'D', defaultOpen: true, links: docLinks })
    if (urlLinks.length > 0) result.push({ key: 'urls', label: 'URLs', icon: 'U', defaultOpen: true, links: urlLinks })
    if (discussionLinks.length > 0) result.push({ key: 'discussions', label: 'Discussions', icon: 'C', defaultOpen: true, links: discussionLinks })
    return result
  }, [filteredLinks])

  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})

  const toggle = useCallback((key: string) => {
    setCollapsed(prev => ({ ...prev, [key]: !prev[key] }))
  }, [])

  if (groups.length === 0) {
    return <div style={{ fontSize: 13, color: 'var(--color-text-muted)', padding: '8px 0' }}>No links yet</div>
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      {groups.map(group => {
        const isCollapsed = collapsed[group.key] ?? !group.defaultOpen
        return (
          <div key={group.key}>
            <button
              onClick={() => toggle(group.key)}
              style={{
                display: 'flex', alignItems: 'center', gap: 6, width: '100%',
                background: 'none', border: 'none', cursor: 'pointer', padding: '2px 0',
                fontSize: 12, color: 'var(--color-text-muted)', fontWeight: 500,
              }}
              className="hover:text-text-secondary transition-colors"
            >
              <span style={{ fontSize: 10 }}>{isCollapsed ? '\u25B6' : '\u25BC'}</span>
              <span>{group.label}</span>
              <span style={{ fontSize: 11, color: 'var(--color-text-muted)' }}>({group.links.length})</span>
              <div style={{ flex: 1, borderTop: '1px solid var(--color-border)', marginLeft: 4 }} />
            </button>
            {!isCollapsed && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 1, marginTop: 4 }}>
                {group.links.map(link => (
                  <CompactLinkItem
                    key={link.id}
                    link={link}
                    config={config}
                    featureStatusMap={featureStatusMap}
                  />
                ))}
              </div>
            )}
          </div>
        )
      })}
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
                    wf.feature.status === 'human-qa'
                      ? <InlineQACard key={wf.feature.id} wf={wf} />
                      : <FeatureRow key={wf.feature.id} wf={wf} />
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

/* ── Human Inbox ── */

interface InboxCategory {
  key: string
  label: string
  color: string
  bgColor: string
  scrollTarget: string
  items: WorkstreamFeature[]
}

function NeedsAttentionSummary({ features, openQuestions }: {
  features: WorkstreamFeature[]
  openQuestions: WorkstreamNote[]
}) {
  const scrollTo = useCallback((targetId: string) => {
    document.getElementById(targetId)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [])

  const categories: InboxCategory[] = useMemo(() => {
    const qaReview = features.filter(wf => wf.feature.status === 'human-qa')
    const blocked = features.filter(wf => wf.feature.status === 'blocked')
    const previouslyRejected = features.filter(wf => wf.feature.status === 'implementing' && wf.rejection_count > 0)
    const needsSpec = features.filter(wf => wf.feature.status === 'draft' && !wf.feature.spec)

    const cats: InboxCategory[] = []
    if (qaReview.length > 0) cats.push({
      key: 'qa', label: 'QA Review', color: 'rgb(245, 158, 11)', bgColor: 'rgba(245, 158, 11, 0.10)',
      scrollTarget: 'qa-features', items: qaReview,
    })
    if (blocked.length > 0) cats.push({
      key: 'blocked', label: 'Blocked', color: 'rgb(239, 68, 68)', bgColor: 'rgba(239, 68, 68, 0.10)',
      scrollTarget: 'blocked-features', items: blocked,
    })
    // Open questions get a synthetic entry (no feature items)
    if (previouslyRejected.length > 0) cats.push({
      key: 'rejected', label: 'Previously Rejected', color: 'rgb(249, 115, 22)', bgColor: 'rgba(249, 115, 22, 0.10)',
      scrollTarget: 'in-progress-features', items: previouslyRejected,
    })
    if (needsSpec.length > 0) cats.push({
      key: 'needs-spec', label: 'Needs Spec', color: 'var(--color-text-muted)', bgColor: 'var(--color-bg-tertiary)',
      scrollTarget: 'backlog-features', items: needsSpec,
    })
    return cats
  }, [features])

  const hasItems = categories.length > 0 || openQuestions.length > 0

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
      style={{ background: 'var(--color-bg-secondary)', border: '1px solid var(--color-border)' }}>
      <div className="px-4 py-3">
        <div className="text-xs font-semibold uppercase tracking-wide mb-2" style={{ color: 'var(--color-text-muted)' }}>
          Human Inbox
        </div>
        <div className="flex flex-col gap-2">
          {categories.map(cat => (
            <div key={cat.key}>
              <button
                onClick={() => scrollTo(cat.scrollTarget)}
                className="text-left text-xs font-semibold uppercase tracking-wide hover:underline cursor-pointer bg-transparent border-none p-0 mb-1"
                style={{ color: cat.color }}
              >
                {cat.label} ({cat.items.length})
              </button>
              <div className="flex flex-col gap-0.5">
                {cat.items.map(wf => (
                  <button
                    key={wf.feature.id}
                    onClick={() => scrollTo(cat.scrollTarget)}
                    className="text-left text-sm hover:underline cursor-pointer bg-transparent border-none p-0 flex items-center gap-2"
                    style={{ color: 'var(--color-text-primary)', paddingLeft: 8 }}
                  >
                    <span style={{
                      display: 'inline-block', width: 6, height: 6, borderRadius: '50%',
                      background: cat.color, flexShrink: 0,
                    }} />
                    <span className="truncate">{wf.feature.name}</span>
                    <InboxPriorityBadge priority={wf.feature.priority} />
                    {cat.key === 'rejected' && (
                      <span style={{ fontSize: 10, color: 'rgb(249, 115, 22)', fontWeight: 600 }}>
                        rejected {wf.rejection_count}x
                      </span>
                    )}
                  </button>
                ))}
              </div>
            </div>
          ))}
          {openQuestions.length > 0 && (
            <div>
              <button
                onClick={() => scrollTo('open-questions')}
                className="text-left text-xs font-semibold uppercase tracking-wide hover:underline cursor-pointer bg-transparent border-none p-0 mb-1"
                style={{ color: 'rgb(217, 169, 56)' }}
              >
                Open Questions ({openQuestions.length})
              </button>
              <div className="flex flex-col gap-0.5">
                {openQuestions.map(q => (
                  <div
                    key={q.id}
                    className="text-sm flex items-center gap-2"
                    style={{ color: 'var(--color-text-primary)', paddingLeft: 8 }}
                  >
                    <span style={{
                      display: 'inline-block', width: 6, height: 6, borderRadius: '50%',
                      background: 'rgb(217, 169, 56)', flexShrink: 0,
                    }} />
                    <span className="truncate">{truncate(q.content, 80)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function InboxPriorityBadge({ priority }: { priority: number }) {
  const color =
    priority >= 8 ? 'rgb(239, 68, 68)'
    : priority >= 5 ? 'rgb(245, 158, 11)'
    : 'var(--color-text-muted)'
  const bg =
    priority >= 8 ? 'rgba(239, 68, 68, 0.15)'
    : priority >= 5 ? 'rgba(245, 158, 11, 0.15)'
    : 'var(--color-bg-tertiary)'
  return (
    <span style={{
      fontSize: 10, fontWeight: 600, padding: '0 5px', borderRadius: 99,
      background: bg, color, lineHeight: '18px', flexShrink: 0,
    }}>
      P{priority}
    </span>
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
    <div style={{ maxWidth: 1200, margin: '0 auto' }}>
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
        openQuestions={openQuestions}
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

      {/* Two-column layout: Features (main) | Links (sidebar) */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 300px', gap: 24, alignItems: 'start' }} className="workstream-grid">
        {/* Main column: Features + Children */}
        <div style={{ minWidth: 0 }}>
          {/* Features */}
          {(wsFeatures || []).length > 0 && (
            <WorkstreamFeatureList groups={featureGroups} />
          )}

          {/* Children */}
          {children.length > 0 && (
            <div style={{ marginTop: 24 }}>
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

          {/* Attachments */}
          <div style={{ marginTop: 24 }}>
            <AttachmentPanel entityType="workstream" entityId={id!} />
          </div>
        </div>

        {/* Sidebar: Links */}
        <div style={{ minWidth: 0 }}>
          {/* Links */}
          <div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
              <h2 style={{ fontSize: 15, fontWeight: 600, color: 'var(--color-text-secondary)', margin: 0 }}>Links</h2>
              <button onClick={() => setShowLinkForm(!showLinkForm)}
                style={{ fontSize: 12, padding: '3px 10px', borderRadius: 4, background: 'var(--color-bg-tertiary)', color: 'var(--color-text-secondary)', border: '1px solid var(--color-border)', cursor: 'pointer' }}>
                + Add link
              </button>
            </div>
            {showLinkForm && (
              <div style={{ padding: 12, background: 'var(--color-bg-secondary)', borderRadius: 8, border: '1px solid var(--color-border)', marginBottom: 8 }}>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <select value={linkType} onChange={e => setLinkType(e.target.value)}
                    style={{ padding: '6px 10px', fontSize: 13, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 4 }}>
                    <option value="doc">Document</option>
                    <option value="feature">Feature</option>
                    <option value="url">URL</option>
                    <option value="discussion">Discussion</option>
                  </select>
                  <input placeholder={linkType === 'feature' || linkType === 'discussion' ? 'ID...' : 'Path or URL...'} value={linkTarget}
                    onChange={e => setLinkTarget(e.target.value)}
                    style={{ padding: '6px 10px', fontSize: 13, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 4 }} />
                  <input placeholder="Label..." value={linkLabel} onChange={e => setLinkLabel(e.target.value)}
                    style={{ padding: '6px 10px', fontSize: 13, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 4 }} />
                  <button onClick={() => linkTarget.trim() && addLinkMut.mutate()}
                    style={{ padding: '6px 12px', fontSize: 13, fontWeight: 600, background: 'var(--color-accent)', color: 'white', border: 'none', borderRadius: 4, cursor: 'pointer' }}>
                    Add
                  </button>
                </div>
              </div>
            )}
            <GroupedLinks links={links} config={config} wsFeatures={wsFeatures} />
          </div>
        </div>
      </div>

      {/* Timeline (full width, below the grid) */}
      <div style={{ marginTop: 24 }}>
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

        {/* Notes timeline */}
        <div id="open-questions" style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {notes.length === 0 ? (
            <div style={{ fontSize: 13, color: 'var(--color-text-muted)', padding: '8px 0' }}>No notes yet. Add one above to start tracking your thinking.</div>
          ) : notes.map(note => (
            <NoteCard key={note.id} note={note} onResolve={() => resolveMut.mutate(note.id)} />
          ))}
        </div>
      </div>
    </div>
  )
}

function FeatureRow({ wf }: { wf: WorkstreamFeature }) {
  return (
    <Link
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
                {truncate(wf.feature.description, 200)}
              </p>
            )}
          </div>
        </div>
        <StatusBadge status={wf.feature.status} />
      </div>
    </Link>
  )
}

function InlineQACard({ wf }: { wf: WorkstreamFeature }) {
  const { id: wsId } = useParams<{ id: string }>()
  const queryClient = useQueryClient()
  const addToast = useStore((s) => s.addToast)
  const [expanded, setExpanded] = useState(false)
  const [rejectNotes, setRejectNotes] = useState('')
  const [showRejectForm, setShowRejectForm] = useState(false)
  const [showApproveNotes, setShowApproveNotes] = useState(false)
  const [approveNotes, setApproveNotes] = useState('')

  const feature = wf.feature

  const qaResults = useQuery({
    queryKey: ['qa-results', feature.id],
    queryFn: () => getQAResults(feature.id),
    enabled: expanded,
  })

  const cycleTypesQuery = useQuery({
    queryKey: ['cycle-types'],
    queryFn: getCycleTypes,
    enabled: expanded,
  })

  const invalidateAll = () => {
    queryClient.invalidateQueries({ queryKey: ['qa-pending'] })
    queryClient.invalidateQueries({ queryKey: ['features'] })
    queryClient.invalidateQueries({ queryKey: ['status'] })
    queryClient.invalidateQueries({ queryKey: ['workstream-features', wsId] })
    queryClient.invalidateQueries({ queryKey: ['workstream', wsId] })
  }

  const approveMut = useMutation({
    mutationFn: (n?: string) => approveFeature(feature.id, n),
    onSuccess: () => {
      invalidateAll()
      addToast('Feature approved', 'success')
    },
    onError: (err) => addToast(`Approve failed: ${err.message}`, 'error'),
  })

  const rejectMut = useMutation({
    mutationFn: (n?: string) => rejectFeature(feature.id, n),
    onSuccess: () => {
      invalidateAll()
      addToast('Feature rejected — sent back to development', 'info')
    },
    onError: (err) => addToast(`Reject failed: ${err.message}`, 'error'),
  })

  const reviewHistory = (qaResults.data || []) as QAResult[]
  const reviewRound = reviewHistory.length + 1

  const cycleTypes = (cycleTypesQuery.data || []) as CycleType[]
  const cycleType = cycleTypes.find((ct) => ct.name === feature.assigned_cycle)
  const humanStep = cycleType?.steps?.find((s) => s.human && s.instructions)
  const testPlanInstructions = humanStep?.instructions

  return (
    <div className={cn(
      'bg-bg-card border border-border rounded-lg overflow-hidden',
      wf.relationship === 'dependency' && 'ml-6'
    )}>
      <div
        className="flex items-center justify-between p-3 cursor-pointer hover:bg-bg-hover/30 transition-colors"
        onClick={() => setExpanded(!expanded)}
      >
        <div className="flex items-center gap-2.5 min-w-0 flex-1">
          <span className={cn(
            'text-xs font-mono shrink-0 w-7 text-center rounded py-0.5',
            feature.priority >= 8 ? 'bg-danger/10 text-danger' :
            feature.priority >= 5 ? 'bg-warning/10 text-warning' :
            'bg-bg-tertiary text-text-muted'
          )}>
            {feature.priority}
          </span>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-medium text-text-primary truncate">{feature.name}</h3>
              {wf.relationship === 'dependency' && (
                <span className="text-[10px] font-medium px-1.5 py-0.5 rounded-full bg-bg-tertiary text-text-muted shrink-0">
                  prerequisite
                </span>
              )}
            </div>
            {feature.description && (
              <p className="text-xs text-text-secondary mt-0.5 truncate">
                {truncate(feature.description, 200)}
              </p>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <StatusBadge status={feature.status} />
          <span className="text-text-muted text-sm">{expanded ? '\u25B2' : '\u25BC'}</span>
        </div>
      </div>

      {expanded && (
        <div className="border-t border-border p-4 space-y-4">
          {/* Full description when expanded */}
          {feature.description && (
            <p className="text-sm text-text-secondary leading-relaxed">
              {feature.description}
            </p>
          )}

          {reviewRound > 1 && (
            <div className="flex items-center gap-2 text-xs text-warning bg-warning/5 border border-warning/20 rounded-md px-3 py-2">
              <span>Review round #{reviewRound} — previously reviewed {reviewRound - 1} time{reviewRound > 2 ? 's' : ''}</span>
            </div>
          )}

          {testPlanInstructions && (
            <div className="bg-warning/5 rounded-lg p-4 border border-warning/20">
              <h4 className="text-xs font-semibold text-warning uppercase tracking-wider mb-2">Test Plan</h4>
              <div className="prose prose-sm prose-invert max-w-none text-sm text-text-secondary">
                <MarkdownContent>{testPlanInstructions}</MarkdownContent>
              </div>
            </div>
          )}

          {feature.spec && (
            <div className="bg-bg-secondary rounded-lg p-4 border border-border-light">
              <h4 className="text-xs font-semibold text-text-muted uppercase tracking-wider mb-3">Feature Spec</h4>
              <div className="prose prose-sm prose-invert max-w-none text-sm text-text-secondary leading-relaxed">
                <MarkdownContent>{feature.spec}</MarkdownContent>
              </div>
            </div>
          )}

          {reviewHistory.length > 0 && (
            <div>
              <h4 className="text-xs font-semibold text-text-muted uppercase tracking-wider mb-2">Review History</h4>
              <div className="space-y-2">
                {reviewHistory.map((r) => (
                  <div key={r.id} className={cn(
                    'text-xs p-2.5 rounded border',
                    r.passed ? 'bg-success/5 border-success/20 text-success' : 'bg-danger/5 border-danger/20 text-danger'
                  )}>
                    <span className="font-medium">{r.passed ? 'Approved' : 'Rejected'}</span>
                    {r.notes && <span className="ml-2 text-text-secondary">— {r.notes}</span>}
                    <span className="ml-2 text-text-muted">{formatTimestamp(r.created_at)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Action buttons */}
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
                  onClick={(e) => { e.stopPropagation(); setShowApproveNotes(true) }}
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

          <div className="pt-2 border-t border-border">
            <Link to={`/features/${feature.id}`} className="text-xs text-accent hover:text-accent/80 transition-colors">
              View full feature details →
            </Link>
          </div>
        </div>
      )}
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
