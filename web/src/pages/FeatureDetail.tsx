import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getFeature, getFeatureDeps, getQAResults, patchFeature } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp, cn } from '../lib/utils'
import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
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
  if (!feature.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Feature not found
      </div>
    )
  }

  const f = feature.data

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/features" className="hover:text-accent transition-colors">Features</Link>
        <span>/</span>
        <span className="text-text-secondary">{f.name}</span>
      </nav>

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
        <MetaItem label="Milestone" value={f.milestone_name || '—'} />
        <MetaItem label="Created" value={formatTimestamp(f.created_at)} />
        <MetaItem label="Updated" value={formatTimestamp(f.updated_at)} />
        {f.estimate_size && <MetaItem label="Estimate" value={f.estimate_size.toUpperCase()} />}
        {f.assigned_cycle && <MetaItem label="Cycle" value={f.assigned_cycle} />}
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
          <div className="prose prose-sm prose-invert max-w-none text-text-secondary">
            <ReactMarkdown>{f.spec}</ReactMarkdown>
          </div>
        </div>
      )}

      {/* Dependencies */}
      {deps.data && (deps.data.depends_on?.length > 0 || deps.data.blocks?.length > 0) && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Dependencies</h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {deps.data.depends_on?.length > 0 && (
              <div>
                <h3 className="text-xs text-text-muted uppercase tracking-wider mb-2">Depends on</h3>
                <div className="space-y-1">
                  {deps.data.depends_on.map((depId) => (
                    <Link
                      key={depId}
                      to={`/features/${depId}`}
                      className="block text-sm text-accent hover:underline"
                    >
                      {depId}
                    </Link>
                  ))}
                </div>
              </div>
            )}
            {deps.data.blocks?.length > 0 && (
              <div>
                <h3 className="text-xs text-text-muted uppercase tracking-wider mb-2">Blocks</h3>
                <div className="space-y-1">
                  {deps.data.blocks.map((blockId) => (
                    <Link
                      key={blockId}
                      to={`/features/${blockId}`}
                      className="block text-sm text-warning hover:underline"
                    >
                      {blockId}
                    </Link>
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
