// Post-reset minimal API types. Mirrors internal/models/models.go.
// New entities will be added here as the consulting-firm roadmap
// progresses (see docs/consulting-firm/roadmap.md).

export interface Project {
  id: string
  name: string
  created_at: string
}

export interface Feature {
  id: number
  project_id: string
  title: string
  description?: string
  status: string
  created_at: string
  updated_at: string
}

export interface Comment {
  id: number
  entity_type: string
  entity_id: string
  author_type: 'human' | 'agent'
  author_role?: string
  body: string
  metadata?: string
  created_at: string
}

export interface Persona {
  name: string
  definition_path: string
  context_path: string
  context_words: number
  context_bytes: number
  updated_at?: string
}

export interface Retro {
  name: string
  path: string
  bytes: number
  updated_at: string
}
