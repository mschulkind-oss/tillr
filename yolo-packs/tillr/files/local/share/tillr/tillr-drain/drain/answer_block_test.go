package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
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

func TestIndependentWaitHoldsThroughDesignAnswer(t *testing.T) {
	for _, waiter := range []string{"owner", "item:item-b"} {
		t.Run(waiter, func(t *testing.T) {
			f, l := passFixture(t, "item-b")
			l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
			ctx := context.Background()
			id := fileFixtureRequestWith(t, f, "Repair the index", "build", item.SingleWorkerWorkflow, requestFence, true)
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			c := item.Conflict{ConflictID: "conflict-a", ItemID: id, RunID: "blocked-run", Text: "requires design", Status: "raised", WaitingOn: "design"}
			fixtureWrite(t, f, func(tx *store.Tx) error { return item.RecordConflict(tx, c, l.cfg.Now()) })
			stalePlan := decisionFor(t, l, id)
			if stalePlan.decision.Action != ActionDispatch || stalePlan.decision.BlockConflict == nil {
				t.Fatalf("fixture did not offer a design run: %+v", stalePlan.decision)
			}
			// Use even the same waiter and reason as a conflict-owned owner hold.
			// Writing it explicitly still makes it an independent wait.
			if waiter == "owner" {
				c.Status, c.WaitingOn = "owner", "owner"
			}
			reason, _, _ := strings.Cut(c.Text, "\n")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if err := item.RecordConflict(tx, c, l.cfg.Now()); err != nil {
					return err
				}
				_, err := item.SetState(ctx, tx, id, StateBlocked, l.cfg.Now(), item.WaitingOn{Waiter: waiter, Reason: strings.TrimSpace(reason)})
				return err
			})
			c.Status, c.WaitingOn = "raised", "design"
			c.Text += "\nDesign conflict raised again."
			fixtureWrite(t, f, func(tx *store.Tx) error { return item.RecordConflict(tx, c, l.cfg.Now()) })
			if applied, _, err := l.applyPlan(ctx, stalePlan, base, l.cfg.Now()); err != nil || applied {
				t.Fatalf("stale design plan: applied=%v err=%v", applied, err)
			}

			assertHeld := func() {
				t.Helper()
				if d := decisionFor(t, l, id).decision; d.Action != ActionHold || d.Reason != ReasonBlocked || d.From != "held" || d.RunRequest != nil {
					t.Fatalf("named wait did not hold dispatch: %+v", d)
				}
				s := openFixture(t, f.storePath)
				defer s.Close()
				if err := s.ReadTx(ctx, func(r *store.Reader) error {
					a, err := Why(r, Pass{}, l.cfg.Policy, nil)
					if err != nil {
						return err
					}
					for _, row := range a.Rows {
						if row.ItemID != id {
							continue
						}
						if row.State != StateBlocked || row.Reason != ReasonBlocked || row.Mover != waiter || !strings.Contains(row.Why, strings.TrimSpace(reason)) {
							t.Fatalf("why did not show the named wait: %+v", row)
						}
						return nil
					}
					return fmt.Errorf("why omitted %s", id)
				}); err != nil {
					t.Fatal(err)
				}
			}
			assertHeld()
			if err := l.recordBlockAnswer(ctx, Candidate{ItemID: id, RunID: "run-design-1", BaseSHA: base, HeadSHA: base, BlockConflict: &c}); err != nil {
				t.Fatal(err)
			}
			if answered, _ := conflictOf(t, f, id); answered.Status != "answered" {
				t.Fatalf("conflict was not answered: %+v", answered)
			}
			assertHeld()
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := item.SetState(ctx, tx, id, StateInQueue, l.cfg.Now())
				return err
			})
			if d := decisionFor(t, l, id).decision; d.Action != ActionDispatch || d.Reason != ReasonConflictAnswered {
				t.Fatalf("answer did not dispatch after release: %+v", d)
			}
		})
	}
}

