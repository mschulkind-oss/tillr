package engine

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
)

// InitProject creates a new project with its config and DB entry.
func InitProject(database *sql.DB, name string) (*models.Project, error) {
	id := slug(name)
	p := &models.Project{ID: id, Name: name}
	if err := db.CreateProject(database, p); err != nil {
		return nil, fmt.Errorf("creating project: %w", err)
	}
	if err := db.InsertEvent(database, &models.Event{
		ProjectID: id,
		EventType: "project.created",
		Data:      fmt.Sprintf(`{"name":%q}`, name),
	}); err != nil {
		return nil, fmt.Errorf("logging event: %w", err)
	}
	return p, nil
}

// AddFeature creates a feature and logs an event.
func AddFeature(database *sql.DB, projectID, name, description, spec, milestoneID string, priority int, dependsOn []string, roadmapItemID string) (*models.Feature, error) {
	id := slug(name)
	f := &models.Feature{
		ID:            id,
		ProjectID:     projectID,
		MilestoneID:   milestoneID,
		Name:          name,
		Description:   description,
		Spec:          spec,
		Priority:      priority,
		RoadmapItemID: roadmapItemID,
	}
	if err := db.CreateFeature(database, f); err != nil {
		return nil, fmt.Errorf("creating feature: %w", err)
	}
	for _, dep := range dependsOn {
		if err := db.AddFeatureDep(database, id, dep); err != nil {
			return nil, fmt.Errorf("adding dependency %s: %w", dep, err)
		}
	}
	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: id,
		EventType: "feature.created",
		Data:      fmt.Sprintf(`{"name":%q,"priority":%d,"description":%q,"has_spec":%t,"roadmap_item_id":%q}`, name, priority, description, spec != "", roadmapItemID),
	})
	return f, nil
}

// ValidTransitions defines the allowed state machine transitions for features.
// A feature MUST go through human-qa before reaching done.
var ValidTransitions = map[string][]string{
	"draft":        {"planning", "implementing", "blocked"},
	"planning":     {"implementing", "blocked"},
	"implementing": {"agent-qa", "human-qa", "blocked"},
	"agent-qa":     {"human-qa", "implementing", "blocked"},
	"human-qa":     {"done", "implementing", "blocked"},
	"blocked":      {"draft", "planning", "implementing"},
	"done":         {"implementing"},
}

// IsValidTransition checks whether transitioning from one status to another is allowed.
func IsValidTransition(from, to string) bool {
	allowed, ok := ValidTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// TransitionFeature moves a feature to a new status, enforcing QA gate rules.
func TransitionFeature(database *sql.DB, projectID, featureID, newStatus string) error {
	f, err := db.GetFeature(database, featureID)
	if err != nil {
		return fmt.Errorf("getting feature: %w", err)
	}
	if !IsValidTransition(f.Status, newStatus) {
		return fmt.Errorf("invalid transition from %q to %q: features must go through human-qa before done", f.Status, newStatus)
	}
	if err := db.UpdateFeature(database, featureID, map[string]any{"status": newStatus}); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: featureID,
		EventType: "feature.status_changed",
		Data:      fmt.Sprintf(`{"from":%q,"to":%q}`, f.Status, newStatus),
	})
	return nil
}

// GetNextWorkItem finds the next pending work item, activates it, and returns it.
func GetNextWorkItem(database *sql.DB) (*models.WorkItem, error) {
	// Check for already active items first
	active, err := db.GetActiveWorkItem(database)
	if err == nil {
		return active, nil
	}
	// Get next pending
	w, err := db.GetNextPendingWorkItem(database)
	if err != nil {
		return nil, fmt.Errorf("no pending work items")
	}
	if err := db.UpdateWorkItemStatus(database, w.ID, "active", ""); err != nil {
		return nil, fmt.Errorf("activating work item: %w", err)
	}
	w.Status = "active"
	return w, nil
}

// GetWorkContext builds the full enriched context for a work item.
// This is the single source of truth for what an agent needs — no OOB info required.
func GetWorkContext(database *sql.DB, w *models.WorkItem) (*models.WorkContext, error) {
	ctx := &models.WorkContext{WorkItem: w}

	// Load feature with full spec
	if f, err := db.GetFeature(database, w.FeatureID); err == nil {
		ctx.Feature = f

		// Load linked roadmap item
		if f.RoadmapItemID != "" {
			if ri, err := db.GetRoadmapItem(database, f.RoadmapItemID); err == nil {
				ctx.RoadmapItem = ri
			}
		}
	}

	// Load active cycle
	if c, err := db.GetActiveCycle(database, w.FeatureID); err == nil {
		ctx.Cycle = c
		for i := range models.CycleTypes {
			if models.CycleTypes[i].Name == c.CycleType {
				ctx.CycleType = &models.CycleTypes[i]
				break
			}
		}
		// Load cycle scores
		if scores, err := db.ListCycleScores(database, c.ID); err == nil {
			ctx.CycleScores = scores
		}
	}

	// Load prior work results for this feature
	if prior, err := db.ListWorkItemsForFeature(database, w.FeatureID); err == nil {
		ctx.PriorResults = prior
	}

	// Build agent guidance — a human-readable summary of what to do
	ctx.AgentGuidance = buildAgentGuidance(ctx)

	return ctx, nil
}

