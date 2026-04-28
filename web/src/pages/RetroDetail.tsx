import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getRetro } from '../api/client'
import { MarkdownContent } from '../components/MarkdownContent'

export function RetroDetail() {
  const { name = '' } = useParams<{ name: string }>()
  const { data, isLoading, error } = useQuery({
    queryKey: ['retro', name],
    queryFn: () => getRetro(name),
    enabled: !!name,
  })

  return (
    <div className="max-w-3xl mx-auto">
      <Link to="/retros" className="text-text-muted text-sm hover:text-text-primary">
        ← Back to retros
      </Link>

      <h1 className="text-2xl font-semibold mt-2 font-mono">{name}</h1>

      {isLoading && <div className="text-text-muted mt-4">Loading…</div>}
      {error && <div className="text-red-500 mt-4">Retro not found.</div>}

      {data && (
        <div className="prose prose-sm max-w-none mt-4 border border-border rounded-md p-4 bg-bg-secondary">
          <MarkdownContent>{data.body}</MarkdownContent>
        </div>
      )}
    </div>
  )
}
