package engine_test

import (
	"testing"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
)

func TestInitProject(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	p, err := engine.InitProject(database, "My Project")
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if p.ID != "my-project" {
		t.Errorf("got id %q, want my-project", p.ID)
	}
	if p.Name != "My Project" {
		t.Errorf("got name %q", p.Name)
	}
}

func TestAddFeature(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()               //nolint:errcheck
	engine.InitProject(database, "Test") //nolint:errcheck

	f, err := engine.AddFeature(database, "test", "Cool Feature", "A cool feature", "", "", 5, nil, "")
	if err != nil {
		t.Fatalf("add feature: %v", err)
	}
	if f.ID != "cool-feature" {
		t.Errorf("got id %q, want cool-feature", f.ID)
	}
}

func TestTransitionFeature(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	if err := engine.TransitionFeature(database, "test", "f1", "implementing"); err != nil {
		t.Fatalf("transition: %v", err)
	}

	f, _ := db.GetFeature(database, "f1")
	if f.Status != "implementing" {
		t.Errorf("got %q, want implementing", f.Status)
	}
}

func TestTransitionFeatureInvalid(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	// draft → done should be rejected (must go through human-qa)
	if err := engine.TransitionFeature(database, "test", "f1", "done"); err == nil {
		t.Error("expected error for draft → done transition")
	}

	// implementing → done should be rejected
	engine.TransitionFeature(database, "test", "f1", "implementing") //nolint:errcheck
	if err := engine.TransitionFeature(database, "test", "f1", "done"); err == nil {
		t.Error("expected error for implementing → done transition")
	}

	// agent-qa → done should be rejected
	engine.TransitionFeature(database, "test", "f1", "agent-qa") //nolint:errcheck
	if err := engine.TransitionFeature(database, "test", "f1", "done"); err == nil {
		t.Error("expected error for agent-qa → done transition")
	}

	// agent-qa → human-qa → done should succeed
	engine.TransitionFeature(database, "test", "f1", "human-qa") //nolint:errcheck
	if err := engine.TransitionFeature(database, "test", "f1", "done"); err != nil {
		t.Fatalf("human-qa → done should be valid: %v", err)
	}
	f, _ := db.GetFeature(database, "f1")
	if f.Status != "done" {
		t.Errorf("got %q, want done", f.Status)
	}
}

func TestIsValidTransition(t *testing.T) {
	tests := []struct {
		from, to string
		valid    bool
	}{
		{"draft", "planning", true},
		{"draft", "implementing", true},
		{"draft", "blocked", true},
		{"draft", "done", false},
		{"draft", "human-qa", false},
		{"implementing", "agent-qa", true},
		{"implementing", "human-qa", true},
		{"implementing", "done", false},
		{"agent-qa", "human-qa", true},
		{"agent-qa", "implementing", true},
		{"agent-qa", "done", false},
		{"human-qa", "done", true},
		{"human-qa", "implementing", true},
		{"blocked", "implementing", true},
		{"done", "implementing", true},
	}
	for _, tt := range tests {
		got := engine.IsValidTransition(tt.from, tt.to)
		if got != tt.valid {
			t.Errorf("IsValidTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.valid)
		}
	}
}

func TestWorkItemFlow(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	// Create work item
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "implement", AgentPrompt: "Build it",
	})

	// Get next
	w, err := engine.GetNextWorkItem(database)
	if err != nil {
		t.Fatalf("get next: %v", err)
	}
	if w.WorkType != "implement" {
		t.Errorf("got %q", w.WorkType)
	}

	// Complete
	if err := engine.CompleteWorkItem(database, "done!"); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// No more work
	_, err = engine.GetNextWorkItem(database)
	if err == nil {
		t.Error("expected error for no work items")
	}
}

func TestCycleFlow(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	// Start cycle
	c, err := engine.StartCycle(database, "test", "f1", "bug-triage")
	if err != nil {
		t.Fatalf("start cycle: %v", err)
	}
	if c.StepName != "report" {
		t.Errorf("got step %q, want report", c.StepName)
	}

	// Score and advance
	if err := engine.ScoreCycleStep(database, "test", "f1", 9.0, "good"); err != nil {
		t.Fatalf("score: %v", err)
	}

	// Check advanced
	active, _ := db.GetActiveCycle(database, "f1")
	if active.CurrentStep != 1 {
		t.Errorf("got step %d, want 1", active.CurrentStep)
	}

	// Can't start another
	_, err = engine.StartCycle(database, "test", "f1", "bug-triage")
	if err == nil {
		t.Error("expected error for duplicate cycle")
	}
}

