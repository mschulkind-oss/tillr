package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
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
