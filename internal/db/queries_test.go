package db_test

import (
	"database/sql"
	"testing"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestCreateAndGetProject(t *testing.T) {
	database := openTestDB(t)

	p := &models.Project{ID: "test-proj", Name: "Test Project", Description: "A test"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	got, err := db.GetProject(database)
	if err != nil {
		t.Fatalf("getting project: %v", err)
	}
	if got.ID != "test-proj" || got.Name != "Test Project" {
		t.Errorf("got %+v, want id=test-proj name=Test Project", got)
	}
}

func TestFeatureCRUD(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"}) //nolint:errcheck

	// Create
	f := &models.Feature{ID: "f1", ProjectID: "p1", Name: "Feature One", Priority: 5}
	if err := db.CreateFeature(database, f); err != nil {
		t.Fatalf("creating feature: %v", err)
	}

	// Get
	got, err := db.GetFeature(database, "f1")
	if err != nil {
		t.Fatalf("getting feature: %v", err)
	}
	if got.Name != "Feature One" || got.Priority != 5 {
		t.Errorf("got %+v", got)
	}

	// List
	features, err := db.ListFeatures(database, "p1", "", "")
	if err != nil {
		t.Fatalf("listing features: %v", err)
	}
	if len(features) != 1 {
		t.Errorf("got %d features, want 1", len(features))
	}

	// Update
	if err := db.UpdateFeature(database, "f1", map[string]any{"status": "implementing"}); err != nil {
		t.Fatalf("updating feature: %v", err)
	}
	got, _ = db.GetFeature(database, "f1")
	if got.Status != "implementing" {
		t.Errorf("got status %q, want implementing", got.Status)
	}

	// Delete
	if err := db.DeleteFeature(database, "f1"); err != nil {
		t.Fatalf("deleting feature: %v", err)
	}
	features, _ = db.ListFeatures(database, "p1", "", "")
	if len(features) != 0 {
		t.Errorf("got %d features after delete, want 0", len(features))
	}
}

func TestMilestones(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"}) //nolint:errcheck

	m := &models.Milestone{ID: "m1", ProjectID: "p1", Name: "v1.0", SortOrder: 1}
	if err := db.CreateMilestone(database, m); err != nil {
		t.Fatalf("creating milestone: %v", err)
	}

	got, err := db.GetMilestone(database, "m1")
	if err != nil {
		t.Fatalf("getting milestone: %v", err)
	}
	if got.Name != "v1.0" {
		t.Errorf("got %q, want v1.0", got.Name)
	}

	milestones, err := db.ListMilestones(database, "p1")
	if err != nil {
		t.Fatalf("listing milestones: %v", err)
	}
	if len(milestones) != 1 {
		t.Errorf("got %d milestones, want 1", len(milestones))
	}
}

func TestRoadmapItems(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"}) //nolint:errcheck

	r := &models.RoadmapItem{
		ID: "r1", ProjectID: "p1", Title: "Add CLI",
		Priority: "high", Category: "core",
	}
	if err := db.CreateRoadmapItem(database, r); err != nil {
		t.Fatalf("creating roadmap item: %v", err)
	}

	items, err := db.ListRoadmapItems(database, "p1")
	if err != nil {
		t.Fatalf("listing roadmap items: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Add CLI" {
		t.Errorf("got %+v", items)
	}

	if err := db.UpdateRoadmapItem(database, "r1", map[string]any{"status": "in-progress"}); err != nil {
		t.Fatalf("updating roadmap item: %v", err)
	}
	got, _ := db.GetRoadmapItem(database, "r1")
	if got.Status != "in-progress" {
		t.Errorf("got status %q, want in-progress", got.Status)
	}
}

func TestWorkItems(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"})                  //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f1", ProjectID: "p1", Name: "F1"}) //nolint:errcheck

	w := &models.WorkItem{FeatureID: "f1", WorkType: "implement", AgentPrompt: "Do stuff"}
	if err := db.CreateWorkItem(database, w); err != nil {
		t.Fatalf("creating work item: %v", err)
	}
	if w.ID == 0 {
		t.Error("work item ID should be set")
	}

	// Get pending
	got, err := db.GetNextPendingWorkItem(database)
	if err != nil {
		t.Fatalf("getting pending work item: %v", err)
	}
	if got.WorkType != "implement" {
		t.Errorf("got %q, want implement", got.WorkType)
	}

	// Activate
	if err := db.UpdateWorkItemStatus(database, w.ID, "active", ""); err != nil {
		t.Fatalf("activating: %v", err)
	}

	active, err := db.GetActiveWorkItem(database)
	if err != nil {
		t.Fatalf("getting active: %v", err)
	}
	if active.Status != "active" {
		t.Errorf("got status %q, want active", active.Status)
	}

	// Complete
	if err := db.UpdateWorkItemStatus(database, w.ID, "done", "finished"); err != nil {
		t.Fatalf("completing: %v", err)
	}
}

func TestEvents(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"}) //nolint:errcheck

	if err := db.InsertEvent(database, &models.Event{
		ProjectID: "p1", EventType: "test.event", Data: `{"key":"value"}`,
	}); err != nil {
		t.Fatalf("inserting event: %v", err)
	}

	events, err := db.ListEvents(database, "p1", "", "", "", 10)
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "test.event" {
		t.Errorf("got %+v", events)
	}

	// Search
	results, err := db.SearchEvents(database, "p1", "value")
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}
}

func TestCycles(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"})                  //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f1", ProjectID: "p1", Name: "F1"}) //nolint:errcheck

	c := &models.CycleInstance{
		FeatureID: "f1", CycleType: "feature-implementation",
		Status: "active", Iteration: 1,
	}
	if err := db.CreateCycleInstance(database, c); err != nil {
		t.Fatalf("creating cycle: %v", err)
	}

	active, err := db.GetActiveCycle(database, "f1")
	if err != nil {
		t.Fatalf("getting active cycle: %v", err)
	}
	if active.CycleType != "feature-implementation" {
		t.Errorf("got %q", active.CycleType)
	}

	// Score
	if err := db.CreateCycleScore(database, &models.CycleScore{
		CycleID: c.ID, Step: 0, Iteration: 1, Score: 8.5, Notes: "good",
	}); err != nil {
		t.Fatalf("creating score: %v", err)
	}

	scores, err := db.ListCycleScores(database, c.ID)
	if err != nil {
		t.Fatalf("listing scores: %v", err)
	}
	if len(scores) != 1 || scores[0].Score != 8.5 {
		t.Errorf("got %+v", scores)
	}

	// Advance
	if err := db.UpdateCycleInstance(database, c.ID, 1, 1, "active"); err != nil {
		t.Fatalf("advancing cycle: %v", err)
	}

	// History
	history, err := db.ListCycleHistory(database, "f1")
	if err != nil {
		t.Fatalf("listing history: %v", err)
	}
	if len(history) != 1 || history[0].CurrentStep != 1 {
		t.Errorf("got %+v", history)
	}
}

