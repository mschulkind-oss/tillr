import type {
  Feature,
  FeatureDetailResponse,
  Milestone,
  RoadmapItem,
  Event,
  CycleInstance,
  CycleType,
  CycleDetail,
  Discussion,
  StatusResponse,
  StatsResponse,
  BurndownData,
  HeatmapDay,
  ActivityDayCount,
  Idea,
  AgentSession,
  AgentStatusDashboard,
  StatusUpdate,
  ContextEntry,
  CoordinationStatus,
  QAResult,
  Decision,
  GroupedSearchResults,
  QueueResponse,
  DependencyGraph,
  SpecDocument,
  FeaturePR,
  TagCount,
  HeatmapGrid,
  AuditEvent,
  AuditStatsResponse,
  Workstream,
  WorkstreamNote,
  WorkstreamLink,
  WorkstreamDetail,
  AppConfig,
} from './types'
import { rewriteApiUrl } from './projects'

async function fetchJson<T>(url: string): Promise<T> {
  const res = await fetch(rewriteApiUrl(url))
  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`)
  }
  return res.json()
}

async function postJson<T>(url: string, body?: unknown): Promise<T> {
  const res = await fetch(rewriteApiUrl(url), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(`API error: ${res.status} ${text}`)
  }
  return res.json()
}

async function patchJson<T>(url: string, body: unknown): Promise<T> {
  const res = await fetch(rewriteApiUrl(url), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`)
  }
  return res.json()
}

// Status
export const getStatus = () => fetchJson<StatusResponse>('/api/status')

// Features
export const getFeatures = () => fetchJson<Feature[]>('/api/features')
export const getFeature = (id: string) => fetchJson<FeatureDetailResponse>(`/api/features/${id}`)
export const getFeatureDeps = (id: string) => fetchJson<{
  depends_on: Array<{ id: string; name: string; status: string }>
  depended_by: Array<{ id: string; name: string; status: string }>
  blocking_chain: string[]
}>(`/api/features/${id}/deps`)
export const patchFeature = (id: string, data: Partial<Feature>) => patchJson<Feature>(`/api/features/${id}`, data)

// Milestones
export const getMilestones = () => fetchJson<Milestone[]>('/api/milestones')
export const getMilestone = (id: string) => fetchJson<Milestone>(`/api/milestones/${id}`)
export const patchMilestone = (id: string, data: Partial<Milestone>) => patchJson<Milestone>(`/api/milestones/${id}`, data)

// Roadmap
export const getRoadmap = () => fetchJson<RoadmapItem[]>('/api/roadmap')
export const getRoadmapItem = (id: string) => fetchJson<RoadmapItem>(`/api/roadmap/${id}`)

// Cycles
export const getCycles = () => fetchJson<CycleInstance[]>('/api/cycles')
export const getCycleTypes = () => fetchJson<CycleType[]>('/api/cycles/types')
export const getCycleDetail = (id: number) => fetchJson<CycleDetail>(`/api/cycles/${id}`)
export const advanceCycle = (id: number, action: 'approve' | 'reject', notes?: string) =>
  postJson<{ feature: string; step: string; action: string; result?: string; next_step?: string }>(
    `/api/cycles/${id}/advance`, { action, notes }
  )

// Discussions
export const getDiscussions = () => fetchJson<Discussion[]>('/api/discussions')
export const getDiscussion = (id: number) => fetchJson<Discussion>(`/api/discussions/${id}`)

// History
export const getHistory = (params?: { limit?: number; feature?: string; type?: string }) => {
  const query = new URLSearchParams()
  if (params?.limit) query.set('limit', String(params.limit))
  if (params?.feature) query.set('feature', params.feature)
  if (params?.type) query.set('type', params.type)
  const qs = query.toString()
  return fetchJson<Event[]>(`/api/history${qs ? `?${qs}` : ''}`)
}

// QA
export const getQAPending = () => fetchJson<Feature[]>('/api/qa/pending')
export const getQAResults = (featureId: string) => fetchJson<QAResult[]>(`/api/qa/${featureId}`)
export const approveFeature = (featureId: string, notes?: string) =>
  postJson('/api/qa/' + featureId + '/approve', { notes })
export const rejectFeature = (featureId: string, notes?: string) =>
  postJson('/api/qa/' + featureId + '/reject', { notes })

// Stats
export const getStats = () => fetchJson<StatsResponse>('/api/stats')
export const getBurndown = () => fetchJson<BurndownData>('/api/stats/burndown')
export const getHeatmap = (days = 365) => fetchJson<{ days: HeatmapDay[] }>(`/api/stats/heatmap?days=${days}`)
export const getActivityHeatmap = (days = 365) => fetchJson<ActivityDayCount[]>(`/api/stats/activity-heatmap?days=${days}`)

