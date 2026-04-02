package engine

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/config"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
)

// findCycleType resolves a cycle type by name, checking built-in types first,
// then custom templates stored in the database.
func findCycleType(database *sql.DB, name string) *models.CycleType {
	for i := range models.CycleTypes {
		if models.CycleTypes[i].Name == name {
			return &models.CycleTypes[i]
		}
	}
	// Fall back to custom templates in the DB.
	if t, err := db.GetCycleTemplate(database, name); err == nil && t != nil {
		return &models.CycleType{
			Name:        t.Name,
			Description: t.Description,
			Steps:       t.Steps,
		}
	}
	return nil
}

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
		allowed := ValidTransitions[f.Status]
		return fmt.Errorf("invalid transition from %q to %q for feature %q: allowed transitions are %v", f.Status, newStatus, featureID, allowed)
	}
	oldStatus := f.Status
	if err := db.UpdateFeature(database, featureID, map[string]any{"status": newStatus}); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: featureID,
		EventType: "feature.status_changed",
		Data:      fmt.Sprintf(`{"from":%q,"to":%q}`, oldStatus, newStatus),
	})
	// Cascade blocking: auto-block/unblock dependents
	if newStatus == "blocked" || oldStatus == "blocked" {
		if err := CascadeBlocking(database, featureID); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cascade blocking: %v\n", err)
		}
	}
	return nil
}

// CascadeBlocking propagates blocking status changes through the dependency graph.
// When a feature is blocked, all transitive dependents are automatically blocked.
// When a feature is unblocked, dependents are restored to their previous status
// (if no other blockers remain).
func CascadeBlocking(database *sql.DB, featureID string) error {
	dependents, err := db.GetAllTransitiveDependents(database, featureID)
	if err != nil {
		return fmt.Errorf("getting dependents: %w", err)
	}

	feature, err := db.GetFeature(database, featureID)
	if err != nil {
		return fmt.Errorf("getting feature: %w", err)
	}

	if feature.Status == "blocked" {
		for _, dep := range dependents {
			if dep.Status != "blocked" && dep.Status != "done" {
				if err := db.SavePreviousStatus(database, dep.ID, dep.Status); err != nil {
					return fmt.Errorf("saving previous status for %s: %w", dep.ID, err)
				}
				if err := db.SetFeatureStatus(database, dep.ID, "blocked"); err != nil {
					return fmt.Errorf("blocking %s: %w", dep.ID, err)
				}
				_ = db.InsertEvent(database, &models.Event{
					ProjectID: dep.ProjectID,
					FeatureID: dep.ID,
					EventType: "feature.cascade_blocked",
					Data:      fmt.Sprintf(`{"blocked_by":%q}`, featureID),
				})
			}
		}
	} else {
		for _, dep := range dependents {
			if dep.Status == "blocked" {
				allClear, err := db.AreAllDependenciesClear(database, dep.ID)
				if err != nil {
					return fmt.Errorf("checking dependencies for %s: %w", dep.ID, err)
				}
				if allClear {
					prevStatus, err := db.GetPreviousStatus(database, dep.ID)
					if err != nil || prevStatus == "" {
						prevStatus = "draft"
					}
					if err := db.SetFeatureStatus(database, dep.ID, prevStatus); err != nil {
						return fmt.Errorf("unblocking %s: %w", dep.ID, err)
					}
					_ = db.InsertEvent(database, &models.Event{
						ProjectID: dep.ProjectID,
						FeatureID: dep.ID,
						EventType: "feature.cascade_unblocked",
						Data:      fmt.Sprintf(`{"unblocked_by":%q,"restored_status":%q}`, featureID, prevStatus),
					})
				}
			}
		}
	}
	return nil
}