func TestFeatureDeps(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"})                  //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f1", ProjectID: "p1", Name: "F1"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f2", ProjectID: "p1", Name: "F2"}) //nolint:errcheck

	if err := db.AddFeatureDep(database, "f2", "f1"); err != nil {
		t.Fatalf("adding dep: %v", err)
	}

	f, _ := db.GetFeature(database, "f2")
	if len(f.DependsOn) != 1 || f.DependsOn[0] != "f1" {
		t.Errorf("got deps %v, want [f1]", f.DependsOn)
	}
}

func TestFeatureCountsAndPendingQA(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"})                  //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f1", ProjectID: "p1", Name: "F1"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f2", ProjectID: "p1", Name: "F2"}) //nolint:errcheck
	db.UpdateFeature(database, "f2", map[string]any{"status": "human-qa"})             //nolint:errcheck

	counts, err := db.FeatureCounts(database, "p1")
	if err != nil {
		t.Fatalf("getting counts: %v", err)
	}
	if counts["draft"] != 1 || counts["human-qa"] != 1 {
		t.Errorf("got %v", counts)
	}

	pending, err := db.PendingQAFeatures(database, "p1")
	if err != nil {
		t.Fatalf("getting pending QA: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "f2" {
		t.Errorf("got %+v", pending)
	}
}
