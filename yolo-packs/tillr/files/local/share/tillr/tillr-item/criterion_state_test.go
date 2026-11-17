package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// The criterion-freeze cases: an edit carries a criterion's recorded state forward, so a
// criterion a verdict froze stays frozen, and an edit that asks for a different state is
// refused by name. A criterion the store already holds with a state has been presented,
// so its text is frozen too: it may be kept or appended after, never rewritten and never
// dropped. The rule is board-utilities.md §5.3's writer contract and D-UT16's retirement
// split; these cases pin the edit half of it.

// showItem reads one item back through the show face, which answers the row rather than
// the newest record in the log.
func showItem(t *testing.T, db, id string) itemOut {
	t.Helper()
	code, out := run(t, "show", "--store", db, "--item", id)
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	return decode(t, out)
}

// criterionOf answers the stored criterion with this id, so a case asserts what the
// store holds rather than what a face echoed.
func criterionOf(t *testing.T, stored itemOut, id string) criterion {
	t.Helper()
	for _, c := range stored.Payload.Criteria {
		if c.CriterionID == id {
			return c
		}
	}
	t.Fatalf("the stored item carries no criterion %q: %+v", id, stored.Payload.Criteria)
	return criterion{}
}

// storedWithCriterion applies one item whose single criterion is in the given state and
// answers its store path: the state a criterion is created in is the state the store
// records, which is what a later verdict would leave behind.
func storedWithCriterion(t *testing.T, prefix, state string) string {
	t.Helper()
	db := filepath.Join(scratch(t, prefix), "board.sqlite3")
	in := base("item_042", "the item face")
	in.Criteria[0].State = state
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", in)); code != 0 {
		t.Fatalf("applying the item exited %d: %s", code, out)
	}
	return db
}

// A criterion stored with no state at all was never presented, so there is nothing to
// freeze: the text moves and the state it has — none — does not. The edit presents no
// state either, since presenting one would itself be a state change (the next case).
func TestAnEditLeavesAnUnpresentedCriterionUnfrozen(t *testing.T) {
	db := storedWithCriterion(t, "criterion-unpresented", "")
	in := base("item_042", "the item face, edited")
	in.Criteria[0].Text = "the first criterion, reworded"
	in.Criteria[0].State = ""
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", in))
	if code != 0 {
		t.Fatalf("the edit exited %d: %s", code, out)
	}
	if got := decode(t, out); criterionOf(t, got, "c1") != (criterion{CriterionID: "c1", Text: "the first criterion, reworded", ClaimClass: "verification-only"}) {
		t.Fatalf("the edit echoed the criterion as %+v", criterionOf(t, got, "c1"))
	}
	stored := criterionOf(t, showItem(t, db, "item_042"), "c1")
	if stored.State != "" || stored.Text != "the first criterion, reworded" {
		t.Fatalf("the store holds %+v, want the reworded text still stating no state", stored)
	}
}

// An edit of a criterion a verdict has frozen leaves the state the verdict wrote. The
// edit here presents the text as the store holds it and no state at all, so the stored
// state stands rather than being cleared or rewritten.
func TestAnEditLeavesAFrozenCriterionFrozen(t *testing.T) {
	db := storedWithCriterion(t, "criterion-frozen", "done")
	in := base("item_042", "the item face, edited")
	in.Criteria[0].State = ""
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", in))
	if code != 0 {
		t.Fatalf("the edit exited %d: %s", code, out)
	}
	if got := decode(t, out); criterionOf(t, got, "c1").State != "done" {
		t.Fatalf("the edit echoed the criterion as %+v, want the state done", criterionOf(t, got, "c1"))
	}
	if stored := criterionOf(t, showItem(t, db, "item_042"), "c1"); stored.State != "done" {
		t.Fatalf("the store holds the criterion as %+v, want the state done", stored)
	}
}

// The refusal is narrow, so the two legitimate acts that ride the same edit still land:
// the item's own state transition, and a criterion appended with the state the caller
// presents — the store holds none for it, so there is nothing to carry forward.
func TestALegitimateEditStillWritesItsState(t *testing.T) {
	db := storedWithCriterion(t, "criterion-legitimate", "done")
	in := base("item_042", "the item face, started")
	in.State = "in-queue"
	in.Criteria[0].State = "done"
	in.Criteria = append(in.Criteria, criterion{CriterionID: "c2", Text: "a criterion added later", State: "open"})
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", in))
	if code != 0 {
		t.Fatalf("the edit exited %d: %s", code, out)
	}
	if got := decode(t, out); got.Payload.State != "in-queue" {
		t.Fatalf("the edit echoed the item as %q, want the state in-queue", got.Payload.State)
	}
	if n := eventsOfKind(t, db, "item.update"); n != 1 {
		t.Fatalf("the edit appended %d item.update events, want 1", n)
	}
	stored := showItem(t, db, "item_042")
	if stored.Payload.State != "in-queue" {
		t.Fatalf("the store holds the item as %q, want the state in-queue", stored.Payload.State)
	}
	if c := criterionOf(t, stored, "c1"); c.State != "done" {
		t.Fatalf("the store holds the frozen criterion as %+v, want the state done", c)
	}
	if c := criterionOf(t, stored, "c2"); c.State != "open" || c.Text != "a criterion added later" {
		t.Fatalf("the store holds the appended criterion as %+v", c)
	}
}

