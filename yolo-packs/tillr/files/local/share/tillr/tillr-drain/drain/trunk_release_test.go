package drain

import (
	"context"
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
