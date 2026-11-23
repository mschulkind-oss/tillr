package drain

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestTrunkAcquittalWaitsUntilTheSelectedBaseContainsTheRepair(t *testing.T) {
	ctx := context.Background()
	fixer := TrunkFixerID("abcdef123456")
	f, l := passFixture(t, "acquitted", fixer)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "fixer landed")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, fixer); err != nil {
			return err
		}
		if err := recordFixtureLanding(tx, fixer, "fixture-run", repaired, 1); err != nil {
			return err
		}
		_, err := item.SetState(ctx, tx, "acquitted", StateBlocked, l.cfg.Now(), item.WaitingOn{
			Waiter: "item:" + fixer, Reason: RefusalTrunkAlreadyRed + ": scratch on " + base,
		})
		return err
	})
	assertState := func(want string, waiter bool) {
		t.Helper()
		s, err := store.OpenRead(ctx, f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			var state string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id='acquitted'`).Scan(&state); err != nil {
				return err
			}
			waiting, _, err := item.ReadWaitingOn(r, "acquitted")
			if err != nil {
				return err
			}
			if state != want || (waiting != nil) != waiter {
				t.Fatalf("state=%s waiting=%+v; want %s waiter=%t", state, waiting, want, waiter)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	assertState(StateBlocked, true)
	runGit(t, f.root, "update-ref", GreenRef, repaired)
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	assertState(StateInQueue, false)
	for _, plan := range mustPlan(t, l) {
		if plan.decision.ItemID == "acquitted" {
			if plan.decision.Action != ActionDispatch {
				t.Fatalf("repaired item still held: %+v", plan.decision)
			}
			return
		}
	}
	t.Fatal("released item missing from plan")
}

// A tooling obstacle's wait holds the item, named for its defect item, until that item
// lands in the base staging selects; then the item is released and dispatched fresh.
func TestAToolingWaitHoldsUntilTheDefectItemLands(t *testing.T) {
	ctx := context.Background()
	defect := DefectItemID("go test ./...")
	f, l := passFixture(t, "waiting", defect)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, "waiting", StateBlocked, l.cfg.Now(), item.WaitingOn{
			Waiter: "item:" + defect, Reason: RefusalToolingDefect + ": go test ./...",
		})
		return err
	})
	held := func() Decision {
		t.Helper()
		for _, plan := range mustPlan(t, l) {
			if plan.decision.ItemID == "waiting" {
				return plan.decision
			}
		}
		t.Fatal("the waiting item is missing from the plan")
		return Decision{}
	}
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if d := held(); d.Action != ActionHold || d.Reason != ReasonToolingDefect || d.DeferredBy != defect {
		t.Fatalf("an open defect item did not hold its waiter: %+v", d)
	}
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "defect repaired")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, defect); err != nil {
			return err
		}
		return recordFixtureLanding(tx, defect, "fixture-run", repaired, 1)
	})
	runGit(t, f.root, "update-ref", GreenRef, repaired)
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if d := held(); d.Action != ActionDispatch {
		t.Fatalf("the repaired tooling still holds its waiter: %+v", d)
	}
}

// Closing the fixer a trunk-broken hold names releases every item held for that red on
// the next pass without an operator retry or a separate proof step.
func TestTrunkBrokenHoldReleasesOnPassWhenFixerIsClosed(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)
	red := ownerCommit(t, f.root, "red.txt")
	fixer, err := l.fileTrunkFixer(ctx, red, []string{"go-substrate/tools-tillr-s-whole-suite-passes"}, GateOutcome{SuspectPaths: []string{"tools/item-a/a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	heldA := fileFixtureRequestWith(t, f, "First request held behind trunk fixer\n", "build", item.SingleWorkerWorkflow, requestFence, false)
	heldB := fileFixtureRequestWith(t, f, "Second request held behind trunk fixer\n", "build", item.SingleWorkerWorkflow, requestFence, false)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, id := range []string{heldA, heldB} {
			if _, err := item.SetState(ctx, tx, id, StateBlocked, l.cfg.Now(), item.WaitingOn{
				Waiter: "item:" + fixer,
				Reason: RefusalTrunkAlreadyRed + ": go-substrate/tools-tillr-s-whole-suite-passes on " + red[:12],
			}); err != nil {
				return err
			}
		}
		return nil
	})
	for _, id := range []string{heldA, heldB} {
		if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonTrunkBroken || d.DeferredBy != fixer {
			t.Fatalf("before closing fixer, %s plan = %+v", id, d)
		}
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, fixer, "fix landed by hand", "operator", l.cfg.Now())
		return err
	})
	steps := &obstacleRequestSteps{
		t: t, f: f,
		report: func(req run.Request) (string, *landingOffer) {
			return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
		},
	}
	l.cfg.Steps = steps
	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	fixtureRead(t, f, func(r *store.Reader) error {
		for _, id := range []string{heldA, heldB} {
			var state string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
				return err
			}
			waiting, _, err := item.ReadWaitingOn(r, id)
			if err != nil {
				return err
			}
			if state == StateBlocked || waiting != nil {
				t.Fatalf("%s stayed held after fixer closed: state=%s waiting=%+v", id, state, waiting)
			}
		}
		return nil
	})
}

// Proving the trunk past the red commit releases items held trunk-broken on the next
// pass, both on a linear trunk and after the red commit was rebased onto a moved base
// (including when trunk_rewrites has no row for an older accepted rewrite).
func TestTrunkBrokenHoldReleasesOnPassWhenTrunkIsProvedPastRedCommit(t *testing.T) {
	for _, tc := range []struct {
		name            string
		rebase          bool
		clearRewrites   bool
		closeFixerFirst bool
	}{
		{name: "linear"},
		{name: "rebased-accepted", rebase: true},
		{name: "rebased-without-rewrite-row", rebase: true, clearRewrites: true, closeFixerFirst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, l, _ := workflowLoopFixture(t, "")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed'`)
				return err
			})
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			runGit(t, f.root, "update-ref", GreenRef, base)
			red := ownerCommit(t, f.root, "red.txt")
			if _, err := l.observeTrunk(ctx, ""); err != nil {
				t.Fatal(err)
			}
			fixer, err := l.fileTrunkFixer(ctx, red, []string{"go-substrate/tools-tillr-s-whole-suite-passes"}, GateOutcome{SuspectPaths: []string{"tools/item-a/a.txt"}})
			if err != nil {
				t.Fatal(err)
			}
			held := fileFixtureRequestWith(t, f, "Request held behind trunk red\n", "build", item.SingleWorkerWorkflow, requestFence, false)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := item.SetState(ctx, tx, held, StateBlocked, l.cfg.Now(), item.WaitingOn{
					Waiter: "item:" + fixer,
					Reason: RefusalTrunkAlreadyRed + ": go-substrate/tools-tillr-s-whole-suite-passes on " + red[:12],
				})
				return err
			})

			later := l.cfg.Now().UTC().Add(time.Minute)
			belowRed := base
			if tc.rebase {
				rebased := rebaseOnto(t, f.root, base, red)
				if _, err := acceptRewrite(f, red, rebased); err != nil {
					t.Fatalf("acceptRewrite: %v", err)
				}
				if tc.clearRewrites {
					fixtureWrite(t, f, func(tx *store.Tx) error {
						_, err := tx.Exec(`DELETE FROM trunk_rewrites`)
						return err
					})
				}
				belowRed = runGit(t, f.root, "rev-parse", rebased+"^")
			}

			// A proof below the red commit (or below its rebased replacement) must not release the hold.
			writeGreenProof(t, l, belowRed, later, []string{"go-substrate/tools-tillr-s-whole-suite-passes"})
			if err := l.resolveProvedFixers(ctx); err != nil {
				t.Fatal(err)
			}
			if err := l.releaseTrunkAcquittals(ctx); err != nil {
				t.Fatal(err)
			}
			if got := fixerState(t, f, held); got != StateBlocked {
				t.Fatalf("proof below red released held item early: %s", got)
			}

			if tc.closeFixerFirst {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := item.Close(ctx, tx, fixer, "landed by hand", "operator", l.cfg.Now())
					return err
				})
			}

			repaired := ownerCommit(t, f.root, "repaired.txt")
			writeGreenProof(t, l, repaired, later.Add(time.Minute), []string{"go-substrate/tools-tillr-s-whole-suite-passes"})
			steps := &obstacleRequestSteps{
				t: t, f: f,
				report: func(req run.Request) (string, *landingOffer) {
					return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
				},
			}
			l.cfg.Steps = steps
			result := &LoopResult{}
			if _, err := l.pass(ctx, result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)

			if !tc.closeFixerFirst {
				if got := fixerState(t, f, fixer); got != StateResolvedElsewhere {
					t.Fatalf("fixer state = %s, want %s", got, StateResolvedElsewhere)
				}
			} else {
				fixtureRead(t, f, func(r *store.Reader) error {
					proved, err := fixerProvedGreen(r, fixer)
					if err != nil {
						return err
					}
					if proved != repaired {
						t.Fatalf("closed fixer proof = %q, want %q", proved, repaired)
					}
					return nil
				})
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var state string
				if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, held).Scan(&state); err != nil {
					return err
				}
				waiting, _, err := item.ReadWaitingOn(r, held)
				if err != nil {
					return err
				}
				if state == StateBlocked || waiting != nil {
					t.Fatalf("%s stayed held after trunk proved past red: state=%s waiting=%+v", held, state, waiting)
				}
				return nil
			})
		})
	}
}

