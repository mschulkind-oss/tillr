package models

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type Milestone struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order"`
	Status      string `json:"status"` // active, blocked, done
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`

	// Computed fields
	TotalFeatures int `json:"total_features,omitempty"`
	DoneFeatures  int `json:"done_features,omitempty"`
}

type Feature struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	MilestoneID   string `json:"milestone_id,omitempty"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	Spec          string `json:"spec,omitempty"`
	Status        string `json:"status"` // draft, planning, implementing, agent-qa, human-qa, done, blocked
	Priority      int    `json:"priority"`
	AssignedCycle string `json:"assigned_cycle,omitempty"`
	RoadmapItemID string `json:"roadmap_item_id,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`

	PreviousStatus string `json:"previous_status,omitempty"`
	EstimatePoints int    `json:"estimate_points,omitempty"`
	EstimateSize   string `json:"estimate_size,omitempty"`

	// Computed fields
	DependsOn     []string `json:"depends_on,omitempty"`
	MilestoneName string   `json:"milestone_name,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// EstimationSummary holds aggregate estimation data for a project or milestone.
type EstimationSummary struct {
	TotalPoints     int                   `json:"total_points"`
	CompletedPoints int                   `json:"completed_points"`
	RemainingPoints int                   `json:"remaining_points"`
	BySizeEntries   []EstimationSizeEntry `json:"by_size"`
	Unestimated     int                   `json:"unestimated"`
}

// EstimationSizeEntry holds count and done count for a t-shirt size.
type EstimationSizeEntry struct {
	Size  string `json:"size"`
	Total int    `json:"total"`
	Done  int    `json:"done"`
}

type WorkItem struct {
	ID            int    `json:"id"`
	FeatureID     string `json:"feature_id"`
	WorkType      string `json:"work_type"`
	Status        string `json:"status"` // pending, active, done, failed
	AgentPrompt   string `json:"agent_prompt,omitempty"`
	Result        string `json:"result,omitempty"`
	AssignedAgent string `json:"assigned_agent,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	CreatedAt     string `json:"created_at"`
}

// Conflict represents multiple agents working on the same feature.
type Conflict struct {
	FeatureID   string   `json:"feature_id"`
	FeatureName string   `json:"feature_name"`
	Agents      []string `json:"agents"`
}

// CoordinationStatus is the full multi-agent coordination snapshot.
type CoordinationStatus struct {
	ActiveAgents []AgentSession `json:"active_agents"`
	StaleAgents  []AgentSession `json:"stale_agents"`
	Conflicts    []Conflict     `json:"conflicts"`
	QueueDepth   int            `json:"queue_depth"`
	ClaimedItems int            `json:"claimed_items"`
}

type Event struct {
	ID        int    `json:"id"`
	ProjectID string `json:"project_id"`
	FeatureID string `json:"feature_id,omitempty"`
	EventType string `json:"event_type"`
	Data      string `json:"data,omitempty"`
	CreatedAt string `json:"created_at"`
}

type RoadmapItem struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Priority    string `json:"priority"` // critical, high, medium, low, nice-to-have
	Status      string `json:"status"`   // proposed, accepted, in-progress, done, deferred, rejected
	Effort      string `json:"effort"`   // xs, s, m, l, xl (t-shirt sizes)
	SortOrder   int    `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type QAResult struct {
	ID        int    `json:"id"`
	FeatureID string `json:"feature_id"`
	QAType    string `json:"qa_type"` // agent, human
	Passed    bool   `json:"passed"`
	Notes     string `json:"notes,omitempty"`
	Checklist string `json:"checklist,omitempty"`
	CreatedAt string `json:"created_at"`
}

type Heartbeat struct {
	ID        int    `json:"id"`
	FeatureID string `json:"feature_id"`
	AgentID   string `json:"agent_id,omitempty"`
	Message   string `json:"message,omitempty"`
	CreatedAt string `json:"created_at"`
}

// CycleType defines a predefined iteration cycle.
type CycleType struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Steps       []string `json:"steps"`
}

// CycleInstance is a running cycle for a feature.
type CycleInstance struct {
	ID          int    `json:"id"`
	FeatureID   string `json:"feature_id"`
	CycleType   string `json:"cycle_type"`
	CurrentStep int    `json:"current_step"`
	Iteration   int    `json:"iteration"`
	Status      string `json:"status"` // active, completed, failed
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`

	// Computed
	StepName string `json:"step_name,omitempty"`
}

