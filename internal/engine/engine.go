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
func AddFeature(database *sql.DB, projectID, name, description, milestoneID string, priority int, dependsOn []string) (*models.Feature, error) {
	id := slug(name)
	f := &models.Feature{
		ID:          id,
		ProjectID:   projectID,
		MilestoneID: milestoneID,
		Name:        name,
		Description: description,
		Priority:    priority,
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
		Data:      fmt.Sprintf(`{"name":%q,"priority":%d}`, name, priority),
	})
	return f, nil
}

// TransitionFeature moves a feature to a new status.
func TransitionFeature(database *sql.DB, projectID, featureID, newStatus string) error {
	f, err := db.GetFeature(database, featureID)
	if err != nil {
		return fmt.Errorf("getting feature: %w", err)
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
	return db.UpdateCycleInstance(database, c.ID, nextStep, c.Iteration, "active")
}

// ApproveFeatureQA approves a feature and transitions it to done.
func ApproveFeatureQA(database *sql.DB, projectID, featureID, notes string) error {
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
func RejectFeatureQA(database *sql.DB, projectID, featureID, notes string) error {
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