func TestQAFlow(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck
	engine.TransitionFeature(database, "test", "f1", "implementing")  //nolint:errcheck
	engine.TransitionFeature(database, "test", "f1", "human-qa")      //nolint:errcheck

	// Reject
	if err := engine.RejectFeatureQA(database, "test", "f1", "needs work"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	f, _ := db.GetFeature(database, "f1")
	if f.Status != "implementing" {
		t.Errorf("after reject got %q, want implementing", f.Status)
	}

	// Put back to QA and approve
	engine.TransitionFeature(database, "test", "f1", "human-qa") //nolint:errcheck
	if err := engine.ApproveFeatureQA(database, "test", "f1", "looks good"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	f, _ = db.GetFeature(database, "f1")
	if f.Status != "done" {
		t.Errorf("after approve got %q, want done", f.Status)
	}
}

func TestQAFlowRequiresHumanQAStatus(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck
	engine.TransitionFeature(database, "test", "f1", "implementing")  //nolint:errcheck

	// Cannot approve a feature not in human-qa
	if err := engine.ApproveFeatureQA(database, "test", "f1", "looks good"); err == nil {
		t.Error("expected error approving feature not in human-qa status")
	}

	// Cannot reject a feature not in human-qa
	if err := engine.RejectFeatureQA(database, "test", "f1", "bad"); err == nil {
		t.Error("expected error rejecting feature not in human-qa status")
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Hello World", "hello-world"},
		{"My Cool Feature!", "my-cool-feature"},
		{"test-123", "test-123"},
		{"UPPER CASE", "upper-case"},
	}
	for _, tt := range tests {
		got := engine.Slug(tt.input)
		if got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBlockingCascade(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	p, err := engine.InitProject(database, "Cascade Test")
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Create A → B → C dependency chain (C depends on B, B depends on A)
	_, err = engine.AddFeature(database, p.ID, "Feature A", "base feature", "", "", 1, nil, "")
	if err != nil {
		t.Fatalf("add A: %v", err)
	}
	_, err = engine.AddFeature(database, p.ID, "Feature B", "middle feature", "", "", 2, []string{"feature-a"}, "")
	if err != nil {
		t.Fatalf("add B: %v", err)
	}
	_, err = engine.AddFeature(database, p.ID, "Feature C", "end feature", "", "", 3, []string{"feature-b"}, "")
	if err != nil {
		t.Fatalf("add C: %v", err)
	}

	// Move B and C to implementing
	if err := engine.TransitionFeature(database, p.ID, "feature-b", "implementing"); err != nil {
		t.Fatalf("transition B: %v", err)
	}
	if err := engine.TransitionFeature(database, p.ID, "feature-c", "implementing"); err != nil {
		t.Fatalf("transition C: %v", err)
	}

	// Block A — B and C should cascade to blocked
	if err := engine.TransitionFeature(database, p.ID, "feature-a", "blocked"); err != nil {
		t.Fatalf("block A: %v", err)
	}

	b, err := db.GetFeature(database, "feature-b")
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if b.Status != "blocked" {
		t.Errorf("expected B to be blocked, got %q", b.Status)
	}
	if b.PreviousStatus != "implementing" {
		t.Errorf("expected B previous_status=implementing, got %q", b.PreviousStatus)
	}

	c, err := db.GetFeature(database, "feature-c")
	if err != nil {
		t.Fatalf("get C: %v", err)
	}
	if c.Status != "blocked" {
		t.Errorf("expected C to be blocked, got %q", c.Status)
	}
	if c.PreviousStatus != "implementing" {
		t.Errorf("expected C previous_status=implementing, got %q", c.PreviousStatus)
	}

	// Unblock A — B and C should restore to implementing
	if err := engine.TransitionFeature(database, p.ID, "feature-a", "implementing"); err != nil {
		t.Fatalf("unblock A: %v", err)
	}

	b, err = db.GetFeature(database, "feature-b")
	if err != nil {
		t.Fatalf("get B: %v", err)
	}
	if b.Status != "implementing" {
		t.Errorf("expected B to be restored to implementing, got %q", b.Status)
	}

	c, err = db.GetFeature(database, "feature-c")
	if err != nil {
		t.Fatalf("get C: %v", err)
	}
	if c.Status != "implementing" {
		t.Errorf("expected C to be restored to implementing, got %q", c.Status)
	}
}

func TestBlockingCascadePartialUnblock(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	p, err := engine.InitProject(database, "Partial Unblock")
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Create A, B both depended on by C
	_, err = engine.AddFeature(database, p.ID, "Feature A", "", "", "", 1, nil, "")
	if err != nil {
		t.Fatalf("add A: %v", err)
	}
	_, err = engine.AddFeature(database, p.ID, "Feature B", "", "", "", 2, nil, "")
	if err != nil {
		t.Fatalf("add B: %v", err)
	}
	_, err = engine.AddFeature(database, p.ID, "Feature C", "", "", "", 3, []string{"feature-a", "feature-b"}, "")
	if err != nil {
		t.Fatalf("add C: %v", err)
	}

	// Move C to implementing
	if err := engine.TransitionFeature(database, p.ID, "feature-c", "implementing"); err != nil {
		t.Fatalf("transition C: %v", err)
	}

	// Block both A and B
	if err := engine.TransitionFeature(database, p.ID, "feature-a", "blocked"); err != nil {
		t.Fatalf("block A: %v", err)
	}
	if err := engine.TransitionFeature(database, p.ID, "feature-b", "blocked"); err != nil {
		t.Fatalf("block B: %v", err)
	}

	c, _ := db.GetFeature(database, "feature-c")
	if c.Status != "blocked" {
		t.Errorf("expected C blocked, got %q", c.Status)
	}

	// Unblock A — C should remain blocked because B is still blocked
	if err := engine.TransitionFeature(database, p.ID, "feature-a", "implementing"); err != nil {
		t.Fatalf("unblock A: %v", err)
	}

	c, _ = db.GetFeature(database, "feature-c")
	if c.Status != "blocked" {
		t.Errorf("expected C still blocked (B still blocked), got %q", c.Status)
	}

	// Unblock B — now C should be restored
	if err := engine.TransitionFeature(database, p.ID, "feature-b", "implementing"); err != nil {
		t.Fatalf("unblock B: %v", err)
	}

	c, _ = db.GetFeature(database, "feature-c")
	if c.Status != "implementing" {
		t.Errorf("expected C restored to implementing, got %q", c.Status)
	}
}

func TestBlockingCascadeSkipsDone(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close() //nolint:errcheck

	p, err := engine.InitProject(database, "Skip Done")
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	// Create A → B where B is done
	_, err = engine.AddFeature(database, p.ID, "Feature A", "", "", "", 1, nil, "")
	if err != nil {
		t.Fatalf("add A: %v", err)
	}
	_, err = engine.AddFeature(database, p.ID, "Feature B", "", "", "", 2, []string{"feature-a"}, "")
	if err != nil {
		t.Fatalf("add B: %v", err)
	}

	// Move B through to done
	if err := engine.TransitionFeature(database, p.ID, "feature-b", "implementing"); err != nil {
		t.Fatalf("transition B to implementing: %v", err)
	}
	if err := engine.TransitionFeature(database, p.ID, "feature-b", "human-qa"); err != nil {
		t.Fatalf("transition B to human-qa: %v", err)
	}
	if err := engine.TransitionFeature(database, p.ID, "feature-b", "done"); err != nil {
		t.Fatalf("transition B to done: %v", err)
	}

	// Block A — B should NOT be affected since it's done
	if err := engine.TransitionFeature(database, p.ID, "feature-a", "blocked"); err != nil {
		t.Fatalf("block A: %v", err)
	}

	b, _ := db.GetFeature(database, "feature-b")
	if b.Status != "done" {
		t.Errorf("expected B to remain done, got %q", b.Status)
	}
}

// --- Multi-Agent Coordination Tests ---

func TestAgentTaskClaiming(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	// Create two work items
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "implement", AgentPrompt: "Task 1",
	})
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "test", AgentPrompt: "Task 2",
	})

	// Agent A claims the first work item
	w1, err := engine.GetNextWorkItem(database, "agent-a")
	if err != nil {
		t.Fatalf("agent-a get next: %v", err)
	}
	if w1.AssignedAgent != "agent-a" {
		t.Errorf("expected assigned_agent=agent-a, got %q", w1.AssignedAgent)
	}
	if w1.Status != "active" {
		t.Errorf("expected status=active, got %q", w1.Status)
	}

	// Agent B should get the second work item (first is claimed)
	w2, err := engine.GetNextWorkItem(database, "agent-b")
	if err != nil {
		t.Fatalf("agent-b get next: %v", err)
	}
	if w2.ID == w1.ID {
		t.Error("agent-b got the same work item as agent-a")
	}
	if w2.AssignedAgent != "agent-b" {
		t.Errorf("expected assigned_agent=agent-b, got %q", w2.AssignedAgent)
	}

	// Agent A asking again should get their already-claimed item back
	w1Again, err := engine.GetNextWorkItem(database, "agent-a")
	if err != nil {
		t.Fatalf("agent-a get next again: %v", err)
	}
	if w1Again.ID != w1.ID {
		t.Errorf("agent-a should get same item back, got #%d instead of #%d", w1Again.ID, w1.ID)
	}

	// No more work items for a new agent
	_, err = engine.GetNextWorkItem(database, "agent-c")
	if err == nil {
		t.Error("expected error for no remaining work items")
	}
}