func TestIndependentWaitIsShownInsteadOfRunLive(t *testing.T) {
	const id = "item-a"
	f, l := passFixture(t, id)
	ctx := context.Background()
	p := decisionFor(t, l, id)
	if applied, _, err := l.applyPlan(ctx, p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now()); err != nil || !applied {
		t.Fatalf("build claim: applied=%v err=%v", applied, err)
	}
	if d := decisionFor(t, l, id).decision; d.Reason != ReasonRunLive {
		t.Fatalf("fixture did not leave live work: %+v", d)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, id, StateBlocked, l.cfg.Now(), item.WaitingOn{Waiter: "owner", Reason: "await approval"})
		return err
	})
	// A failed session resume has an immediate replacement path outside normal
	// planning. The same wait must suppress that replacement, too.
	before := eventCount(t, f.storePath)
	res := &LoopResult{}
	outcome := l.replaceFailedDesigner(ctx, p.decision, run.Record{RunID: p.decision.RunRequest.RunID}, "", "", l.cfg.Now(), res, nil, nil, false)
	if outcome.err != nil || res.Dispatched != 0 || eventCount(t, f.storePath) != before {
		t.Fatalf("replacement advanced a held item: %+v, dispatched=%d", outcome, res.Dispatched)
	}
	assertWhyHeld := func(action string) {
		t.Helper()
		s := openFixture(t, f.storePath)
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			a, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339Nano)}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			if len(a.Rows) != 1 || a.Rows[0].Reason != ReasonBlocked || a.Rows[0].Mover != "owner" || a.Summary.Running != 0 || a.Rows[0].Decision.Action != action {
				t.Fatalf("why did not show the wait while planning %s: %+v", action, a)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertWhyHeld(ActionHold)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		req := p.decision.RunRequest
		at := l.cfg.Now().Format(time.RFC3339)
		if err := applyTerminal(tx, run.Record{RunID: req.RunID, ItemID: id, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET leased_until=1 WHERE lane_id=?`, req.LaneID)
		return err
	})
	assertWhyHeld(ActionSettle)
}

// commitBriefs replaces briefs/item-a.md on the trunk.
func commitBriefs(t *testing.T, f refusalLoopFixture, content string) string {
	t.Helper()
	writeForkFile(t, filepath.Join(f.root, "briefs/item-a.md"), content)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "amend the block")
	return runGit(t, f.root, "rev-parse", "HEAD")
}

func syncBriefs(t *testing.T, f refusalLoopFixture, content string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Partial: true, Documents: []item.SyncDocument{{Path: "briefs/item-a.md", Content: []byte(content)}}, At: time.Now().UTC()})
		return err
	})
}

func faultOf(t *testing.T, f refusalLoopFixture, itemID string) (ItemFault, bool) {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var got ItemFault
	var ok bool
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		got, ok = faults[itemID]
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return got, ok
}

const siblingDoc = "# Doc\n\n### Build item a\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/item-a/\" -->\n- **c1:** the first criterion\n\n### Build item b\n\n<!-- tillr: item id=\"item-b\" paths=\"%s\" -->\n- **c1:** the b criterion\n"

// A landed answer for item-a is not refused because a sibling block in the same document
// is frozen: the frozen definition is item-b's alone (item-heavy-revert-time-to-verdict was
// held on 2026-11-18 for item-gate-stall-watch-without-perf-counter's frozen block).
func TestALandedAnswerIsNotRefusedForASiblingsFrozenBlock(t *testing.T) {
	f, l, _, _ := designAnswerFixture(t)
	syncBriefs(t, f, fmt.Sprintf(siblingDoc, "tools/item-b/"))
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET state = 'done' WHERE item_id = 'item-b'`)
		return err
	})
	head := commitBriefs(t, f, fmt.Sprintf(siblingDoc, "tools/item-b/,tests/item-b/"))
	if err := l.answerBlock(context.Background(), landedAnswer(t, head, "item-a")); err != nil {
		t.Fatal(err)
	}
	if fault, held := faultOf(t, f, "item-a"); held {
		t.Fatalf("item-a was held for its sibling's frozen block: %+v", fault)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "answered" || c.WaitingOn != "build" {
		t.Fatalf("item-a's conflict is not answered: %+v", c)
	}
}

