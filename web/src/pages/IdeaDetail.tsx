import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getIdea, approveIdea, rejectIdea, getHistory } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp, formatTimeAgo } from '../lib/utils'
import { MarkdownContent } from '../components/MarkdownContent'
import { useState } from 'react'

export function IdeaDetail() {
  const { id } = useParams<{ id: string }>()
  const queryClient = useQueryClient()
  const [actionError, setActionError] = useState<string | null>(null)

  const idea = useQuery({
    queryKey: ['idea', id],
    queryFn: () => getIdea(Number(id)),
    enabled: !!id,
  })

  const history = useQuery({
    queryKey: ['history', { type: 'idea', limit: 50 }],
    queryFn: () => getHistory({ type: 'idea', limit: 50 }),
  })

  const approveMutation = useMutation({
    mutationFn: () => approveIdea(Number(id)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['idea', id] })
      queryClient.invalidateQueries({ queryKey: ['ideas'] })
      setActionError(null)
    },
    onError: (err: Error) => setActionError(err.message),
  })

  const rejectMutation = useMutation({
    mutationFn: () => rejectIdea(Number(id)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['idea', id] })
      queryClient.invalidateQueries({ queryKey: ['ideas'] })
      setActionError(null)
    },
    onError: (err: Error) => setActionError(err.message),
  })

  if (idea.isLoading) return <PageSkeleton />
  if (!idea.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Idea not found
      </div>
    )
  }

  const i = idea.data

  // Filter history events that mention this idea
  const ideaEvents = (history.data || []).filter((e) => {
    if (!e.data) return false
    try {
      const data = JSON.parse(e.data)
      return data.idea_id === Number(id) || data.id === Number(id)
    } catch {
      return false
    }
  })

  const canApprove = i.status === 'pending' || i.status === 'spec-ready'
  const canReject = i.status === 'pending' || i.status === 'spec-ready'

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/ideas" className="hover:text-accent transition-colors">Ideas</Link>
        <span>/</span>
        <span className="text-text-secondary">{i.title}</span>
      </nav>

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-text-primary">{i.title}</h1>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <span className="text-[10px] bg-bg-tertiary text-text-muted px-1.5 py-0.5 rounded">
            {i.idea_type}
          </span>
          <StatusBadge status={i.status} />
        </div>
      </div>

      {/* Metadata grid */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <MetaItem label="Submitted by" value={i.submitted_by} />
        {i.source_page && <MetaItem label="Source page" value={i.source_page} />}
        <MetaItem
          label="Auto-implement"
          value={
            <span className={i.auto_implement ? 'text-success' : 'text-text-muted'}>
              {i.auto_implement ? 'Yes' : 'No'}
            </span>
          }
        />
        <MetaItem label="Created" value={formatTimestamp(i.created_at)} />
        {i.updated_at && i.updated_at !== i.created_at && (
          <MetaItem label="Updated" value={formatTimestamp(i.updated_at)} />
        )}
      </div>

      {/* Action buttons */}
      {(canApprove || canReject) && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Actions</h2>
          {actionError && (
            <div className="text-sm text-danger mb-3 bg-danger/10 rounded px-3 py-2">{actionError}</div>
          )}
          <div className="flex items-center gap-3">
            {canApprove && (
              <button
                onClick={() => approveMutation.mutate()}
                disabled={approveMutation.isPending}
                className="px-4 py-2 bg-success/20 text-success border border-success/30 rounded-lg text-sm font-medium hover:bg-success/30 transition-colors disabled:opacity-50"
              >
                {approveMutation.isPending ? 'Approving...' : 'Approve'}
              </button>
            )}
            {canReject && (
              <button
                onClick={() => rejectMutation.mutate()}
                disabled={rejectMutation.isPending}
                className="px-4 py-2 bg-danger/20 text-danger border border-danger/30 rounded-lg text-sm font-medium hover:bg-danger/30 transition-colors disabled:opacity-50"
              >
                {rejectMutation.isPending ? 'Rejecting...' : 'Reject'}
              </button>
            )}
          </div>
        </div>
      )}

      {/* Raw Input */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-3">Raw Input</h2>
        <div className="text-sm text-text-secondary whitespace-pre-wrap bg-bg-secondary rounded p-4 font-mono">
          {i.raw_input}
        </div>
      </div>

      {/* Spec */}
      {i.spec_md && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Spec</h2>
          <div className="prose prose-sm prose-invert max-w-none text-text-secondary">
            <MarkdownContent>{i.spec_md}</MarkdownContent>
          </div>
        </div>
      )}

      {/* Context */}
      {i.context && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Context</h2>
          <p className="text-sm text-text-secondary">{i.context}</p>
        </div>
      )}

      {/* Created Feature */}
      {i.feature_id && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Linked Feature</h2>
          <EntityLink type="feature" id={i.feature_id} showIcon />
        </div>
      )}

      {/* Assigned Agent */}
      {i.assigned_agent && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Assigned Agent</h2>
          <EntityLink type="agent" id={i.assigned_agent} name={i.assigned_agent} showIcon />
        </div>
      )}

      {/* Processing History */}
      {ideaEvents.length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Processing History</h2>
          <div className="space-y-2">
            {ideaEvents.map((event) => (
              <div key={event.id} className="flex items-start gap-3 text-sm">
                <span className="text-text-muted shrink-0 text-xs">{formatTimeAgo(event.created_at)}</span>
                <div className="min-w-0">
                  <span className="text-text-secondary">{event.event_type.replace('idea.', '')}</span>
                  {event.feature_id && (
                    <span className="ml-2">
                      <EntityLink type="feature" id={event.feature_id} />
                    </span>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
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
