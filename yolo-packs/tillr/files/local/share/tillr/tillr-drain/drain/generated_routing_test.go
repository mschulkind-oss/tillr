package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// generatedFixture is a document-fed store with one declared item and the generated items
// the loop files or sync produces, none of which has a block of its own in the document.
func generatedFixture(t *testing.T, generated ...string) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := passFixture(t, append([]string{"item-declared"}, generated...)...)
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), triageBlock)
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "declare the item")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='build', body_ref='docs/request.md#' || item_id`)
		return err
	})
	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "designer"}
	l.cfg.Policy.Backends["designer"] = Backend{Adapter: "fixture"}
	l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", "printf '%s' '{\"index\":{\"documents\":[]}}'"}}
	return f, l
}

// One predicate names every item the loop or sync generates, so no caller keeps its own
// partial list of prefixes.
func TestOnePredicateNamesEveryGeneratedItem(t *testing.T) {
	for id, want := range map[string]bool{
		TrunkFixerID("c6614acf3c09aa"):  true,
		DefectItemID("go test ./..."):   true,
		"gate-budget-2026-11-18":        true,
		"rewrite-item-declared":         true,
		RevertItemID("c6614acf3c09aa"):  true,
		"item-declared":                 false,
		"item-gate-budget-in-the-title": false,
	} {
		if got := IsGeneratedItem(id); got != want {
			t.Errorf("IsGeneratedItem(%q) = %v, want %v", id, got, want)
		}
	}
}

// A generated item is filed by the loop or produced by sync, not requested by anyone: it
// has no block of its own to declare, so document-fed triage never takes it, and nothing
// sends it to the owner for a document it can never have.
func TestGeneratedItemsAreNeverTriagedForADocument(t *testing.T) {
	generated := []string{"rewrite-item-declared", DefectItemID("go test ./..."), "gate-budget-2026-11-18"}
	_, l := generatedFixture(t, generated...)
	for _, id := range generated {
		d := planFor(t, l, id)
		if d.Reason == ReasonTriageNoBlock || d.Reason == ReasonConflictOwner || d.BlockConflict != nil {
			t.Fatalf("generated item %s was triaged or sent to the owner: %+v", id, d)
		}
	}
}

// A gate-budget item is the loop's old overrun alarm. A budget is a reported target, never
// a hold, so the loop no longer files one, and one an older build filed is closed with the
// reason rather than left waiting on the owner behind a triage conflict.
func TestALegacyGateBudgetItemIsClosedNotLeftWithTheOwner(t *testing.T) {
	ctx := context.Background()
	const alarm = "gate-budget-2026-11-18"
	f, l := generatedFixture(t, alarm)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return item.RecordConflict(tx, item.Conflict{
			ConflictID: "triage-" + alarm + "-v1", ItemID: alarm, TriageVersion: "v1", RunID: "run-" + alarm + "-a1", DesignRunID: "run-" + alarm + "-a1",
			Status: "owner", WaitingOn: "owner", BlockedCriteria: []string{},
			Text: "Request has no document block; triage must declare it before it is built.\nRequest still has no document block after triage; the owner must close it or answer the conflict.",
		}, l.cfg.Now().UTC())
	})
	var report strings.Builder
	l.cfg.Report = &report
	if err := l.releaseGeneratedTriage(ctx); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var state, reason, by string
		if err := tx.QueryRow(`SELECT state, COALESCE(close_reason,''), COALESCE(closed_by,'') FROM items WHERE item_id=?`, alarm).Scan(&state, &reason, &by); err != nil {
			return err
		}
		cs, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		if state != "closed" || by != "tillr-drain" || !strings.Contains(reason, "reported target") {
			t.Fatalf("the alarm is %s (closed by %q: %q), want closed by tillr-drain with the budget ruling", state, by, reason)
		}
		if c := cs[alarm]; c.Status != "closed" || c.WaitingOn == "owner" {
			t.Fatalf("the alarm's triage conflict still waits: %+v", c)
		}
		return nil
	})
	if !strings.Contains(report.String(), alarm) {
		t.Fatalf("the loop did not report closing %s:\n%s", alarm, report.String())
	}
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == alarm && (p.decision.Action == ActionDispatch || p.decision.Reason == ReasonConflictOwner) {
			t.Fatalf("the closed alarm is still planned: %+v", p.decision)
		}
	}
	before := eventCount(t, f.storePath)
	if err := l.releaseGeneratedTriage(ctx); err != nil {
		t.Fatal(err)
	}
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("a second release wrote %d event(s)", got-before)
	}
}

// A rewrite item an older build parked on an owner triage conflict returns to its worker,
// as a fixer does: nothing in that conflict was the owner's to answer.
func TestARewriteParkedOnTriageIsReturnedToItsWorker(t *testing.T) {
	ctx := context.Background()
	const rewrite = "rewrite-item-declared"
	f, l := generatedFixture(t, rewrite)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return item.RecordConflict(tx, item.Conflict{
			ConflictID: "triage-" + rewrite + "-v1", ItemID: rewrite, TriageVersion: "v1", Status: "owner", WaitingOn: "owner",
			BlockedCriteria: []string{}, Text: "Request has no document block; triage must declare it before it is built.",
		}, l.cfg.Now().UTC())
	})
	if err := l.releaseGeneratedTriage(ctx); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var state string
		if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, rewrite).Scan(&state); err != nil {
			return err
		}
		cs, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		if state != StateInQueue || cs[rewrite].Status != "closed" || cs[rewrite].WaitingOn == "owner" {
			t.Fatalf("rewrite still parked: state=%s conflict=%+v", state, cs[rewrite])
		}
		return nil
	})
}
