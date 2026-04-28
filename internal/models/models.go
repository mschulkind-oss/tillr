// Package models holds the post-reset minimal data types.
//
// Pre-reset models lived in this same file with ~25 entity types
// (features, work_items, events, cycles, decisions, discussions, ideas,
// milestones, sprints, agent_sessions, etc.). After the reset, the
// surface is just three: Project, Feature, Comment.
//
// Future entities will be added per the consulting-firm roadmap
// (see docs/consulting-firm/roadmap.md).
package models

import "time"

// Project is the single root record for a tillr-managed project.
// One project per tillr.db.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Feature is a unit of work tracked by tillr.
//
// Status flows minimally: draft → queued → claimed → done. Cycle
// states will return as the roadmap demands.
//
// TargetPersona names the persona expected to handle this feature
// (e.g., "implementer", "researcher", "reviewer"). Empty string means
// untargeted; the conductor or human assigns later.
type Feature struct {
	ID            int64     `json:"id"`
	ProjectID     string    `json:"project_id"`
	Title         string    `json:"title"`
	Description   string    `json:"description,omitempty"`
	Status        string    `json:"status"`
	TargetPersona string    `json:"target_persona,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// OrchestratorRun records a single dispatch of a persona via
// `claude -p` against a feature. The orchestrator process inserts a
// row when it spawns the worker, updates it on completion, and uses
// it for metrics + retros + the inspector UI.
type OrchestratorRun struct {
	ID           int64      `json:"id"`
	FeatureID    int64      `json:"feature_id"`
	Persona      string     `json:"persona"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	DurationMS   *int64     `json:"duration_ms,omitempty"`
	ExitCode     *int       `json:"exit_code,omitempty"`
	CostUSD      *float64   `json:"cost_usd,omitempty"`
	InputTokens  *int64     `json:"input_tokens,omitempty"`
	OutputTokens *int64     `json:"output_tokens,omitempty"`
	SessionID    string     `json:"session_id,omitempty"`
	Model        string     `json:"model,omitempty"`
	Result       string     `json:"result"` // running | completed | blocked | needs_review | error
	Error        string     `json:"error,omitempty"`
	Summary      string     `json:"summary,omitempty"`
}

// Comment is the conversation substrate from
// docs/consulting-firm/implementation-layers.md Layer 1.
//
// EntityType is "feature" today; will extend to "pr", "philosophy",
// "style_rule", etc. as later layers ship.
//
// AuthorType is "human" or "agent". For agents, AuthorRole names the
// role within the cycle (implementer / reviewer / style-enforcer / ...).
type Comment struct {
	ID         int64     `json:"id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	AuthorType string    `json:"author_type"`
	AuthorRole string    `json:"author_role,omitempty"`
	Body       string    `json:"body"`
	Metadata   string    `json:"metadata,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
