package main_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// state runs the state face against one store.
func state(t *testing.T, db, id, want, now string) (int, []byte) {
	t.Helper()
	args := []string{"state", "--store", db, "--item", id, "--state", want, "--now", now}
	if want == "blocked" {
		args = append(args, "--waiter", "owner", "--reason", "needs a decision")
	}
	return run(t, args...)
}

// The state face is the writer of board-item-state-model.md §2's item states: a landed
// candidate moves its item to `landed`, and the move touches nothing else on the item.
//
// The criterion is seeded already judged, because that is what a landed candidate's item
// looks like: the verdict face has retired its criteria and the item's own state is the
// remaining half. A face that rewrote criteria to move the item would clear the verdict it
// did not record, so the case asserts the criterion is exactly as it was stored.
func TestStateMovesTheItemAndLeavesTheCriteriaAlone(t *testing.T) {
	db := filepath.Join(scratch(t, "state"), "board.sqlite3")
	seeded := base("item_042", "the item state face")
	seeded.State = "in-queue"
	seeded.Criteria = []criterion{{CriterionID: "c1", Text: "the first criterion", State: "done"}}
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", seeded)); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	addTerminalEvidence(t, db, "item_042", "landing.recorded")
	updates := eventsOfKind(t, db, "item.update")

	code, out := state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z")
	if code != 0 {
		t.Fatalf("state exited %d: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "item.record" {
		t.Fatalf("state emitted kind %q, want item.record", got.Kind)
	}
	if got.Payload.State != "landed" {
		t.Fatalf("the echo says the item is %q, want landed", got.Payload.State)
	}
	if got.Payload.UpdatedAt != "2026-11-10T00:10:00Z" {
		t.Fatalf("updated_at is %q, want the injected clock", got.Payload.UpdatedAt)
	}
	if len(got.Payload.Criteria) != 1 || got.Payload.Criteria[0].State != "done" ||
		got.Payload.Criteria[0].Text != "the first criterion" {
		t.Fatalf("the state move rewrote the item's criteria: %+v", got.Payload.Criteria)
	}
	if n := eventsOfKind(t, db, "item.update"); n != updates+1 {
		t.Fatalf("the state move appended %d item.update event(s), want 1", n-updates)
	}
	// The row is the item's state, so a read after the write answers the move rather than
	// the echo of it.
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	} else if stored := decode(t, out); stored.Payload.State != "landed" {
		t.Fatalf("the stored item is %q after the move, want landed", stored.Payload.State)
	}
}

// A move to the state an item is already in writes nothing: a loop step retried after a
// half-completed tick must not record a transition that did not happen.
func TestStateIsIdempotent(t *testing.T) {
	db := filepath.Join(scratch(t, "state-idempotent"), "board.sqlite3")
	seeded := base("item_042", "the item state face")
	seeded.State = "blocked"
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", seeded)); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	if code, out := state(t, db, "item_042", "blocked", "2026-11-10T00:09:00Z"); code != 0 {
		t.Fatalf("initial block exited %d: %s", code, out)
	}
	updates := eventsOfKind(t, db, "item.update")
	code, out := state(t, db, "item_042", "blocked", "2026-11-10T00:10:00Z")
	if code != 0 {
		t.Fatalf("state exited %d: %s", code, out)
	}
	if got := decode(t, out); got.Payload.State != "blocked" {
		t.Fatalf("the echo says the item is %q, want blocked", got.Payload.State)
	}
	if n := eventsOfKind(t, db, "item.update"); n != updates {
		t.Fatalf("a move to the state the item is already in appended %d event(s), want none", n-updates)
	}
}

// The state vocabulary is item's closed one, and its two refusals are the two the record's
// writer already makes: a frozen legacy value is a negative decision, and a word that is
// not a state is malformed input.
func TestStateRefusesAStateNoTransitionMayWrite(t *testing.T) {
	db := filepath.Join(scratch(t, "state-refusal"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "the item"))); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	for _, c := range []struct {
		want string
		code int
		rule string
	}{
		{"deleted", 1, "state-frozen"},
		{"abandoned", 1, "state-frozen"},
		{"in-progress", 1, "state-frozen"},
		{"in-queue ", 2, ""},
	} {
		code, out := state(t, db, "item_042", c.want, "2026-11-10T00:10:00Z")
		if code != c.code {
			t.Fatalf("state %q exited %d, want %d: %s", c.want, code, c.code, out)
		}
		if c.rule != "" {
			if got := decode(t, out); got.Payload.Rule != c.rule {
				t.Fatalf("state %q was refused under rule %q, want %q", c.want, got.Payload.Rule, c.rule)
			}
		}
	}
	// Neither refusal moved the item: the refusals are answers, not half-writes.
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	} else if got := decode(t, out); got.Payload.State != "not-started" {
		t.Fatalf("a refused state move left the item %q, want not-started", got.Payload.State)
	}
}

