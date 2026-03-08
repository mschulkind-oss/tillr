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

func TestGetFeatureDependencyTree(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"})                  //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f1", ProjectID: "p1", Name: "F1"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f2", ProjectID: "p1", Name: "F2"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f3", ProjectID: "p1", Name: "F3"}) //nolint:errcheck

	db.AddFeatureDep(database, "f3", "f2") //nolint:errcheck
	db.AddFeatureDep(database, "f2", "f1") //nolint:errcheck

	tree, err := db.GetFeatureDependencyTree(database, "f3")
	if err != nil {
		t.Fatalf("getting tree: %v", err)
	}
	if len(tree) != 3 {
		t.Errorf("got %d nodes, want 3", len(tree))
	}
	ids := map[string]bool{}
	for _, f := range tree {
		ids[f.ID] = true
	}
	for _, want := range []string{"f1", "f2", "f3"} {
		if !ids[want] {
			t.Errorf("tree missing %s", want)
		}
	}
}

func TestGetFeatureDependents(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"})                  //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f1", ProjectID: "p1", Name: "F1"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f2", ProjectID: "p1", Name: "F2"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f3", ProjectID: "p1", Name: "F3"}) //nolint:errcheck

	db.AddFeatureDep(database, "f2", "f1") //nolint:errcheck
	db.AddFeatureDep(database, "f3", "f1") //nolint:errcheck

	dependents, err := db.GetFeatureDependents(database, "f1")
	if err != nil {
		t.Fatalf("getting dependents: %v", err)
	}
	if len(dependents) != 2 {
		t.Errorf("got %d dependents, want 2", len(dependents))
	}
	ids := map[string]bool{}
	for _, f := range dependents {
		ids[f.ID] = true
	}
	if !ids["f2"] || !ids["f3"] {
		t.Errorf("expected f2 and f3 as dependents, got %v", ids)
	}
}

func TestGetBlockedFeatures(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"})                  //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f1", ProjectID: "p1", Name: "F1"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f2", ProjectID: "p1", Name: "F2"}) //nolint:errcheck
	db.CreateFeature(database, &models.Feature{ID: "f3", ProjectID: "p1", Name: "F3"}) //nolint:errcheck

	db.AddFeatureDep(database, "f2", "f1") //nolint:errcheck
	db.AddFeatureDep(database, "f3", "f1") //nolint:errcheck

	// f1 is draft (default), so f2 and f3 are blocked
	blocked, err := db.GetBlockedFeatures(database)
	if err != nil {
		t.Fatalf("getting blocked: %v", err)
	}
	if len(blocked) != 2 {
		t.Errorf("expected 2 blocked features, got %d", len(blocked))
	}

	// Make f1 done
	db.UpdateFeature(database, "f1", map[string]any{"status": "done"}) //nolint:errcheck

	blocked, err = db.GetBlockedFeatures(database)
	if err != nil {
		t.Fatalf("getting blocked after f1 done: %v", err)
	}
	if len(blocked) != 0 {
		ids := make([]string, len(blocked))
		for i, b := range blocked {
			ids[i] = b.ID
		}
		t.Errorf("expected 0 blocked features after f1 is done, got %v", ids)
	}
}

