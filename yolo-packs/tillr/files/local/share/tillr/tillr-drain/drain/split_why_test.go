package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func whyRowForItem(t *testing.T, l *Loop, policy *Policy, itemID string) WhyRow {
	t.Helper()
	var found *WhyRow
	if err := l.readStore(context.Background(), func(r *store.Reader) error {
		ans, err := WhyForItem(r, Pass{
			Now:       l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			StorePath: l.cfg.Store,
		}, policy, nil, itemID)
		if err != nil {
			return err
		}
		for i := range ans.Rows {
			if ans.Rows[i].ItemID == itemID {
				row := ans.Rows[i]
				found = &row
				break
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatalf("WhyForItem did not return a row for %s", itemID)
	}
	return *found
}

func TestWhyShowsPendingSplitCallReasonAndDesignMover(t *testing.T) {
	f, l, parent := splitFixture(t)
	recordTestSplitCallFromRefusal(t, f, l, parent, "run-why-pending", 3, 2, false, nil)

	const wantReason = "split called by the verifier; waiting on the design seat"

	// Held (no policy): still shows the split call reason and design seat mover.
	heldRow := whyRowForItem(t, l, nil, parent)
	if heldRow.Reason != ReasonSplitCallDesign {
		t.Errorf("held Reason = %q, want %q", heldRow.Reason, ReasonSplitCallDesign)
	}
	if !strings.HasPrefix(heldRow.Why, wantReason) || heldRow.TableReason() != wantReason {
		t.Errorf("held Why/TableReason = %q / %q, want %q", heldRow.Why, heldRow.TableReason(), wantReason)
	}
	if !strings.Contains(heldRow.Mover, "design") {
		t.Errorf("held Mover = %q, want it to name design", heldRow.Mover)
	}

	// Ready to dispatch to design seat: still shows the split call reason and design seat mover.
	readyRow := whyRowForItem(t, l, splitTestDesignPolicy(t, "designer"), parent)
	if readyRow.Reason != ReasonSplitCallDesign {
		t.Errorf("ready Reason = %q, want %q", readyRow.Reason, ReasonSplitCallDesign)
	}
	if !strings.HasPrefix(readyRow.Why, wantReason) || readyRow.TableReason() != wantReason {
		t.Errorf("ready Why/TableReason = %q / %q, want %q", readyRow.Why, readyRow.TableReason(), wantReason)
	}
	if !strings.Contains(readyRow.Mover, "design") {
		t.Errorf("ready Mover = %q, want it to name design", readyRow.Mover)
	}
}

func TestWhyIncludesNoSplitAnswerAndReason(t *testing.T) {
	f, l, parent := splitFixture(t)
	recordTestSplitCallFromRefusal(t, f, l, parent, "run-why-nosplit", 3, 2, false, nil)

	const noSplitReason = "shared invariant across files prevents splitting"
	s, err := store.Open(context.Background(), l.cfg.Store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.AnswerSplitNoSplit(context.Background(), tx, parent, "design-why-1", noSplitReason, l.cfg.Now())
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	row := whyRowForItem(t, l, splitTestDesignPolicy(t, "designer"), parent)
	if row.Reason == ReasonSplitCallDesign {
		t.Errorf("after no-split, Reason = %q, should no longer be %q", row.Reason, ReasonSplitCallDesign)
	}
	if !strings.Contains(row.Why, "no split: "+noSplitReason) {
		t.Errorf("Why = %q, want it to include %q", row.Why, "no split: "+noSplitReason)
	}
	if !strings.Contains(row.TableReason(), "no split: "+noSplitReason) {
		t.Errorf("TableReason() = %q, want it to include %q", row.TableReason(), "no split: "+noSplitReason)
	}
}
