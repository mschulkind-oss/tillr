import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { createFeature, getFeatures } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'

export function Features() {
  const qc = useQueryClient()
  const [title, setTitle] = useState('')

  const { data: features, isLoading, error } = useQuery({
    queryKey: ['features'],
    queryFn: getFeatures,
  })

  const create = useMutation({
    mutationFn: (t: string) => createFeature({ title: t }),
    onSuccess: () => {
      setTitle('')
      qc.invalidateQueries({ queryKey: ['features'] })
    },
  })

  return (
    <div className="max-w-3xl mx-auto">
      <h1 className="text-2xl font-semibold mb-4">Features</h1>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          if (title.trim()) create.mutate(title.trim())
        }}
        className="flex gap-2 mb-6"
      >
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="New feature title…"
          className="flex-1 px-3 py-2 rounded-md bg-bg-secondary border border-border text-text-primary"
        />
        <button
          type="submit"
          disabled={!title.trim() || create.isPending}
          className="px-4 py-2 rounded-md bg-accent text-white disabled:opacity-50"
        >
          {create.isPending ? 'Adding…' : 'Add'}
        </button>
      </form>

      {isLoading && <div className="text-text-muted">Loading…</div>}
      {error && <div className="text-red-500">Error loading features.</div>}

      {features && features.length === 0 && (
        <div className="text-text-muted text-sm">
          No features yet. Add one above, or run{' '}
          <code className="px-1 rounded bg-bg-secondary">tillr feature add "Title"</code>.
        </div>
      )}

      {features && features.length > 0 && (
        <ul className="divide-y divide-border border border-border rounded-md">
          {features.map((f) => (
            <li key={f.id} className="px-4 py-3 hover:bg-sidebar-hover">
              <Link to={`/features/${f.id}`} className="flex items-center gap-3">
                <span className="text-text-muted text-sm">#{f.id}</span>
                <span className="flex-1 text-text-primary">{f.title}</span>
                <StatusBadge status={f.status} />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
