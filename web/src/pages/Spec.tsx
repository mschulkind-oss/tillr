import { useQuery } from '@tanstack/react-query'
import { getSpecDocument } from '../api/client'
import { StatusBadge } from '../components/StatusBadge'
import { PageSkeleton } from '../components/Skeleton'
import { EntityLink } from '../components/EntityLink'
import { formatTimestamp, cn } from '../lib/utils'
import { useState } from 'react'
import { MarkdownContent } from '../components/MarkdownContent'
import type { SpecSection } from '../api/types'

export function Spec() {
  const doc = useQuery({ queryKey: ['spec-document'], queryFn: getSpecDocument })
  const [expandedFeatures, setExpandedFeatures] = useState<Set<string>>(new Set())
  const [tocOpen, setTocOpen] = useState(true)

  if (doc.isLoading) return <PageSkeleton />
  if (!doc.data) {
    return (
      <div className="text-center py-12 text-text-muted">
        No spec document available. Run <code className="bg-bg-secondary px-1.5 py-0.5 rounded text-xs">tillr spec generate</code> to create one.
      </div>
    )
  }

  const d = doc.data
  const toggleFeature = (id: string) => {
    setExpandedFeatures((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  return (
    <div className="flex gap-6">
      {/* Table of Contents sidebar */}
      <aside className={cn(
        'shrink-0 hidden lg:block',
        tocOpen ? 'w-56' : 'w-0'
      )}>
        {tocOpen && (
          <div className="sticky top-4">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-xs font-semibold text-text-muted uppercase tracking-wider">Contents</h3>
              <button
                onClick={() => setTocOpen(false)}
                className="text-text-muted hover:text-text-secondary text-xs"
                title="Hide table of contents"
              >
                ✕
              </button>
            </div>
            <nav className="space-y-0.5">
              {d.sections.map((s) => (
                <a
                  key={s.id}
                  href={`#section-${s.id}`}
                  className={cn(
                    'block text-xs text-text-secondary hover:text-accent transition-colors py-1 truncate',
                    s.level === 1 && 'font-medium text-text-primary',
                    s.level === 2 && 'pl-3',
                    s.level >= 3 && 'pl-6 text-text-muted'
                  )}
                >
                  {s.title}
                </a>
              ))}
            </nav>
          </div>
        )}
      </aside>

      {/* Main document body */}
      <div className="flex-1 min-w-0 max-w-4xl mx-auto space-y-6">
        {/* Header */}
        <div>
          <div className="flex items-center gap-2">
            {!tocOpen && (
              <button
                onClick={() => setTocOpen(true)}
                className="text-text-muted hover:text-text-secondary text-sm hidden lg:inline"
                title="Show table of contents"
              >
                ☰
              </button>
            )}
            <h1 className="text-2xl font-bold text-text-primary">{d.title}</h1>
          </div>
          <p className="text-sm text-text-secondary mt-1">
            Generated {formatTimestamp(d.generated_at)}
          </p>
        </div>

        {/* Stats row */}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <StatCard label="Total Features" value={d.stats.total_features} icon="✨" />
          <StatCard label="Done" value={d.stats.done} icon="✅" accent="text-success" />
          <StatCard label="In Progress" value={d.stats.in_progress} icon="🔄" accent="text-accent" />
          <StatCard label="Blocked" value={d.stats.blocked} icon="🚫" accent="text-danger" />
        </div>

        {/* Mobile TOC */}
        <details className="lg:hidden bg-bg-card border border-border rounded-lg">
          <summary className="px-4 py-3 text-sm font-semibold text-text-primary cursor-pointer">
            📑 Table of Contents
          </summary>
          <nav className="px-4 pb-3 space-y-0.5">
            {d.sections.map((s) => (
              <a
                key={s.id}
                href={`#section-${s.id}`}
                className={cn(
                  'block text-xs text-text-secondary hover:text-accent transition-colors py-1',
                  s.level === 1 && 'font-medium text-text-primary',
                  s.level === 2 && 'pl-3',
                  s.level >= 3 && 'pl-6 text-text-muted'
                )}
              >
                {s.title}
              </a>
            ))}
          </nav>
        </details>

        {/* Document sections */}
        {d.sections.map((section) => (
          <SectionBlock
            key={section.id}
            section={section}
            expandedFeatures={expandedFeatures}
            onToggleFeature={toggleFeature}
          />
        ))}

        {/* Footer */}
        <footer className="border-t border-border pt-4 pb-8 text-xs text-text-muted">
          <p>
            Generated {formatTimestamp(d.generated_at)} ·{' '}
            {d.stats.total_milestones} milestone{d.stats.total_milestones !== 1 ? 's' : ''} ·{' '}
            {d.stats.total_roadmap_items} roadmap item{d.stats.total_roadmap_items !== 1 ? 's' : ''}
          </p>
        </footer>
      </div>
    </div>
  )
}

function SectionBlock({
  section,
  expandedFeatures,
  onToggleFeature,
}: {
  section: SpecSection
  expandedFeatures: Set<string>
  onToggleFeature: (id: string) => void
}) {
  const Heading = section.level <= 1 ? 'h2' : 'h3'
  const headingClass = section.level <= 1
    ? 'text-xl font-bold text-text-primary'
    : 'text-lg font-semibold text-text-primary'

  return (
    <section id={`section-${section.id}`} className="py-6 border-b border-border last:border-b-0 scroll-mt-4">
      <Heading className={headingClass}>{section.title}</Heading>

      {section.content_md && (
        <div className="prose prose-sm prose-invert max-w-none text-text-secondary mt-3">
          <MarkdownContent>{section.content_md}</MarkdownContent>
        </div>
      )}

      {section.features && section.features.length > 0 && (
        <div className="mt-4 space-y-2">
          <h4 className="text-xs font-semibold text-text-muted uppercase tracking-wider">
            Features ({section.features.length})
          </h4>
          <div className="space-y-2">
            {section.features.map((f) => {
              const isExpanded = expandedFeatures.has(f.id)
              return (
                <div key={f.id} className="bg-bg-secondary rounded-lg p-3 border border-border-light">
                  <div className="flex items-center gap-3">
                    <button
                      onClick={() => onToggleFeature(f.id)}
                      className="text-text-muted hover:text-text-secondary text-xs shrink-0 w-4"
                      aria-label={isExpanded ? 'Collapse spec' : 'Expand spec'}
                    >
                      {isExpanded ? '▾' : '▸'}
                    </button>
                    <EntityLink type="feature" id={f.id} name={f.name} showIcon />
                    <StatusBadge status={f.status} />
                    <span className={cn(
                      'text-xs font-mono ml-auto shrink-0',
                      f.priority >= 8 ? 'text-danger' : f.priority >= 5 ? 'text-warning' : 'text-text-muted'
                    )}>
                      P{f.priority}
                    </span>
                  </div>

                  {f.description && !isExpanded && (
                    <p className="text-xs text-text-muted mt-1 ml-7 truncate">{f.description}</p>
                  )}

                  {isExpanded && (
                    <div className="mt-3 ml-7 space-y-2">
                      {f.description && (
                        <p className="text-sm text-text-secondary">{f.description}</p>
                      )}
                      {f.spec_md && (
                        <div className="prose prose-sm prose-invert max-w-none text-text-secondary bg-bg-tertiary rounded p-3 border border-border-light">
                          <MarkdownContent>{f.spec_md}</MarkdownContent>
                        </div>
                      )}
                      {f.dependencies && f.dependencies.length > 0 && (
                        <div className="flex items-center gap-2 text-xs text-text-muted">
                          <span>Depends on:</span>
                          {f.dependencies.map((depId) => (
                            <EntityLink key={depId} type="feature" id={depId} name={depId} />
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </section>
  )
}

function StatCard({ label, value, icon, accent }: {
  label: string
  value: number
  icon: string
  accent?: string
}) {
  return (
    <div className="bg-bg-card border border-border rounded-lg p-3 flex items-center gap-3">
      <span className="text-xl">{icon}</span>
      <div>
        <div className={cn('text-xl font-bold', accent || 'text-text-primary')}>
          {value}
        </div>
        <div className="text-[10px] text-text-secondary">{label}</div>
      </div>
    </div>
  )
}
