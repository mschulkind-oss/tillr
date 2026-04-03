package models

import "encoding/json"

type AgentCapability struct {
	AgentID    string `json:"agent_id"`
	Capability string `json:"capability"`
}

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

// CycleStep defines a single step within a cycle type.
type CycleStep struct {
	Name         string `json:"name"`
	Human        bool   `json:"human,omitempty"`        // true = human-owned step (no agent work item)
	Instructions string `json:"instructions,omitempty"` // markdown instructions for human steps
}

// CycleType defines a predefined iteration cycle.
type CycleType struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Steps       []CycleStep `json:"steps"`
}

// StepNames returns the step names as a string slice (convenience helper).
func (ct CycleType) StepNames() []string {
	names := make([]string, len(ct.Steps))
	for i, s := range ct.Steps {
		names[i] = s.Name
	}
	return names
}

// IsHumanStep returns true if the step at the given index is human-owned.
func (ct CycleType) IsHumanStep(idx int) bool {
	if idx < 0 || idx >= len(ct.Steps) {
		return false
	}
	return ct.Steps[idx].Human
}

// CycleInstance is a running cycle attached to any entity (feature, workstream, etc.).
type CycleInstance struct {
	ID          int    `json:"id"`
	EntityType  string `json:"entity_type"` // "feature", "workstream", "roadmap_item", etc.
	EntityID    string `json:"entity_id"`
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
	Votes        map[string]int      `json:"votes,omitempty"`
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

// DiscussionVote is a reaction on a discussion.
type DiscussionVote struct {
	DiscussionID int    `json:"discussion_id"`
	Voter        string `json:"voter"`
	Reaction     string `json:"reaction"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// VoteSummary holds the count per reaction for a discussion.
type VoteSummary struct {
	DiscussionID int            `json:"discussion_id"`
	Counts       map[string]int `json:"counts"`
	Total        int            `json:"total"`
}

// StatusOverview is the project dashboard summary.
type StatusOverview struct {
	Project         *Project       `json:"project"`
	FeatureCounts   map[string]int `json:"feature_counts"`
	MilestoneCount  int            `json:"milestone_count"`
	ActiveCycles    int            `json:"active_cycles"`
	OpenDiscussions int            `json:"open_discussions"`
	RecentEvents    []Event        `json:"recent_events"`
	ActiveWork      []WorkItem     `json:"active_work"`
}

// WorkContext is the enriched response from `tillr next --json`.
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
	Notifications map[string]any `json:"notifications,omitempty"`
}

// CycleDetail is the enriched response for GET /api/cycles/{id}.
type CycleDetail struct {
	Cycle  CycleInstance `json:"cycle"`
	Scores []CycleScore  `json:"scores"`
	Steps  []CycleStep   `json:"steps"`
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

// AgentHeartbeatInfo is an enriched agent session for the heartbeat dashboard.
type AgentHeartbeatInfo struct {
	Session         AgentSession `json:"session"`
	HeartbeatStatus string       `json:"heartbeat_status"` // active, stale, failed
	SessionDuration int64        `json:"session_duration_secs"`
	CurrentWorkItem *WorkItem    `json:"current_work_item,omitempty"`
	FeatureName     string       `json:"feature_name,omitempty"`
	CompletedCount  int          `json:"completed_count"`
	FailedCount     int          `json:"failed_count"`
}

// AgentStatusDashboard is the full agent heartbeat dashboard response.
type AgentStatusDashboard struct {
	Agents         []AgentHeartbeatInfo `json:"agents"`
	TotalSessions  int                  `json:"total_sessions"`
	ActiveCount    int                  `json:"active_count"`
	StaleCount     int                  `json:"stale_count"`
	FailedCount    int                  `json:"failed_count"`
	CompletedCount int                  `json:"completed_count"`
	TotalWorkDone  int                  `json:"total_work_done"`
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
	SourcePage    string `json:"source_page,omitempty"`
	Context       string `json:"context,omitempty"`
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
	SupersededBy string `json:"superseded_by,omitempty"`
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

// GroupedSearchResults holds search results organized by entity type.
type GroupedSearchResults struct {
	Query        string                    `json:"query"`
	Total        int                       `json:"total"`
	Groups       map[string][]SearchResult `json:"groups"`
	OrderedTypes []string                  `json:"ordered_types"`
}

// FeatureSearchResult is a compact search result for feature find.
type FeatureSearchResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Snippet string `json:"snippet"`
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

// ActivityDayCount holds a simple date + count pair for the activity heatmap.
type ActivityDayCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// step is a shorthand constructor for agent-owned steps.
func step(name string) CycleStep { return CycleStep{Name: name} }

// humanStep is a shorthand constructor for human-owned steps.
func humanStep(name, instructions string) CycleStep {
	return CycleStep{Name: name, Human: true, Instructions: instructions}
}

// Predefined cycle types
var CycleTypes = []CycleType{
	{Name: "ui-refinement", Description: "UI Refinement", Steps: []CycleStep{step("design"), step("ux-review"), step("develop"), step("manual-qa"), step("judge")}},
	{Name: "feature-implementation", Description: "Feature Implementation", Steps: []CycleStep{
		step("research"), step("develop"), step("agent-qa"), step("judge"),
		humanStep("human-qa", `## Manual QA Checklist

1. **Verify the feature works end-to-end** — follow the feature spec and confirm each acceptance criterion
2. **Check edge cases** — empty states, error states, boundary values
3. **Test the UI** — responsive layout, keyboard navigation, loading states
4. **Check for regressions** — ensure existing functionality still works
5. **Review the code changes** — look for anything suspicious or incomplete

> Approve if all criteria pass. Reject with notes describing what failed.`),
	}},
	{Name: "roadmap-planning", Description: "Roadmap Planning", Steps: []CycleStep{
		step("research"), step("plan"), step("create-roadmap"), step("prioritize"),
		humanStep("human-review", `## Roadmap Review

1. **Check prioritization** — are the highest-impact items at the top?
2. **Validate scope** — are items appropriately sized? Split anything too large.
3. **Confirm dependencies** — are blocking relationships captured correctly?
4. **Sanity-check timelines** — do estimates feel realistic given current velocity?

> Approve to finalize the roadmap. Reject with notes on what needs adjustment.`),
	}},
	{Name: "bug-triage", Description: "Bug Triage", Steps: []CycleStep{step("report"), step("reproduce"), step("root-cause"), step("fix"), step("verify")}},
	{Name: "documentation", Description: "Documentation", Steps: []CycleStep{step("research"), step("draft"), step("review"), step("edit"), step("publish")}},
	{Name: "architecture-review", Description: "Architecture Review", Steps: []CycleStep{step("analyze"), step("propose"), step("discuss"), step("decide"), step("implement")}},
	{Name: "release", Description: "Release", Steps: []CycleStep{step("freeze"), step("qa"), step("fix"), step("staging"), step("verify"), step("ship")}},
	{Name: "onboarding-dx", Description: "Onboarding/DX", Steps: []CycleStep{step("try"), step("friction-log"), step("improve"), step("verify"), step("document")}},
	{Name: "spec-iteration", Description: "Spec Iteration", Steps: []CycleStep{
		step("research"), step("draft-spec"), step("review"), step("judge"),
		humanStep("human-review", `## Spec Review

1. **Read the spec** — does it clearly describe the problem and proposed solution?
2. **Check completeness** — are edge cases, error handling, and migration covered?
3. **Validate trade-offs** — are the chosen approaches well-justified?
4. **Assess feasibility** — can this be built with current architecture and constraints?

> Approve to move to implementation. Reject with specific feedback on what to revise.`),
	}},
	{Name: "collaborative-design", Description: "Collaborative Design (human-in-the-loop)", Steps: []CycleStep{
		step("intake"), step("research"),
		humanStep("human-review", `## Design Review

1. **Review research findings** — are the key insights captured?
2. **Validate problem framing** — does the research address the right questions?
3. **Provide direction** — add notes on what the design should prioritize

> Approve to proceed to design phase. Reject to request more research.`),
		step("design"),
		humanStep("human-approve", `## Design Approval

1. **Review the design** — does it solve the problem identified in research?
2. **Check visual consistency** — does it match existing UI patterns and style?
3. **Validate interactions** — are user flows intuitive and complete?
4. **Confirm scope** — is the design implementable within current constraints?

> Approve to finalize. Reject with specific design feedback.`),
	}},
}

// AgentWorkTypeStat holds average completion time for a work type.
type AgentWorkTypeStat struct {
	WorkType    string  `json:"work_type"`
	Count       int     `json:"count"`
	AvgSec      float64 `json:"avg_seconds"`
	AvgDuration string  `json:"avg_duration"`
}

// AgentSuccessRate holds success/failure counts for a single agent.
type AgentSuccessRate struct {
	AgentName   string  `json:"agent_name"`
	Completed   int     `json:"completed"`
	Failed      int     `json:"failed"`
	Total       int     `json:"total"`
	SuccessRate float64 `json:"success_rate"`
}

// AgentActiveTask describes an agent's current in-progress work.
type AgentActiveTask struct {
	AgentName       string `json:"agent_name"`
	SessionID       string `json:"session_id"`
	TaskDescription string `json:"task_description,omitempty"`
	CurrentPhase    string `json:"current_phase,omitempty"`
	ProgressPct     int    `json:"progress_pct"`
	StartedAt       string `json:"started_at"`
}

// AgentThroughput holds items-per-hour throughput over a time window.
type AgentThroughput struct {
	Period       string  `json:"period"`
	ItemsTotal   int     `json:"items_total"`
	HoursSpan    float64 `json:"hours_span"`
	ItemsPerHour float64 `json:"items_per_hour"`
}

// AgentStats is the full response for `tillr agent stats`.
type AgentStats struct {
	TotalCompleted int                 `json:"total_completed"`
	TotalFailed    int                 `json:"total_failed"`
	AvgByWorkType  []AgentWorkTypeStat `json:"avg_by_work_type"`
	SuccessRates   []AgentSuccessRate  `json:"success_rates"`
	ActiveAgents   []AgentActiveTask   `json:"active_agents"`
	Throughput     []AgentThroughput   `json:"throughput"`
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

// Webhook represents a registered webhook endpoint.
type Webhook struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Secret    string `json:"secret,omitempty"`
	Events    string `json:"events"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

// WebhookDelivery represents a single webhook delivery payload.
type WebhookDelivery struct {
	ID        string         `json:"id"`
	Event     string         `json:"event"`
	Timestamp string         `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

// Sprint represents a time-boxed planning period.
type Sprint struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Goal      string `json:"goal,omitempty"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Status    string `json:"status"` // active, closed
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`

	// Computed fields (populated by queries)
	TotalFeatures    int `json:"total_features,omitempty"`
	DoneFeatures     int `json:"done_features,omitempty"`
	InProgFeatures   int `json:"in_progress_features,omitempty"`
	NotStartFeatures int `json:"not_started_features,omitempty"`
}

// SprintFeature links a feature to a sprint.
type SprintFeature struct {
	SprintID  string `json:"sprint_id"`
	FeatureID string `json:"feature_id"`
}

// FeaturePR links a pull request to a feature.
type FeaturePR struct {
	FeatureID string `json:"feature_id"`
	PRURL     string `json:"pr_url"`
	PRNumber  int    `json:"pr_number,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Status    string `json:"status"` // open, closed, merged
	CreatedAt string `json:"created_at"`
}

// CycleTemplate is a user-defined or built-in iteration cycle template.
type CycleTemplate struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Steps       []CycleStep `json:"steps"`
	IsBuiltin   bool        `json:"is_builtin"`
	CreatedAt   string      `json:"created_at,omitempty"`
}

// ParseStepsJSON unmarshals a JSON steps column, supporting both the legacy
// format (["step1","step2"]) and the new format ([{"name":"step1","human":false}]).
func ParseStepsJSON(raw string) ([]CycleStep, error) {
	// Try new format first
	var steps []CycleStep
	if err := json.Unmarshal([]byte(raw), &steps); err == nil && len(steps) > 0 && steps[0].Name != "" {
		return steps, nil
	}
	// Fall back to legacy string array
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, err
	}
	steps = make([]CycleStep, len(names))
	for i, n := range names {
		steps[i] = CycleStep{Name: n}
	}
	return steps, nil
}

