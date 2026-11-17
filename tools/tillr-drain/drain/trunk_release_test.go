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
		if _, err := tx.Exec(`CREATE TABLE landings(item_id TEXT,trunk_after TEXT,result TEXT,event_seq INTEGER)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings VALUES(?,?,'landed',1)`, fixer, repaired); err != nil {
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
