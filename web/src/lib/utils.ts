export function formatTimestamp(dateStr: string): string {
  if (!dateStr) return ''
  const d = new Date(dateStr.includes('T') ? dateStr : dateStr.replace(' ', 'T') + 'Z')
  if (isNaN(d.getTime())) return dateStr

  const now = new Date()
  const diff = now.getTime() - d.getTime()
  const relative = formatRelative(diff)

  const formatted = d.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
  }) + ', ' + d.toLocaleTimeString('en-US', {
    hour: 'numeric',
    minute: '2-digit',
  })

  return `${formatted} (${relative})`
}

export function formatRelative(diffMs: number): string {
  const seconds = Math.floor(diffMs / 1000)
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  const months = Math.floor(days / 30)
  return `${months}mo ago`
}

export function formatTimeAgo(dateStr: string): string {
  if (!dateStr) return ''
  const d = new Date(dateStr.includes('T') ? dateStr : dateStr.replace(' ', 'T') + 'Z')
  if (isNaN(d.getTime())) return ''
  return formatRelative(Date.now() - d.getTime())
}

export function cn(...classes: (string | false | undefined | null)[]): string {
  return classes.filter(Boolean).join(' ')
}

export function groupBy<T>(items: T[], key: (item: T) => string): Record<string, T[]> {
  return items.reduce((acc, item) => {
    const k = key(item)
    ;(acc[k] ??= []).push(item)
    return acc
  }, {} as Record<string, T[]>)
}

export function truncate(str: string, max: number): string {
  if (!str || str.length <= max) return str
  return str.slice(0, max) + '…'
}
