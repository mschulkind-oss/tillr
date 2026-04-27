// Post-reset minimal API client. Mirrors the surface in
// internal/server/server.go.
import type { Comment, Feature, Project } from './types'
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

export const getProject = () => fetchJson<Project>('/api/project')
export const getFeatures = () => fetchJson<Feature[]>('/api/features')
export const getFeature = (id: number) => fetchJson<Feature>(`/api/features/${id}`)
export const createFeature = (data: { title: string; description?: string }) =>
  postJson<Feature>('/api/features', data)

export const getComments = (featureId: number) =>
  fetchJson<Comment[]>(`/api/features/${featureId}/comments`)
export const addComment = (
  featureId: number,
  data: { body: string; author_type?: string; author_role?: string },
) => postJson<Comment>(`/api/features/${featureId}/comments`, data)

export { fetchJson, postJson }
