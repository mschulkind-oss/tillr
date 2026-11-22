package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type depBlockedWorkflowSteps struct {
	*workflowSteps
	onPlanStart func()
	buildBrief  string
	buildHead   string
	depTip      string
}

func (s *depBlockedWorkflowSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	s.gates++
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *depBlockedWorkflowSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	out, err := s.workflowSteps.Land(ctx, c)
	if err == nil && c.ItemID == "item-b" {
		s.depTip = runGit(s.t, s.f.root, "rev-parse", TrunkRef)
	}
	return out, err
}

func (s *depBlockedWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	report := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(report, []byte("RESULT: done\n"), 0644); err != nil {
		return run.Record{}, "", err
	}
	if req.ItemID == "item-a" && req.NodeID == "plan" {
		if s.onPlanStart != nil {
			s.onPlanStart()
		}
		outDir := nodeOutputsDir(s.f.root, req.RunID)
		writeForkFile(s.t, filepath.Join(outDir, deliveryOutput), `{"kind":"repo"}`+"\n")
		writeForkFile(s.t, filepath.Join(outDir, "plan"), "finished plan output\n")
	} else {
		target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
		if req.ItemID == "item-a" && req.NodeID == "build" {
			s.buildHead = runGit(s.t, target, "rev-parse", "HEAD")
			if raw, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile)); err == nil {
				s.buildBrief = string(raw)
			}
		}
		dir := filepath.Join(target, "tools", req.ItemID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(dir, req.RunID+".txt"), "finished\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", req.ItemID+"-"+req.RunID)
	}
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		EndCause:     run.EndNormalExit,
		StartedAt:    "2026-11-22T05:48:50Z",
		FinishedAt:   "2026-11-22T05:59:24Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", err
}

// Reproduces the 2026-11-22 incident (item 9a70a13f96): a dependency is added with
// `tillr item dep --add` while an item's plan step is running, the plan step exits
// normally with valid delivery and plan outputs, and the dependency later lands.
// The finished plan step must settle instead of being abandoned by workflow-invalidated,
// and once the dependency lands the item must continue from the plan's outputs without
// ever holding as run-link-missing or requiring operator intervention.
func TestDependencyAddedWhilePlanRunsSettlesAndContinuesAfterDepLands(t *testing.T) {
	f, l, old := workflowLoopFixture(t, "")
	planBuildWorkflow := `{"name":"plan-then-build","version":1,"stages":[` +
		`{"id":"plan","kind":"agent","seat":"plan","access":"read_only","outputs":{"delivery":"json","plan":"markdown"}},` +
		`{"id":"build","kind":"agent","seat":"build","access":"write","inputs":{"plan":"markdown"}}` +
		`],"edges":[{"from_node":"plan","from_output":"plan","to_node":"build","to_input":"plan"}]}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/plan-then-build@v1.json"), planBuildWorkflow)
	writeForkFile(t, filepath.Join(f.root, "briefs/item-b.md"), "# Task\nLand the dependency.\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "plan-then-build workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='plan-then-build@v1' WHERE item_id='item-a'`)
		return err
	})
	l.cfg.Policy.Seats["plan"] = "fixture"

	depAdded := false
	steps := &depBlockedWorkflowSteps{
		workflowSteps: old,
		onPlanStart: func() {
			if depAdded {
				return
			}
			depAdded = true
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := item.SetDeps(context.Background(), tx, "item-a", []string{"item-b"}, nil, l.cfg.Now())
				return err
			})
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}

	// Pass 1: dispatch item-a's plan step; while it runs, add item-b as a dependency; plan exits normally.
	graphPass(t, l, result)
	if !depAdded {
		t.Fatal("plan step did not run and trigger dependency addition")
	}
	if len(steps.runs) != 1 || steps.runs[0].ItemID != "item-a" || steps.runs[0].NodeID != "plan" {
		t.Fatalf("unexpected runs after pass 1: %+v", steps.runs)
	}
	planRunID := steps.runs[0].RunID

	// Before settlement: item-a must plan ActionSettle (not WorkflowRelease / workflow-invalidated).
	beforeSettle := decisionFor(t, l, "item-a").decision
	if beforeSettle.Action != ActionSettle || beforeSettle.WorkflowRelease {
		t.Fatalf("finished plan step while dependency-blocked planned %+v, want ActionSettle without WorkflowRelease", beforeSettle)
	}

	// Pass 2: settle the finished plan step. Because item-a is blocked on item-b, build must not dispatch,
	// and the lane must be released so item-b can run even on a single-lane queue.
	graphPass(t, l, result)

	// Verify plan's node_runs row is settled as passed (never left 'running')
	// and its delivery output was recorded.
	s := openFixture(t, f.storePath)
	var planOutcome string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, planRunID).Scan(&planOutcome)
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	if planOutcome != "passed" {
		t.Fatalf("plan node_runs outcome = %q, want passed", planOutcome)
	}
	delivery, err := l.recordedDelivery(context.Background(), "item-a")
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Kind != "repo" {
		t.Fatalf("delivery = %+v, want kind=repo recorded from finished plan", delivery)
	}
	w := pendingForTest(t, f, "item-a")
	if w == nil || w.Done || !w.Ready || w.Decision.NodeIndex != 1 {
		t.Fatalf("settled plan did not retain ready build continuation: %+v", w)
	}

	// While item-b is still in-queue, item-a must hold as ReasonBlocked (never run-link-missing or WorkflowRelease).
	held := decisionFor(t, l, "item-a").decision
	if held.Action != ActionHold || held.Reason != ReasonBlocked || held.WorkflowRelease {
		t.Fatalf("item-a while blocked on item-b planned %+v, want hold / %s", held, ReasonBlocked)
	}

	// Drive item-b to landing on the same single lane (lane-1).
	for i := 0; i < 6; i++ {
		if d := decisionFor(t, l, "item-a").decision; strings.HasPrefix(d.Reason, ReasonRunLinkMissing) {
			t.Fatalf("item-a held as run-link-missing: %+v", d)
		}
		graphPass(t, l, result)
		if result.Landed >= 2 {
			break
		}
	}

	// Verify both item-b and item-a landed without any operator retry, and plan was NOT rerun.
	if result.Landed != 2 {
		t.Fatalf("landed = %d, want 2 (item-b then item-a); item-a decision = %+v", result.Landed, decisionFor(t, l, "item-a").decision)
	}
	planRuns, buildRuns := 0, 0
	for _, r := range steps.runs {
		if r.ItemID != "item-a" {
			continue
		}
		switch r.NodeID {
		case "plan":
			planRuns++
		case "build":
			buildRuns++
		}
	}
	if planRuns != 1 || buildRuns != 1 {
		t.Fatalf("item-a ran plan=%d build=%d, want 1 and 1 (runs=%+v)", planRuns, buildRuns, steps.runs)
	}
	planOutputPath := filepath.Join(nodeOutputsDir(f.root, planRunID), "plan")
	if !strings.Contains(steps.buildBrief, planOutputPath) {
		t.Fatalf("build brief did not reference finished plan output %s", planOutputPath)
	}
	if steps.depTip == "" || steps.buildHead != steps.depTip {
		t.Fatalf("build started at %s, want landed dependency tip %s", steps.buildHead, steps.depTip)
	}
}

