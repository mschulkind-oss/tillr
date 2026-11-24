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
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
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
		{"trunk acquitted", errTrunkAcquittedHeld, false},
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

// Reproduces the 2026-11-23 04:34 lane-1 hold: a finished candidate in the current
// loop's own domain could not release lane-1 because an earlier finished run on the
// same lane from another PID namespace of the same boot had an oversized run_id
// (>255 bytes) whose stream path returned ENAMETOOLONG from os.Open.
func TestHeldCandidateReleasesLaneDespiteForeignENAMETOOLONGHistoryRun(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	l.cfg.Steps = streamSteps{&recordingSteps{}, f.root}
	// The candidate's own run finished and left closed stream files in .tillr/loop/.
	runStreams(t, f, c.RunID)

	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	boot, _, _ := strings.Cut(here.Domain, ":")
	foreignOwner := *here
	foreignOwner.Domain = boot + ":pid:[1]"
	foreignOwner.PID = 3789886

	oldRunID := "run-item-old-a3-n1-" + strings.Repeat("failover-", 26) + "restart"
	if len(oldRunID) <= 255 {
		t.Fatalf("oldRunID len = %d, want > 255", len(oldRunID))
	}
	oldWork := pendingWork{
		Owner: &foreignOwner,
		Decision: Decision{
			ItemID: "item-old",
			LaneID: c.LaneID,
			RunRequest: &run.Request{
				RunID:   oldRunID,
				ItemID:  "item-old",
				LaneID:  c.LaneID,
				Backend: "fixture",
			},
		},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES('item-old','item-old','landed',0,'')`); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, oldRunID, oldWork, l.cfg.Now().Add(-time.Hour)); err != nil {
			return err
		}
		rec := run.Record{
			RunID:        oldRunID,
			ItemID:       "item-old",
			LaneID:       c.LaneID,
			Backend:      "fixture",
			State:        run.StateTimedOut,
			EndCause:     "lease-reclaimed",
			StartedAt:    l.cfg.Now().Add(-time.Hour).Format(time.RFC3339),
			FinishedAt:   l.cfg.Now().Add(-55 * time.Minute).Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if err := appendEffect(tx, loopOrphanedKind, oldRunID, orphanedRunEffect{}, l.cfg.Now().Add(-55*time.Minute)); err != nil {
			return err
		}
		return appendEffect(tx, examinationDoneKind, oldRunID, struct{}{}, l.cfg.Now().Add(-50*time.Minute))
	})

	w := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
	if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, "gate failed"); err != nil {
		t.Fatal(err)
	}
	released, err := l.releaseHeldCandidate(context.Background(), w, "gate-failed")
	if err != nil || !released {
		t.Fatalf("finished candidate kept lane despite ENAMETOOLONG foreign history run: released=%v err=%v", released, err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		states, err := PendingLaneStates(r)
		if err != nil {
			return err
		}
		if got := states[c.ItemID]; got.Status != "released" {
			t.Fatalf("lane state = %+v, want released", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := mustPlan(t, l)[1].decision; got.Action != ActionDispatch || got.LaneID != c.LaneID {
		t.Fatalf("released lane not offered to waiting item: %+v", got)
	}
}

// A run that is genuinely live in another process domain (its stream file is still
// open for writing) must still keep the lane.
func TestHeldCandidateKeepsLaneWhenForeignRunStreamIsOpenForWriting(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	l.cfg.Steps = streamSteps{&recordingSteps{}, f.root}

	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	boot, _, _ := strings.Cut(here.Domain, ":")
	foreignOwner := *here
	foreignOwner.Domain = boot + ":pid:[1]"
	foreignOwner.PID = 8300

	foreignRunID := "run-item-foreign-live-a1"
	foreignWork := pendingWork{
		Owner: &foreignOwner,
		Decision: Decision{
			ItemID: "item-foreign",
			LaneID: c.LaneID,
			RunRequest: &run.Request{
				RunID:   foreignRunID,
				ItemID:  "item-foreign",
				LaneID:  c.LaneID,
				Backend: "fixture",
			},
		},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES('item-foreign','item-foreign','landed',0,'')`); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, foreignRunID, foreignWork, l.cfg.Now().Add(-time.Minute)); err != nil {
			return err
		}
		return appendEffect(tx, examinationDoneKind, foreignRunID, struct{}{}, l.cfg.Now().Add(-30*time.Second))
	})
	paths := runStreams(t, f, foreignRunID)
	writer, err := os.OpenFile(paths[1], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	w := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
	released, err := l.releaseHeldCandidate(context.Background(), w, "gate-failed")
	if err != nil || released {
		t.Fatalf("lane released while foreign run stream is open for writing: released=%v err=%v", released, err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("kept lane changed to %s, want %s", got, c.HeadSHA)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		states, err := PendingLaneStates(r)
		if err != nil {
			return err
		}
		if got := states[c.ItemID]; got.Status != "kept" || got.Reason != "lane may have a live run in another process domain" {
			t.Fatalf("lane state = %+v, want kept with foreign-domain reason", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Reproduces the 2026-11-23 lane-2 hold: a historical run on the lane recorded a
// run.resume event after its run.finish and never wrote a second run.finish, and its
// item later landed or closed. That leftover record must not keep the lane, while an
// unfinished resume for an item that has not landed or closed still keeps it.
func TestHeldCandidateIgnoresUnfinishedResumeOfEndedItem(t *testing.T) {
	for _, tc := range []struct {
		name      string
		itemState string
		wantKept  bool
	}{
		{name: "landed", itemState: StateLanded, wantKept: false},
		{name: "closed", itemState: "closed", wantKept: false},
		{name: "resolved-elsewhere", itemState: StateResolvedElsewhere, wantKept: false},
		{name: "active-in-queue", itemState: "in-queue", wantKept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, c := heldCandidateFixture(t)
			oldItemID := "item-old-" + tc.name
			oldRunID := "run-" + oldItemID + "-a7"
			finishedAt := l.cfg.Now().Add(-time.Hour)
			resumedAt := finishedAt.Add(time.Minute)

			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES(?,?,?,?,?)`, oldItemID, oldItemID, tc.itemState, 0, ""); err != nil {
					return err
				}
				exitCode := 0
				rec := run.Record{
					RunID:        oldRunID,
					ItemID:       oldItemID,
					LaneID:       c.LaneID,
					Backend:      "fixture",
					State:        run.StateExited,
					ExitCode:     &exitCode,
					EndCause:     "normal-exit",
					StartedAt:    finishedAt.Add(-5 * time.Minute).Format(time.RFC3339),
					FinishedAt:   finishedAt.Format(time.RFC3339),
					ArtifactRefs: []run.ArtifactRef{},
				}
				if err := applyTerminal(tx, rec); err != nil {
					return err
				}
				return appendEffect(tx, run.ResumeKind, oldRunID, map[string]any{
					"run_id":     oldRunID,
					"item_id":    oldItemID,
					"lane_id":    c.LaneID,
					"backend":    "fixture",
					"session_id": "80a9eb94-0eb3-4c5b-8d09-17480030cdd0",
					"resumed_at": resumedAt.UTC().Format(time.RFC3339Nano),
				}, resumedAt)
			})

			w := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
			if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, "gate failed"); err != nil {
				t.Fatal(err)
			}
			released, err := l.releaseHeldCandidate(context.Background(), w, "gate-failed")
			if err != nil {
				t.Fatalf("releaseHeldCandidate: %v", err)
			}
			if released == tc.wantKept {
				t.Fatalf("released=%v, want %v", released, !tc.wantKept)
			}

			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				states, err := PendingLaneStates(r)
				if err != nil {
					return err
				}
				got := states[c.ItemID]
				if tc.wantKept {
					wantReason := "lane has an unfinished resume " + oldRunID
					if got.Status != "kept" || got.Reason != wantReason {
						t.Fatalf("lane state = %+v, want kept with reason %q", got, wantReason)
					}
					return nil
				}
				if got.Status != "released" {
					t.Fatalf("lane state = %+v, want released", got)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			if tc.wantKept {
				if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
					t.Fatalf("kept lane changed to %s, want %s", got, c.HeadSHA)
				}
				return
			}
			if got := mustPlan(t, l)[1].decision; got.Action != ActionDispatch || got.LaneID != c.LaneID {
				t.Fatalf("released lane not offered to waiting item: %+v", got)
			}
		})
	}
}

// Reproduces the 2026-11-24 lane-4 hold: a paused run left on a lane whose item
// later closed (or landed, or otherwise ended) must not stop a failed candidate
// from releasing its lane, while a paused run of an open item or a non-paused
// unfinished run still keeps it.
func TestHeldCandidateIgnoresPausedRunOfEndedItem(t *testing.T) {
	for _, tc := range []struct {
		name      string
		itemState string
		runState  string
		wantKept  bool
	}{
		{name: "closed", itemState: "closed", runState: run.StatePaused, wantKept: false},
		{name: "landed", itemState: StateLanded, runState: run.StatePaused, wantKept: false},
		{name: "resolved-elsewhere", itemState: StateResolvedElsewhere, runState: run.StatePaused, wantKept: false},
		{name: "active-in-queue", itemState: "in-queue", runState: run.StatePaused, wantKept: true},
		{name: "running-closed", itemState: "closed", runState: "running", wantKept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, c := heldCandidateFixture(t)
			oldItemID := "item-old-" + tc.name
			oldRunID := "run-" + oldItemID + "-a3-n2"
			finishedAt := l.cfg.Now().Add(-time.Hour)

			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES(?,?,?,?,?)`, oldItemID, oldItemID, tc.itemState, 0, ""); err != nil {
					return err
				}
				if tc.runState == run.StatePaused {
					rec := run.Record{
						RunID:        oldRunID,
						ItemID:       oldItemID,
						LaneID:       c.LaneID,
						Backend:      "fixture",
						State:        run.StatePaused,
						EndCause:     run.EndRestart,
						StartedAt:    finishedAt.Add(-5 * time.Minute).Format(time.RFC3339),
						FinishedAt:   finishedAt.Format(time.RFC3339),
						ArtifactRefs: []run.ArtifactRef{},
					}
					return applyTerminal(tx, rec)
				}
				_, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,?,?,'fixture',?,1,0,'[]',0)`, oldRunID, oldItemID, c.LaneID, tc.runState)
				return err
			})

			w := &pendingWork{Decision: mustPlan(t, l)[0].decision, Candidate: &c}
			if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, "gate failed"); err != nil {
				t.Fatal(err)
			}
			released, err := l.releaseHeldCandidate(context.Background(), w, "gate-failed")
			if err != nil {
				t.Fatalf("releaseHeldCandidate: %v", err)
			}
			if released == tc.wantKept {
				t.Fatalf("released=%v, want %v", released, !tc.wantKept)
			}

			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				states, err := PendingLaneStates(r)
				if err != nil {
					return err
				}
				got := states[c.ItemID]
				if tc.wantKept {
					wantReason := "lane has an unfinished run " + oldRunID
					if got.Status != "kept" || got.Reason != wantReason {
						t.Fatalf("lane state = %+v, want kept with reason %q", got, wantReason)
					}
					return nil
				}
				if got.Status != "released" {
					t.Fatalf("lane state = %+v, want released", got)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			if tc.wantKept {
				if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
					t.Fatalf("kept lane changed to %s, want %s", got, c.HeadSHA)
				}
				return
			}
			if got := mustPlan(t, l)[1].decision; got.Action != ActionDispatch || got.LaneID != c.LaneID {
				t.Fatalf("released lane not offered to waiting item: %+v", got)
			}
		})
	}
}
