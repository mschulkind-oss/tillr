// API response types matching Go models

export interface Project {
  id: string
  name: string
  description?: string
  created_at: string
  updated_at: string
}

export interface Milestone {
  id: string
  project_id: string
  name: string
  description?: string
  sort_order: number
  status: 'active' | 'blocked' | 'done'
  created_at: string
  updated_at: string
  total_features?: number
  done_features?: number
}

export interface Feature {
  id: string
  project_id: string
  milestone_id?: string
  name: string
  description?: string
  spec?: string
  status: FeatureStatus
  priority: number
  assigned_cycle?: string
  roadmap_item_id?: string
  created_at: string
  updated_at: string
  previous_status?: string
  estimate_points?: number
  estimate_size?: string
  depends_on?: string[]
  milestone_name?: string
  tags?: string[]
}

export type FeatureStatus =
  | 'draft'
  | 'planning'
  | 'implementing'
  | 'agent-qa'
  | 'human-qa'
  | 'done'
  | 'blocked'

export interface RoadmapItem {
  id: string
  project_id: string
  title: string
  description?: string
  category?: string
  priority: 'critical' | 'high' | 'medium' | 'low' | 'nice-to-have'
  status: 'proposed' | 'accepted' | 'in-progress' | 'done' | 'deferred' | 'rejected'
  effort: 'xs' | 's' | 'm' | 'l' | 'xl'
  sort_order: number
  created_at: string
  updated_at: string
}

export interface Event {
  id: number
  project_id: string
  feature_id?: string
  event_type: string
  data?: string
  created_at: string
}

export interface CycleInstance {
  id: number
  feature_id: string
  cycle_type: string
  current_step: number
  iteration: number
  status: 'active' | 'completed' | 'failed'
  created_at: string
  updated_at: string
  step_name?: string
}

export interface CycleType {
  name: string
  description: string
  steps: string[]
}

export interface CycleScore {
  id: number
  cycle_id: number
  step: number
  iteration: number
  score: number
  notes?: string
  created_at: string
}

export interface Discussion {
  id: number
  project_id: string
  feature_id?: string
  title: string
  body?: string
  status: 'open' | 'resolved' | 'merged' | 'closed'
  author: string
  created_at: string
  updated_at: string
  comment_count?: number
  comments?: DiscussionComment[]
  votes?: Record<string, number>
}

export interface DiscussionComment {
  id: number
  discussion_id: number
  author: string
  content: string
  parent_id?: number
  comment_type: string
  created_at: string
}

export interface QAResult {
  id: number
  feature_id: string
  qa_type: 'agent' | 'human'
  passed: boolean
  notes?: string
  checklist?: string
  created_at: string
}

export interface WorkItem {
  id: number
  feature_id: string
  work_type: string
  status: 'pending' | 'active' | 'done' | 'failed'
  agent_prompt?: string
  result?: string
  assigned_agent?: string
  started_at?: string
  completed_at?: string
  created_at: string
}

export interface AgentSession {
  id: string
  agent_type: string
  status: 'active' | 'completed' | 'failed'
  current_feature?: string
  capabilities?: string
  created_at: string
  updated_at: string
}

export interface Idea {
  id: number
  project_id: string
  title: string
  description?: string
  source: string
  status: 'pending' | 'approved' | 'rejected' | 'implemented'
  priority?: number
  feature_id?: string
  created_at: string
  updated_at: string
}

export interface StatusResponse {
  project: Project
  feature_counts: Record<string, number>
  milestone_count: number
  active_cycles: number
  recent_events: Event[]
}

export interface StatsResponse {
  features: {
    total: number
    by_status: Record<string, number>
    avg_priority: number
    completion_rate: number
  }
  cycles: {
    total: number
    active: number
    completed: number
    avg_score: number
    by_type: Record<string, number>
  }
  roadmap: {
    total: number
    by_status: Record<string, number>
    by_priority: Record<string, number>
    by_category: Record<string, number>
  }
  milestones: Milestone[]
  recent_activity: Event[]
}

export interface ContextEntry {
  id: number
  project_id: string
  key: string
  value: string
  category?: string
  created_at: string
  updated_at: string
}

export interface CoordinationStatus {
  active_agents: AgentSession[]
  stale_agents: AgentSession[]
  conflicts: Array<{
    feature_id: string
    feature_name: string
    agents: string[]
  }>
  queue_depth: number
  claimed_items: number
}
