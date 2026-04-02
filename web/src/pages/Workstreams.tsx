import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { getWorkstreams, createWorkstream } from '../api/client'
import type { Workstream } from '../api/types'
import { useState } from 'react'

function WorkstreamCard({ ws }: { ws: Workstream }) {
  const tags = ws.tags ? ws.tags.split(',').map(t => t.trim()).filter(Boolean) : []
  const timeAgo = ws.updated_at ? new Date(ws.updated_at + 'Z').toLocaleDateString() : ''

  return (
    <Link
      to={`/workstreams/${ws.id}`}
      data-list-item
      style={{
        display: 'block',
        padding: '16px 20px',
        background: 'var(--color-bg-secondary)',
        borderRadius: 8,
        textDecoration: 'none',
        color: 'inherit',
        border: '1px solid var(--color-border)',
        transition: 'border-color 0.15s',
      }}
      onMouseEnter={e => (e.currentTarget.style.borderColor = 'var(--color-accent)')}
      onMouseLeave={e => (e.currentTarget.style.borderColor = 'var(--color-border)')}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 12 }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 4 }}>{ws.name}</div>
          {ws.description && (
            <div style={{ fontSize: 13, color: 'var(--color-text-secondary)', lineHeight: 1.4, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {ws.description}
            </div>
          )}
        </div>
        <div style={{ fontSize: 12, color: 'var(--color-text-muted)', whiteSpace: 'nowrap' }}>{timeAgo}</div>
      </div>
      {tags.length > 0 && (
        <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
          {tags.map(tag => (
            <span key={tag} style={{ fontSize: 11, padding: '2px 8px', borderRadius: 99, background: 'var(--color-bg-tertiary)', color: 'var(--color-text-secondary)' }}>
              {tag}
            </span>
          ))}
        </div>
      )}
      {ws.parent_id && (
        <div style={{ fontSize: 11, color: 'var(--color-text-muted)', marginTop: 6 }}>
          Child of {ws.parent_id}
        </div>
      )}
    </Link>
  )
}

export default function Workstreams() {
  const [showArchived, setShowArchived] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDesc, setNewDesc] = useState('')

  const queryClient = useQueryClient()
  const { data: workstreams, isLoading } = useQuery({
    queryKey: ['workstreams', showArchived ? 'all' : 'active'],
    queryFn: () => getWorkstreams(showArchived ? 'all' : 'active'),
  })

  const createMut = useMutation({
    mutationFn: () => createWorkstream({ name: newName, description: newDesc }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['workstreams'] })
      setCreating(false)
      setNewName('')
      setNewDesc('')
    },
  })

  // Group into top-level and children
  const topLevel = workstreams?.filter(w => !w.parent_id) ?? []
  const children = workstreams?.filter(w => w.parent_id) ?? []
  const childrenByParent = children.reduce<Record<string, Workstream[]>>((acc, w) => {
    const pid = w.parent_id!
    if (!acc[pid]) acc[pid] = []
    acc[pid].push(w)
    return acc
  }, {})

  return (
    <div style={{ maxWidth: 800, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
        <h1 style={{ fontSize: 22, fontWeight: 700 }}>Workstreams</h1>
        <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
          <label style={{ fontSize: 13, color: 'var(--color-text-secondary)', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6 }}>
            <input type="checkbox" checked={showArchived} onChange={e => setShowArchived(e.target.checked)} />
            Show archived
          </label>
          <button
            onClick={() => setCreating(true)}
            style={{
              padding: '6px 14px', fontSize: 13, fontWeight: 600,
              background: 'var(--color-accent)', color: 'white', border: 'none',
              borderRadius: 6, cursor: 'pointer',
            }}
          >
            + New
          </button>
        </div>
      </div>

      {creating && (
        <div style={{ padding: 16, background: 'var(--color-bg-secondary)', borderRadius: 8, border: '1px solid var(--color-border)', marginBottom: 16 }}>
          <input
            autoFocus
            placeholder="Workstream name..."
            value={newName}
            onChange={e => setNewName(e.target.value)}
            style={{ width: '100%', padding: '8px 12px', fontSize: 14, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 6, marginBottom: 8, boxSizing: 'border-box' }}
            onKeyDown={e => e.key === 'Enter' && newName.trim() && createMut.mutate()}
          />
          <textarea
            placeholder="Description (optional)..."
            value={newDesc}
            onChange={e => setNewDesc(e.target.value)}
            rows={2}
            style={{ width: '100%', padding: '8px 12px', fontSize: 13, background: 'var(--color-bg-primary)', color: 'var(--color-text-primary)', border: '1px solid var(--color-border)', borderRadius: 6, marginBottom: 8, resize: 'vertical', boxSizing: 'border-box' }}
          />
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              onClick={() => newName.trim() && createMut.mutate()}
              disabled={!newName.trim() || createMut.isPending}
              style={{ padding: '6px 14px', fontSize: 13, fontWeight: 600, background: 'var(--color-accent)', color: 'white', border: 'none', borderRadius: 6, cursor: 'pointer', opacity: !newName.trim() ? 0.5 : 1 }}
            >
              Create
            </button>
            <button onClick={() => { setCreating(false); setNewName(''); setNewDesc('') }}
              style={{ padding: '6px 14px', fontSize: 13, background: 'transparent', color: 'var(--color-text-secondary)', border: '1px solid var(--color-border)', borderRadius: 6, cursor: 'pointer' }}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {isLoading ? (
        <div style={{ color: 'var(--color-text-muted)', padding: 40, textAlign: 'center' }}>Loading...</div>
      ) : topLevel.length === 0 ? (
        <div style={{ color: 'var(--color-text-muted)', padding: 40, textAlign: 'center' }}>
          No workstreams yet. Create one to start tracking your work threads.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {topLevel.map(ws => (
            <div key={ws.id}>
              <WorkstreamCard ws={ws} />
              {childrenByParent[ws.id]?.map(child => (
                <div key={child.id} style={{ marginLeft: 24, marginTop: 4 }}>
                  <WorkstreamCard ws={child} />
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
