package drain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRetainLaneHoldClasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		keep bool
	}{
		{"gate stall", errGateStallHeld, false},
		{"gate no verdict", errGateNoVerdict, false},
		{"item fault", errItemFaultHeld, false},
		{"held repair", errRepairHeld, false},
		{"pending repair handoff", errRepairPending, true},
		{"dirty trunk", errDirtyHeld, false},
		{"red cap", errRedCapHeld, false},
		{"rewritten trunk", errTrunkRewritten, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held, keep := retainLane(tc.err)
			if !held || keep != tc.keep {
				t.Fatalf("held=%v keep=%v, want true/%v", held, keep, tc.keep)
			}
		})
	}
	if held, _ := retainLane(errors.New("unclassified failure")); held {
		t.Fatal("ordinary failure swallowed as a hold")
	}
}

func TestIdleReleaseCannotResetAClaimedExaminer(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	// Reconciliation read this snapshot before the operator acquitted the hold
	// and claimed examination. Reproduce that exact interleaving without sleeps.
	p := mustPlan(t, l)[0]
	stale := &pendingWork{Decision: p.decision, Candidate: &c}
	ok, _, err := l.applyPlan(context.Background(), p, c.BaseSHA, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim examiner: %v/%v", ok, err)
	}
	released, err := l.releaseHeldCandidate(context.Background(), stale, "gate-harness-error")
	if err != nil || released {
		t.Fatalf("stale release stole examination: %v/%v", released, err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("stale release reset examiner's checkout: %s", got)
	}
	reader := openFixture(t, f.storePath)
	defer reader.Close()
	if err := reader.ReadTx(context.Background(), func(r *store.Reader) error {
		var state, runID string
		if err := r.QueryRow(`SELECT state,run_id FROM lanes WHERE lane_id=?`, c.LaneID).Scan(&state, &runID); err != nil {
			return err
		}
		if state != "leased" || runID != c.RunID {
			t.Fatalf("stale release cleared examiner's lease: %s/%s", state, runID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseClaimExcludesConcurrentOperatorExamination(t *testing.T) {
	_, l, c := heldCandidateFixture(t)
	w := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
	claimed := make(chan error, 1)
	proceed := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, ok, err := l.claimCandidateRelease(context.Background(), w, "gate-harness-error", false)
		if err == nil && !ok {
			err = fmt.Errorf("idle release could not claim its lane")
		}
		claimed <- err
		<-proceed
		if err != nil {
			done <- err
			return
		}
		// Continue under the claim, exactly as an examination releasing its
		// held candidate does. The operator runs in the protected interval.
		released, err := l.releaseCandidate(context.Background(), w, "gate-harness-error", true)
		if err == nil && !released {
			err = fmt.Errorf("claimed release did not complete")
		}
		done <- err
	}()
	claimErr := <-claimed
	if claimErr == nil {
		_, err := l.Examine(context.Background(), c.RunID)
		if err == nil || !strings.Contains(err.Error(), "held") {
			t.Errorf("operator acquired the releasing checkout: %v", err)
		}
		if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
			t.Errorf("operator changed releasing checkout: %s", got)
		}
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if claimErr != nil {
		t.Fatal(claimErr)
	}
}

func TestHeldCandidateUnreadablePinKeepsLane(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	// The pin write succeeds; the subsequent commit read fails. All other Git
	// calls use the real binary, so this specifically reproduces unreadable pins.
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(scratchUnderDurable(t), "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncase \"$4\" in 'refs/tillr/runs/'*'^{commit}') exit 1;; esac\nexec %q \"$@\"\n", realGit)
	if err := writeForkSafe(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	work := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
	released, err := l.releaseHeldCandidate(context.Background(), work, "gate-harness-error")
	if err != nil || released {
		t.Fatalf("unreadable commit released: %v/%v", released, err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("unreadable pin reset lane: %s", got)
	}
	if got := mustPlan(t, l)[1].decision; got.Reason != ReasonNoLane {
		t.Fatalf("unreadable pin freed capacity: %+v", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		states, err := PendingLaneStates(r)
		if err != nil {
			return err
		}
		if got := states[c.ItemID]; got.Status != "kept" || !strings.Contains(got.Reason, "cannot be read back") {
			t.Errorf("missing kept explanation: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHeldCandidateWorktreeDependentHoldsKeepLane(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		mutate       func(*pendingWork)
	}{
		{"fork delivery", "gate-harness-error", func(w *pendingWork) { w.Candidate.BaseRef = "refs/heads/patch/pi" }},
		{"workflow", "gate-harness-error", func(w *pendingWork) { w.Decision.Workflow = &Workflow{} }},
		{"boundary", StopHostBoundaryViolation, func(w *pendingWork) { w.BoundaryFailed = true }},
		{"mismatch", ReasonCandidateMismatch, func(w *pendingWork) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, l, c := heldCandidateFixture(t)
			w := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
			tc.mutate(w)
			if reason := candidateKeepReason(w, tc.reason); reason == "" {
				t.Fatal("kept class has no reason")
			}
			released, err := l.releaseHeldCandidate(context.Background(), w, tc.reason)
			if err != nil || released {
				t.Fatalf("worktree-dependent hold released: %v/%v", released, err)
			}
			if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
				t.Fatalf("kept lane changed to %s", got)
			}
		})
	}
}

func TestIdleRedCapCandidateFromOlderLoopReleasesBeforePlanning(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, redCapHeldKind, c.RunID, redCapHold{Root: f.root, Candidate: c}, l.cfg.Now())
	})
	if err := l.releaseIdleHeldCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader := openFixture(t, f.storePath)
	defer reader.Close()
	if err := reader.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if w := works[c.ItemID]; !w.LaneReleased || !w.Gated || w.Candidate.HeadSHA != c.HeadSHA {
			t.Fatalf("red-cap release lost candidate or passing verdict: %+v", w)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c.RunID+"^{commit}"); got != c.HeadSHA {
		t.Fatalf("pin %s, want %s", got, c.HeadSHA)
	}
}

func TestIdleHeldCandidateFromOlderLoopReleasesBeforePlanning(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, "old loop hold"); err != nil {
		t.Fatal(err)
	}
	// The old hold has no release event. This is the reconciliation the pass runs
	// before planning; no worker or examination owns its idle lane.
	if err := l.releaseIdleHeldCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustPlan(t, l)[1].decision; got.Action != ActionDispatch || got.LaneID != c.LaneID {
		t.Fatalf("same pass cannot offer released lane: %+v", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		states, err := PendingLaneStates(r)
		if err != nil {
			return err
		}
		if states[c.ItemID].Status != "released" {
			t.Errorf("release not durable: %+v", states)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The original fence survives lane reuse in durable pending state.
	writeForkFile(t, filepath.Join(l.laneDirPath(c.LaneID), FenceFile), "sibling-only.txt\n")
}
