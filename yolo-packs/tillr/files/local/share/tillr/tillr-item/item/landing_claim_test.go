package item_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// While a landing of the item is publishing — its claim row stands between the lander's two
// short writes — the item cannot be closed or resolved elsewhere; once the claim is gone it
// can. The refusal names the run, so an owner knows which landing to wait for or retry.
func TestALandingClaimRefusesCloseAndResolvedElsewhere(t *testing.T) {
	s := syncStore(t)
	if report := runSync(t, s, "", doc("docs/design/plan.md", goodBlock)); len(report.Findings) != 0 {
		t.Fatalf("initial sync: %+v", report)
	}
	ctx := context.Background()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TABLE landing_claims (run_id TEXT PRIMARY KEY, item_id TEXT NOT NULL);
			INSERT INTO landing_claims VALUES ('run-publishing', 'item-build-thing')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	closeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, "item-build-thing", "not wanted", "owner", syncAt)
		return err
	})
	if closeErr == nil || !strings.Contains(closeErr.Error(), "run-publishing") {
		t.Fatalf("close under a landing claim: %v", closeErr)
	}
	resolveErr := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetStateWithReceipt(ctx, tx, "item-build-thing", "resolved-elsewhere", syncAt, item.MoveReceipt{ResolvesVia: "doc:docs/elsewhere.md"})
		return err
	})
	if resolveErr == nil || !strings.Contains(resolveErr.Error(), "run-publishing") {
		t.Fatalf("resolved-elsewhere under a landing claim: %v", resolveErr)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`DELETE FROM landing_claims`); err != nil {
			return err
		}
		_, err := item.Close(ctx, tx, "item-build-thing", "not wanted", "owner", syncAt)
		return err
	}); err != nil {
		t.Fatalf("close after the claim was released: %v", err)
	}
}
