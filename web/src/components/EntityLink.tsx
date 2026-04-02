import { Link } from 'react-router-dom'

export type EntityType =
  | 'feature'
  | 'milestone'
  | 'roadmap'
  | 'cycle'
  | 'agent'
  | 'idea'
  | 'discussion'
  | 'decision'

const routeMap: Record<EntityType, string> = {
  feature: '/features',
  milestone: '/milestones',
  roadmap: '/roadmap',
  cycle: '/cycles',
  agent: '/agents',
  idea: '/ideas',
  discussion: '/discussions',
  decision: '/decisions',
}

const iconMap: Record<EntityType, string> = {
  feature: '📦',
  milestone: '🏁',
  roadmap: '🗺️',
  cycle: '🔄',
  agent: '🤖',
  idea: '💡',
  discussion: '💬',
  decision: '⚖️',
}

interface EntityLinkProps {
  type: EntityType
  id: string | number
  name?: string
  showIcon?: boolean
  className?: string
}

export function EntityLink({ type, id, name, showIcon = false, className = '' }: EntityLinkProps) {
  const path = `${routeMap[type]}/${id}`
  const displayName = name || String(id)

  return (
    <Link
      to={path}
      className={`text-accent hover:underline inline-flex items-center gap-1 ${className}`}
      title={`View ${type}: ${displayName}`}
    >
      {showIcon && <span className="text-xs">{iconMap[type]}</span>}
      <span>{displayName}</span>
    </Link>
  )
}

interface EntityChipProps {
  type: EntityType
  id: string | number
  name?: string
  className?: string
}

export function EntityChip({ type, id, name, className = '' }: EntityChipProps) {
  const path = `${routeMap[type]}/${id}`
  const displayName = name || String(id)

  return (
    <Link
      to={path}
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs 
        bg-bg-secondary text-text-secondary hover:bg-accent/20 hover:text-accent 
        transition-colors ${className}`}
      title={`View ${type}: ${displayName}`}
    >
      <span>{iconMap[type]}</span>
      <span>{displayName}</span>
    </Link>
  )
}
