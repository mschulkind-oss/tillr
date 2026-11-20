package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyCountsRefusalsPerItem(t *testing.T) {
	f, l := passFixture(t, "alpha", "beta")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, step := range []string{RefusalVerifyNotDone, RefusalVerifyNotDone, RefusalGate, RefusalFence, RefusalConflict} {
			if err := appendEffect(tx, RefusalKind, "run-alpha-a"+string(rune('1'+i)), map[string]string{"item_id": "alpha", "step": step}, l.cfg.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		a, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(a.Rows) != 2 {
			t.Fatalf("expected both items: %+v", a.Rows)
		}
		for _, row := range a.Rows {
			if row.ItemID == "alpha" {
				if row.Refusals[RefusalVerifyNotDone] != 2 || row.Refusals[RefusalFence] != 1 || row.Refusals[RefusalGate] != 1 || row.Refusals[RefusalConflict] != 1 || !strings.Contains(row.Why, "verify-not-done=2") {
					t.Fatalf("alpha: %+v", row)
				}
			} else if len(row.Refusals) != 0 {
				t.Fatalf("other item: %+v", row)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
