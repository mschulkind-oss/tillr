package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func recordFixtureFork(tx *store.Tx, ids ...string) error {
	for _, id := range ids {
		if err := appendEffectOn(tx, deliveryDecisionKind, "item", id, deliveryDecision{Kind: "fork", Fork: "pi"}, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func TestDeliveryClassificationPrecedesWriterDispatch(t *testing.T) {
	for _, workflow := range []string{"single-worker@v1", "planned-build@v1"} {
		t.Run(workflow, func(t *testing.T) { testDeliveryClassificationDispatch(t, workflow) })
	}
}

func testDeliveryClassificationDispatch(t *testing.T, workflow string) {
	f, l, _ := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["plan"] = "fixture"
	if err := os.Mkdir(filepath.Join(f.root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
	if workflow == "planned-build@v1" {
		l.cfg.Policy.Seats["verify"] = "fixture"

		raw, err := os.ReadFile("../../../config/tillr/workflows/planned-build@v1.json")
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/planned-build@v1.json"), string(raw))
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow=? WHERE item_id='item-a'`, workflow)
		return err
	})
	var got bool
	for _, p := range mustPlan(t, l) {
		if d := p.decision; d.ItemID == "item-a" && d.Action == ActionDispatch {
			got = d.Workflow != nil && d.Seat == "plan" && d.Workflow.Stages[d.NodeIndex].Access == "read_only" && classificationNode(d.Workflow.Stages[d.NodeIndex])
		}
	}
	if !got {
		t.Fatal("workflow did not dispatch its read-only classifier first")
	}
}

func TestDeliveryPartialReceiptSurvivesRestart(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffectOn(tx, deliveryDecisionKind, "item", "item-a", deliveryDecision{Kind: "both", Fork: "pi"}, l.cfg.Now())
	})
	c := Candidate{ItemID: "item-a", RunID: "run-fork", BaseRef: "refs/heads/patch/pi"}
	repo := Candidate{ItemID: c.ItemID, RunID: "run-repo"}
	if err := l.checkDeliveryOrder(context.Background(), repo); err == nil {
		t.Fatal("repository landing admitted before fork receipt")
	}
	for i := 0; i < 2; i++ {
		restarted := &Loop{cfg: l.cfg}
		if partial, err := restarted.settlePartialDelivery(context.Background(), c, "fork-commit"); err != nil || !partial {
			t.Fatalf("settled partial=%v: %v", partial, err)
		}
		if err := restarted.checkDeliveryOrder(context.Background(), repo); err != nil {
			t.Fatalf("repository landing refused after fork receipt: %v", err)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		events, err := tx.Reader().EventsOfKind(0, deliveryPartialKind)
		if err != nil || len(events) != 1 {
			t.Fatalf("partial events=%d: %v", len(events), err)
		}
		deliveries, err := readDeliveries(tx.Reader())
		if d := deliveries[c.ItemID]; err != nil || d.ForkRun != c.RunID || d.activeFork() != "" {
			t.Fatalf("restarted delivery=%+v: %v", d, err)
		}
		return nil
	})
}

type mixedDeliverySteps struct {
	*workflowSteps
	landings []string
	runsOn   []string
}

func (s *mixedDeliverySteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	active, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, TargetFile))
	if err == nil {
		target = strings.TrimSpace(string(active))
	}
	s.runsOn = append(s.runsOn, target)
	if req.NodeID == "classify" {
		dir := nodeOutputsDir(s.f.root, req.RunID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(dir, deliveryOutput), `{"kind":"both","fork":"pi"}`)
	} else {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/delivered.txt"), "delivered\n")
		runGit(s.t, target, "add", "tools/item-a/delivered.txt")
		runGit(s.t, target, "commit", "-qm", "delivery piece")
	}
	report := "RESULT: done\n"
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	writeForkFile(s.t, path, report)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, nil
}

func (s *mixedDeliverySteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *mixedDeliverySteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.landings = append(s.landings, c.BaseRef)
	if c.BaseRef == "refs/heads/patch/pi" {
		runGit(s.t, s.f.root, "fetch", "--no-tags", c.Worktree, "refs/heads/main:"+c.BaseRef)
		fixtureLanding(s.t, s.f.storePath, c)
		return LandOutcome{Result: LandLanded, TrunkAfter: c.HeadSHA}, nil
	}
	return s.workflowSteps.Land(ctx, c)
}

func TestDeliveryMixedContinuesAfterLoopRestart(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["plan"] = "fixture"
	if err := os.Mkdir(filepath.Join(f.root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", "bin/pi-forks.list", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "fork catalog")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET workflow='single-worker@v1',allowed_paths='["tools/item-a/"]' WHERE item_id='item-a'`)
		return err
	})
	fork := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	if err := os.MkdirAll(fork, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-qb", "main")
	writeForkFile(t, filepath.Join(fork, "fork.txt"), "base\n")
	runGit(t, fork, "add", "fork.txt")
	runGit(t, fork, "commit", "-qm", "fork base")
	runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	s := &mixedDeliverySteps{workflowSteps: steps}
	l.cfg.Steps = s
	for i := 0; i < 8 && len(s.landings) == 0; i++ {
		graphPass(t, l, &LoopResult{})
	}
	if len(s.landings) != 1 || s.landings[0] != "refs/heads/patch/pi" || len(s.runsOn) != 2 || s.runsOn[1] != fork {
		t.Fatalf("before restart: Land=%v Run=%v", s.landings, s.runsOn)
	}
	restarted := f.loop(t, s, l.cfg.Stage)
	restarted.cfg.Lanes = l.cfg.Lanes
	restarted.cfg.Policy = l.cfg.Policy
	for i := 0; i < 8 && len(s.landings) < 2; i++ {
		graphPass(t, restarted, &LoopResult{})
	}
	if len(s.landings) != 2 || s.landings[1] == "refs/heads/patch/pi" || len(s.runsOn) != 3 || s.runsOn[2] == fork {
		t.Fatalf("after restart: Land=%v Run=%v", s.landings, s.runsOn)
	}
	finished := f.loop(t, s, l.cfg.Stage)
	finished.cfg.Lanes = l.cfg.Lanes
	finished.cfg.Policy = l.cfg.Policy
	graphPass(t, finished, &LoopResult{})
	if len(s.landings) != 2 || len(s.runsOn) != 3 {
		t.Fatalf("restart repeated a completed piece: Land=%v Run=%v", s.landings, s.runsOn)
	}
}

func TestDeliveryTargetDoesNotComeFromFence(t *testing.T) {
	root := t.TempDir()
	target := checkoutForDelivery(root, "lane-7", deliveryDecision{Kind: "both", Fork: "pi"})
	if target.Checkout != filepath.Join(root, ".forks", "lanes", "lane-7", "pi") || target.Ref != "refs/heads/patch/pi" {
		t.Fatalf("wrong explicit checkout: %+v", target)
	}
	if target := checkoutForDelivery(root, "lane-7", deliveryDecision{Kind: "repo"}); target != (ForkTarget{}) {
		t.Fatalf("repository delivery got a fork checkout: %+v", target)
	}
}
