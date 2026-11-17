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

// designAnswerFixture is designQuotaFixture with item-a's block committed and lane-1 on
// that commit, fenced to the block's document. It answers the design decision and the
// base.
func designAnswerFixture(t *testing.T) (refusalLoopFixture, *Loop, Decision, string) {
	t.Helper()
	f, l := designQuotaFixture(t)
	if err := os.MkdirAll(filepath.Join(f.root, "briefs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "briefs/item-a.md"), "# Doc\n\n### Build item a\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/item-a/\" -->\n- **c1:** the first criterion\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "item definition")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, l.laneTarget("lane-1"), "reset", "--hard", base)
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "briefs/item-a.md\n")
	return f, l, mustPlan(t, l)[0].decision, base
}

func landedAnswer(t *testing.T, base, itemID string) Candidate {
	t.Helper()
	c := item.Conflict{ConflictID: "conflict-run-" + itemID + "-a3", ItemID: itemID, RunID: "run-" + itemID + "-a2", Text: "a conflict", Status: "awaiting", WaitingOn: "design", DesignRunID: "run-" + itemID + "-a3", BodyRef: "briefs/item-a.md#build-item-a", BlockedCriteria: []string{}}
	return Candidate{ItemID: itemID, RunID: c.DesignRunID, LaneID: "lane-1", BaseSHA: base, HeadSHA: base, BlockConflict: &c}
}

// A prose-rewrite item is generated, never declared: its block is its original item's
// (item.ProseRewriteOriginal). A landed design answer for one is read on that block, and
// answers its conflict, instead of failing as "does not declare item" (the fault that
// stopped the loop twice on 2026-11-17).
func TestALandedAnswerForARewriteItemIsReadOnItsOriginalsBlock(t *testing.T) {
	f, l, _, base := designAnswerFixture(t)
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, s, "rewrite-item-a")
	s.Close()
	if err := l.answerBlock(context.Background(), landedAnswer(t, base, "rewrite-item-a")); err != nil {
		t.Fatalf("a rewrite item's landed answer failed: %v", err)
	}
	if c, _ := conflictOf(t, f, "rewrite-item-a"); c.Status != "answered" || c.WaitingOn != "build" {
		t.Fatalf("the rewrite item's conflict is not answered: %+v", c)
	}
}

// A landed answer whose document does not declare its item is that one item's condition:
// the item is held with a fault naming its mover, and the loop is not stopped.
func TestALandedAnswerThatDeclaresNoItemHoldsOnlyThatItem(t *testing.T) {
	f, l, _, base := designAnswerFixture(t)
	if err := l.answerBlock(context.Background(), landedAnswer(t, base, "item-gone")); err != nil {
		t.Fatalf("one item's undeclared answer stopped the loop: %v", err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if got := faults["item-gone"]; got.Reason != ReasonDefinitionSyncFailed || !strings.Contains(got.Detail, "does not declare item item-gone") {
			t.Fatalf("the item is not held on its own fault: %+v", faults)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A landed answer for an item that is already terminal (landed, retired) is the item's
// terminal settlement, which the pass records once (settleTerminalItem), and never a
// loop error or a fault that recurs every pass.
func TestALandedAnswerForATerminalItemIsItsTerminalSettlement(t *testing.T) {
	f, l, _, base := designAnswerFixture(t)
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, s, "rewrite-item-a")
	s.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, StateLanded, "rewrite-item-a")
		return err
	})
	err = l.answerBlock(context.Background(), landedAnswer(t, base, "rewrite-item-a"))
	if asItemTerminal(err) == nil {
		t.Fatalf("a terminal item's landed answer is not its terminal settlement: %v", err)
	}
}