func TestGetBurndownData(t *testing.T) {
	database := openTestDB(t)
	p := &models.Project{ID: "bd-proj", Name: "Burndown Test"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	// Insert feature-created events
	events := []models.Event{
		{ProjectID: p.ID, FeatureID: "f1", EventType: "feature.created", Data: `{"name":"f1"}`},
		{ProjectID: p.ID, FeatureID: "f2", EventType: "feature.created", Data: `{"name":"f2"}`},
		{ProjectID: p.ID, FeatureID: "f3", EventType: "feature.created", Data: `{"name":"f3"}`},
	}
	for _, e := range events {
		if err := db.InsertEvent(database, &e); err != nil {
			t.Fatalf("inserting event: %v", err)
		}
	}

	// Insert a status change to done
	doneEvt := models.Event{
		ProjectID: p.ID, FeatureID: "f1",
		EventType: "feature.status_changed",
		Data:      `{"from":"implementing","to":"done"}`,
	}
	if err := db.InsertEvent(database, &doneEvt); err != nil {
		t.Fatalf("inserting done event: %v", err)
	}

	data, err := db.GetBurndownData(database, p.ID)
	if err != nil {
		t.Fatalf("GetBurndownData: %v", err)
	}

	if len(data.Points) == 0 {
		t.Fatal("expected burndown points, got none")
	}

	// Last point should show: total=3, done=1, remaining=2
	last := data.Points[len(data.Points)-1]
	if last.Total != 3 {
		t.Errorf("expected total=3, got %d", last.Total)
	}
	if last.Done != 1 {
		t.Errorf("expected done=1, got %d", last.Done)
	}
	if last.Remaining != 2 {
		t.Errorf("expected remaining=2, got %d", last.Remaining)
	}

	// Empty project should return empty data
	emptyData, err := db.GetBurndownData(database, "nonexistent")
	if err != nil {
		t.Fatalf("GetBurndownData for empty: %v", err)
	}
	if len(emptyData.Points) != 0 {
		t.Errorf("expected 0 points for empty project, got %d", len(emptyData.Points))
	}
}

func TestBatchUpdateFeatures(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"}) //nolint:errcheck

	// Create three features
	for _, id := range []string{"f1", "f2", "f3"} {
		db.CreateFeature(database, &models.Feature{ID: id, ProjectID: "p1", Name: "Feature " + id}) //nolint:errcheck
	}

	// Batch update status
	updated, err := db.BatchUpdateFeatures(database, []string{"f1", "f2"}, "status", "implementing")
	if err != nil {
		t.Fatalf("batch update status: %v", err)
	}
	if updated != 2 {
		t.Errorf("expected 2 updated, got %d", updated)
	}

	f1, _ := db.GetFeature(database, "f1")
	f2, _ := db.GetFeature(database, "f2")
	f3, _ := db.GetFeature(database, "f3")
	if f1.Status != "implementing" {
		t.Errorf("f1 status = %q, want implementing", f1.Status)
	}
	if f2.Status != "implementing" {
		t.Errorf("f2 status = %q, want implementing", f2.Status)
	}
	if f3.Status != "draft" {
		t.Errorf("f3 status = %q, want draft (unchanged)", f3.Status)
	}

	// Batch update priority
	updated, err = db.BatchUpdateFeatures(database, []string{"f1", "f2", "f3"}, "priority", "8")
	if err != nil {
		t.Fatalf("batch update priority: %v", err)
	}
	if updated != 3 {
		t.Errorf("expected 3 updated, got %d", updated)
	}

	f1, _ = db.GetFeature(database, "f1")
	if f1.Priority != 8 {
		t.Errorf("f1 priority = %d, want 8", f1.Priority)
	}

	// Invalid field should error
	_, err = db.BatchUpdateFeatures(database, []string{"f1"}, "invalid_field", "x")
	if err == nil {
		t.Error("expected error for invalid field")
	}

	// Empty IDs should return 0
	updated, err = db.BatchUpdateFeatures(database, []string{}, "status", "done")
	if err != nil {
		t.Fatalf("batch update empty: %v", err)
	}
	if updated != 0 {
		t.Errorf("expected 0 updated for empty IDs, got %d", updated)
	}
}

func TestSearchFTS(t *testing.T) {
	database := openTestDB(t)

	// Create project
	p := &models.Project{ID: "test-proj", Name: "Test Project"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	// Create features — migration 14 auto-populates FTS from existing rows
	f1 := &models.Feature{ID: "auth-module", ProjectID: p.ID, Name: "Authentication Module", Description: "JWT-based auth system", Spec: "OAuth2 support"}
	f2 := &models.Feature{ID: "search-api", ProjectID: p.ID, Name: "Search API", Description: "Full-text search endpoint"}
	if err := db.CreateFeature(database, f1); err != nil {
		t.Fatalf("creating feature: %v", err)
	}
	if err := db.CreateFeature(database, f2); err != nil {
		t.Fatalf("creating feature: %v", err)
	}

	// Index the features manually (since they were created after migration)
	if err := db.IndexEntity(database, "feature", f1.ID, f1.Name, f1.Description+" "+f1.Spec); err != nil {
		t.Fatalf("indexing feature: %v", err)
	}
	if err := db.IndexEntity(database, "feature", f2.ID, f2.Name, f2.Description); err != nil {
		t.Fatalf("indexing feature: %v", err)
	}

	// Search for "auth"
	results, err := db.SearchFTS(database, "auth", 10)
	if err != nil {
		t.Fatalf("searching FTS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'auth', got %d", len(results))
	}
	if results[0].EntityID != "auth-module" {
		t.Errorf("expected entity_id 'auth-module', got %q", results[0].EntityID)
	}
	if results[0].EntityType != "feature" {
		t.Errorf("expected entity_type 'feature', got %q", results[0].EntityType)
	}

	// Search for "search"
	results, err = db.SearchFTS(database, "search", 10)
	if err != nil {
		t.Fatalf("searching FTS: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'search', got %d", len(results))
	}
	if results[0].EntityID != "search-api" {
		t.Errorf("expected entity_id 'search-api', got %q", results[0].EntityID)
	}

	// Remove from index and verify
	if err := db.RemoveFromIndex(database, "feature", "auth-module"); err != nil {
		t.Fatalf("removing from index: %v", err)
	}
	results, err = db.SearchFTS(database, "auth", 10)
	if err != nil {
		t.Fatalf("searching FTS after removal: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after removal, got %d", len(results))
	}
}

func TestIndexEntityUpsert(t *testing.T) {
	database := openTestDB(t)

	// Create project
	p := &models.Project{ID: "test-proj", Name: "Test Project"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	// Index and then re-index with updated content
	if err := db.IndexEntity(database, "feature", "f1", "Original Title", "original content"); err != nil {
		t.Fatalf("first index: %v", err)
	}
	if err := db.IndexEntity(database, "feature", "f1", "Updated Title", "updated content"); err != nil {
		t.Fatalf("re-index: %v", err)
	}

	// Search for updated content should work
	results, err := db.SearchFTS(database, "updated", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Updated Title" {
		t.Errorf("expected 'Updated Title', got %q", results[0].Title)
	}

	// Original content should not be found
	results, err = db.SearchFTS(database, "original", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for old content, got %d", len(results))
	}
}

func TestBuildFTSQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"auth", "auth*"},
		{"user login", "user* login*"},
		{"feat", "feat*"},
		{"", ""},
		{"  spaces  ", "spaces*"},
		{"special(chars)", "special* chars*"},
		{`"quoted"`, "quoted*"},
		{"colon:value", "colon* value*"},
		{"AND keyword", "and* keyword*"},
		{"OR test", "or* test*"},
		{"NOT excluded", "not* excluded*"},
	}

	for _, tt := range tests {
		got := db.BuildFTSQuery(tt.input)
		if got != tt.want {
			t.Errorf("BuildFTSQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSearchFTSPrefixMatching(t *testing.T) {
	database := openTestDB(t)

	p := &models.Project{ID: "test-proj", Name: "Test Project"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	// Index features
	if err := db.IndexEntity(database, "feature", "auth-module", "Authentication Module", "JWT-based auth system with OAuth2"); err != nil {
		t.Fatalf("indexing: %v", err)
	}
	if err := db.IndexEntity(database, "feature", "search-api", "Search API", "Full-text search endpoint"); err != nil {
		t.Fatalf("indexing: %v", err)
	}
	if err := db.IndexEntity(database, "roadmap", "perf-item", "Performance Optimization", "Optimize query patterns and caching"); err != nil {
		t.Fatalf("indexing: %v", err)
	}

	// Prefix match: "auth" should match "authentication"
	results, err := db.SearchFTSFiltered(database, "auth", "", 10)
	if err != nil {
		t.Fatalf("prefix search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for prefix 'auth', got %d", len(results))
	}
	if results[0].EntityID != "auth-module" {
		t.Errorf("expected 'auth-module', got %q", results[0].EntityID)
	}

	// Prefix match: "sea" should match "search"
	results, err = db.SearchFTSFiltered(database, "sea", "", 10)
	if err != nil {
		t.Fatalf("prefix search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for prefix 'sea', got %d", len(results))
	}
	if results[0].EntityID != "search-api" {
		t.Errorf("expected 'search-api', got %q", results[0].EntityID)
	}
}

func TestSearchFTSFiltered(t *testing.T) {
	database := openTestDB(t)

	p := &models.Project{ID: "test-proj", Name: "Test Project"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	// Index mixed entity types
	if err := db.IndexEntity(database, "feature", "auth-feature", "Auth Feature", "authentication login"); err != nil {
		t.Fatalf("indexing: %v", err)
	}
	if err := db.IndexEntity(database, "roadmap", "auth-roadmap", "Auth Roadmap", "authentication roadmap item"); err != nil {
		t.Fatalf("indexing: %v", err)
	}

	// Unfiltered: both results
	results, err := db.SearchFTSFiltered(database, "auth", "", 10)
	if err != nil {
		t.Fatalf("unfiltered search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results unfiltered, got %d", len(results))
	}

	// Filtered to feature only
	results, err = db.SearchFTSFiltered(database, "auth", "feature", 10)
	if err != nil {
		t.Fatalf("filtered search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for feature filter, got %d", len(results))
	}
	if results[0].EntityType != "feature" {
		t.Errorf("expected entity_type 'feature', got %q", results[0].EntityType)
	}

	// Filtered to roadmap only
	results, err = db.SearchFTSFiltered(database, "auth", "roadmap", 10)
	if err != nil {
		t.Fatalf("filtered search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for roadmap filter, got %d", len(results))
	}
	if results[0].EntityType != "roadmap" {
		t.Errorf("expected entity_type 'roadmap', got %q", results[0].EntityType)
	}

	// Empty query returns nil
	results, err = db.SearchFTSFiltered(database, "", "", 10)
	if err != nil {
		t.Fatalf("empty query: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty query, got %d results", len(results))
	}
}

func TestSearchFTSSnippet(t *testing.T) {
	database := openTestDB(t)

	p := &models.Project{ID: "test-proj", Name: "Test Project"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	if err := db.IndexEntity(database, "feature", "f1", "Auth Module", "JWT-based authentication with OAuth2 support and refresh tokens"); err != nil {
		t.Fatalf("indexing: %v", err)
	}

	results, err := db.SearchFTSFiltered(database, "OAuth2", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Snippet should contain the highlight markers
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
}

func TestSearchFeaturesFTS(t *testing.T) {
	database := openTestDB(t)

	p := &models.Project{ID: "test-proj", Name: "Test Project"}
	if err := db.CreateProject(database, p); err != nil {
		t.Fatalf("creating project: %v", err)
	}

	// Create actual features
	f1 := &models.Feature{ID: "auth-module", ProjectID: p.ID, Name: "Authentication Module", Description: "JWT-based auth", Spec: "OAuth2 support"}
	f2 := &models.Feature{ID: "search-api", ProjectID: p.ID, Name: "Search API", Description: "Full-text search"}
	f3 := &models.Feature{ID: "other-proj-feature", ProjectID: "other-proj", Name: "Auth Other", Description: "Different project auth"}
	if err := db.CreateFeature(database, f1); err != nil {
		t.Fatalf("creating feature: %v", err)
	}
	if err := db.CreateFeature(database, f2); err != nil {
		t.Fatalf("creating feature: %v", err)
	}
	if err := db.CreateFeature(database, f3); err != nil {
		t.Fatalf("creating feature: %v", err)
	}

	// Index them
	if err := db.IndexEntity(database, "feature", f1.ID, f1.Name, f1.Description+" "+f1.Spec); err != nil {
		t.Fatalf("indexing: %v", err)
	}
	if err := db.IndexEntity(database, "feature", f2.ID, f2.Name, f2.Description); err != nil {
		t.Fatalf("indexing: %v", err)
	}
	if err := db.IndexEntity(database, "feature", f3.ID, f3.Name, f3.Description); err != nil {
		t.Fatalf("indexing: %v", err)
	}

	// Search for "auth" in test-proj — should only find f1, not f3
	results, err := db.SearchFeaturesFTS(database, p.ID, "auth", 10)
	if err != nil {
		t.Fatalf("feature search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 feature result for 'auth' in test-proj, got %d", len(results))
	}
	if results[0].ID != "auth-module" {
		t.Errorf("expected 'auth-module', got %q", results[0].ID)
	}
	if results[0].Name != "Authentication Module" {
		t.Errorf("expected name 'Authentication Module', got %q", results[0].Name)
	}
}

func TestInsertAndGetPerfMetrics(t *testing.T) {
	database := openTestDB(t)

	// Insert some command metrics
	if err := db.InsertCommandMetric(database, "feature list", 45.2, true, 3); err != nil {
		t.Fatalf("inserting metric: %v", err)
	}
	if err := db.InsertCommandMetric(database, "feature add", 120.5, true, 5); err != nil {
		t.Fatalf("inserting metric: %v", err)
	}
	if err := db.InsertCommandMetric(database, "feature list", 38.1, true, 3); err != nil {
		t.Fatalf("inserting metric: %v", err)
	}
	if err := db.InsertCommandMetric(database, "status", 200.0, false, 2); err != nil {
		t.Fatalf("inserting metric: %v", err)
	}

	summary, err := db.GetPerfSummary(database, 10)
	if err != nil {
		t.Fatalf("getting perf summary: %v", err)
	}

	if summary.TotalCommands != 4 {
		t.Errorf("expected 4 total commands, got %d", summary.TotalCommands)
	}
	if summary.SuccessRate < 74 || summary.SuccessRate > 76 {
		t.Errorf("expected ~75%% success rate, got %.1f%%", summary.SuccessRate)
	}
	if len(summary.ByCommand) == 0 {
		t.Error("expected per-command breakdown")
	}
	if len(summary.RecentSlow) == 0 {
		t.Error("expected recent slow commands")
	}

	// Verify the slowest command is first
	if summary.RecentSlow[0].Command != "status" {
		t.Errorf("expected slowest command to be 'status', got %q", summary.RecentSlow[0].Command)
	}
	if summary.RecentSlow[0].DurationMs != 200.0 {
		t.Errorf("expected 200ms for slowest, got %.1f", summary.RecentSlow[0].DurationMs)
	}
}

func TestGetPerfSummaryEmpty(t *testing.T) {
	database := openTestDB(t)

	summary, err := db.GetPerfSummary(database, 10)
	if err != nil {
		t.Fatalf("getting perf summary on empty DB: %v", err)
	}
	if summary.TotalCommands != 0 {
		t.Errorf("expected 0 commands, got %d", summary.TotalCommands)
	}
}

func TestCountPendingHighPriorityItems(t *testing.T) {
	database := openTestDB(t)
	db.CreateProject(database, &models.Project{ID: "p1", Name: "P1"}) //nolint:errcheck

	// Empty DB should return zeros.
	ideas, features, err := db.CountPendingHighPriorityItems(database)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ideas != 0 || features != 0 {
		t.Errorf("expected 0,0 got %d,%d", ideas, features)
	}

	// Insert pending ideas.
	database.Exec(`INSERT INTO idea_queue (project_id, title, raw_input, status) VALUES ('p1','idea1','raw1','pending')`)  //nolint:errcheck
	database.Exec(`INSERT INTO idea_queue (project_id, title, raw_input, status) VALUES ('p1','idea2','raw2','pending')`)  //nolint:errcheck
	database.Exec(`INSERT INTO idea_queue (project_id, title, raw_input, status) VALUES ('p1','idea3','raw3','approved')`) //nolint:errcheck

	// Insert features with varying priorities and statuses.
	database.Exec(`INSERT INTO features (id, project_id, name, status, priority) VALUES ('f1','p1','F1','draft',9)`)        //nolint:errcheck
	database.Exec(`INSERT INTO features (id, project_id, name, status, priority) VALUES ('f2','p1','F2','implementing',8)`) //nolint:errcheck
	database.Exec(`INSERT INTO features (id, project_id, name, status, priority) VALUES ('f3','p1','F3','done',10)`)        //nolint:errcheck
	database.Exec(`INSERT INTO features (id, project_id, name, status, priority) VALUES ('f4','p1','F4','draft',5)`)        //nolint:errcheck

	ideas, features, err = db.CountPendingHighPriorityItems(database)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ideas != 2 {
		t.Errorf("expected 2 pending ideas, got %d", ideas)
	}
	// f1(9,draft) + f2(8,implementing) = 2; f3 excluded (done), f4 excluded (priority 5)
	if features != 2 {
		t.Errorf("expected 2 high-priority features, got %d", features)
	}
}