type acquittalRegateSteps struct {
	obstacleRequestSteps
	mu        sync.Mutex
	gateCalls []Candidate
	runIDs    []string
	gateFn    func(c Candidate) (GateOutcome, error)
	replays   map[string]ReplayOutcome
	commitSeq int
}

func (s *acquittalRegateSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.mu.Lock()
	s.commitSeq++
	seq := s.commitSeq
	s.runIDs = append(s.runIDs, req.RunID)
	s.mu.Unlock()
	if req.ItemID != "" && !IsTrunkFixer(req.ItemID) {
		target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
		path := filepath.Join(target, "request.txt")
		if err := os.WriteFile(path, []byte("change "+string(rune('0'+seq))+"\n"), 0o644); err != nil {
			return run.Record{}, "", err
		}
		runGit(s.t, target, "add", "request.txt")
		runGit(s.t, target, "commit", "-qm", "candidate commit")
	}
	return s.obstacleRequestSteps.Run(ctx, req)
}

func (s *acquittalRegateSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	s.gateCalls = append(s.gateCalls, c)
	fn := s.gateFn
	s.mu.Unlock()
	return fn(c)
}

func (s *acquittalRegateSteps) Replay(_ context.Context, _ Candidate, rev string, _ []string) (ReplayOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if out, ok := s.replays[rev]; ok {
		return out, nil
	}
	return ReplayOutcome{Rev: rev, Known: true}, nil
}