// CycleScore records a judge score for a cycle step.
type CycleScore struct {
	ID        int     `json:"id"`
	CycleID   int     `json:"cycle_id"`
	Step      int     `json:"step"`
	Iteration int     `json:"iteration"`
	Score     float64 `json:"score"`
	Notes     string  `json:"notes,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// Discussion is an RFC-style thread for agent collaboration.
type Discussion struct {
	ID        int    `json:"id"`
	ProjectID string `json:"project_id"`
	FeatureID string `json:"feature_id,omitempty"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Status    string `json:"status"` // open, resolved, merged, closed
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	// Computed
	CommentCount int                 `json:"comment_count,omitempty"`
	Comments     []DiscussionComment `json:"comments,omitempty"`
}

// DiscussionComment is a comment in a discussion thread.
type DiscussionComment struct {
	ID           int    `json:"id"`
	DiscussionID int    `json:"discussion_id"`
	Author       string `json:"author"`
	Content      string `json:"content"`
	ParentID     int    `json:"parent_id,omitempty"`
	CommentType  string `json:"comment_type"` // comment, proposal, approval, objection, revision, decision
	CreatedAt    string `json:"created_at"`
}

// StatusOverview is the project dashboard summary.
type StatusOverview struct {
	Project        *Project       `json:"project"`
	FeatureCounts  map[string]int `json:"feature_counts"`
	MilestoneCount int            `json:"milestone_count"`
	ActiveCycles   int            `json:"active_cycles"`
	RecentEvents   []Event        `json:"recent_events"`
	ActiveWork     []WorkItem     `json:"active_work"`
}

// WorkContext is the enriched response from `lifecycle next --json`.
// It carries ALL context an agent needs to do the work — no OOB info needed.
type WorkContext struct {
	WorkItem      *WorkItem      `json:"work_item"`
	Feature       *Feature       `json:"feature"`
	Cycle         *CycleInstance `json:"cycle,omitempty"`
	CycleType     *CycleType     `json:"cycle_type,omitempty"`
	RoadmapItem   *RoadmapItem   `json:"roadmap_item,omitempty"`
	PriorResults  []WorkItem     `json:"prior_results,omitempty"`
	CycleScores   []CycleScore   `json:"cycle_scores,omitempty"`
	AgentGuidance string         `json:"agent_guidance"`
}

// CycleDetail is the enriched response for GET /api/cycles/{id}.
type CycleDetail struct {
	Cycle  CycleInstance `json:"cycle"`
	Scores []CycleScore  `json:"scores"`
	Steps  []string      `json:"steps"`
}

// AgentSession tracks an active agent work session.
type AgentSession struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	FeatureID       string `json:"feature_id,omitempty"`
	Name            string `json:"name"`
	TaskDescription string `json:"task_description,omitempty"`
	Status          string `json:"status"`
	ProgressPct     int    `json:"progress_pct"`
	CurrentPhase    string `json:"current_phase,omitempty"`
	ETA             string `json:"eta,omitempty"`
	ContextSnapshot string `json:"context_snapshot,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// StatusUpdate is an agent progress update (markdown).
type StatusUpdate struct {
	ID             int    `json:"id"`
	AgentSessionID string `json:"agent_session_id"`
	MessageMD      string `json:"message_md"`
	ProgressPct    *int   `json:"progress_pct,omitempty"`
	Phase          string `json:"phase,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// IdeaQueueItem is a human-submitted feature/bug idea.
type IdeaQueueItem struct {
	ID            int    `json:"id"`
	ProjectID     string `json:"project_id"`
	Title         string `json:"title"`
	RawInput      string `json:"raw_input"`
	IdeaType      string `json:"idea_type"`
	Status        string `json:"status"`
	SpecMD        string `json:"spec_md,omitempty"`
	AutoImplement bool   `json:"auto_implement"`
	SubmittedBy   string `json:"submitted_by"`
	AssignedAgent string `json:"assigned_agent,omitempty"`
	FeatureID     string `json:"feature_id,omitempty"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// ContextEntry is a stored context entry for features/tasks.
type ContextEntry struct {
	ID          int    `json:"id"`
	ProjectID   string `json:"project_id"`
	FeatureID   string `json:"feature_id,omitempty"`
	ContextType string `json:"context_type"`
	Title       string `json:"title"`
	ContentMD   string `json:"content_md"`
	Author      string `json:"author"`
	Tags        string `json:"tags,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// Worktree represents a git/jj worktree linked to the project.
type Worktree struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Path           string `json:"path"`
	Branch         string `json:"branch,omitempty"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// QueueEntry is an enriched work item for queue display, including feature context.
type QueueEntry struct {
	WorkItemID    int    `json:"work_item_id"`
	FeatureID     string `json:"feature_id"`
	FeatureName   string `json:"feature_name"`
	WorkType      string `json:"work_type"`
	Priority      int    `json:"priority"`
	CycleType     string `json:"cycle_type"`
	AssignedAgent string `json:"assigned_agent"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
}

