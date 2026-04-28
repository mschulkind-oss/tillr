import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getPersonas } from '../api/client'

export function Personas() {
  const { data: personas, isLoading, error } = useQuery({
    queryKey: ['personas'],
    queryFn: getPersonas,
  })

  return (
    <div className="max-w-3xl mx-auto">
      <h1 className="text-2xl font-semibold mb-2">Personas</h1>
      <p className="text-text-muted text-sm mb-4">
        Discovered from <code className="px-1 rounded bg-bg-secondary">.claude/agents/</code>.
        Context files live under <code className="px-1 rounded bg-bg-secondary">swarf/agents/&lt;name&gt;/</code>.
      </p>

      {isLoading && <div className="text-text-muted">Loading…</div>}
      {error && <div className="text-red-500">Error loading personas.</div>}

      {personas && personas.length === 0 && (
        <div className="text-text-muted text-sm">
          No personas. Add one at <code className="px-1 rounded bg-bg-secondary">.claude/agents/&lt;name&gt;.md</code>.
        </div>
      )}

      {personas && personas.length > 0 && (
        <ul className="divide-y divide-border border border-border rounded-md">
          {personas.map((p) => (
            <li key={p.name} className="px-4 py-3 hover:bg-sidebar-hover">
              <Link to={`/personas/${p.name}`} className="flex items-center gap-3">
                <span className="font-medium text-text-primary">{p.name}</span>
                <span className="flex-1 text-sm text-text-muted">
                  {p.context_words.toLocaleString()} words
                </span>
                {p.updated_at ? (
                  <span className="text-xs text-text-muted">
                    {new Date(p.updated_at).toLocaleString()}
                  </span>
                ) : (
                  <span className="text-xs text-text-muted italic">empty</span>
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
