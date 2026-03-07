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

	// Computed fields
	DependsOn     []string `json:"depends_on,omitempty"`
	MilestoneName string   `json:"milestone_name,omitempty"`
}

type WorkItem struct {
	ID          int    `json:"id"`
	FeatureID   string `json:"feature_id"`
	WorkType    string `json:"work_type"`
	Status      string `json:"status"` // pending, active, done, failed
	AgentPrompt string `json:"agent_prompt,omitempty"`
	Result      string `json:"result,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	CreatedAt   string `json:"created_at"`
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
}