// GetNextWorkItem finds the next available work item for an agent.
// If agentID is provided, it first checks for items already assigned to that agent,
// then claims an unassigned pending item. This prevents two agents from getting the same work.
func GetNextWorkItem(database *sql.DB, agentID ...string) (*models.WorkItem, error) {
	agent := ""
	if len(agentID) > 0 {
		agent = agentID[0]
	}

	// If agent specified, check for items already assigned to this agent
	if agent != "" {
		if active, err := db.GetActiveWorkItemForAgent(database, agent); err == nil {
			return active, nil
		}
	} else {
		// Legacy: check for any active item
		if active, err := db.GetActiveWorkItem(database); err == nil {
			return active, nil
		}
	}

	// Get next pending item
	w, err := db.GetNextPendingWorkItem(database)
	if err != nil {
		return nil, fmt.Errorf("no pending work items")
	}

	// Claim it atomically
	if agent != "" {
		if err := db.ClaimWorkItem(database, w.ID, agent); err != nil {
			return nil, fmt.Errorf("claiming work item: %w", err)
		}
		w.AssignedAgent = agent
	} else {
		if err := db.UpdateWorkItemStatus(database, w.ID, "active", ""); err != nil {
			return nil, fmt.Errorf("activating work item: %w", err)
		}
	}
	w.Status = "active"
	return w, nil
}

// GetCoordinationStatus returns the full multi-agent coordination snapshot.
func GetCoordinationStatus(database *sql.DB, projectID string) (*models.CoordinationStatus, error) {
	active, err := db.GetActiveAgents(database, projectID, 5)
	if err != nil {
		return nil, fmt.Errorf("getting active agents: %w", err)
	}
	if active == nil {
		active = []models.AgentSession{}
	}

	stale, err := db.GetStaleAgents(database, projectID, 5)
	if err != nil {
		return nil, fmt.Errorf("getting stale agents: %w", err)
	}
	if stale == nil {
		stale = []models.AgentSession{}
	}

	conflicts, err := db.DetectConflicts(database)
	if err != nil {
		return nil, fmt.Errorf("detecting conflicts: %w", err)
	}
	if conflicts == nil {
		conflicts = []models.Conflict{}
	}

	queueDepth, err := db.CountPendingWorkItems(database)
	if err != nil {
		return nil, fmt.Errorf("counting pending work items: %w", err)
	}

	claimed, err := db.CountClaimedWorkItems(database)
	if err != nil {
		return nil, fmt.Errorf("counting claimed work items: %w", err)
	}

	return &models.CoordinationStatus{
		ActiveAgents: active,
		StaleAgents:  stale,
		Conflicts:    conflicts,
		QueueDepth:   queueDepth,
		ClaimedItems: claimed,
	}, nil
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
		ctx.CycleType = findCycleType(database, c.CycleType)
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
	fmt.Fprintf(&b, "You are working on feature %q", ctx.Feature.Name)
	if ctx.Feature.Description != "" {
		fmt.Fprintf(&b, ": %s", ctx.Feature.Description)
	}
	fmt.Fprintf(&b, "\n\nCurrent task: %s (work type: %s)", ctx.WorkItem.AgentPrompt, ctx.WorkItem.WorkType)

	if ctx.Feature.Spec != "" {
		fmt.Fprintf(&b, "\n\n## Feature Spec\n%s", ctx.Feature.Spec)
	}

	if ctx.Cycle != nil && ctx.CycleType != nil {
		fmt.Fprintf(&b, "\n\n## Cycle Context\nCycle type: %s (step %d/%d: %s)",
			ctx.CycleType.Description, ctx.Cycle.CurrentStep+1, len(ctx.CycleType.Steps),
			ctx.CycleType.Steps[ctx.Cycle.CurrentStep].Name)
		fmt.Fprintf(&b, "\nAll steps: %s", strings.Join(ctx.CycleType.StepNames(), " → "))
	}

	if len(ctx.PriorResults) > 0 {
		b.WriteString("\n\n## Prior Step Results")
		for _, pr := range ctx.PriorResults {
			if pr.Result != "" {
				fmt.Fprintf(&b, "\n- [%s] %s", pr.WorkType, pr.Result)
			}
		}
	}

	if ctx.RoadmapItem != nil {
		fmt.Fprintf(&b, "\n\n## Roadmap Context\nTitle: %s\nPriority: %s\nDescription: %s",
			ctx.RoadmapItem.Title, ctx.RoadmapItem.Priority, ctx.RoadmapItem.Description)
	}

	return b.String()
}