// Ideas
export const getIdeas = () => fetchJson<Idea[]>('/api/ideas')
export const getIdeasHistory = () => fetchJson<Idea[]>('/api/ideas?view=history')
export const getIdea = (id: number) => fetchJson<Idea>(`/api/ideas/${id}`)
export const approveIdea = (id: number) => postJson<Idea>(`/api/ideas/${id}/approve`, {})
export const rejectIdea = (id: number) => postJson<Idea>(`/api/ideas/${id}/reject`, {})

// Agents
export const getAgents = () => fetchJson<AgentSession[]>('/api/agents')
export const getAgentDashboard = () => fetchJson<AgentStatusDashboard>('/api/agents/status')
export const getAgentCoordination = () => fetchJson<CoordinationStatus>('/api/agents/coordination')
export const getAgentDetail = (id: string) =>
  fetchJson<{ session: AgentSession; updates: StatusUpdate[]; worktree: unknown }>(`/api/agents/${id}`)

// Context
export const getContextEntries = () => fetchJson<ContextEntry[]>('/api/context')

// Decisions (ADRs)
export const getDecisions = () => fetchJson<Decision[]>('/api/decisions')
export const getDecision = (id: string) => fetchJson<Decision>(`/api/decisions/${id}`)

// Git
export const getGitLog = () => fetchJson<unknown[]>('/api/git/log')

// Search
export const search = (query: string) => fetchJson<GroupedSearchResults>(`/api/search?q=${encodeURIComponent(query)}`)

// Dependencies graph
export const getDependencies = () => fetchJson<DependencyGraph>('/api/dependencies')

// Queue
export const getQueue = () => fetchJson<QueueResponse>('/api/queue')
export const reclaimStaleItems = () => postJson<{ reclaimed: number }>('/api/queue')

// Spec document
export const getSpecDocument = () => fetchJson<SpecDocument>('/api/spec-document')

// Feature PRs
export const getFeaturePRs = (featureId: string) => fetchJson<FeaturePR[]>(`/api/features/${featureId}/prs`)

// Tags
export const getTags = () => fetchJson<TagCount[]>('/api/tags')

// Audit
export const getAuditEvents = (params?: { since?: string; until?: string; type?: string; feature?: string; limit?: number; offset?: number }) => {
  const query = new URLSearchParams()
  if (params?.since) query.set('since', params.since)
  if (params?.until) query.set('until', params.until)
  if (params?.type) query.set('type', params.type)
  if (params?.feature) query.set('feature', params.feature)
  if (params?.limit) query.set('limit', String(params.limit))
  if (params?.offset) query.set('offset', String(params.offset))
  const qs = query.toString()
  return fetchJson<{ events: AuditEvent[]; total: number }>(`/api/audit${qs ? `?${qs}` : ''}`)
}
export const getAuditStats = () => fetchJson<AuditStatsResponse>('/api/audit/stats')

// Analytics heatmap (hour-of-day x day-of-week grid)
export const getAnalyticsHeatmap = () => fetchJson<HeatmapGrid>('/api/analytics/heatmap')

// Workstreams
export const getWorkstreams = (status = 'active') => fetchJson<Workstream[]>(`/api/workstreams?status=${status}`)
export const getWorkstream = (id: string) => fetchJson<WorkstreamDetail>(`/api/workstreams/${id}`)
export const createWorkstream = (data: { name: string; description?: string; parent_id?: string; tags?: string; project_id?: string }) =>
  postJson<Workstream>('/api/workstreams', data)
export const patchWorkstream = (id: string, data: Partial<Workstream>) => patchJson<Workstream>(`/api/workstreams/${id}`, data)
export const archiveWorkstream = (id: string) => fetchJson<{ archived: string }>(`/api/workstreams/${id}`) // DELETE handled via fetch
export const addWorkstreamNote = (wsId: string, data: { content: string; note_type?: string; source?: string }) =>
  postJson<WorkstreamNote>(`/api/workstreams/${wsId}/notes`, data)
export const resolveWorkstreamNote = (wsId: string, noteId: number) =>
  patchJson<void>(`/api/workstreams/${wsId}/notes/${noteId}`, { resolved: 1 })
export const addWorkstreamLink = (wsId: string, data: { link_type: string; target_id?: string; target_url?: string; label?: string }) =>
  postJson<WorkstreamLink>(`/api/workstreams/${wsId}/links`, data)

// Config
export const getConfig = () => fetchJson<AppConfig>('/api/config')

export { postJson, patchJson, fetchJson }