func buildAgentGuidance(ctx *models.WorkContext) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("You are working on feature %q", ctx.Feature.Name))
	if ctx.Feature.Description != "" {
		b.WriteString(fmt.Sprintf(": %s", ctx.Feature.Description))
	}
	b.WriteString(fmt.Sprintf("\n\nCurrent task: %s (work type: %s)", ctx.WorkItem.AgentPrompt, ctx.WorkItem.WorkType))

	if ctx.Feature.Spec != "" {
		b.WriteString(fmt.Sprintf("\n\n## Feature Spec\n%s", ctx.Feature.Spec))
	}

	if ctx.Cycle != nil && ctx.CycleType != nil {
		b.WriteString(fmt.Sprintf("\n\n## Cycle Context\nCycle type: %s (step %d/%d: %s)",
			ctx.CycleType.Description, ctx.Cycle.CurrentStep+1, len(ctx.CycleType.Steps),
			ctx.CycleType.Steps[ctx.Cycle.CurrentStep]))
		b.WriteString(fmt.Sprintf("\nAll steps: %s", strings.Join(ctx.CycleType.Steps, " → ")))
	}

	if len(ctx.PriorResults) > 0 {
		b.WriteString("\n\n## Prior Step Results")
		for _, pr := range ctx.PriorResults {
			if pr.Result != "" {
				b.WriteString(fmt.Sprintf("\n- [%s] %s", pr.WorkType, pr.Result))
			}
		}
	}

	if ctx.RoadmapItem != nil {
		b.WriteString(fmt.Sprintf("\n\n## Roadmap Context\nTitle: %s\nPriority: %s\nDescription: %s",
			ctx.RoadmapItem.Title, ctx.RoadmapItem.Priority, ctx.RoadmapItem.Description))
	}

	return b.String()
}

// CompleteWorkItem marks the active work item as done.
func CompleteWorkItem(database *sql.DB, result string) error {
	w, err := db.GetActiveWorkItem(database)
	if err != nil {
		return fmt.Errorf("no active work item")
	}
	if err := db.UpdateWorkItemStatus(database, w.ID, "done", result); err != nil {
		return fmt.Errorf("completing work item: %w", err)
	}
	p, _ := db.GetProject(database)
	if p != nil {
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: w.FeatureID,
			EventType: "work.completed",
			Data:      fmt.Sprintf(`{"work_type":%q,"result":%q}`, w.WorkType, result),
		})
	}
	// Auto-transition feature to human-qa when implementation/agent-qa work completes
	if w.FeatureID != "" && p != nil {
		f, fErr := db.GetFeature(database, w.FeatureID)
		if fErr == nil && (f.Status == "implementing" || f.Status == "agent-qa") {
			_ = TransitionFeature(database, p.ID, w.FeatureID, "human-qa")
		}
	}
	return nil
}

// FailWorkItem marks the active work item as failed.
func FailWorkItem(database *sql.DB, reason string) error {
	w, err := db.GetActiveWorkItem(database)
	if err != nil {
		return fmt.Errorf("no active work item")
	}
	if err := db.UpdateWorkItemStatus(database, w.ID, "failed", reason); err != nil {
		return fmt.Errorf("failing work item: %w", err)
	}
	return nil
}

// StartCycle starts a new iteration cycle for a feature.
func StartCycle(database *sql.DB, projectID, featureID, cycleType string) (*models.CycleInstance, error) {
	// Validate cycle type
	var ct *models.CycleType
	for i := range models.CycleTypes {
		if models.CycleTypes[i].Name == cycleType {
			ct = &models.CycleTypes[i]
			break
		}
	}
	if ct == nil {
		return nil, fmt.Errorf("unknown cycle type: %s", cycleType)
	}

	// Check no active cycle
	if existing, err := db.GetActiveCycle(database, featureID); err == nil {
		return nil, fmt.Errorf("feature already has active cycle: %s (step %d)", existing.CycleType, existing.CurrentStep)
	}

	c := &models.CycleInstance{
		FeatureID: featureID,
		CycleType: cycleType,
		Status:    "active",
		Iteration: 1,
	}
	if err := db.CreateCycleInstance(database, c); err != nil {
		return nil, fmt.Errorf("creating cycle: %w", err)
	}
	c.StepName = ct.Steps[0]

	// Build enriched prompt with feature context
	prompt := buildWorkItemPrompt(database, featureID, cycleType, ct.Steps[0])

	// Auto-create work item for the first cycle step
	_ = db.CreateWorkItem(database, &models.WorkItem{
		FeatureID:   featureID,
		WorkType:    ct.Steps[0],
		AgentPrompt: prompt,
	})

	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: featureID,
		EventType: "cycle.started",
		Data:      fmt.Sprintf(`{"cycle_type":%q,"step":%q}`, cycleType, ct.Steps[0]),
	})

	return c, nil
}