// CompleteWorkItem marks the active work item as done.
func CompleteWorkItem(database *sql.DB, result string) error {
	_, err := CompleteWorkItemAndReturn(database, result)
	return err
}

// CompleteWorkItemAndReturn completes the active work item and returns it.
func CompleteWorkItemAndReturn(database *sql.DB, result string) (*models.WorkItem, error) {
	w, err := db.GetActiveWorkItem(database)
	if err != nil {
		return nil, fmt.Errorf("no active work item")
	}
	if err := db.UpdateWorkItemStatus(database, w.ID, "done", result); err != nil {
		return nil, fmt.Errorf("completing work item: %w", err)
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

	// Auto-advance cycle to next step and create work item
	if w.FeatureID != "" {
		if c, cErr := db.GetActiveCycle(database, w.FeatureID); cErr == nil {
			ct := findCycleType(database, c.CycleType)
			if ct != nil {
				// Only advance if current step matches the completed work type
				if c.CurrentStep < len(ct.Steps) && ct.Steps[c.CurrentStep].Name == w.WorkType {
					nextStep := c.CurrentStep + 1
					if nextStep >= len(ct.Steps) {
						// Cycle complete
						_ = db.UpdateCycleInstance(database, c.ID, c.CurrentStep, c.Iteration, "completed")
					} else {
						// Advance and create next work item unless it's a judge or human step
						if ct.Steps[nextStep].Name != "judge" && !ct.Steps[nextStep].Human {
							prompt := buildWorkItemPrompt(database, w.FeatureID, c.CycleType, ct.Steps[nextStep].Name)
							_ = db.CreateWorkItem(database, &models.WorkItem{
								FeatureID:   w.FeatureID,
								WorkType:    ct.Steps[nextStep].Name,
								AgentPrompt: prompt,
							})
						}
						_ = db.UpdateCycleInstance(database, c.ID, nextStep, c.Iteration, "active")
						if p != nil {
							_ = db.InsertEvent(database, &models.Event{
								ProjectID: p.ID,
								FeatureID: w.FeatureID,
								EventType: "cycle.advanced",
								Data:      fmt.Sprintf(`{"step":%q,"step_index":%d}`, ct.Steps[nextStep].Name, nextStep),
							})
						}
					}
				}
			}
		}
	}

	// Auto-transition feature to human-qa when implementation/agent-qa work completes
	if w.FeatureID != "" && p != nil {
		f, fErr := db.GetFeature(database, w.FeatureID)
		if fErr == nil && (f.Status == "implementing" || f.Status == "agent-qa") {
			_ = TransitionFeature(database, p.ID, w.FeatureID, "human-qa")
		}
	}
	return w, nil
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

// ReclaimStaleWorkItems finds work items claimed by agents with no heartbeat
// for more than staleMins minutes and releases them back to the pending queue.
func ReclaimStaleWorkItems(database *sql.DB, staleMins int) (int, error) {
	return db.ReclaimStaleWorkItems(database, staleMins)
}

// StartCycle starts a new iteration cycle for a feature.
// StartCycleForEntity starts a cycle for any entity type.
func StartCycleForEntity(database *sql.DB, projectID, entityType, entityID, cycleType string) (*models.CycleInstance, error) {
	ct := findCycleType(database, cycleType)
	if ct == nil {
		return nil, fmt.Errorf("unknown cycle type: %s", cycleType)
	}
	if existing, err := db.GetActiveCycleForEntity(database, entityType, entityID); err == nil {
		return nil, fmt.Errorf("%s already has active cycle: %s (step %d)", entityType, existing.CycleType, existing.CurrentStep)
	}
	c := &models.CycleInstance{
		EntityType: entityType,
		EntityID:   entityID,
		CycleType:  cycleType,
		Status:     "active",
		Iteration:  1,
	}
	if err := db.CreateCycleInstance(database, c); err != nil {
		return nil, fmt.Errorf("creating cycle: %w", err)
	}
	c.StepName = ct.Steps[0].Name
	return c, nil
}

// StartCycle starts a cycle for a feature (backward compat wrapper).
func StartCycle(database *sql.DB, projectID, featureID, cycleType string) (*models.CycleInstance, error) {
	// Validate cycle type
	ct := findCycleType(database, cycleType)
	if ct == nil {
		return nil, fmt.Errorf("unknown cycle type: %s", cycleType)
	}

	// Check no active cycle
	if existing, err := db.GetActiveCycle(database, featureID); err == nil {
		return nil, fmt.Errorf("feature already has active cycle: %s (step %d)", existing.CycleType, existing.CurrentStep)
	}

	c := &models.CycleInstance{
		EntityType: "feature", EntityID: featureID,
		CycleType: cycleType,
		Status:    "active",
		Iteration: 1,
	}
	if err := db.CreateCycleInstance(database, c); err != nil {
		return nil, fmt.Errorf("creating cycle: %w", err)
	}
	c.StepName = ct.Steps[0].Name

	// Only create work item for agent steps (not human-owned steps)
	if !ct.Steps[0].Human {
		prompt := buildWorkItemPrompt(database, featureID, cycleType, ct.Steps[0].Name)
		_ = db.CreateWorkItem(database, &models.WorkItem{
			FeatureID:   featureID,
			WorkType:    ct.Steps[0].Name,
			AgentPrompt: prompt,
		})
	}

	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: featureID,
		EventType: "cycle.started",
		Data:      fmt.Sprintf(`{"cycle_type":%q,"step":%q}`, cycleType, ct.Steps[0].Name),
	})

	return c, nil
}

