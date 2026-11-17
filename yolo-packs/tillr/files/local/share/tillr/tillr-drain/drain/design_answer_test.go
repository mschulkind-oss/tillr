package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const answerDocBase = "# Doc\n\n### Old title\n\n<!-- tillr: item id=\"item-x\" paths=\"tools/x/\" -->\n- **c1:** the first criterion\n\n### Next\n\nother text\n"

// commitDoc commits docs/x.md with body and answers the commit.
func commitDoc(t *testing.T, root, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "docs/x.md"), body)
	runGit(t, root, "add", "docs/x.md")
	runGit(t, root, "commit", "-qm", "doc")
	return runGit(t, root, "rev-parse", "HEAD")
}

// On 2026-11-17 run-item-drain-spend-ceiling-a6 added a ruling inside its item's block, on
// a base older than the trunk commit that renamed the block's heading. Its body_ref names
// the new heading, which neither the base nor the candidate holds, so both read "body
// anchor missing", the answer read as "did not amend the blocked item block", and the
// committed answer went to the owner without a gate. The block is found by its id when
// the anchor is gone.
func TestADesignAnswerOnABaseOlderThanItsHeadingIsReadAsAmended(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	base := commitDoc(t, f.root, answerDocBase)
	head := commitDoc(t, f.root, "# Doc\n\n### Old title\n\n<!-- tillr: item id=\"item-x\" paths=\"tools/x/\" -->\n- **c1:** the first criterion\n\n**Ruling:** c1 stands.\n\n### Next\n\nother text\n")
	exists, changed, err := l.designBlockAnswer(context.Background(), Candidate{ItemID: "item-x", RunID: "run-item-x-a2", BaseSHA: base, HeadSHA: head, Worktree: f.root}, "docs/x.md#new-title")
	if err != nil || !exists || !changed {
		t.Fatalf("the amended block was read as exists=%v changed=%v (%v)", exists, changed, err)
	}
	same, err := l.definitionChanged(context.Background(), Candidate{ItemID: "item-x", BaseSHA: base, HeadSHA: base, Worktree: f.root}, "docs/x.md#new-title")
	if err != nil || same {
		t.Fatalf("an unchanged block was read as changed: %v (%v)", same, err)
	}
}

// On 2026-11-17 run-rewrite-item-run-refs-dropped-once-landed-a4 answered its rewrite
// conflict by retitling the section, which is what a prose rewrite does, and committed it.
// The settlement looked for a block whose id is the rewrite item's own, which no document
// holds, read the answer as "removed or renamed its item block", and sent it to the owner
// without a gate. A rewrite item's block is its original item's.
func TestARewriteDesignAnswerThatRetitlesItsSectionIsReadAsAmended(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	base := commitDoc(t, f.root, answerDocBase)
	head := commitDoc(t, f.root, "# Doc\n\n### A clearer title\n\n<!-- tillr: item id=\"item-x\" paths=\"tools/x/\" -->\n- **c1:** the first criterion, in plainer words\n\n### Next\n\nother text\n")
	exists, changed, err := l.designBlockAnswer(context.Background(), Candidate{ItemID: "rewrite-item-x", RunID: "run-rewrite-item-x-a4", BaseSHA: base, HeadSHA: head, Worktree: f.root}, "docs/x.md#old-title")
	if err != nil || !exists || !changed {
		t.Fatalf("the retitled rewrite was read as exists=%v changed=%v (%v)", exists, changed, err)
	}
	gone := commitDoc(t, f.root, "# Doc\n\n### Next\n\nother text\n")
	exists, _, err = l.designBlockAnswer(context.Background(), Candidate{ItemID: "rewrite-item-x", RunID: "run-rewrite-item-x-a5", BaseSHA: base, HeadSHA: gone, Worktree: f.root}, "docs/x.md#old-title")
	if err != nil || exists {
		t.Fatalf("a removed block was read as present: %v (%v)", exists, err)
	}
}

// misreadDesignAnswer claims item-a's design run, commits head on its lane as that run's
// candidate, and records the settlement the earlier rule wrote: the conflict with the
// owner, reading the committed answer as no amendment.
func misreadDesignAnswer(t *testing.T, f refusalLoopFixture, l *Loop, amend bool) (string, string) {
	t.Helper()
	p := decisionFor(t, l, "item-a")
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("design claim: %v %v", err, ok)
	}
	d := work.Decision
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	head := base
	if amend {
		lane := l.lanePath("lane-1")
		runGit(t, lane, "checkout", "-q", "--detach", base)
		writeBlock(t, lane, "item-a", "- **c1:** the criterion, amended by the design run")
		runGit(t, lane, "add", ".")
		runGit(t, lane, "commit", "-qm", "design answer")
		head = runGit(t, lane, "rev-parse", "HEAD")
		runGit(t, f.root, "update-ref", "refs/tillr/runs/"+d.RunRequest.RunID, head)
	}
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	c, _ := conflictOf(t, f, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		cand := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, BaseSHA: base, HeadSHA: head, Worktree: l.lanePath("lane-1")}
		if err := appendEffect(tx, candidateReadyKind, rec.RunID, cand, l.cfg.Now()); err != nil {
			return err
		}
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text += "\nDesign run " + rec.RunID + " did not amend the blocked item block briefs/item-a.md"
		return item.RecordConflict(tx, c, l.cfg.Now())
	})
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return rec.RunID, head
}

// The two answers dropped on 2026-11-17 sit with the owner, recorded by the earlier
// rule. A recorded "did not amend" or "removed or renamed" whose committed candidate does
// amend the block is re-read once: the conflict is raised again for the design seat,
// naming the commit that holds the earlier answer, and nothing is spent.
func TestADesignAnswerMisreadBeforeTheFixIsRaisedAgainNamingItsCommit(t *testing.T) {
	f, l := designConflictFixture(t)
	design, head := misreadDesignAnswer(t, f, l, true)
	if err := l.settleMisreadDesignAnswers(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, _ := conflictOf(t, f, "item-a")
	if c.Status != "raised" || c.WaitingOn != "design" || c.DesignRunID != "" || !strings.Contains(c.Text, shortSHA(head)) || !strings.Contains(c.Text, design) {
		t.Fatalf("the misread answer was not raised again naming %s: %+v", shortSHA(head), c)
	}
	if m := lastDesignMark(t, f, c.ConflictID); m.RunID != design || m.Reraised || !m.Misread {
		t.Fatalf("the re-read was not recorded, or spent the allowance: %+v", m)
	}
	if d := decisionFor(t, l, "item-a").decision; d.Action != ActionDispatch || d.BlockConflict == nil {
		t.Fatalf("the design seat is not dispatched again: %+v", d)
	}
}

// A recorded "did not amend" that is true stays with the owner.
func TestADesignRunThatTrulyDidNotAmendStaysWithTheOwner(t *testing.T) {
	f, l := designConflictFixture(t)
	misreadDesignAnswer(t, f, l, false)
	before := eventCount(t, f.storePath)
	if err := l.settleMisreadDesignAnswers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" {
		t.Fatalf("a true no-amendment was raised: %+v", c)
	}
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("a true no-amendment wrote %d event(s)", got-before)
	}
}