func TestAgentClaimWorkItem(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "implement", AgentPrompt: "Build it",
	})

	// Claim the work item
	err := db.ClaimWorkItem(database, 1, "agent-x")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Verify it's claimed
	w, err := db.GetWorkItemByID(database, 1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if w.AssignedAgent != "agent-x" {
		t.Errorf("expected agent-x, got %q", w.AssignedAgent)
	}
	if w.Status != "active" {
		t.Errorf("expected active, got %q", w.Status)
	}

	// Double-claim should fail
	err = db.ClaimWorkItem(database, 1, "agent-y")
	if err == nil {
		t.Error("expected error for double-claim")
	}
}

func TestConflictDetection(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	// Create two work items for the same feature
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "implement", AgentPrompt: "Task 1",
	})
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "test", AgentPrompt: "Task 2",
	})

	// Two agents claim work on the same feature
	_, err := engine.GetNextWorkItem(database, "agent-a")
	if err != nil {
		t.Fatalf("agent-a: %v", err)
	}
	_, err = engine.GetNextWorkItem(database, "agent-b")
	if err != nil {
		t.Fatalf("agent-b: %v", err)
	}

	// Should detect a conflict
	conflicts, err := db.DetectConflicts(database)
	if err != nil {
		t.Fatalf("detect conflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].FeatureID != "f1" {
		t.Errorf("expected feature f1, got %q", conflicts[0].FeatureID)
	}
	if len(conflicts[0].Agents) != 2 {
		t.Errorf("expected 2 agents in conflict, got %d", len(conflicts[0].Agents))
	}
}

