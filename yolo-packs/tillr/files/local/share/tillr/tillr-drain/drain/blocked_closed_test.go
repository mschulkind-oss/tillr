package drain

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPlanAndWhyClosedWithoutLandingBlocker(t *testing.T) {
	f, l := passFixture(t, "closed-dep", "landed-dep", "resolved-dep", "active-dep", "wait-closed", "wait-mixed", "wait-waiter-closed", "wait-unblocked")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`
			UPDATE items SET state='closed' WHERE item_id='closed-dep';
			UPDATE items SET state='landed' WHERE item_id='landed-dep';
			UPDATE items SET state='resolved-elsewhere' WHERE item_id='resolved-dep';
			UPDATE items SET state='blocked', waiting_on='{"waiter":"item:closed-dep","reason":"needs closed-dep"}' WHERE item_id='wait-waiter-closed';
			INSERT INTO item_edges(item_id,blocked_by) VALUES
				('wait-closed','closed-dep'),
				('wait-mixed','active-dep'),
				('wait-mixed','closed-dep'),
				('wait-unblocked','landed-dep'),
				('wait-unblocked','resolved-dep');
			INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle');
		`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()

	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		envs, err := Plan(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		decisions := map[string]Decision{}
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			decisions[d.ItemID] = d
		}
		for _, id := range []string{"wait-closed", "wait-mixed", "wait-waiter-closed"} {
			if got := decisions[id]; got.Action != ActionHold || got.Reason != ReasonBlocked {
				t.Fatalf("%s planned %s (%s), want hold (%s)", id, got.Action, got.Reason, ReasonBlocked)
			}
		}
		if got := decisions["wait-unblocked"]; got.Reason == ReasonBlocked {
			t.Fatalf("wait-unblocked should not be blocked behind landed/resolved-elsewhere deps: %+v", got)
		}

		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		rows := map[string]WhyRow{}
		for _, row := range answer.Rows {
			rows[row.ItemID] = row
		}

		closedRow := rows["wait-closed"]
		if closedRow.Reason != ReasonBlocked || closedRow.Class != "needs you" {
			t.Fatalf("wait-closed row: %+v", closedRow)
		}
		if !reflect.DeepEqual(closedRow.WaitingOn, []string{"closed-dep"}) {
			t.Fatalf("wait-closed WaitingOn = %v, want [closed-dep]", closedRow.WaitingOn)
		}
		if !strings.Contains(closedRow.Why, "closed-dep") || !strings.Contains(closedRow.Why, "closed without landing") {
			t.Fatalf("wait-closed Why = %q, want closed-dep and closed without landing", closedRow.Why)
		}
		if !strings.HasPrefix(closedRow.Mover, "operator;") || !strings.Contains(closedRow.Mover, "closed-dep") || !strings.Contains(closedRow.Mover, "remove the dependency or reopen the closed item") {
			t.Fatalf("wait-closed Mover = %q", closedRow.Mover)
		}

		mixedRow := rows["wait-mixed"]
		if mixedRow.Reason != ReasonBlocked || mixedRow.Class != "needs you" {
			t.Fatalf("wait-mixed row: %+v", mixedRow)
		}
		if !reflect.DeepEqual(mixedRow.WaitingOn, []string{"active-dep", "closed-dep"}) {
			t.Fatalf("wait-mixed WaitingOn = %v, want [active-dep closed-dep]", mixedRow.WaitingOn)
		}
		if !strings.Contains(mixedRow.Why, "active-dep") || !strings.Contains(mixedRow.Why, "closed-dep") || !strings.Contains(mixedRow.Why, "closed without landing") {
			t.Fatalf("wait-mixed Why = %q", mixedRow.Why)
		}
		if !strings.HasPrefix(mixedRow.Mover, "operator;") || !strings.Contains(mixedRow.Mover, "active-dep") || !strings.Contains(mixedRow.Mover, "closed-dep") || !strings.Contains(mixedRow.Mover, "remove the dependency or reopen the closed item") {
			t.Fatalf("wait-mixed Mover = %q", mixedRow.Mover)
		}

		waiterRow := rows["wait-waiter-closed"]
		if waiterRow.Reason != ReasonBlocked || waiterRow.Class != "needs you" {
			t.Fatalf("wait-waiter-closed row: %+v", waiterRow)
		}
		if !strings.Contains(waiterRow.Why, "closed-dep") || !strings.Contains(waiterRow.Why, "closed without landing") {
			t.Fatalf("wait-waiter-closed Why = %q, want closed-dep and closed without landing", waiterRow.Why)
		}
		if !strings.HasPrefix(waiterRow.Mover, "operator;") || !strings.Contains(waiterRow.Mover, "closed-dep") || !strings.Contains(waiterRow.Mover, "remove the dependency or reopen the closed item") {
			t.Fatalf("wait-waiter-closed Mover = %q", waiterRow.Mover)
		}

		if rows["wait-unblocked"].Reason == ReasonBlocked {
			t.Fatalf("wait-unblocked should not report ReasonBlocked: %+v", rows["wait-unblocked"])
		}
		if answer.Summary.OperatorNext != 3 {
			t.Fatalf("OperatorNext = %d, want 3", answer.Summary.OperatorNext)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