// QueueStats holds aggregate statistics about the work queue.
type QueueStats struct {
	TotalPending      int `json:"total_pending"`
	TotalClaimed      int `json:"total_claimed"`
	TotalCompletedDay int `json:"total_completed_today"`
}

// QueueResponse is the full response for GET /api/queue.
type QueueResponse struct {
	Queue []QueueEntry `json:"queue"`
	Stats QueueStats   `json:"stats"`
}

// Decision represents an Architecture Decision Record (ADR).
type Decision struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Status       string `json:"status"`       // proposed, accepted, rejected, superseded, deprecated
	Context      string `json:"context"`      // Why is this decision needed?
	Decision     string `json:"decision"`     // What was decided?
	Consequences string `json:"consequences"` // What are the consequences?
	FeatureID    string `json:"feature_id,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// SearchResult represents a ranked FTS5 search result with a snippet.
type SearchResult struct {
	EntityType string  `json:"entity_type"`
	EntityID   string  `json:"entity_id"`
	Title      string  `json:"title"`
	Snippet    string  `json:"snippet"`
	Rank       float64 `json:"rank"`
}

// HeatmapDay holds aggregated daily activity counts for heatmap visualization.
type HeatmapDay struct {
	Date   string         `json:"date"`
	Count  int            `json:"count"`
	Events map[string]int `json:"events"`
}

// HeatmapResponse wraps heatmap data returned by the API.
type HeatmapResponse struct {
	Days []HeatmapDay `json:"days"`
}

// Predefined cycle types
var CycleTypes = []CycleType{
	{Name: "ui-refinement", Description: "UI Refinement", Steps: []string{"design", "ux-review", "develop", "manual-qa", "judge"}},
	{Name: "feature-implementation", Description: "Feature Implementation", Steps: []string{"research", "develop", "agent-qa", "judge", "human-qa"}},
	{Name: "roadmap-planning", Description: "Roadmap Planning", Steps: []string{"research", "plan", "create-roadmap", "prioritize", "human-review"}},
	{Name: "bug-triage", Description: "Bug Triage", Steps: []string{"report", "reproduce", "root-cause", "fix", "verify"}},
	{Name: "documentation", Description: "Documentation", Steps: []string{"research", "draft", "review", "edit", "publish"}},
	{Name: "architecture-review", Description: "Architecture Review", Steps: []string{"analyze", "propose", "discuss", "decide", "implement"}},
	{Name: "release", Description: "Release", Steps: []string{"freeze", "qa", "fix", "staging", "verify", "ship"}},
	{Name: "onboarding-dx", Description: "Onboarding/DX", Steps: []string{"try", "friction-log", "improve", "verify", "document"}},
	{Name: "spec-iteration", Description: "Spec Iteration", Steps: []string{"research", "draft-spec", "review", "judge", "human-review"}},
}

// TagCount represents a tag with its usage count.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// WorkItemTime represents a work item with its computed duration.
type WorkItemTime struct {
	ID          int     `json:"id"`
	FeatureID   string  `json:"feature_id"`
	WorkType    string  `json:"work_type"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	CompletedAt string  `json:"completed_at"`
	DurationSec float64 `json:"duration_seconds"`
	Duration    string  `json:"duration"`
}

// FeatureTimeReport is the response for time show <feature-id>.
type FeatureTimeReport struct {
	FeatureID     string         `json:"feature_id"`
	FeatureName   string         `json:"feature_name"`
	Items         []WorkItemTime `json:"items"`
	TotalSec      float64        `json:"total_seconds"`
	TotalDuration string         `json:"total_duration"`
}

// WorkTypeAvg represents average time for a work type.
type WorkTypeAvg struct {
	WorkType    string  `json:"work_type"`
	Count       int     `json:"count"`
	TotalSec    float64 `json:"total_seconds"`
	AvgSec      float64 `json:"avg_seconds"`
	AvgDuration string  `json:"avg_duration"`
}

// FeatureTimeSummary is a feature entry in the top-N list.
type FeatureTimeSummary struct {
	FeatureID string  `json:"feature_id"`
	Name      string  `json:"name"`
	TotalSec  float64 `json:"total_seconds"`
	Duration  string  `json:"duration"`
}

// StatusTime represents time spent on features by status.
type StatusTime struct {
	Status   string  `json:"status"`
	TotalSec float64 `json:"total_seconds"`
	Duration string  `json:"duration"`
}

// ProjectTimeSummary is the response for time summary.
type ProjectTimeSummary struct {
	TotalSec      float64              `json:"total_seconds"`
	TotalDuration string               `json:"total_duration"`
	ByWorkType    []WorkTypeAvg        `json:"by_work_type"`
	TopFeatures   []FeatureTimeSummary `json:"top_features"`
	ByStatus      []StatusTime         `json:"by_status"`
}