func TestCoordinationStatus(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 0, nil, "") //nolint:errcheck

	// Create a work item
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "implement", AgentPrompt: "Build",
	})

	// Get coordination status
	status, err := engine.GetCoordinationStatus(database, "test")
	if err != nil {
		t.Fatalf("coordination status: %v", err)
	}
	if status.QueueDepth != 1 {
		t.Errorf("expected queue_depth=1, got %d", status.QueueDepth)
	}
	if status.ClaimedItems != 0 {
		t.Errorf("expected claimed_items=0, got %d", status.ClaimedItems)
	}

	// Claim it
	_, err = engine.GetNextWorkItem(database, "agent-a")
	if err != nil {
		t.Fatalf("get next: %v", err)
	}

	status, err = engine.GetCoordinationStatus(database, "test")
	if err != nil {
		t.Fatalf("coordination status after claim: %v", err)
	}
	if status.QueueDepth != 0 {
		t.Errorf("expected queue_depth=0, got %d", status.QueueDepth)
	}
	if status.ClaimedItems != 1 {
		t.Errorf("expected claimed_items=1, got %d", status.ClaimedItems)
	}
}

func TestPriorityBasedQueueOrdering(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()               //nolint:errcheck
	engine.InitProject(database, "Test") //nolint:errcheck

	// Create features with different priorities
	engine.AddFeature(database, "test", "Low Priority", "", "", "", 1, nil, "")   //nolint:errcheck
	engine.AddFeature(database, "test", "High Priority", "", "", "", 10, nil, "") //nolint:errcheck

	// Create work items — low priority first (created earlier)
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "low-priority", WorkType: "develop", AgentPrompt: "Low prio task",
	})
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "high-priority", WorkType: "develop", AgentPrompt: "High prio task",
	})

	// GetNextWorkItem should return the high-priority item first
	w, err := engine.GetNextWorkItem(database, "agent-a")
	if err != nil {
		t.Fatalf("get next: %v", err)
	}
	if w.FeatureID != "high-priority" {
		t.Errorf("expected high-priority first, got %q", w.FeatureID)
	}
}

