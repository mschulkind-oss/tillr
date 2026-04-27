import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { addComment, getComments, getFeature } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { MarkdownContent } from '../components/MarkdownContent'

export function FeatureDetail() {
  const { id } = useParams<{ id: string }>()
  const featureID = Number(id)
  const qc = useQueryClient()
  const [body, setBody] = useState('')

  const featureQ = useQuery({
    queryKey: ['feature', featureID],
    queryFn: () => getFeature(featureID),
    enabled: !Number.isNaN(featureID),
  })

  const commentsQ = useQuery({
    queryKey: ['comments', featureID],
    queryFn: () => getComments(featureID),
    enabled: !Number.isNaN(featureID),
  })

  const post = useMutation({
    mutationFn: (text: string) => addComment(featureID, { body: text }),
    onSuccess: () => {
      setBody('')
      qc.invalidateQueries({ queryKey: ['comments', featureID] })
    },
  })

  if (Number.isNaN(featureID)) {
    return <div className="text-red-500">Invalid feature ID</div>
  }
  if (featureQ.isLoading) return <div className="text-text-muted">Loading…</div>
  if (featureQ.error || !featureQ.data) {
    return <div className="text-red-500">Feature not found.</div>
  }

  const f = featureQ.data
  const comments = commentsQ.data || []

  return (
    <div className="max-w-3xl mx-auto">
      <Link to="/features" className="text-text-muted text-sm hover:text-text-primary">
        ← Back to features
      </Link>

      <div className="flex items-center gap-3 mt-2">
        <h1 className="text-2xl font-semibold">#{f.id} {f.title}</h1>
        <StatusBadge status={f.status} />
      </div>

      {f.description && (
        <div className="mt-4 prose prose-sm max-w-none">
          <MarkdownContent>{f.description}</MarkdownContent>
        </div>
      )}

      <h2 className="text-lg font-semibold mt-8 mb-3">
        Comments {comments.length > 0 && <span className="text-text-muted text-sm font-normal">({comments.length})</span>}
      </h2>

      {commentsQ.isLoading && <div className="text-text-muted">Loading comments…</div>}

      {comments.length > 0 && (
        <ul className="space-y-3 mb-6">
          {comments.map((c) => (
            <li key={c.id} className="border border-border rounded-md p-3 bg-bg-secondary">
              <div className="text-xs text-text-muted mb-1">
                {c.author_type}{c.author_role ? `/${c.author_role}` : ''} ·{' '}
                {new Date(c.created_at).toLocaleString()}
              </div>
              <div className="prose prose-sm max-w-none">
                <MarkdownContent>{c.body}</MarkdownContent>
              </div>
            </li>
          ))}
        </ul>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (body.trim()) post.mutate(body.trim())
        }}
      >
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder="Add a comment…"
          rows={3}
          className="w-full px-3 py-2 rounded-md bg-bg-secondary border border-border text-text-primary"
        />
        <button
          type="submit"
          disabled={!body.trim() || post.isPending}
          className="mt-2 px-4 py-2 rounded-md bg-accent text-white disabled:opacity-50"
        >
          {post.isPending ? 'Posting…' : 'Post comment'}
        </button>
      </form>
    </div>
  )
}