// An item the store does not hold is a negative decision that names it, not a silent
// success: the loop's step reads the exit code, and a move that "succeeded" on no item
// would report a landed item the board never moved.
func TestStateOfAMissingItemIsARejection(t *testing.T) {
	db := filepath.Join(scratch(t, "state-missing"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "the item"))); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	code, out := state(t, db, "item_999", "landed", "2026-11-10T00:10:00Z")
	if code != 1 {
		t.Fatalf("state of a missing item exited %d, want 1: %s", code, out)
	}
	if got := decode(t, out); got.Payload.Rule != "not-found" || got.Payload.ItemID != "item_999" {
		t.Fatalf("the rejection is %q about %q, want not-found about item_999", got.Payload.Rule, got.Payload.ItemID)
	}
}

// The criterion rule the state face must not weaken: after an item's state has moved, an
// apply that presents a different criterion state is still refused. The two records are
// different records — an item's position and a judge's finding — and the state face writes
// only the first.
func TestAStateMoveDoesNotUnfreezeACriterion(t *testing.T) {
	db := filepath.Join(scratch(t, "state-criterion"), "board.sqlite3")
	seeded := base("item_042", "the item state face")
	seeded.State = "in-queue"
	seeded.Criteria = []criterion{{CriterionID: "c1", Text: "the first criterion", State: "done"}}
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", seeded)); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	addTerminalEvidence(t, db, "item_042", "landing.recorded")
	if code, out := state(t, db, "item_042", "landed", "2026-11-10T00:10:00Z"); code != 0 {
		t.Fatalf("state exited %d: %s", code, out)
	}
	edited := base("item_042", "the item state face")
	edited.State = "landed"
	edited.Criteria = []criterion{{CriterionID: "c1", Text: "the first criterion", State: "not-done"}}
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", edited))
	if code != 1 {
		t.Fatalf("an apply presenting a changed criterion state exited %d, want 1: %s", code, out)
	}
	if got := decode(t, out); got.Payload.Rule != "criterion-state" {
		t.Fatalf("the refusal names rule %q, want criterion-state", got.Payload.Rule)
	}
}

// stateRow reads one item's stored state through the substrate's reader, which is how a
// caller that is not the item utility answers "what does the board say".
func stateRow(t *testing.T, db, id string) string {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	var got string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, id).Scan(&got)
	}); err != nil {
		t.Fatalf("reading the state of %q failed: %v", id, err)
	}
	return got
}

// The row, not only the echo, is what the next reader sees: the same claim as the first
// case, read the other way round, so a face that answered the echo without writing the row
// fails here even though it passed there.
func TestStateWritesTheRow(t *testing.T) {
	db := filepath.Join(scratch(t, "state-row"), "board.sqlite3")
	seeded := base("item_042", "the item state face")
	seeded.State = "in-queue"
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", seeded)); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	if code, out := state(t, db, "item_042", "blocked", "2026-11-10T00:10:00Z"); code != 0 {
		t.Fatalf("state exited %d: %s", code, out)
	}
	if got := stateRow(t, db, "item_042"); got != "blocked" {
		t.Fatalf("the row says %q, want blocked", got)
	}
}

// A legacy `in-progress` row is frozen, not deleted: it stays readable and moves out
// through the same face. The state became unwritable because no transition should create
// one, and a row written before that rule is not stranded by it.
func TestStateMovesALegacyInProgressRowOut(t *testing.T) {
	db := filepath.Join(scratch(t, "state-legacy"), "board.sqlite3")
	ctx := context.Background()
	s, err := store.Open(ctx, db, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", db, err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, ?)`,
			"item_042", "a row written before in-progress was frozen", "in-progress", 0,
		)
		return err
	}); err != nil {
		s.Close()
		t.Fatalf("writing the legacy row failed: %v", err)
	}
	s.Close()

	if got := stateRow(t, db, "item_042"); got != "in-progress" {
		t.Fatalf("the legacy row reads %q, want in-progress", got)
	}
	if code, out := state(t, db, "item_042", "in-queue", "2026-11-10T00:10:00Z"); code != 0 {
		t.Fatalf("state exited %d: %s", code, out)
	}
	if got := stateRow(t, db, "item_042"); got != "in-queue" {
		t.Fatalf("the legacy row moved to %q, want in-queue", got)
	}
}
