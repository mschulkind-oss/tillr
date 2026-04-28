package orchestrator

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
)

// setupProject creates a tmp project dir with .tillr.json + .claude/agents/<name>.md
// and an open SQLite database with one project row. Returns (root, db).
func setupProject(t *testing.T, personas ...string) (string, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	cfgPath := filepath.Join(root, ".tillr.json")
	if err := os.WriteFile(cfgPath, []byte(`{"db_path":"tillr.db","server_port":3847}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	agents := filepath.Join(root, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatalf("mkdir agents: %v", err)
	}
	for _, p := range personas {
		if err := os.WriteFile(filepath.Join(agents, p+".md"),
			[]byte("---\nname: "+p+"\n---\n"), 0o644); err != nil {
			t.Fatalf("write agent: %v", err)
		}
	}
	dbPath := filepath.Join(root, "tillr.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.CreateProject(database, "test"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return root, database
}

// fakeSpawn returns a SpawnFunc that returns the given Spawned value
// every time. It also records each invocation's opts for assertion.
func fakeSpawn(t *testing.T, want Spawned) (SpawnFunc, *[]SpawnOpts) {
	t.Helper()
	calls := []SpawnOpts{}
	fn := func(_ context.Context, opts SpawnOpts) Spawned {
		calls = append(calls, opts)
		return want
	}
	return fn, &calls
}

func TestDaemon_DispatchesAndAppliesSideEffects(t *testing.T) {
	root, database := setupProject(t, "implementer")
	project, _ := db.GetProject(database)

	feat, err := db.AddFeature(database, project.ID, "do the thing", "details", "implementer")
	if err != nil {
		t.Fatalf("add feature: %v", err)
	}

	want := Spawned{
		ExitCode:    0,
		DurationMS:  42,
		CostUSD:     0.0123,
		InputTokens: 100, OutputTokens: 200,
		SessionID: "sess-abc",
		Model:     "claude-sonnet-4-6",
		Result: Result{
			Summary:      "Implemented the thing.",
			ContextEntry: "Used pattern X. Avoid Y.",
			FilesTouched: []string{"foo.go"},
			FollowUpFeatures: []FollowUpFeature{
				{Persona: "reviewer", Title: "Review the thing", Description: "see PR"},
			},
			Result: "completed",
		},
	}
	spawn, calls := fakeSpawn(t, want)

	d := NewDaemon(database, Config{
		ProjectRoot:    root,
		MaxParallelism: 1,
		PollInterval:   50 * time.Millisecond,
	}, spawn)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	go func() {
		_ = d.Run(ctx)
	}()

	// Wait until run is finished (status leaves 'running'/'draft').
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		runs, _ := db.ListOrchestratorRuns(database, feat.ID, 0)
		if len(runs) > 0 && runs[0].Result != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(*calls) != 1 {
		t.Fatalf("want 1 spawn call, got %d", len(*calls))
	}
	if (*calls)[0].Persona != "implementer" {
		t.Errorf("wrong persona: %s", (*calls)[0].Persona)
	}
	if (*calls)[0].FeatureID != feat.ID {
		t.Errorf("wrong feature_id: %d", (*calls)[0].FeatureID)
	}

	// Side effects:

	// (a) feature transitioned to 'done'
	got, err := db.GetFeature(database, feat.ID)
	if err != nil {
		t.Fatalf("get feature: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("feature status %q, want done", got.Status)
	}

	// (b) comment was posted
	comments, err := db.ListComments(database, "feature", strconv.FormatInt(feat.ID, 10))
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("want 1 comment, got %d", len(comments))
	}
	if comments[0].AuthorRole != "implementer" {
		t.Errorf("wrong comment role: %s", comments[0].AuthorRole)
	}
	if !strings.Contains(comments[0].Body, "Implemented the thing") {
		t.Errorf("comment body wrong: %q", comments[0].Body)
	}

	// (c) persona context was appended (orchestrator owns this — Principle Zero)
	ctxData, err := os.ReadFile(filepath.Join(root, "swarf", "agents", "implementer", "context.md"))
	if err != nil {
		t.Fatalf("read persona context: %v", err)
	}
	if !strings.Contains(string(ctxData), "Used pattern X") {
		t.Errorf("persona context missing entry: %q", string(ctxData))
	}

	// (d) follow-up feature was filed
	feats, err := db.ListFeatures(database, project.ID, db.ListFeaturesFilter{Persona: "reviewer"})
	if err != nil {
		t.Fatalf("list features: %v", err)
	}
	if len(feats) != 1 {
		t.Fatalf("want 1 follow-up, got %d", len(feats))
	}
	if feats[0].Title != "Review the thing" {
		t.Errorf("wrong follow-up title: %s", feats[0].Title)
	}

	// (e) orchestrator_runs row finished correctly
	runs, err := db.ListOrchestratorRuns(database, feat.ID, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	r := runs[0]
	if r.Result != "completed" {
		t.Errorf("run result %q, want completed", r.Result)
	}
	if r.CostUSD == nil || *r.CostUSD != 0.0123 {
		t.Errorf("run cost wrong: %+v", r.CostUSD)
	}
	if r.SessionID != "sess-abc" {
		t.Errorf("run session_id wrong: %s", r.SessionID)
	}
}

func TestDaemon_RespectsMaxParallelism(t *testing.T) {
	root, database := setupProject(t, "implementer", "researcher")
	project, _ := db.GetProject(database)
	for i := 0; i < 4; i++ {
		_, err := db.AddFeature(database, project.ID, "feat", "", "implementer")
		if err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	for i := 0; i < 4; i++ {
		_, err := db.AddFeature(database, project.ID, "feat", "", "researcher")
		if err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	// blockingSpawn lets us count concurrent in-flight workers.
	type counter struct {
		max     int
		current int
	}
	var c counter
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}

	spawn := SpawnFunc(func(ctx context.Context, opts SpawnOpts) Spawned {
		<-mu
		c.current++
		if c.current > c.max {
			c.max = c.current
		}
		mu <- struct{}{}
		// hold long enough that we'd see >max if it weren't capped
		time.Sleep(80 * time.Millisecond)
		<-mu
		c.current--
		mu <- struct{}{}
		return Spawned{
			ExitCode: 0,
			Result:   Result{Summary: "ok", Result: "completed"},
		}
	})

	d := NewDaemon(database, Config{
		ProjectRoot:    root,
		MaxParallelism: 2,
		PollInterval:   30 * time.Millisecond,
	}, spawn)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if c.max > 2 {
		t.Fatalf("max-parallelism violated: peak in-flight = %d (cap was 2)", c.max)
	}
	if c.max < 2 {
		t.Logf("peak in-flight = %d (under cap; OK if total work didn't require it)", c.max)
	}
}

func TestDaemon_BlockedResultStaysBlocked(t *testing.T) {
	root, database := setupProject(t, "implementer")
	project, _ := db.GetProject(database)
	feat, _ := db.AddFeature(database, project.ID, "needs help", "", "implementer")

	spawn := SpawnFunc(func(_ context.Context, _ SpawnOpts) Spawned {
		return Spawned{
			ExitCode: 0,
			Result:   Result{Summary: "Need API key.", Result: "blocked", Blocker: "missing GOOGLE_API_KEY"},
		}
	})

	d := NewDaemon(database, Config{
		ProjectRoot:    root,
		MaxParallelism: 1,
		PollInterval:   30 * time.Millisecond,
	}, spawn)
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	_ = d.Run(ctx)

	got, _ := db.GetFeature(database, feat.ID)
	if got.Status != "blocked" {
		t.Errorf("want blocked, got %q", got.Status)
	}
	runs, _ := db.ListOrchestratorRuns(database, feat.ID, 0)
	if len(runs) != 1 || runs[0].Result != "blocked" {
		t.Errorf("expected one blocked run, got %+v", runs)
	}
}