// ScoreCycleStep records a score and advances the cycle.
func ScoreCycleStep(database *sql.DB, projectID, featureID string, score float64, notes string) error {
	c, err := db.GetActiveCycle(database, featureID)
	if err != nil {
		return fmt.Errorf("no active cycle for feature %s", featureID)
	}

	ct := findCycleType(database, c.CycleType)
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
		Data:      fmt.Sprintf(`{"step":%q,"score":%.1f}`, ct.Steps[c.CurrentStep].Name, score),
	})

	// Advance to next step or complete
	nextStep := c.CurrentStep + 1
	if nextStep >= len(ct.Steps) {
		return db.UpdateCycleInstance(database, c.ID, c.CurrentStep, c.Iteration, "completed")
	}
	// Only create work item for agent steps (not human or judge steps)
	if !ct.Steps[nextStep].Human && ct.Steps[nextStep].Name != "judge" {
		prompt := buildWorkItemPrompt(database, featureID, c.CycleType, ct.Steps[nextStep].Name)
		_ = db.CreateWorkItem(database, &models.WorkItem{
			FeatureID:   featureID,
			WorkType:    ct.Steps[nextStep].Name,
			AgentPrompt: prompt,
		})
	}
	return db.UpdateCycleInstance(database, c.ID, nextStep, c.Iteration, "active")
}

// EvaluateQARule checks QA rules for a feature and returns the action and matched rule.
// This is called when a feature transitions to human-qa to determine if it should be auto-approved.
func EvaluateQARule(database *sql.DB, cfg *config.Config, featureID string) (action string, matchedRule string) {
	f, err := db.GetFeature(database, featureID)
	if err != nil {
		return "review", ""
	}

	// Get tags
	tags, _ := db.GetFeatureTags(database, featureID)

	// Get cycle type and last score
	var cycleType string
	var lastScore float64
	if c, cErr := db.GetActiveCycle(database, featureID); cErr == nil {
		cycleType = c.CycleType
		if scores, sErr := db.ListCycleScores(database, c.ID); sErr == nil && len(scores) > 0 {
			lastScore = scores[len(scores)-1].Score
		}
	}

	return cfg.EvaluateQARules(f.Priority, tags, cycleType, lastScore)
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

	openDisc, _ := db.CountOpenDiscussions(database, p.ID)
	overview.OpenDiscussions = openDisc

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
	fmt.Fprintf(&b, "Cycle %s, step: %s for feature %q", cycleType, stepName, f.Name)
	if f.Description != "" {
		fmt.Fprintf(&b, " — %s", f.Description)
	}
	if f.Spec != "" {
		fmt.Fprintf(&b, "\n\nSpec: %s", f.Spec)
	}
	return b.String()
}