func acquittedCandidateFixture(t *testing.T) (refusalLoopFixture, *Loop, *acquittalRegateSteps, string, string, Candidate) {
	t.Helper()
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)
	reqID := fileFixtureRequestWith(t, f, "Acquitted candidate re-gate test\n", "build", item.SingleWorkerWorkflow, []string{"request.txt"}, false)
	steps := &acquittalRegateSteps{
		obstacleRequestSteps: obstacleRequestSteps{
			t: t, f: f,
			report: func(req run.Request) (string, *landingOffer) {
				return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
			},
		},
		replays: map[string]ReplayOutcome{
			base: {Rev: base, Red: []string{"unit"}, Known: true},
		},
		gateFn: func(c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateFailed,
				FailingChecks: []string{"unit"},
				SuspectPaths:  []string{"request.txt"},
				CheckWatches:  map[string][]string{"unit": {"request.txt"}},
			}, nil
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if result.TrunkAcquitted != 1 {
		t.Fatalf("TrunkAcquitted = %d, want 1", result.TrunkAcquitted)
	}
	fixer := TrunkFixerID(base)
	if got := fixerState(t, f, reqID); got != StateBlocked {
		t.Fatalf("acquitted item state = %s, want %s", got, StateBlocked)
	}
	if len(steps.runIDs) != 1 || len(steps.gateCalls) != 1 {
		t.Fatalf("runs=%d gates=%d, want 1 and 1", len(steps.runIDs), len(steps.gateCalls))
	}
	if d := planFor(t, l, reqID); d.Action != ActionHold || d.Reason != ReasonTrunkBroken || d.DeferredBy != fixer {
		t.Fatalf("held plan = %+v, want hold/%s deferred by %s", d, ReasonTrunkBroken, fixer)
	}
	return f, l, steps, reqID, fixer, steps.gateCalls[0]
}