// ScoreCycleStep records a score and advances the cycle.
func ScoreCycleStep(database *sql.DB, projectID, featureID string, score float64, notes string) error {
	c, err := db.GetActiveCycle(database, featureID)
	if err != nil {
		return fmt.Errorf("no active cycle for feature %s", featureID)
	}

	var ct *models.CycleType
	for i := range models.CycleTypes {
		if models.CycleTypes[i].Name == c.CycleType {
			ct = &models.CycleTypes[i]
			break
		}
	}
	if ct == nil {
		return fmt.Errorf("unknown cycle type: %s", c.CycleType)
	}

	if err := db.CreateCycleScore(database, &models.CycleScore{
		CycleID:   c.ID,
		Step:      c.CurrentStep,
		Iteration: c.Iteration,
		Score:     score,
		Notes:     notes,
	}); err != nil {
		return fmt.Errorf("recording score: %w", err)
	}

	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: featureID,
		EventType: "cycle.scored",
		Data:      fmt.Sprintf(`{"step":%q,"score":%.1f}`, ct.Steps[c.CurrentStep], score),
	})

	// Advance to next step or complete
	nextStep := c.CurrentStep + 1
	if nextStep >= len(ct.Steps) {
		return db.UpdateCycleInstance(database, c.ID, c.CurrentStep, c.Iteration, "completed")
	}
	// Auto-create work item for the next cycle step
	prompt := buildWorkItemPrompt(database, featureID, c.CycleType, ct.Steps[nextStep])
	_ = db.CreateWorkItem(database, &models.WorkItem{
		FeatureID:   featureID,
		WorkType:    ct.Steps[nextStep],
		AgentPrompt: prompt,
	})
	return db.UpdateCycleInstance(database, c.ID, nextStep, c.Iteration, "active")
}

// ApproveFeatureQA approves a feature and transitions it to done.
// The feature must be in human-qa status.
func ApproveFeatureQA(database *sql.DB, projectID, featureID, notes string) error {
	f, err := db.GetFeature(database, featureID)
	if err != nil {
		return fmt.Errorf("getting feature: %w", err)
	}
	if f.Status != "human-qa" {
		return fmt.Errorf("cannot approve feature in %q status: must be in human-qa", f.Status)
	}
	if err := db.CreateQAResult(database, &models.QAResult{
		FeatureID: featureID,
		QAType:    "human",
		Passed:    true,
		Notes:     notes,
	}); err != nil {
		return fmt.Errorf("recording QA result: %w", err)
	}
	return TransitionFeature(database, projectID, featureID, "done")
}

// RejectFeatureQA rejects a feature and sends it back to implementing.
// The feature must be in human-qa status.
func RejectFeatureQA(database *sql.DB, projectID, featureID, notes string) error {
	f, err := db.GetFeature(database, featureID)
	if err != nil {
		return fmt.Errorf("getting feature: %w", err)
	}
	if f.Status != "human-qa" {
		return fmt.Errorf("cannot reject feature in %q status: must be in human-qa", f.Status)
	}
	if err := db.CreateQAResult(database, &models.QAResult{
		FeatureID: featureID,
		QAType:    "human",
		Passed:    false,
		Notes:     notes,
	}); err != nil {
		return fmt.Errorf("recording QA result: %w", err)
	}
	return TransitionFeature(database, projectID, featureID, "implementing")
}

// GetStatusOverview returns the project dashboard data.
func GetStatusOverview(database *sql.DB) (*models.StatusOverview, error) {
	p, err := db.GetProject(database)
	if err != nil {
		return nil, fmt.Errorf("getting project: %w", err)
	}
	counts, _ := db.FeatureCounts(database, p.ID)
	events, _ := db.ListEvents(database, p.ID, "", "", "", 10)
	active, _ := db.GetActiveWorkItem(database)

	overview := &models.StatusOverview{
		Project:       p,
		FeatureCounts: counts,
		RecentEvents:  events,
	}

	milestones, _ := db.ListMilestones(database, p.ID)
	overview.MilestoneCount = len(milestones)

	cycles, _ := db.ListActiveCycles(database)
	overview.ActiveCycles = len(cycles)

	if active != nil {
		overview.ActiveWork = []models.WorkItem{*active}
	}

	return overview, nil
}

// Slug creates a URL-friendly slug from a name.
func Slug(name string) string {
	return slug(name)
}

func slug(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, s)
	return s
}

// buildWorkItemPrompt creates an enriched agent prompt with feature context.
func buildWorkItemPrompt(database *sql.DB, featureID, cycleType, stepName string) string {
	f, err := db.GetFeature(database, featureID)
	if err != nil {
		return fmt.Sprintf("Cycle %s, step: %s for feature %s", cycleType, stepName, featureID)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Cycle %s, step: %s for feature %q", cycleType, stepName, f.Name))
	if f.Description != "" {
		b.WriteString(fmt.Sprintf(" — %s", f.Description))
	}
	if f.Spec != "" {
		b.WriteString(fmt.Sprintf("\n\nSpec: %s", f.Spec))
	}
	return b.String()
}
