package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// designConflictFixture is item-a with a committed block, one terminal build run (a1), a
// raised design conflict and the design seat configured. No lane refuses staging.
func designConflictFixture(t *testing.T) (refusalLoopFixture, *Loop) {
	t.Helper()
	f := newRefusalLoopFixture(t, "item-a")
	commitBlock(t, f.root, "item-a", "- **c1:** the first criterion of item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.SeatLists = map[string][]string{"design": {"fixture"}}
	holdWALKeeper(t, f.storePath)
	rec := run.Record{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	recordItemAConflict(t, f, l, "raised", "design")
	return f, l
}

// endDesignRun claims item-a's design-seat dispatch and ends that run without answering
// its conflict: refused at step, or interrupted by a loop stop when step is "orphaned".
// It answers the design run's id.
func endDesignRun(t *testing.T, f refusalLoopFixture, l *Loop, step string) string {
	t.Helper()
	return endDesignRunReporting(t, f, l, step, "")
}

// endDesignRunReporting is endDesignRun with the run's report: when report is not empty,
// the terminal record carries it as the run's report artifact.
func endDesignRunReporting(t *testing.T, f refusalLoopFixture, l *Loop, step, report string) string {
	t.Helper()
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch || p.decision.BlockConflict == nil {
		t.Fatalf("fixture: the design seat is not dispatched: %+v", p.decision)
	}
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("design claim: %v %v", err, ok)
	}
	d := work.Decision
	state, cause, exit := run.StateExited, run.EndNormalExit, countPtr(0)
	if step == "orphaned" {
		state, cause, exit = run.StateTimedOut, run.EndLeaseReclaimed, nil
	}
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: state, ExitCode: exit, StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: cause, ArtifactRefs: []run.ArtifactRef{}}
	if report != "" {
		path := filepath.Join(t.TempDir(), "report.md")
		if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
			t.Fatal(err)
		}
		rec.ArtifactRefs = []run.ArtifactRef{{Kind: "report", Ref: path}}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if step == "orphaned" {
			return appendEffect(tx, loopOrphanedKind, rec.RunID, struct{}{}, l.cfg.Now())
		}
		return appendEffect(tx, RefusalKind, rec.RunID, refusalPayload{Step: step, ItemID: d.ItemID, LaneID: d.LaneID}, l.cfg.Now())
	})
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "awaiting" || c.DesignRunID != rec.RunID {
		t.Fatalf("fixture: the ended design run did not leave the conflict awaiting it: %+v", c)
	}
	return rec.RunID
}

func settleDesign(t *testing.T, l *Loop) {
	t.Helper()
	if err := l.settleUnansweredDesignRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// On 2026-11-17 five design conflicts sat awaiting a design run the gate had refused, or
// one that never launched, and every view said "waits on work: design" with nobody
// working. A design run that ends without answering, when nothing it read has changed,
// hands the conflict to the owner and names the run.
func TestADesignRunRefusedAtTheGateWithNothingChangedGoesToTheOwner(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, RefusalGate)
	settleDesign(t, l)
	c, _ := conflictOf(t, f, "item-a")
	if c.Status != "owner" || c.WaitingOn != "owner" || c.DesignRunID != design || !strings.Contains(c.Text, "Design run "+design+" ended without answering") || !strings.Contains(c.Text, "refused at gate") {
		t.Fatalf("the unanswered conflict was not handed to the owner naming %s: %+v", design, c)
	}
	if d := decisionFor(t, l, "item-a").decision; d.Action != ActionHold || d.Reason != ReasonConflictOwner || d.DeferredBy != design {
		t.Fatalf("the item is not held for the owner naming %s: %+v", design, d)
	}
	// A second pass with nothing changed writes nothing.
	before := eventCount(t, f.storePath)
	settleDesign(t, l)
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("a second pass wrote %d event(s)", got-before)
	}
}

// When something the design run read has changed since its dispatch — here the item's
// block on trunk — the conflict is raised once more and the design seat is dispatched
// again. That is the one automatic attempt per input change: when the second design run
// also ends unanswered, the owner decides, whatever changed since.
func TestADesignRunThatEndedUnansweredIsRaisedOnceMoreAfterAnInputChange(t *testing.T) {
	f, l := designConflictFixture(t)
	first := endDesignRun(t, f, l, RefusalGate)
	commitBlock(t, f.root, "item-a", "- **c1:** the criterion, amended on trunk")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "raised" || c.WaitingOn != "design" || c.DesignRunID != "" || !strings.Contains(c.Text, "briefs/item-a.md") {
		t.Fatalf("the changed input did not raise the conflict again: %+v", c)
	}
	second := endDesignRun(t, f, l, RefusalNoOp)
	if second == first {
		t.Fatalf("the second design run reused %s", first)
	}
	commitBlock(t, f.root, "item-a", "- **c1:** the criterion, amended again")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" || c.DesignRunID != second || !strings.Contains(c.Text, "already spent") {
		t.Fatalf("a second unanswered design run did not reach the owner: %+v", c)
	}
}

// An owner hold this rule wrote is re-read when the trunk moves: an input change that
// lands later still earns the one automatic design run that was not spent.
func TestAnUnansweredDesignConflictWithTheOwnerIsRaisedWhenAnInputChangesLater(t *testing.T) {
	f, l := designConflictFixture(t)
	endDesignRun(t, f, l, RefusalGate)
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" {
		t.Fatalf("fixture: not with the owner: %+v", c)
	}
	commitBlock(t, f.root, "item-a", "- **c1:** the criterion, fixed after the refusal")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "raised" || c.WaitingOn != "design" {
		t.Fatalf("the later input change did not raise the conflict: %+v", c)
	}
	if d := decisionFor(t, l, "item-a").decision; d.Action != ActionDispatch || d.BlockConflict == nil {
		t.Fatalf("the design seat is not dispatched again: %+v", d)
	}
}

// A design run a loop stop interrupted said nothing about the conflict, and an interrupted
// run is no attempt: the conflict is raised again, with no input change and no allowance
// spent.
func TestAnInterruptedDesignRunRaisesItsConflictAgain(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, "orphaned")
	settleDesign(t, l)
	c, _ := conflictOf(t, f, "item-a")
	if c.Status != "raised" || c.WaitingOn != "design" || c.DesignRunID != "" || !strings.Contains(c.Text, design) {
		t.Fatalf("the interrupted design run left the conflict %+v", c)
	}
	// The interruption spent no allowance: a gate refusal after a later input change
	// still earns the one automatic re-raise.
	endDesignRun(t, f, l, RefusalGate)
	commitBlock(t, f.root, "item-a", "- **c1:** amended after the interruption")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "raised" {
		t.Fatalf("the interruption spent the automatic re-raise: %+v", c)
	}
}
