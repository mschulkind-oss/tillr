package engine

import (
	"database/sql"
	"testing"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func initTestProject(t *testing.T, database *sql.DB) *models.Project {
	t.Helper()
	p, err := InitProject(database, "Test Project")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFullAgentWorkflowLoop(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close() //nolint:errcheck

	p := initTestProject(t, database)

	// Add milestone
	ms := &models.Milestone{ID: "v1", ProjectID: p.ID, Name: "v1.0"}
	if err := db.CreateMilestone(database, ms); err != nil {
		t.Fatal(err)
	}

	// Add feature with spec
	f, err := AddFeature(database, p.ID, "Login Page", "User login form",
		"1. Email input\n2. Password input\n3. Submit button", "v1", 5, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// Start a feature-implementation cycle
	cycle, err := StartCycle(database, p.ID, f.ID, "feature-implementation")
	if err != nil {
		t.Fatal(err)
	}
	if cycle.CycleType != "feature-implementation" {
		t.Errorf("expected cycle type feature-implementation, got %s", cycle.CycleType)
	}

	// Transition feature to implementing (required for auto-transition to human-qa)
	if err := TransitionFeature(database, p.ID, f.ID, "implementing"); err != nil {
		t.Fatal(err)
	}

	// Get next work item (should be "research" step)
	w, err := GetNextWorkItem(database)
	if err != nil {
		t.Fatal(err)
	}
	if w.WorkType != "research" {
		t.Errorf("expected work type research, got %s", w.WorkType)
	}

	// Complete research step
	if err := CompleteWorkItem(database, "Researched login patterns"); err != nil {
		t.Fatal(err)
	}

	// Score research step to advance cycle to develop
	if err := ScoreCycleStep(database, p.ID, f.ID, 8.0, "good research"); err != nil {
		t.Fatal(err)
	}

	// Get next work item (should be "develop")
	w, err = GetNextWorkItem(database)
	if err != nil {
		t.Fatal(err)
	}
	if w.WorkType != "develop" {
		t.Errorf("expected work type develop, got %s", w.WorkType)
	}

	// Complete develop step
	if err := CompleteWorkItem(database, "Built login form"); err != nil {
		t.Fatal(err)
	}

	// Feature should be in human-qa now (auto-transition from CompleteWorkItem)
	feat, _ := db.GetFeature(database, f.ID)
	if feat.Status != "human-qa" {
		t.Errorf("expected feature status human-qa after completing develop, got %s", feat.Status)
	}

	// QA approve
	if err := ApproveFeatureQA(database, p.ID, f.ID, "looks great"); err != nil {
		t.Fatal(err)
	}
	feat, _ = db.GetFeature(database, f.ID)
	if feat.Status != "done" {
		t.Errorf("expected feature status done after QA approve, got %s", feat.Status)
	}
}

func TestAgentWorkItemFailure(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close() //nolint:errcheck

	p := initTestProject(t, database)
	f, _ := AddFeature(database, p.ID, "Fail Test", "Test failure handling", "", "", 1, nil, "")

	// Create a work item manually
	if err := db.CreateWorkItem(database, &models.WorkItem{
		FeatureID:   f.ID,
		WorkType:    "develop",
		AgentPrompt: "build something",
	}); err != nil {
		t.Fatal(err)
	}

	// Get and activate the work item
	w, err := GetNextWorkItem(database)
	if err != nil {
		t.Fatal(err)
	}

	// Fail the work item
	if err := FailWorkItem(database, "compiler error"); err != nil {
		t.Fatal(err)
	}

	// Verify it's failed
	failed, err := db.GetWorkItemByID(database, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" {
		t.Errorf("expected work item status failed, got %s", failed.Status)
	}
	if failed.Result != "compiler error" {
		t.Errorf("expected result 'compiler error', got %q", failed.Result)
	}
}

func TestMultipleFeatureQueue(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close() //nolint:errcheck

	p := initTestProject(t, database)

	// Add multiple features with work items
	f1, _ := AddFeature(database, p.ID, "Feature One", "first", "", "", 5, nil, "")
	f2, _ := AddFeature(database, p.ID, "Feature Two", "second", "", "", 3, nil, "")

	_ = db.CreateWorkItem(database, &models.WorkItem{FeatureID: f1.ID, WorkType: "develop", AgentPrompt: "build f1"})
	_ = db.CreateWorkItem(database, &models.WorkItem{FeatureID: f2.ID, WorkType: "develop", AgentPrompt: "build f2"})

	// Get first work item
	w1, err := GetNextWorkItem(database)
	if err != nil {
		t.Fatal(err)
	}
	if w1.FeatureID != f1.ID {
		t.Errorf("expected first feature, got %s", w1.FeatureID)
	}

	// Complete it
	_ = CompleteWorkItem(database, "done")

	// Get second work item
	w2, err := GetNextWorkItem(database)
	if err != nil {
		t.Fatal(err)
	}
	if w2.FeatureID != f2.ID {
		t.Errorf("expected second feature, got %s", w2.FeatureID)
	}
}

func TestCycleScoring(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close() //nolint:errcheck

	p := initTestProject(t, database)
	f, _ := AddFeature(database, p.ID, "Score Test", "scoring", "spec here", "", 5, nil, "")

	_, err := StartCycle(database, p.ID, f.ID, "feature-implementation")
	if err != nil {
		t.Fatal(err)
	}

	// Get & complete the research work item (auto-advances to develop)
	w, _ := GetNextWorkItem(database)
	if w.WorkType != "research" {
		t.Fatalf("expected research, got %s", w.WorkType)
	}
	_ = CompleteWorkItem(database, "researched")

	// Complete develop (auto-advances to agent-qa)
	w2, _ := GetNextWorkItem(database)
	if w2.WorkType != "develop" {
		t.Fatalf("expected develop, got %s", w2.WorkType)
	}
	_ = CompleteWorkItem(database, "developed")

	// Complete agent-qa (auto-advances to judge step, no work item created)
	w3, _ := GetNextWorkItem(database)
	if w3.WorkType != "agent-qa" {
		t.Fatalf("expected agent-qa, got %s", w3.WorkType)
	}
	_ = CompleteWorkItem(database, "qa passed")

	// Score the judge step
	if err := ScoreCycleStep(database, p.ID, f.ID, 9.5, "excellent"); err != nil {
		t.Fatal(err)
	}

	// Verify score was recorded
	c, _ := db.GetActiveCycle(database, f.ID)
	scores, err := db.ListCycleScores(database, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	if scores[0].Score != 9.5 {
		t.Errorf("expected score 9.5, got %f", scores[0].Score)
	}

	// Cycle should have advanced to step 4 (human-qa)
	if c.CurrentStep != 4 {
		t.Errorf("expected current step 4 (human-qa), got %d", c.CurrentStep)
	}
}

func TestQAGateEnforcement(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close() //nolint:errcheck

	p := initTestProject(t, database)
	f, _ := AddFeature(database, p.ID, "QA Gate", "qa test", "", "", 5, nil, "")

	// Try to go directly from implementing to done (should fail)
	_ = TransitionFeature(database, p.ID, f.ID, "implementing")
	err := TransitionFeature(database, p.ID, f.ID, "done")
	if err == nil {
		t.Fatal("expected error when transitioning directly from implementing to done")
	}

	// Proper flow: implementing → human-qa → done
	if err := TransitionFeature(database, p.ID, f.ID, "human-qa"); err != nil {
		t.Fatal(err)
	}
	if err := ApproveFeatureQA(database, p.ID, f.ID, "approved"); err != nil {
		t.Fatal(err)
	}

	feat, _ := db.GetFeature(database, f.ID)
	if feat.Status != "done" {
		t.Errorf("expected done, got %s", feat.Status)
	}
}

func TestWorkContextEnrichment(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close() //nolint:errcheck

	p := initTestProject(t, database)

	// Create roadmap item
	ri := &models.RoadmapItem{
		ID: "ri-1", ProjectID: p.ID, Title: "Auth System",
		Description: "Build auth", Priority: "high", Status: "accepted",
	}
	_ = db.CreateRoadmapItem(database, ri)

	// Create feature linked to roadmap
	f, _ := AddFeature(database, p.ID, "Login", "login page", "1. email\n2. password", "", 5, nil, "ri-1")

	// Start cycle and get work item
	_, _ = StartCycle(database, p.ID, f.ID, "feature-implementation")
	w, _ := GetNextWorkItem(database)

	// Get enriched context
	ctx, err := GetWorkContext(database, w)
	if err != nil {
		t.Fatal(err)
	}

	// Verify all context fields are populated
	if ctx.Feature == nil {
		t.Fatal("expected feature in context")
	}
	if ctx.Feature.Name != "Login" {
		t.Errorf("expected feature name Login, got %s", ctx.Feature.Name)
	}
	if ctx.Feature.Spec == "" {
		t.Error("expected feature spec to be populated")
	}
	if ctx.RoadmapItem == nil {
		t.Fatal("expected roadmap item in context")
	}
	if ctx.RoadmapItem.Title != "Auth System" {
		t.Errorf("expected roadmap title Auth System, got %s", ctx.RoadmapItem.Title)
	}
	if ctx.Cycle == nil {
		t.Fatal("expected cycle in context")
	}
	if ctx.CycleType == nil {
		t.Fatal("expected cycle type in context")
	}
	if ctx.AgentGuidance == "" {
		t.Error("expected agent guidance to be populated")
	}
}

func TestBlockingCascade(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close() //nolint:errcheck

	p := initTestProject(t, database)

	// Create feature chain: C depends on B depends on A
	fA, _ := AddFeature(database, p.ID, "Feature A", "base", "", "", 5, nil, "")
	fB, _ := AddFeature(database, p.ID, "Feature B", "mid", "", "", 3, []string{fA.ID}, "")
	fC, _ := AddFeature(database, p.ID, "Feature C", "top", "", "", 1, []string{fB.ID}, "")

	// Transition A to implementing first
	_ = TransitionFeature(database, p.ID, fA.ID, "implementing")
	_ = TransitionFeature(database, p.ID, fB.ID, "implementing")
	_ = TransitionFeature(database, p.ID, fC.ID, "implementing")

	// Block A — should cascade to B and C
	if err := TransitionFeature(database, p.ID, fA.ID, "blocked"); err != nil {
		t.Fatal(err)
	}

	bFeat, _ := db.GetFeature(database, fB.ID)
	cFeat, _ := db.GetFeature(database, fC.ID)
	if bFeat.Status != "blocked" {
		t.Errorf("expected B to be blocked, got %s", bFeat.Status)
	}
	if cFeat.Status != "blocked" {
		t.Errorf("expected C to be blocked, got %s", cFeat.Status)
	}

	// Unblock A — should cascade unblock B and C
	if err := TransitionFeature(database, p.ID, fA.ID, "implementing"); err != nil {
		t.Fatal(err)
	}

	bFeat, _ = db.GetFeature(database, fB.ID)
	cFeat, _ = db.GetFeature(database, fC.ID)
	if bFeat.Status == "blocked" {
		t.Errorf("expected B to be unblocked, still %s", bFeat.Status)
	}
	if cFeat.Status == "blocked" {
		t.Errorf("expected C to be unblocked, still %s", cFeat.Status)
	}
}
