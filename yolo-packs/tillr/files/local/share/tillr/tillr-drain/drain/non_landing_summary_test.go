package drain_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCharterFinishedStateIsNotListedAsHeldByLoopSummary(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_confirmed", "in-queue"),
		itemRecord("item_blocked", "blocked"),
	)
	raw := []byte(`{
		"definition": "findings",
		"version": 1,
		"states": ["in-queue", "blocked", "closed", "confirmed", "rejected"],
		"ready": "in-queue",
		"blocked": "blocked",
		"closed": "closed",
		"finished": ["confirmed", "rejected"],
		"transitions": [
			{"from": "in-queue", "to": "blocked"},
			{"from": "in-queue", "to": "closed"},
			{"from": "in-queue", "to": "confirmed"},
			{"from": "in-queue", "to": "rejected"},
			{"from": "blocked", "to": "in-queue"},
			{"from": "blocked", "to": "closed"},
			{"from": "closed", "to": "in-queue"}
		],
		"prerequisites": [],
		"completes": "hook",
		"retention": "keep",
		"brief_template": "",
		"hooks": {"argv": ["/bin/true"], "names": ["complete-item"]}
	}`)
	s, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = 'confirmed' WHERE item_id = 'item_confirmed'`)
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("stop = %q, want %q (detail: %s)", result.Stop, drain.StopQueueDrained, result.Detail)
	}
	if strings.Contains(result.Detail, "item_confirmed") {
		t.Fatalf("finished item_confirmed was listed as held in loop summary: %s", result.Detail)
	}
	if !strings.Contains(result.Detail, "1 held item(s)") || !strings.Contains(result.Detail, "item_blocked ("+drain.ReasonBlocked+")") {
		t.Fatalf("detail = %q, want 1 held item(s) naming item_blocked", result.Detail)
	}
}
