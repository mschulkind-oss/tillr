import { useQuery } from '@tanstack/react-query'
import { getDiscussion } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { useParams, Link } from 'react-router-dom'
import { formatTimestamp } from '../lib/utils'
import { cn } from '../lib/utils'
import type { DiscussionComment } from '../api/types'

const COMMENT_TYPE_BORDER: Record<string, string> = {
  proposal: 'border-l-accent',
  approval: 'border-l-success',
  objection: 'border-l-danger',
}

const COMMENT_TYPE_BG: Record<string, string> = {
  proposal: 'bg-accent/5',
  approval: 'bg-success/5',
  objection: 'bg-danger/5',
}

export function DiscussionDetail() {
  const { id } = useParams<{ id: string }>()

  const discussion = useQuery({
    queryKey: ['discussion', id],
    queryFn: () => getDiscussion(Number(id)),
    enabled: !!id,
  })

  if (discussion.isLoading) return <PageSkeleton />
  if (!discussion.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        Discussion not found
      </div>
    )
  }

  const d = discussion.data

  return (
    <div className="max-w-4xl space-y-6">
      {/* Breadcrumb */}
      <nav className="text-xs text-text-muted flex items-center gap-1">
        <Link to="/discussions" className="hover:text-accent transition-colors">Discussions</Link>
        <span>/</span>
        <span className="text-text-secondary">{d.title}</span>
      </nav>

      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-2xl font-bold text-text-primary">{d.title}</h1>
          <div className="flex items-center gap-3 mt-2">
            <span className="text-sm text-text-secondary">by {d.author}</span>
            <span className="text-xs text-text-muted">{formatTimestamp(d.created_at)}</span>
          </div>
        </div>
        <StatusBadge status={d.status} />
      </div>

      {/* Feature link */}
      {d.feature_id && (
        <div className="flex items-center gap-2 text-sm">
          <span className="text-text-muted">Feature:</span>
          <EntityLink type="feature" id={d.feature_id} showIcon />
        </div>
      )}

      {/* Body */}
      {d.body && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <div className="text-sm text-text-secondary whitespace-pre-wrap">{d.body}</div>
        </div>
      )}

      {/* Votes */}
      {d.votes && Object.keys(d.votes).length > 0 && (
        <div className="bg-bg-card border border-border rounded-lg p-5">
          <h2 className="text-sm font-semibold text-text-primary mb-3">Reactions</h2>
          <div className="flex items-center gap-3">
            {Object.entries(d.votes).map(([emoji, count]) => (
              <span
                key={emoji}
                className="inline-flex items-center gap-1 bg-bg-secondary rounded-full px-3 py-1 text-sm"
              >
                <span>{emoji}</span>
                <span className="text-text-secondary font-medium">{count}</span>
              </span>
            ))}
          </div>
        </div>
      )}

      {/* Comments */}
      <div className="bg-bg-card border border-border rounded-lg p-5">
        <h2 className="text-sm font-semibold text-text-primary mb-4">
          Comments {d.comments ? `(${d.comments.length})` : ''}
        </h2>
        {d.comments && d.comments.length > 0 ? (
          <div className="space-y-3">
            {d.comments.map((comment) => (
              <CommentCard key={comment.id} comment={comment} />
            ))}
          </div>
        ) : (
          <p className="text-sm text-text-muted">No comments yet</p>
        )}
      </div>
    </div>
  )
}

function CommentCard({ comment }: { comment: DiscussionComment }) {
  const borderClass = COMMENT_TYPE_BORDER[comment.comment_type] || 'border-l-border'
  const bgClass = COMMENT_TYPE_BG[comment.comment_type] || ''

  return (
    <div className={cn('border-l-2 pl-4 py-3 rounded-r', borderClass, bgClass)}>
      <div className="flex items-center gap-2 mb-2">
        <span className="text-xs font-medium text-text-primary">{comment.author}</span>
        <span className="text-[10px] bg-bg-tertiary text-text-muted px-1.5 py-0.5 rounded">
          {comment.comment_type}
        </span>
        <span className="text-xs text-text-muted ml-auto">
          {formatTimestamp(comment.created_at)}
        </span>
      </div>
      <div className="text-sm text-text-secondary whitespace-pre-wrap">{comment.content}</div>
    </div>
  )
}