// A presented criterion is frozen as to its text: the words a judge applies cannot be
// rewritten under it, so the edit is refused by name, writes nothing, and leaves the
// store holding the text the verdict was recorded against.
func TestAFrozenCriterionsTextCannotBeRewritten(t *testing.T) {
	db := storedWithCriterion(t, "criterion-rewrite", "done")
	in := base("item_042", "the item face, edited")
	in.Criteria[0].Text = "the first criterion, eased"
	in.Criteria[0].State = ""
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", in))
	if code != 1 {
		t.Fatalf("the edit exited %d, want 1: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "item.rejection" {
		t.Fatalf("the edit emitted kind %q, want the rejection record", got.Kind)
	}
	if got.Payload.Rule != "criterion-frozen" {
		t.Fatalf("the edit refused with rule %q, want criterion-frozen", got.Payload.Rule)
	}
	// The reason has to name the criterion and say what the writer should do instead:
	// a refusal a caller cannot act on is a dead end.
	if reason := got.Payload.Reason; !strings.Contains(reason, "c1") || !strings.Contains(reason, "appended") {
		t.Fatalf("the refusal does not name the criterion and the append it needs: %q", reason)
	}
	if n := eventsOfKind(t, db, "item.update"); n != 0 {
		t.Fatalf("a refused edit appended %d item.update events", n)
	}
	stored := criterionOf(t, showItem(t, db, "item_042"), "c1")
	if stored.State != "done" || stored.Text != "the first criterion" {
		t.Fatalf("the store holds %+v, want the criterion as it was", stored)
	}
}

// Dropping a presented criterion is the same move as rewriting it — the row a verdict
// was recorded against disappears and the item's bar silently changes — so it is
// refused under the same rule.
func TestAFrozenCriterionCannotBeDropped(t *testing.T) {
	db := storedWithCriterion(t, "criterion-drop", "done")
	in := base("item_042", "the item face, edited")
	in.Criteria = []criterion{}
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", in))
	if code != 1 {
		t.Fatalf("the edit exited %d, want 1: %s", code, out)
	}
	got := decode(t, out)
	if got.Payload.Rule != "criterion-frozen" {
		t.Fatalf("the edit refused with rule %q, want criterion-frozen", got.Payload.Rule)
	}
	if reason := got.Payload.Reason; !strings.Contains(reason, "c1") || !strings.Contains(reason, "appended") {
		t.Fatalf("the refusal does not name the criterion and the append it needs: %q", reason)
	}
	if n := eventsOfKind(t, db, "item.update"); n != 0 {
		t.Fatalf("a refused edit appended %d item.update events", n)
	}
	if stored := showItem(t, db, "item_042"); len(stored.Payload.Criteria) != 1 {
		t.Fatalf("the store holds %d criteria, want the frozen one still there", len(stored.Payload.Criteria))
	}
}

// Growth is what the freeze leaves open: a criterion appended after a presented one is
// accepted, and the presented one keeps the text and state it was judged under.
func TestACriterionMayBeAppendedAfterAFrozenOne(t *testing.T) {
	db := storedWithCriterion(t, "criterion-append", "done")
	in := base("item_042", "the item face, grown")
	in.Criteria[0].State = "done"
	in.Criteria = append(in.Criteria, criterion{CriterionID: "c2", Text: "a criterion appended after the freeze"})
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", in))
	if code != 0 {
		t.Fatalf("the edit exited %d: %s", code, out)
	}
	stored := showItem(t, db, "item_042")
	if c := criterionOf(t, stored, "c1"); c.Text != "the first criterion" || c.State != "done" {
		t.Fatalf("the store holds the frozen criterion as %+v", c)
	}
	if c := criterionOf(t, stored, "c2"); c.Text != "a criterion appended after the freeze" || c.State != "" {
		t.Fatalf("the store holds the appended criterion as %+v", c)
	}
}

// An edit that also carries a state transition is refused with the rule it breaks, and
// refuses by name rather than overwriting: exit 1 with a criterion-state rejection, no
// update event, and the criterion as the store had it. The state rule answers before the
// text rule, so a caller that asked for both is told the one it must resolve first.
func TestAnEditCarryingAStateTransitionIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "criterion-transition", "done")
	in := base("item_042", "the item face, edited")
	in.Criteria[0].Text = "the first criterion, eased"
	in.Criteria[0].State = "open"
	code, out := apply(t, db, envelope(t, "item.record", "rec-2", in))
	if code != 1 {
		t.Fatalf("the edit exited %d, want 1: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "item.rejection" {
		t.Fatalf("the edit emitted kind %q, want the rejection record", got.Kind)
	}
	if got.Payload.Rule != "criterion-state" {
		t.Fatalf("the edit refused with rule %q, want criterion-state", got.Payload.Rule)
	}
	if reason := got.Payload.Rule + " " + got.Payload.Reason; !strings.Contains(reason, "item.verdict") {
		t.Fatalf("the refusal does not name the act a state change needs: %q", reason)
	}
	if n := eventsOfKind(t, db, "item.update"); n != 0 {
		t.Fatalf("a refused edit appended %d item.update events", n)
	}
	stored := criterionOf(t, showItem(t, db, "item_042"), "c1")
	if stored.State != "done" || stored.Text != "the first criterion" {
		t.Fatalf("the store holds %+v, want the criterion as it was", stored)
	}
}