// CommandMetric records a single CLI command execution with timing data.
// Workstream is a human-tracked thread of work.
type Workstream struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	ParentID    string `json:"parent_id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Tags        string `json:"tags"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// WorkstreamNote is a timestamped entry on a workstream.
type WorkstreamNote struct {
	ID           int    `json:"id"`
	WorkstreamID string `json:"workstream_id"`
	Content      string `json:"content"`
	NoteType     string `json:"note_type"`
	Source       string `json:"source,omitempty"`
	Resolved     int    `json:"resolved"`
	CreatedAt    string `json:"created_at"`
}

// WorkstreamLink connects a workstream to a feature, doc, URL, or discussion.
type WorkstreamLink struct {
	ID           int    `json:"id"`
	WorkstreamID string `json:"workstream_id"`
	LinkType     string `json:"link_type"`
	TargetID     string `json:"target_id,omitempty"`
	TargetURL    string `json:"target_url,omitempty"`
	Label        string `json:"label,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// WorkstreamDetail is the enriched response for GET /api/workstreams/{id}.
type WorkstreamDetail struct {
	Workstream Workstream       `json:"workstream"`
	Notes      []WorkstreamNote `json:"notes"`
	Links      []WorkstreamLink `json:"links"`
	Children   []Workstream     `json:"children"`
}

// WorkstreamFeature is a feature linked to a workstream with the relationship type.
type WorkstreamFeature struct {
	Feature      Feature `json:"feature"`
	Relationship string  `json:"relationship"` // "owned" or "dependency"
}

type CommandMetric struct {
	ID         int     `json:"id"`
	Command    string  `json:"command"`
	DurationMs float64 `json:"duration_ms"`
	Success    bool    `json:"success"`
	DBQueries  int     `json:"db_queries"`
	CreatedAt  string  `json:"created_at"`
}

// PerfSummary holds aggregated performance metrics.
type PerfSummary struct {
	TotalCommands int                `json:"total_commands"`
	AvgDurationMs float64            `json:"avg_duration_ms"`
	P95DurationMs float64            `json:"p95_duration_ms"`
	SuccessRate   float64            `json:"success_rate"`
	ByCommand     []CommandPerfStats `json:"by_command"`
	RecentSlow    []CommandMetric    `json:"recent_slow,omitempty"`
}

// CommandPerfStats holds per-command aggregated stats.
type CommandPerfStats struct {
	Command       string  `json:"command"`
	Count         int     `json:"count"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	MaxDurationMs float64 `json:"max_duration_ms"`
	SuccessRate   float64 `json:"success_rate"`
}

