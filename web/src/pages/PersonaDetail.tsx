import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getPersona, getPersonaContext } from '../api/client'
import { MarkdownContent } from '../components/MarkdownContent'

export function PersonaDetail() {
  const { name = '' } = useParams<{ name: string }>()

  const personaQ = useQuery({
    queryKey: ['persona', name],
    queryFn: () => getPersona(name),
    enabled: !!name,
  })
  const contextQ = useQuery({
    queryKey: ['persona-context', name],
    queryFn: () => getPersonaContext(name),
    enabled: !!name,
  })

  if (personaQ.isLoading) return <div className="text-text-muted">Loading…</div>
  if (personaQ.error || !personaQ.data) {
    return <div className="text-red-500">Persona not found.</div>
  }

  const p = personaQ.data
  const body = contextQ.data?.body ?? ''

  return (
    <div className="max-w-3xl mx-auto">
      <Link to="/personas" className="text-text-muted text-sm hover:text-text-primary">
        ← Back to personas
      </Link>

      <h1 className="text-2xl font-semibold mt-2">{p.name}</h1>

      <dl className="mt-4 grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-sm">
        <dt className="text-text-muted">Definition:</dt>
        <dd className="font-mono text-xs">{p.definition_path}</dd>
        <dt className="text-text-muted">Context:</dt>
        <dd className="font-mono text-xs">{p.context_path}</dd>
        <dt className="text-text-muted">Size:</dt>
        <dd>{p.context_words.toLocaleString()} words · {p.context_bytes.toLocaleString()} bytes</dd>
        {p.updated_at && (
          <>
            <dt className="text-text-muted">Updated:</dt>
            <dd>{new Date(p.updated_at).toLocaleString()}</dd>
          </>
        )}
      </dl>

      <h2 className="text-lg font-semibold mt-8 mb-2">Context</h2>

      {contextQ.isLoading && <div className="text-text-muted">Loading context…</div>}

      {!contextQ.isLoading && body === '' && (
        <div className="text-text-muted italic text-sm">(no context yet)</div>
      )}

      {body !== '' && (
        <div className="prose prose-sm max-w-none border border-border rounded-md p-4 bg-bg-secondary">
          <MarkdownContent>{body}</MarkdownContent>
        </div>
      )}
    </div>
  )
}
