package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRestageForkAtKeptHeadPinsCandidateBeforeReset(t *testing.T) {
	f, l, c := heldForkFixture(t)
	ctx := context.Background()
	stager := &LaneStager{Root: f.root, Store: f.storePath, LaneRoot: l.cfg.LaneRoot, Now: l.cfg.Now}
	spec := StageSpec{LaneID: c.LaneID, ItemID: c.ItemID, RunID: c.RunID, KeptHeadSHA: c.BaseSHA}
	fork := ForkTarget{Checkout: c.Worktree, Ref: c.BaseRef}

	// The candidate-ready event identifies the owning run, but its commit is
	// still only in the separate fork checkout when staging begins.
	if err := stager.restageForkAtKeptHead(ctx, spec, fork); err != nil {
		t.Fatalf("restaging the owning run's fork candidate: %v", err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.BaseSHA {
		t.Fatalf("fork HEAD = %s, want reset to %s", got, c.BaseSHA)
	}
	if _, err := os.Stat(filepath.Join(c.Worktree, "candidate.txt")); !os.IsNotExist(err) {
		t.Fatalf("candidate file survived the reset: %v", err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", pinRef(c.RunID)+"^{commit}"); got != c.HeadSHA {
		t.Fatalf("fork run pin = %s, want retained candidate %s", got, c.HeadSHA)
	}
	if _, err := l.revParse(ctx, f.root, pinRef(c.RunID)+"^{commit}"); err == nil {
		t.Fatal("fork run was pinned in the root repository")
	}
	if got := runGit(t, f.root, "rev-parse", c.BaseRef); got != c.BaseSHA {
		t.Fatalf("delivery ref moved to %s, want %s", got, c.BaseSHA)
	}
}

func TestForkCleanupPinFailureHoldsStageWithoutReset(t *testing.T) {
	f, l, c := heldForkFixture(t)
	ctx := context.Background()
	lockRunPin(t, c.Worktree, c.RunID)
	stager := &LaneStager{Root: f.root, Store: f.storePath, LaneRoot: l.cfg.LaneRoot, Now: l.cfg.Now}
	l.cfg.Stage = stagerFunc(func(ctx context.Context, spec StageSpec) error {
		spec.KeptHeadSHA = c.BaseSHA
		return stager.restageForkAtKeptHead(ctx, spec, ForkTarget{Checkout: c.Worktree, Ref: c.BaseRef})
	})
	const stageRun = "run-new-attempt"
	d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: &run.Request{RunID: stageRun, ItemID: c.ItemID, LaneID: c.LaneID}}
	result := &LoopResult{}
	staged, err := l.stageDispatches(ctx, []Decision{d}, c.BaseSHA, result)
	if err != nil || len(staged) != 0 || result.InfraFailures != 0 {
		t.Fatalf("cleanup failure: staged = %v, err = %v, infrastructure failures = %d", staged, err, result.InfraFailures)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		fault := mustFaults(t, r)[c.ItemID]
		if fault.Reason != ReasonStageFailed || fault.RunID != stageRun || !strings.Contains(fault.Detail, pinRef(c.RunID)) {
			t.Fatalf("cleanup failure hold = %+v, want stage fault naming the failed pin", fault)
		}
		_, _, mover := whyPresentation(Decision{Action: ActionHold, Reason: fault.Reason})
		if !strings.Contains(mover, "tillr drain clear-fault") {
			t.Fatalf("cleanup hold mover = %q", mover)
		}
		return nil
	})
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("cleanup reset an unpinned candidate: HEAD = %s", got)
	}
}

func TestPinLaneKeepsForkCandidateInFork(t *testing.T) {
	f, l, c := heldForkFixture(t)
	ctx := context.Background()
	head := l.headOf(ctx, c.LaneID)
	if head != c.HeadSHA {
		t.Fatalf("worker HEAD = %s, want fork candidate %s", head, c.HeadSHA)
	}
	if err := l.pinLane(ctx, c.LaneID, c.ItemID, c.RunID, head); err != nil {
		t.Fatalf("pinning a candidate absent from root: %v", err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", pinRef(c.RunID)+"^{commit}"); got != c.HeadSHA {
		t.Fatalf("fork run pin = %s, want %s", got, c.HeadSHA)
	}
	if got := runGit(t, c.Worktree, "rev-parse", itemPinRef(c.ItemID)+"^{commit}"); got != c.HeadSHA {
		t.Fatalf("fork item pin = %s, want %s", got, c.HeadSHA)
	}
	if _, err := l.revParse(ctx, f.root, c.HeadSHA+"^{commit}"); err == nil {
		t.Fatal("pinning imported the fork-only commit into the root repository")
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("pinning changed fork HEAD to %s", got)
	}
	if got := runGit(t, f.root, "rev-parse", c.BaseRef); got != c.BaseSHA {
		t.Fatalf("pinning moved delivery ref to %s", got)
	}
}

func lockRunPin(t *testing.T, root, runID string) {
	t.Helper()
	lock := runGit(t, root, "rev-parse", "--git-path", pinRef(runID)+".lock")
	if !filepath.IsAbs(lock) {
		lock = filepath.Join(root, lock)
	}
	if err := os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, nil, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestForkRunPinFailureHoldsItemWithoutStoppingLoop(t *testing.T) {
	f, l, c := heldForkFixture(t)
	ctx := context.Background()
	lockRunPin(t, c.Worktree, c.RunID)
	pinErr := l.pinLane(ctx, c.LaneID, c.ItemID, c.RunID, c.HeadSHA)
	if pinErr == nil || !strings.Contains(pinErr.Error(), "lock") || !strings.Contains(pinErr.Error(), pinRef(c.RunID)) {
		t.Fatalf("pin error = %v, want this run's ref-lock failure", pinErr)
	}

	result := &LoopResult{}
	stop, err := l.takeCompletion(runCompletion{
		id: c.RunID, itemID: c.ItemID, step: "settle",
		outcome: laneOutcome{err: pinErr},
	}, result)
	if err != nil || stop != "" || result.StopItemID != "" || result.StopStep != "" {
		t.Errorf("one run's pin failure stopped the loop: stop=%q err=%v result=%+v", stop, err, result)
	}
	// Reopen the store rather than relying on an in-memory hold.
	fixtureRead(t, f, func(r *store.Reader) error {
		faults := mustFaults(t, r)
		fault, ok := faults[c.ItemID]
		if !ok || fault.Reason != ReasonSettleFailed || fault.RunID != c.RunID || !strings.Contains(fault.Detail, pinErr.Error()) || !strings.Contains(fault.Detail, "settle") {
			t.Errorf("durable pin failure hold = %+v (present=%v), want run and settle cause", fault, ok)
		}
		if sibling, held := faults["item-b"]; held {
			t.Errorf("sibling inherited the pin failure: %+v", sibling)
		}
		if ok {
			class, where, mover := whyPresentation(Decision{Action: ActionHold, Reason: fault.Reason})
			if class != "needs a cause" || where != "loop" || !strings.Contains(mover, "fix the drain defect") || !strings.Contains(mover, "tillr drain clear-fault") {
				t.Errorf("pin failure presentation = (%q, %q, %q), want named defect repair and clear-fault mover", class, where, mover)
			}
		}
		return nil
	})
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("failed pin discarded fork candidate: HEAD = %s", got)
	}
	if got := runGit(t, f.root, "rev-parse", c.BaseRef); got != c.BaseSHA {
		t.Fatalf("failed pin moved delivery ref to %s", got)
	}
}
