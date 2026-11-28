package drain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestBlockedForkItemWithNoLiveRunAllowsSecondForkItemAndPreservesBuiltWork(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-blocker")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Policy.Seats = map[string]string{"plan": "fixture", "build": "fixture", "verify": "fixture"}
	l.cfg.Stage = &LaneStager{
		Root:     f.root,
		LaneRoot: l.cfg.LaneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}
	ctx := context.Background()

	// Initialize fork checkouts for both lanes from a shared patch/pi tip.
	fork1 := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	fork2 := filepath.Join(f.root, ".forks/lanes/lane-2/pi")
	if err := os.MkdirAll(fork1, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork1, "init", "-q", "-b", "main")
	runGit(t, fork1, "commit", "--allow-empty", "-qm", "fork base")
	forkBase := runGit(t, fork1, "rev-parse", "HEAD")
	runGit(t, f.root, "fetch", "--no-tags", fork1, "refs/heads/main:refs/heads/patch/pi")
	if err := os.MkdirAll(filepath.Dir(fork2), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "clone", "-q", fork1, fork2)

	trunk := runGit(t, f.root, "rev-parse", TrunkRef)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := recordFixtureFork(tx, "item-a"); err != nil {
			return err
		}
		if err := recordFixtureFork(tx, "item-b"); err != nil {
			return err
		}
		// item-blocker is held on the owner (blocked).
		_, err := tx.Exec(`UPDATE items SET state='blocked' WHERE item_id='item-blocker'`)
		return err
	})

	// Dispatch item-a on lane-1 for a build -> verify workflow on fork pi.
	wf := &Workflow{Name: "fork-wf", Version: 1, Stages: []WorkflowStage{
		{ID: "build", Kind: "agent", Seat: "build", Access: "write"},
		{ID: "verify", Kind: "agent", Seat: "verify", Access: "read_only"},
	}}
	p1 := mustPlan(t, l)[0]
	if p1.decision.ItemID != "item-a" || p1.decision.LaneID != "lane-1" {
		t.Fatalf("unexpected initial plan for item-a: %+v", p1.decision)
	}
	p1.decision.Workflow = wf
	p1.decision.NodeIndex = 0
	p1.decision.Seat = "build"
	p1.decision.RunRequest.NodeID = "build"
	ok, workA, err := l.applyPlan(ctx, p1, trunk, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan(item-a) = %v, %v", ok, err)
	}
	staged, err := l.stageDispatches(ctx, []Decision{workA.Decision}, trunk, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stageDispatches(item-a) = %+v, %v", staged, err)
	}

	// While item-a has a live run on the fork, adding blocked_by still keeps the fork held.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by, kind) VALUES ('item-a', 'item-blocker', ?)`, item.EdgeKindBlockedBy)
		return err
	})
	whileLive := mustPlan(t, l)
	if d := whileLive[1].decision; d.ItemID != "item-b" || d.Action != ActionHold || d.Reason != ReasonForkHeld || d.DeferredBy != "item-a" {
		t.Fatalf("while item-a is live, item-b should be fork-held behind item-a: %+v", d)
	}

	// item-a's build step commits real work on fork1, pins it, finishes its run, and advances to verify.
	builtFile := filepath.Join(fork1, "built.txt")
	if err := os.WriteFile(builtFile, []byte("built-by-item-a\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork1, "add", "built.txt")
	runGit(t, fork1, "commit", "-qm", "item-a build work")
	builtHead := runGit(t, fork1, "rev-parse", "HEAD")
	if err := l.pinLane(ctx, "lane-1", "item-a", staged[0].RunRequest.RunID, builtHead); err != nil {
		t.Fatalf("pinLane: %v", err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now().Format(time.RFC3339Nano)
		return applyTerminal(tx, run.Record{
			RunID: staged[0].RunRequest.RunID, ItemID: "item-a", LaneID: "lane-1",
			Backend: staged[0].RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{},
		})
	})
	if err := l.advanceNode(ctx, staged[0], forkBase, ""); err != nil {
		t.Fatalf("advanceNode: %v", err)
	}

	// Now item-a is blocked on item-blocker with no live run.
	// c5: item-b for the same fork must be dispatched, not fork-held.
	plans := mustPlan(t, l)
	if d := plans[0].decision; d.ItemID != "item-a" || d.Action != ActionHold || d.Reason != ReasonBlocked {
		t.Fatalf("item-a should be held as blocked: %+v", d)
	}
	if d := plans[1].decision; d.ItemID != "item-b" || !isDispatch(d) || d.LaneID != "lane-2" {
		t.Fatalf("item-b should be dispatched on lane-2 when blocked item-a has no live run: %+v", d)
	}
	ok, workB, err := l.applyPlan(ctx, plans[1], trunk, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan(item-b) should succeed when blocked item-a has no live run: %v, %v", ok, err)
	}

	// Finish item-b and advance refs/heads/patch/pi as if item-b landed.
	if err := os.WriteFile(filepath.Join(fork2, "item-b.txt"), []byte("item-b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork2, "add", "item-b.txt")
	runGit(t, fork2, "commit", "-qm", "item-b work")
	runGit(t, f.root, "fetch", "--no-tags", fork2, "refs/heads/main:refs/heads/patch/pi")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now().Format(time.RFC3339Nano)
		if err := applyTerminal(tx, run.Record{
			RunID: workB.Decision.RunRequest.RunID, ItemID: "item-b", LaneID: "lane-2",
			Backend: workB.Decision.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id IN ('item-b', 'item-blocker')`)
		return err
	})
	if err := l.markExamined(ctx, workB.Decision); err != nil {
		t.Fatalf("markExamined(item-b): %v", err)
	}

	// c6: item-a is now unblocked; it resumes verify and its built fork work is still there.
	resumedPlans := mustPlan(t, l)
	if d := resumedPlans[0].decision; d.ItemID != "item-a" || !isDispatch(d) || d.LaneID != "lane-1" || d.NodeIndex != 1 {
		t.Fatalf("unblocked item-a should resume verify on lane-1: %+v", d)
	}
	ok, resumedWork, err := l.applyPlan(ctx, resumedPlans[0], trunk, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan(resumed item-a) = %v, %v", ok, err)
	}
	resumedStaged, err := l.stageDispatches(ctx, []Decision{resumedWork.Decision}, trunk, &LoopResult{})
	if err != nil || len(resumedStaged) != 1 {
		t.Fatalf("stageDispatches(resumed item-a) = %+v, %v", resumedStaged, err)
	}
	if gotHead := runGit(t, fork1, "rev-parse", "HEAD"); gotHead != builtHead {
		t.Fatalf("fork1 HEAD after release = %s, want built commit %s", gotHead, builtHead)
	}
	gotBody, err := os.ReadFile(builtFile)
	if err != nil || string(gotBody) != "built-by-item-a\n" {
		t.Fatalf("built.txt after release = %q (%v), want built-by-item-a", gotBody, err)
	}
}

func TestWhyNamesForkHolderAndMover(t *testing.T) {
	t.Run("live run holds fork", func(t *testing.T) {
		f, l := passFixture(t, "item-a", "item-b")
		ctx := context.Background()
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := recordFixtureFork(tx, "item-a"); err != nil {
				return err
			}
			return recordFixtureFork(tx, "item-b")
		})
		holder := mustPlan(t, l)[0]
		if ok, _, err := l.applyPlan(ctx, holder, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("applyPlan(item-a) = %v, %v", ok, err)
		}

		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339Nano), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			row := answer.Rows[1]
			if row.ItemID != "item-b" || row.Reason != ReasonForkHeld || !slices.Equal(row.WaitingOn, []string{"item-a"}) {
				t.Fatalf("unexpected fork-held row: %+v", row)
			}
			for _, want := range []string{"pi", "item-a", holder.decision.RunRequest.RunID} {
				if !strings.Contains(row.Why, want) {
					t.Errorf("row.Why = %q, want it to name %q", row.Why, want)
				}
			}
			if row.Mover == "item-a" || !strings.Contains(row.Mover, "item-a") || !strings.Contains(row.Mover, holder.decision.RunRequest.RunID) {
				t.Errorf("row.Mover = %q, want concrete mover naming item-a and live run %s", row.Mover, holder.decision.RunRequest.RunID)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("blocked item with kept-checkout reason holds fork with mover", func(t *testing.T) {
		f, l := passFixture(t, "item-a", "item-b", "item-blocker")
		ctx := context.Background()
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := recordFixtureFork(tx, "item-a"); err != nil {
				return err
			}
			if err := recordFixtureFork(tx, "item-b"); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET state='blocked' WHERE item_id='item-blocker'`); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by, kind) VALUES ('item-a', 'item-blocker', ?)`, item.EdgeKindBlockedBy); err != nil {
				return err
			}
			d := f.decisions()[0]
			d.Workflow = &Workflow{Stages: []WorkflowStage{
				{ID: "build", Seat: "build", Access: "write"},
				{ID: "verify", Seat: "verify", Access: "read_only"},
			}}
			d.Attempt, d.NodeIndex, d.Seat = 1, 1, "verify"
			d.Reason, d.NodeBaseSHA = "workflow-next-node", base
			d.RunRequest.RunID = "run-item-a-a1-n2"
			return appendEffect(tx, nodeAdvancedKind, d.RunRequest.RunID, pendingWork{
				Decision:       d,
				BaseSHA:        base,
				Ready:          true,
				LaneKeepReason: "fork checkout has uncommitted work",
				ParentRunID:    "run-item-a-a1-n1",
			}, l.cfg.Now())
		})

		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339Nano), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			row := answer.Rows[1]
			if row.ItemID != "item-b" || row.Reason != ReasonForkHeld || !slices.Equal(row.WaitingOn, []string{"item-a"}) {
				t.Fatalf("unexpected fork-held row: %+v", row)
			}
			for _, want := range []string{"pi", "item-a", "uncommitted work"} {
				if !strings.Contains(row.Why, want) {
					t.Errorf("row.Why = %q, want it to name %q", row.Why, want)
				}
			}
			if row.Mover == "item-a" || !strings.Contains(row.Mover, "item-a") || !strings.Contains(row.Mover, "operator") {
				t.Errorf("row.Mover = %q, want concrete operator mover naming who can release item-a", row.Mover)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}
