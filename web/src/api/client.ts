import type {
  Feature,
  Milestone,
  RoadmapItem,
  Event,
  CycleInstance,
  CycleType,
  CycleScore,
  Discussion,
  StatusResponse,
  StatsResponse,
  Idea,
  AgentSession,
  ContextEntry,
  CoordinationStatus,
  QAResult,
} from './types'

const BASE = ''

async function fetchJson<T>(url: string): Promise<T> {
  const res = await fetch(`${BASE}${url}`)
  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`)
  }
  return res.json()
}

async function postJson<T>(url: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${url}`, {
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
  const res = await fetch(`${BASE}${url}`, {
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
export const getFeature = (id: string) => fetchJson<Feature>(`/api/features/${id}`)
export const getFeatureDeps = (id: string) => fetchJson<{ depends_on: string[]; blocks: string[] }>(`/api/features/${id}/deps`)
export const patchFeature = (id: string, data: Partial<Feature>) => patchJson<Feature>(`/api/features/${id}`, data)

// Milestones
export const getMilestones = () => fetchJson<Milestone[]>('/api/milestones')
export const patchMilestone = (id: string, data: Partial<Milestone>) => patchJson<Milestone>(`/api/milestones/${id}`, data)

// Roadmap
export const getRoadmap = () => fetchJson<RoadmapItem[]>('/api/roadmap')

// Cycles
export const getCycles = () => fetchJson<CycleInstance[]>('/api/cycles')
export const getCycleTypes = () => fetchJson<CycleType[]>('/api/cycles/types')
export const getCycleScores = (id: number) => fetchJson<CycleScore[]>(`/api/cycles/${id}/scores`)

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
export const getBurndown = () => fetchJson<unknown>('/api/stats/burndown')
export const getActivityHeatmap = () => fetchJson<unknown>('/api/stats/activity-heatmap')

// Ideas
export const getIdeas = () => fetchJson<Idea[]>('/api/ideas')
export const getIdeasHistory = () => fetchJson<Idea[]>('/api/ideas?view=history')
export const getIdea = (id: number) => fetchJson<Idea>(`/api/ideas/${id}`)

// Agents
export const getAgents = () => fetchJson<AgentSession[]>('/api/agents')
export const getAgentCoordination = () => fetchJson<CoordinationStatus>('/api/agents/coordination')

// Context
export const getContextEntries = () => fetchJson<ContextEntry[]>('/api/context')

// Decisions (ADRs)
export const getDecisions = () => fetchJson<unknown[]>('/api/decisions')

// Git
export const getGitLog = () => fetchJson<unknown[]>('/api/git/log')

// Search
export const search = (query: string) => fetchJson<unknown[]>(`/api/search?q=${encodeURIComponent(query)}`)

// Dependencies graph
export const getDependencies = () => fetchJson<unknown>('/api/dependencies')

// Queue
export const getQueue = () => fetchJson<unknown>('/api/queue')

// Spec document
export const getSpecDocument = () => fetchJson<unknown>('/api/spec-document')

export { postJson, patchJson, fetchJson }
