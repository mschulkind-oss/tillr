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

export interface FeatureDetailResponse {
  feature: Feature
  cycles: CycleInstance[]
  scores: CycleScore[]
  work_items: WorkItem[]
}

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
  entity_type: string
  entity_id: string
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
  steps: CycleStep[]
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
  project_id: string
  feature_id?: string
  name: string
  task_description?: string
  status: 'active' | 'paused' | 'completed' | 'failed' | 'abandoned'
  progress_pct: number
  current_phase?: string
  eta?: string
  context_snapshot?: string
  created_at: string
  updated_at: string
}

export interface StatusUpdate {
  id: number
  agent_session_id: string
  message_md: string
  progress_pct?: number
  phase?: string
  created_at: string
}

export interface AgentHeartbeatInfo {
  session: AgentSession
  heartbeat_status: 'active' | 'stale' | 'failed'
  session_duration_secs: number
  current_work_item?: WorkItem
  feature_name?: string
  completed_count: number
  failed_count: number
}

export interface AgentStatusDashboard {
  agents: AgentHeartbeatInfo[]
  total_sessions: number
  active_count: number
  stale_count: number
  failed_count: number
  completed_count: number
  total_work_done: number
}

export interface Idea {
  id: number
  project_id: string
  title: string
  raw_input: string
  idea_type: string
  status: string
  spec_md?: string
  auto_implement: boolean
  submitted_by: string
  assigned_agent?: string
  feature_id?: string
  source_page?: string
  context?: string
  created_at: string
  updated_at: string
}

export interface Decision {
  id: string
  title: string
  status: 'proposed' | 'accepted' | 'rejected' | 'superseded' | 'deprecated'
  context: string
  decision: string
  consequences: string
  superseded_by?: string
  feature_id?: string
  created_at: string
  updated_at: string
}

export interface CycleDetail {
  cycle: CycleInstance
  scores: CycleScore[]
  steps: CycleStep[]
}

export interface SearchResult {
  entity_type: string
  entity_id: string
  title: string
  snippet: string
  rank: number
}

export interface GroupedSearchResults {
  query: string
  total: number
  groups: Record<string, SearchResult[]>
  ordered_types: string[]
}

export interface StatusResponse {
  project: Project
  feature_counts: Record<string, number>
  milestone_count: number
  active_cycles: number
  open_discussions: number
  recent_events: Event[]
}

export interface StatsResponse {
  feature_stats: {
    total: number
    by_status: Record<string, number>
    completion_rate: number
  }
  cycle_stats: {
    total_cycles: number
    total_iterations: number
    avg_score: number
    scores_over_time?: Array<{ date: string; score: number; cycle: string }>
  }
  roadmap_stats: {
    total: number
    by_status: Record<string, number>
    by_priority: Record<string, number>
    by_category: Record<string, number>
  }
  milestone_stats: Array<{ name: string; total: number; done: number; progress: number }>
  activity: {
    total_events: number
    events_last_7_days: number
    events_last_30_days: number
  }
}

export interface BurndownPoint {
  date: string
  remaining: number
  completed: number
}

export interface WeekVelocity {
  week: string
  completed_count: number
}

export interface BurndownData {
  points: BurndownPoint[]
  velocity: WeekVelocity[]
}

export interface HeatmapDay {
  date: string
  count: number
  events: Record<string, number>
}

export interface ActivityDayCount {
  date: string
  count: number
}

export interface QueueEntry {
  work_item_id: number
  feature_id: string
  feature_name: string
  work_type: string
  priority: number
  cycle_type: string
  assigned_agent: string
  status: string
  created_at: string
}

export interface QueueStats {
  total_pending: number
  total_claimed: number
  total_completed_today: number
}

export interface QueueResponse {
  queue: QueueEntry[]
  stats: QueueStats
}

export interface DependencyGraph {
  nodes: Array<{ id: string; name: string; status: string }>
  edges: Array<{ from: string; to: string }>
}

export interface SpecSection {
  id: string
  title: string
  content_md: string
  level: number
  features?: Array<{
    id: string
    name: string
    status: string
    priority: number
    spec_md: string
    description: string
    dependencies?: string[]
  }>
}

export interface SpecDocument {
  title: string
  generated_at: string
  sections: SpecSection[]
  stats: {
    total_features: number
    done: number
    in_progress: number
    blocked: number
    total_milestones: number
    total_roadmap_items: number
  }
}

export interface FeaturePR {
  feature_id: string
  pr_url: string
  pr_number: number
  repo: string
  status: 'open' | 'closed' | 'merged'
  created_at: string
}

export interface TagCount {
  tag: string
  count: number
}

export interface HeatmapGrid {
  cells: Array<{ day: number; hour: number; count: number }>
  max_count: number
}

export interface AuditEvent {
  id: number
  project_id: string
  feature_id?: string
  event_type: string
  data?: string
  created_at: string
}

export interface AuditStatsResponse {
  by_type: Record<string, number>
  total: number
}

export interface ContextEntry {
  id: number
  project_id: string
  feature_id?: string
  context_type: string
  title: string
  content_md: string
  author: string
  tags?: string
  created_at: string
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

// Workstreams

export interface Workstream {
  id: string
  project_id: string
  parent_id?: string
  name: string
  description: string
  status: 'active' | 'archived'
  tags: string
  created_at: string
  updated_at: string
}

export interface WorkstreamNote {
  id: number
  workstream_id: string
  content: string
  note_type: 'note' | 'question' | 'decision' | 'idea' | 'import'
  source?: string
  resolved: number
  created_at: string
}

export interface WorkstreamLink {
  id: number
  workstream_id: string
  link_type: 'feature' | 'doc' | 'url' | 'discussion'
  target_id?: string
  target_url?: string
  label?: string
  created_at: string
}

export interface WorkstreamDetail {
  workstream: Workstream
  notes: WorkstreamNote[]
  links: WorkstreamLink[]
  children: Workstream[]
}

export interface WorkstreamFeature {
  feature: Feature
  relationship: 'owned' | 'dependency'
}

export interface CycleStep {
  name: string
  human?: boolean
  instructions?: string
}

export interface AppConfig {
  vantage_url?: string
  project_id?: string
}
