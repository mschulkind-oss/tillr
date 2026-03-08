import type { FeatureStatus } from '../api/types'

const statusConfig: Record<string, { label: string; classes: string }> = {
  draft: { label: 'Draft', classes: 'bg-bg-tertiary text-text-secondary' },
  planning: { label: 'Planning', classes: 'bg-purple/20 text-purple' },
  implementing: { label: 'Implementing', classes: 'bg-accent/20 text-accent' },
  'agent-qa': { label: 'Agent QA', classes: 'bg-orange/20 text-orange' },
  'human-qa': { label: 'Human QA', classes: 'bg-warning/20 text-warning' },
  done: { label: 'Done', classes: 'bg-success/20 text-success' },
  blocked: { label: 'Blocked', classes: 'bg-danger/20 text-danger' },
  // Roadmap statuses
  proposed: { label: 'Proposed', classes: 'bg-bg-tertiary text-text-secondary' },
  accepted: { label: 'Accepted', classes: 'bg-accent/20 text-accent' },
  'in-progress': { label: 'In Progress', classes: 'bg-purple/20 text-purple' },
  deferred: { label: 'Deferred', classes: 'bg-bg-tertiary text-text-muted' },
  rejected: { label: 'Rejected', classes: 'bg-danger/20 text-danger' },
  // Milestone
  active: { label: 'Active', classes: 'bg-success/20 text-success' },
  // Cycle
  completed: { label: 'Completed', classes: 'bg-success/20 text-success' },
  failed: { label: 'Failed', classes: 'bg-danger/20 text-danger' },
  // Generic
  open: { label: 'Open', classes: 'bg-accent/20 text-accent' },
  resolved: { label: 'Resolved', classes: 'bg-success/20 text-success' },
  merged: { label: 'Merged', classes: 'bg-purple/20 text-purple' },
  closed: { label: 'Closed', classes: 'bg-bg-tertiary text-text-muted' },
}

export function StatusBadge({ status }: { status: FeatureStatus | string }) {
  const config = statusConfig[status] || {
    label: status,
    classes: 'bg-bg-tertiary text-text-secondary',
  }
  return (
    <span
      className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${config.classes}`}
    >
      {config.label}
    </span>
  )
}