// UndoEntry represents a single undoable operation in the undo log.
type UndoEntry struct {
	ID         int64  `json:"id"`
	ProjectID  string `json:"project_id"`
	Operation  string `json:"operation"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	BeforeData string `json:"before_data"`
	AfterData  string `json:"after_data"`
	Undone     bool   `json:"undone"`
	CreatedAt  string `json:"created_at"`
}

// DashboardConfig represents a saved dashboard layout configuration.
type DashboardConfig struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Layout    json.RawMessage `json:"layout"`
	IsDefault bool            `json:"is_default"`
	CreatedAt string          `json:"created_at"`
}

// DashboardWidget describes a single widget within a dashboard layout.
type DashboardWidget struct {
	Type   string         `json:"type"` // "feature-summary", "milestone-progress", "roadmap-overview", etc.
	Title  string         `json:"title"`
	Size   string         `json:"size"` // "small", "medium", "large"
	Config map[string]any `json:"config,omitempty"`
}

// Notification represents an in-app notification.
type Notification struct {
	ID         int    `json:"id"`
	ProjectID  string `json:"project_id"`
	Recipient  string `json:"recipient"`
	Type       string `json:"type"` // mention, qa_needed, approved, rejected, blocked, assigned
	Message    string `json:"message"`
	EntityType string `json:"entity_type,omitempty"`
	EntityID   string `json:"entity_id,omitempty"`
	Read       bool   `json:"read"`
	CreatedAt  string `json:"created_at"`
}

// DiscussionPoll represents a poll within a discussion.
type DiscussionPoll struct {
	ID           int                    `json:"id"`
	DiscussionID int                    `json:"discussion_id"`
	Question     string                 `json:"question"`
	PollType     string                 `json:"poll_type"` // single, multiple
	Status       string                 `json:"status"`    // open, closed
	CreatedBy    string                 `json:"created_by"`
	CreatedAt    string                 `json:"created_at"`
	Options      []DiscussionPollOption `json:"options,omitempty"`
}

// DiscussionPollOption is a single option in a poll.
type DiscussionPollOption struct {
	ID        int    `json:"id"`
	PollID    int    `json:"poll_id"`
	Label     string `json:"label"`
	SortOrder int    `json:"sort_order"`
	Votes     int    `json:"votes,omitempty"` // computed
}

// DiscussionPollVote is a vote on a poll option.
type DiscussionPollVote struct {
	PollID    int    `json:"poll_id"`
	OptionID  int    `json:"option_id"`
	Voter     string `json:"voter"`
	CreatedAt string `json:"created_at,omitempty"`
}

// DiscussionTemplate is a reusable template for discussion creation.
type DiscussionTemplate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	IsBuiltin   bool   `json:"is_builtin"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// APIToken represents a stored API token for authentication.
type APIToken struct {
	ID        int      `json:"id"`
	ProjectID string   `json:"project_id"`
	Name      string   `json:"name"`
	TokenHash string   `json:"-"` // never expose the hash
	Scopes    []string `json:"scopes"`
	CreatedAt string   `json:"created_at"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	RevokedAt string   `json:"revoked_at,omitempty"`
}
