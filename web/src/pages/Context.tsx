import { useQuery } from '@tanstack/react-query'
import { getContextEntries, fetchJson } from '../api/client'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { formatTimeAgo, truncate } from '../lib/utils'
import { useState, useMemo, useCallback, useRef, useEffect } from 'react'
import { MarkdownContent } from '../components/MarkdownContent'
import type { ContextEntry } from '../api/types'

const TYPE_TABS = ['all', 'note', 'reference', 'decision', 'research'] as const
type TypeTab = (typeof TYPE_TABS)[number]

const typeColors: Record<string, string> = {
  note: 'bg-accent/20 text-accent',
  reference: 'bg-purple/20 text-purple',
  decision: 'bg-warning/20 text-warning',
  research: 'bg-success/20 text-success',
}

function TypeBadge({ type }: { type: string }) {
  const classes = typeColors[type] || 'bg-bg-tertiary text-text-secondary'
  return (
    <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${classes}`}>
      {type}
    </span>
  )
}

export function Context() {
  const entries = useQuery({ queryKey: ['context'], queryFn: getContextEntries })
  const [typeFilter, setTypeFilter] = useState<TypeTab>('all')
  const [authorFilter, setAuthorFilter] = useState<string>('all')
  const [searchQuery, setSearchQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [expandedId, setExpandedId] = useState<number | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const handleSearch = useCallback((value: string) => {
    setSearchQuery(value)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => setDebouncedQuery(value), 300)
  }, [])

  useEffect(() => {
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current) }
  }, [])

  const searchResults = useQuery({
    queryKey: ['context-search', debouncedQuery],
    queryFn: () => fetchJson<ContextEntry[]>(`/api/context/search?q=${encodeURIComponent(debouncedQuery)}`),
    enabled: debouncedQuery.length > 0,
  })

  const allEntries = debouncedQuery ? (searchResults.data || []) : (entries.data || [])

  const authors = useMemo(() => {
    const set = new Set<string>()
    for (const e of entries.data || []) set.add(e.author)
    return Array.from(set).sort()
  }, [entries.data])

  const typeCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const e of allEntries) {
      counts[e.context_type] = (counts[e.context_type] || 0) + 1
    }
    return counts
  }, [allEntries])

  const filtered = useMemo(() => {
    let result = allEntries
    if (typeFilter !== 'all') result = result.filter((e) => e.context_type === typeFilter)
    if (authorFilter !== 'all') result = result.filter((e) => e.author === authorFilter)
    return result
  }, [allEntries, typeFilter, authorFilter])

  if (entries.isLoading) return <PageSkeleton />

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold text-text-primary">Context Library</h1>
        <p className="text-sm text-text-secondary mt-1">
          {allEntries.length} entries
          {typeCounts['note'] ? ` · ${typeCounts['note']} notes` : ''}
          {typeCounts['reference'] ? ` · ${typeCounts['reference']} references` : ''}
          {typeCounts['decision'] ? ` · ${typeCounts['decision']} decisions` : ''}
          {typeCounts['research'] ? ` · ${typeCounts['research']} research` : ''}
        </p>
      </div>

      {/* Search & author filter */}
      <div className="flex flex-col sm:flex-row gap-3">
        <input
          type="text"
          placeholder="Search context entries…"
          value={searchQuery}
          onChange={(e) => handleSearch(e.target.value)}
          className="bg-bg-secondary border border-border rounded px-3 py-2 w-full text-sm text-text-primary placeholder:text-text-muted focus:outline-none focus:ring-1 focus:ring-accent"
        />
        {authors.length > 1 && (
          <select
            value={authorFilter}
            onChange={(e) => setAuthorFilter(e.target.value)}
            className="bg-bg-secondary border border-border rounded px-3 py-2 text-sm text-text-primary shrink-0"
          >
            <option value="all">All authors</option>
            {authors.map((a) => (
              <option key={a} value={a}>{a}</option>
            ))}
          </select>
        )}
      </div>

      {/* Type filter tabs */}
      <div className="flex items-center gap-1 bg-bg-secondary rounded-lg p-1">
        {TYPE_TABS.map((tab) => (
          <button
            key={tab}
            onClick={() => setTypeFilter(tab)}
            className={`px-3 py-1.5 rounded-md text-sm font-medium transition-colors ${
              typeFilter === tab
                ? 'bg-bg-card text-text-primary shadow-sm'
                : 'text-text-muted hover:text-text-secondary'
            }`}
          >
            {tab === 'all' ? 'All' : tab.charAt(0).toUpperCase() + tab.slice(1)}
            {tab === 'all'
              ? ` (${allEntries.length})`
              : typeCounts[tab]
                ? ` (${typeCounts[tab]})`
                : ''}
          </button>
        ))}
      </div>

      {/* Entry cards */}
      {filtered.length === 0 ? (
        <div className="text-center py-16">
          <p className="text-4xl mb-3">📚</p>
          <p className="text-text-secondary font-medium">No context entries found</p>
          <p className="text-sm text-text-muted mt-1">
            Context entries capture notes, references, decisions, and research tied to your project and features.
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {filtered.map((entry) => (
            <ContextCard
              key={entry.id}
              entry={entry}
              expanded={expandedId === entry.id}
              onToggle={() => setExpandedId(expandedId === entry.id ? null : entry.id)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function ContextCard({
  entry,
  expanded,
  onToggle,
}: {
  entry: ContextEntry
  expanded: boolean
  onToggle: () => void
}) {
  const tags = entry.tags
    ? entry.tags.split(',').map((t) => t.trim()).filter(Boolean)
    : []

  return (
    <div
      className={`bg-bg-secondary rounded-lg p-4 cursor-pointer transition-all ${
        expanded ? 'ring-1 ring-accent col-span-1 lg:col-span-2' : 'hover:ring-1 hover:ring-border'
      }`}
      onClick={onToggle}
    >
      {/* Header row */}
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-sm font-medium text-text-primary">{entry.title}</span>
            <TypeBadge type={entry.context_type} />
          </div>

          <div className="flex items-center gap-3 mt-1.5 flex-wrap">
            <span className="text-xs text-text-muted">by {entry.author}</span>
            {entry.feature_id && (
              <span className="text-xs text-text-secondary flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
                Feature: <EntityLink type="feature" id={entry.feature_id} />
              </span>
            )}
            <span className="text-xs text-text-muted">{formatTimeAgo(entry.created_at)}</span>
          </div>

          {tags.length > 0 && (
            <div className="flex items-center gap-1.5 mt-2 flex-wrap">
              {tags.map((tag) => (
                <span key={tag} className="bg-bg-tertiary px-2 py-0.5 rounded text-xs text-text-secondary">
                  {tag}
                </span>
              ))}
            </div>
          )}
        </div>

        <span className="text-text-muted text-xs shrink-0 mt-0.5">
          {expanded ? '▲' : '▼'}
        </span>
      </div>

      {/* Collapsed preview */}
      {!expanded && (
        <p className="text-xs text-text-secondary mt-2 line-clamp-2">
          {truncate(entry.content_md, 150)}
        </p>
      )}

      {/* Expanded content */}
      {expanded && (
        <div className="mt-4 pt-4 border-t border-border space-y-3">
          <div className="prose prose-sm prose-invert max-w-none text-text-secondary text-sm [&_h1]:text-text-primary [&_h2]:text-text-primary [&_h3]:text-text-primary [&_strong]:text-text-primary [&_a]:text-accent [&_code]:bg-bg-tertiary [&_code]:px-1 [&_code]:rounded [&_pre]:bg-bg-tertiary [&_pre]:p-3 [&_pre]:rounded-lg">
            <MarkdownContent>{entry.content_md}</MarkdownContent>
          </div>

          <div className="flex items-center gap-4 text-xs text-text-muted pt-2 border-t border-border">
            <span>Type: {entry.context_type}</span>
            <span>Author: {entry.author}</span>
            <span>Created: {formatTimeAgo(entry.created_at)}</span>
            {entry.feature_id && (
              <span className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
                Feature: <EntityLink type="feature" id={entry.feature_id} />
              </span>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