// Also verify a plain (non-classification) read-only plan step whose item gains a
// dependency while running: the plan settles, releases the lane, holds as blocked,
// and continues to build on the landed dependency's tip without run-link-missing.
func TestDependencyAddedWhileNonClassificationPlanRunsSettlesAndContinues(t *testing.T) {
	f, l, old := workflowLoopFixture(t, "")
	planBuildWorkflow := `{"name":"plain-plan-build","version":1,"stages":[` +
		`{"id":"plan","kind":"agent","seat":"plan","access":"read_only","outputs":{"plan":"markdown"}},` +
		`{"id":"build","kind":"agent","seat":"build","access":"write","inputs":{"plan":"markdown"}}` +
		`],"edges":[{"from_node":"plan","from_output":"plan","to_node":"build","to_input":"plan"}]}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/plain-plan-build@v1.json"), planBuildWorkflow)
	writeForkFile(t, filepath.Join(f.root, "briefs/item-b.md"), "# Task\nLand the dependency.\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "plain-plan-build workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='plain-plan-build@v1' WHERE item_id='item-a'`)
		return err
	})
	l.cfg.Policy.Seats["plan"] = "fixture"

	depAdded := false
	steps := &depBlockedWorkflowSteps{
		workflowSteps: old,
		onPlanStart: func() {
			if depAdded {
				return
			}
			depAdded = true
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := item.SetDeps(context.Background(), tx, "item-a", []string{"item-b"}, nil, l.cfg.Now())
				return err
			})
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 8 && result.Landed < 2; i++ {
		if d := decisionFor(t, l, "item-a").decision; strings.HasPrefix(d.Reason, ReasonRunLinkMissing) {
			t.Fatalf("item-a held as run-link-missing: %+v", d)
		}
		graphPass(t, l, result)
	}
	if result.Landed != 2 {
		t.Fatalf("landed = %d, want 2; item-a decision = %+v", result.Landed, decisionFor(t, l, "item-a").decision)
	}
	planRuns, buildRuns := 0, 0
	for _, r := range steps.runs {
		if r.ItemID != "item-a" {
			continue
		}
		switch r.NodeID {
		case "plan":
			planRuns++
		case "build":
			buildRuns++
		}
	}
	if planRuns != 1 || buildRuns != 1 {
		t.Fatalf("item-a ran plan=%d build=%d, want 1 and 1", planRuns, buildRuns)
	}
	if steps.depTip == "" || steps.buildHead != steps.depTip {
		t.Fatalf("build started at %s, want landed dependency tip %s", steps.buildHead, steps.depTip)
	}
}