// A held landed design answer is recorded again by the next document sync once the trunk
// and store let it apply (here, its frozen verdicts withdrawn), and its hold is cleared in
// the same transaction: the mover is the correction, never a hand clear-fault.
func TestAHeldLandedAnswerAppliesAtTheNextSyncOnceItsDefinitionIsFree(t *testing.T) {
	f, l, _, _ := designAnswerFixture(t)
	doc := "# Doc\n\n### Build item a\n\n<!-- tillr: item id=\"item-a\" paths=\"%s\" -->\n- **c1:** the first criterion\n"
	syncBriefs(t, f, fmt.Sprintf(doc, "tools/item-a/"))
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET state = 'done' WHERE item_id = 'item-a'`)
		return err
	})
	head := commitBriefs(t, f, fmt.Sprintf(doc, "tools/item-a/,tests/item-a/"))
	answer := landedAnswer(t, head, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := item.RecordConflict(tx, *answer.BlockConflict, l.cfg.Now()); err != nil {
			return err
		}
		if err := recordFixtureLanding(tx, "item-a", answer.RunID, head, 50); err != nil {
			return err
		}
		return appendEffect(tx, examinationDoneKind, answer.RunID, struct{}{}, l.cfg.Now().UTC())
	})
	if err := l.answerBlock(context.Background(), answer); err != nil {
		t.Fatal(err)
	}
	if fault, held := faultOf(t, f, "item-a"); !held || fault.Reason != ReasonDefinitionSyncFailed {
		t.Fatalf("the frozen answer was not held: %+v", fault)
	}
	// Nothing changed: the next sync leaves the hold standing, with no new event.
	before, _ := faultOf(t, f, "item-a")
	if err := l.settleDefinitionHolds(context.Background(), SyncOutcome{}); err != nil {
		t.Fatal(err)
	}
	if after, held := faultOf(t, f, "item-a"); !held || after.Seq != before.Seq {
		t.Fatalf("an unchanged frozen answer moved its hold: %+v -> %+v", before, after)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.WithdrawVerdicts(context.Background(), tx, "item-a", "landing x reverted", "tillr-drain", l.cfg.Now())
		return err
	})
	if err := l.settleDefinitionHolds(context.Background(), SyncOutcome{}); err != nil {
		t.Fatal(err)
	}
	if fault, held := faultOf(t, f, "item-a"); held {
		t.Fatalf("the applied answer's hold stands: %+v", fault)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "answered" || c.WaitingOn != "build" || c.AnswerHead != head {
		t.Fatalf("the held answer was not recorded: %+v", c)
	}
}

// A hold the whole-document sync wrote ends when a whole sync applies that item's block,
// and stands while the sync still reports it.
func TestAWholeSyncHoldEndsWhenTheBlockApplies(t *testing.T) {
	f, l, _, _ := designAnswerFixture(t)
	if err := l.holdItemFault(context.Background(), Candidate{ItemID: "item-a"}, ReasonDefinitionSyncFailed, "briefs/item-a.md:3 item-a: definition-frozen: x"); err != nil {
		t.Fatal(err)
	}
	if err := l.settleDefinitionHolds(context.Background(), SyncOutcome{ItemFaults: map[string]string{"item-a": "still frozen"}}); err != nil {
		t.Fatal(err)
	}
	if _, held := faultOf(t, f, "item-a"); !held {
		t.Fatal("a hold the sync still reports was cleared")
	}
	if err := l.settleDefinitionHolds(context.Background(), SyncOutcome{}); err != nil {
		t.Fatal(err)
	}
	if fault, held := faultOf(t, f, "item-a"); held {
		t.Fatalf("a hold whose block now applies stands: %+v", fault)
	}
}