func TestQueueStatsAndList(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 5, nil, "") //nolint:errcheck

	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "develop", AgentPrompt: "Task 1",
	})
	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "test", AgentPrompt: "Task 2",
	})

	// Check stats — 2 pending
	stats, err := db.GetQueueStats(database)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalPending != 2 {
		t.Errorf("expected 2 pending, got %d", stats.TotalPending)
	}
	if stats.TotalClaimed != 0 {
		t.Errorf("expected 0 claimed, got %d", stats.TotalClaimed)
	}

	// Queue list should have 2 items
	items, err := db.GetQueuedWorkItems(database)
	if err != nil {
		t.Fatalf("queue list: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 queue items, got %d", len(items))
	}
	if items[0].FeatureName != "F1" {
		t.Errorf("expected feature name F1, got %q", items[0].FeatureName)
	}
	if items[0].Priority != 5 {
		t.Errorf("expected priority 5, got %d", items[0].Priority)
	}

	// Claim one
	engine.GetNextWorkItem(database, "agent-a") //nolint:errcheck

	stats, err = db.GetQueueStats(database)
	if err != nil {
		t.Fatalf("stats after claim: %v", err)
	}
	if stats.TotalPending != 1 {
		t.Errorf("expected 1 pending after claim, got %d", stats.TotalPending)
	}
	if stats.TotalClaimed != 1 {
		t.Errorf("expected 1 claimed after claim, got %d", stats.TotalClaimed)
	}
}

func TestReleaseWorkItem(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 5, nil, "") //nolint:errcheck

	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "develop", AgentPrompt: "Task 1",
	})

	// Claim work item
	w, err := engine.GetNextWorkItem(database, "agent-a")
	if err != nil {
		t.Fatalf("get next: %v", err)
	}
	if w.Status != "active" {
		t.Fatalf("expected active, got %q", w.Status)
	}

	// Release it back to pending
	if err := db.ReleaseWorkItem(database, w.ID); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Verify it's pending again
	w2, err := db.GetWorkItemByID(database, w.ID)
	if err != nil {
		t.Fatalf("get after release: %v", err)
	}
	if w2.Status != "pending" {
		t.Errorf("expected pending after release, got %q", w2.Status)
	}
	if w2.AssignedAgent != "" {
		t.Errorf("expected empty agent after release, got %q", w2.AssignedAgent)
	}

	// Cannot release a pending item
	if err := db.ReleaseWorkItem(database, w.ID); err == nil {
		t.Error("expected error releasing pending item")
	}
}

func TestReclaimStaleWorkItems(t *testing.T) {
	database, _ := db.Open(":memory:")
	defer database.Close()                                            //nolint:errcheck
	engine.InitProject(database, "Test")                              //nolint:errcheck
	engine.AddFeature(database, "test", "F1", "", "", "", 5, nil, "") //nolint:errcheck

	db.CreateWorkItem(database, &models.WorkItem{ //nolint:errcheck
		FeatureID: "f1", WorkType: "develop", AgentPrompt: "Task 1",
	})

	// Claim it
	w, err := engine.GetNextWorkItem(database, "agent-stale")
	if err != nil {
		t.Fatalf("get next: %v", err)
	}

	// Backdate started_at to simulate a stale claim (45 minutes ago)
	database.Exec("UPDATE work_items SET started_at = datetime('now', '-45 minutes') WHERE id = ?", w.ID) //nolint:errcheck

	// Reclaim with 30-minute threshold — should reclaim since started 45 min ago with no heartbeat
	reclaimed, err := engine.ReclaimStaleWorkItems(database, 30)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed != 1 {
		t.Errorf("expected 1 reclaimed, got %d", reclaimed)
	}

	// Verify it's pending again
	stats, err := db.GetQueueStats(database)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalPending != 1 {
		t.Errorf("expected 1 pending after reclaim, got %d", stats.TotalPending)
	}
	if stats.TotalClaimed != 0 {
		t.Errorf("expected 0 claimed after reclaim, got %d", stats.TotalClaimed)
	}
}