// An acquitted candidate released by its fixer's close reaches the gate with the same
// commit and no new worker run.
func TestAcquittedCandidateReleasedByFixerCloseGatesSameCommitWithoutNewRun(t *testing.T) {
	ctx := context.Background()
	f, l, steps, reqID, fixer, firstCand := acquittedCandidateFixture(t)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, fixer, "false red", "operator", l.cfg.Now())
		return err
	})
	steps.mu.Lock()
	steps.gateFn = func(c Candidate) (GateOutcome, error) {
		return GateOutcome{Outcome: GatePassed}, nil
	}
	steps.mu.Unlock()

	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)

	if len(steps.runIDs) != 1 {
		t.Fatalf("worker runs = %v, want 1 (no new worker run)", steps.runIDs)
	}
	if len(steps.gateCalls) != 2 {
		t.Fatalf("gate calls = %d, want 2", len(steps.gateCalls))
	}
	if steps.gateCalls[1].HeadSHA != firstCand.HeadSHA {
		t.Fatalf("re-gated head = %s, want same commit %s", steps.gateCalls[1].HeadSHA, firstCand.HeadSHA)
	}
	if steps.gateCalls[1].RunID != firstCand.RunID {
		t.Fatalf("re-gated run = %s, want same run %s", steps.gateCalls[1].RunID, firstCand.RunID)
	}
	if got := fixerState(t, f, reqID); got != StateLanded {
		t.Fatalf("released item state = %s, want %s", got, StateLanded)
	}
}

// An acquitted candidate released when its fixer lands reaches the gate without a new
// worker run.
func TestAcquittedCandidateReleasedByFixerLandingGatesKeptCandidateWithoutNewRun(t *testing.T) {
	ctx := context.Background()
	f, l, steps, reqID, fixer, firstCand := acquittedCandidateFixture(t)

	repaired := ownerCommit(t, f.root, "repair.txt")
	runGit(t, f.root, "update-ref", GreenRef, repaired)
	if err := recordTrunkRepair(ctx, f.root, repaired, "fixer landed"); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, fixer); err != nil {
			return err
		}
		return recordFixtureLanding(tx, fixer, "fixer-run-1", repaired, 1)
	})
	steps.mu.Lock()
	steps.gateFn = func(c Candidate) (GateOutcome, error) {
		return GateOutcome{Outcome: GatePassed}, nil
	}
	steps.mu.Unlock()

	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)

	if len(steps.runIDs) != 1 {
		t.Fatalf("worker runs = %v, want 1 (no new worker run)", steps.runIDs)
	}
	if len(steps.gateCalls) != 2 {
		t.Fatalf("gate calls = %d, want 2", len(steps.gateCalls))
	}
	if steps.gateCalls[1].RunID != firstCand.RunID {
		t.Fatalf("re-gated run = %s, want same run %s", steps.gateCalls[1].RunID, firstCand.RunID)
	}
	if got := fixerState(t, f, reqID); got != StateLanded {
		t.Fatalf("released item state = %s, want %s", got, StateLanded)
	}
}

// An acquitted item whose candidate commit is gone is redone with a new worker run when
// released.
func TestAcquittedCandidateWithGoneCommitIsRedone(t *testing.T) {
	ctx := context.Background()
	f, l, steps, reqID, fixer, firstCand := acquittedCandidateFixture(t)

	// Remove the kept candidate's pin ref so the candidate commit is gone.
	runGit(t, f.root, "update-ref", "-d", candidatePinRef(firstCand))
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, fixer, "false red", "operator", l.cfg.Now())
		return err
	})
	steps.mu.Lock()
	steps.gateFn = func(c Candidate) (GateOutcome, error) {
		return GateOutcome{Outcome: GatePassed}, nil
	}
	steps.mu.Unlock()

	result := &LoopResult{}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)

	if len(steps.runIDs) != 2 {
		t.Fatalf("worker runs = %v, want 2 (redone after gone candidate)", steps.runIDs)
	}
	if steps.runIDs[1] == firstCand.RunID {
		t.Fatalf("second run reused %s, want a new worker run", firstCand.RunID)
	}
	if got := fixerState(t, f, reqID); got != StateLanded {
		t.Fatalf("redone item state = %s, want %s", got, StateLanded)
	}
}
