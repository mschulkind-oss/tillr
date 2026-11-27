package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
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
// its conflict: refused at step, interrupted by a loop stop when step is "orphaned", or
// reclaimed after its loop died when step is "reclaimed".
// It answers the design run's id.
func endDesignRun(t *testing.T, f refusalLoopFixture, l *Loop, step string) string {
	t.Helper()
	return endDesignRunReporting(t, f, l, step, "")
}

// endDesignRunReporting is endDesignRun with the run's report: when report is not empty,
// the terminal record carries it as the run's report artifact.
func endDesignRunReporting(t *testing.T, f refusalLoopFixture, l *Loop, step, report string) string {
	t.Helper()
	return endDesignRunWith(t, f, l, step, report, "")
}

// endDesignRunWith is endDesignRunReporting with the evidence key its refusal records.
func endDesignRunWith(t *testing.T, f refusalLoopFixture, l *Loop, step, report, evidence string) string {
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
	if step == "orphaned" || step == "reclaimed" {
		state, cause, exit = run.StateTimedOut, run.EndLeaseReclaimed, nil
	} else if step == "infrastructure" {
		exit = countPtr(4)
	} else if step == "killed" {
		cause, exit = run.EndSignal, countPtr(143)
	} else if step == "exit-143" {
		exit = countPtr(143)
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
		if step == "infrastructure" || step == "killed" || step == "exit-143" {
			return nil
		}
		if step == "reclaimed" {
			// A lease reclaimed after its loop died (a jail relaunch): no refusal and no
			// orphan record, only the terminal record the reclaim wrote.
			return nil
		}
		return appendEffect(tx, RefusalKind, rec.RunID, refusalPayload{Step: step, ItemID: d.ItemID, LaneID: d.LaneID, Evidence: evidence}, l.cfg.Now())
	})
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "awaiting" || c.DesignRunID != rec.RunID {
		t.Fatalf("fixture: the ended design run did not leave the conflict awaiting it: %+v", c)
	}
	return rec.RunID
}

func lastDesignMark(t *testing.T, f refusalLoopFixture, conflict string) designUnanswered {
	t.Helper()
	var marks map[string][]designUnanswered
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		marks, err = readDesignUnanswered(r)
		return err
	})
	m := marks[conflict]
	if len(m) == 0 {
		t.Fatalf("no drain.design-unanswered record for %s", conflict)
	}
	return m[len(m)-1]
}

// objectToOwner moves item-a's conflict, awaiting the ended design run, to the owner as
// that run's objection naming the owner would (reportedBlock).
func objectToOwner(t *testing.T, f refusalLoopFixture, l *Loop, design string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		cs, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		c := cs["item-a"]
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text += "\n\nDesign run " + design + ":\nNeeds a ruling.\n" + DesignNextActorField + ": owner\nRESULT: blocked"
		return item.RecordConflict(tx, c, l.cfg.Now())
	})
}

func settleDesign(t *testing.T, l *Loop) {
	t.Helper()
	if err := l.settleUnansweredDesignRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// raisedForDesign fails unless item-a's conflict is raised again for the design seat with
// text naming the design run and want, and the next plan dispatches the design seat.
func raisedForDesign(t *testing.T, f refusalLoopFixture, l *Loop, design, want string) {
	t.Helper()
	c, _ := conflictOf(t, f, "item-a")
	if c.Status != "raised" || c.WaitingOn != "design" || !strings.Contains(c.Text, design) || !strings.Contains(c.Text, want) {
		t.Fatalf("the conflict was not raised for the design seat naming %s and %q: %+v", design, want, c)
	}
	if d := decisionFor(t, l, "item-a").decision; d.Action != ActionDispatch || d.BlockConflict == nil {
		t.Fatalf("the design seat is not dispatched: %+v", d)
	}
}

// The owner ruled on 2026-11-17 that a gate refusal of a design run's commit is no
// attempt and has no cap: the refusal goes back to the design seat to fix, as many times
// as it takes, and never to the owner by count.
func TestAGateRefusedDesignRunGoesBackToTheDesignSeatEveryTime(t *testing.T) {
	f, l := designConflictFixture(t)
	for i := 0; i < 3; i++ {
		design := endDesignRun(t, f, l, RefusalGate)
		settleDesign(t, l)
		raisedForDesign(t, f, l, design, "was refused at the gate")
	}
	// A pass with nothing new to settle writes nothing.
	before := eventCount(t, f.storePath)
	settleDesign(t, l)
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("a second pass wrote %d event(s)", got-before)
	}
}

// A run a loop stop interrupted said nothing about the conflict: it is run again.
func TestFailedDesignRunWaitsAndStartsOnlyOnItsOwnBackend(t *testing.T) {
	f, l := designConflictFixture(t)
	l.cfg.Policy.Backends["alternate"] = l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.SeatLists["design"] = []string{"fixture", "alternate"}
	failed := endDesignRunReporting(t, f, l, "infrastructure", "design harness unavailable\n")
	settleDesign(t, l)
	wait := decisionFor(t, l, "item-a").decision
	if wait.Action != ActionHold || wait.Reason != ReasonBackendClosed || wait.DeferredBy != "fixture" || !strings.Contains(wait.BackendHoldCause, "design harness unavailable") {
		t.Fatalf("failed design run did not wait on fixture: %+v", wait)
	}
	if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	next := decisionFor(t, l, "item-a").decision
	if next.Action != ActionDispatch || next.RunRequest == nil || next.RunRequest.Backend != "fixture" || next.SelectSeat {
		t.Fatalf("reopened design run not pinned: %+v", next)
	}
	runID := endDesignRunReporting(t, f, l, "infrastructure", "different design fault\n")
	fixtureRead(t, f, func(r *store.Reader) error {
		rec, err := terminalRecord(r, runID)
		if err == nil && (rec == nil || rec.Backend != "fixture" || runID == failed) {
			t.Fatalf("design run record = %+v, wanted a new run on fixture", rec)
		}
		return err
	})
}

func TestAnInterruptedDesignRunRaisesItsConflictAgain(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, "orphaned")
	settleDesign(t, l)
	raisedForDesign(t, f, l, design, "interrupted")
}

func TestWhyKilledDesignRunNamesTheInterruptionAndMover(t *testing.T) {
	f, l := designConflictFixture(t)
	origin := endDesignRun(t, f, l, "killed")
	settleDesign(t, l)
	l.cfg.Lanes = []string{"express-1"} // not eligible for this ordinary item
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes, Now: l.cfg.Now().Format(time.RFC3339)}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			if row.Decision.Action != ActionHold || row.Position != "held" || row.Reason == ReasonNoLane || answer.Summary.CouldRun != 0 || answer.Summary.Held[row.Reason] != 1 {
				t.Fatalf("killed design run counted as waiting for a lane: %+v (summary %+v)", row, answer.Summary)
			}
			for _, want := range []string{origin, "exit 143", "without answering"} {
				if !strings.Contains(row.Why, want) || !strings.Contains(row.TableReason(), want) {
					t.Errorf("killed design run explanation lacks %q: why=%q, table=%q", want, row.Why, row.TableReason())
				}
			}
			if !strings.Contains(row.Mover, "offer an eligible lane") {
				t.Errorf("missing recovery action: %+v", row)
			}
			return nil
		}
		t.Fatal("missing item-a from why")
		return nil
	})
}

func TestKilledDesignRunRestartsWhenItsLaneIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		step string
		busy bool
	}{
		{name: "no longer offered", step: "killed"},
		{name: "occupied after exit 143 without a signal cause", step: "exit-143", busy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := designConflictFixture(t)
			origin := endDesignRun(t, f, l, tc.step)
			settleDesign(t, l)
			l.cfg.Lanes = []string{"lane-2", "lane-3", "express-1"}
			if tc.busy {
				l.cfg.Lanes = append([]string{"lane-1"}, l.cfg.Lanes...)
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "occupant", ItemID: "other", Backend: "fixture"}, "lane-1", l.cfg.Now())
					return err
				})
			}
			p := decisionFor(t, l, "item-a")
			if p.decision.Action != ActionDispatch || p.decision.LaneID != "lane-2" {
				t.Fatalf("killed design run was not restarted on a free eligible lane: %+v", p.decision)
			}
			ok, w, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("claim on replacement lane: %v %v", ok, err)
			}
			got := w.Decision
			if got.RunRequest.SessionTurn || got.DesignContinuationFrom != origin || !strings.Contains(got.BlockConflict.Text, "starting a fresh design session") {
				t.Fatalf("replacement lane did not start a fresh design session: %+v", got)
			}
		})
	}
}

func TestKilledDesignRunUsesRecordedSessionOrExplainsFreshSession(t *testing.T) {
	for _, available := range []bool{true, false} {
		name := "missing transcript"
		if available {
			name = "retained transcript"
		}
		t.Run(name, func(t *testing.T) {
			f, l := designConflictFixture(t)
			origin := endDesignRun(t, f, l, "reclaimed")
			session, err := NewSessionID()
			if err != nil {
				t.Fatal(err)
			}
			dir := RunDir(f.root, origin)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(dir, SessionIDFile), session+"\n")
			transcript := filepath.Join(dir, "transcript.jsonl")
			if available {
				writeForkFile(t, transcript, "{}\n")
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				refs, err := json.Marshal([]run.ArtifactRef{{Kind: "transcript", Ref: transcript}})
				if err != nil {
					return err
				}
				_, err = tx.Exec("UPDATE runs SET artifact_refs=? WHERE run_id=?", string(refs), origin)
				return err
			})
			settleDesign(t, l)
			p := decisionFor(t, l, "item-a")
			ok, w, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			got := w.Decision
			if got.RunRequest.SessionTurn != available {
				t.Fatalf("session turn=%v want %v", got.RunRequest.SessionTurn, available)
			}
			want := "starting a fresh design session"
			if available {
				want = "Continuing the recorded designer session"
				if got.DesignerSessionID != session {
					t.Fatal("wrong session")
				}
			}
			if !strings.Contains(got.BlockConflict.Text, want) {
				t.Fatalf("missing %q: %s", want, got.BlockConflict.Text)
			}
		})
	}
}

// A design run that commits nothing and names no next actor has neither answered nor
// objected: the conflict goes back to the design seat, which is told the contract.
func TestADesignRunThatCommitsNothingAndNamesNoNextActorGoesBackToTheDesignSeat(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, RefusalNoOp)
	settleDesign(t, l)
	raisedForDesign(t, f, l, design, DesignNextActorField)
}

// The owner ruled on 2026-11-17 that a change to the item's block, its design document or
// the trunk is no reason to spend a design run. A conflict the owner holds on an explicit
// objection stays with the owner however the block changes, and nothing is written.
func TestAnInputChangeNeverRaisesADesignConflictAgain(t *testing.T) {
	f, l := designConflictFixture(t)
	objectToOwner(t, f, l, endDesignRun(t, f, l, RefusalNoOp))
	commitBlock(t, f.root, "item-a", "- **c1:** the criterion, amended on trunk")
	before := eventCount(t, f.storePath)
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" {
		t.Fatalf("an input change moved the owner's conflict: %+v", c)
	}
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("an input change wrote %d event(s)", got-before)
	}
}

// An owner hold the removed count wrote (a drain.design-unanswered record naming the
// design run the hold names) is no objection naming the owner. It is raised again for
// the design seat once, and nothing re-raises it after that.
func TestAnOwnerHoldTheRemovedCountWroteIsRaisedAgain(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, RefusalGate)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		cs, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		c := cs["item-a"]
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text += "\n\nDesign run " + design + " ended without answering this conflict (refused at gate). The automatic second design run was already spent, so the owner decides."
		if err := item.RecordConflict(tx, c, l.cfg.Now()); err != nil {
			return err
		}
		return appendEffect(tx, designUnansweredKind, design, designUnanswered{ConflictID: c.ConflictID, ItemID: c.ItemID, RunID: design, Outcome: "refused at gate", Changes: []string{}}, l.cfg.Now())
	})
	settleDesign(t, l)
	raisedForDesign(t, f, l, design, "never counted")
	before := eventCount(t, f.storePath)
	settleDesign(t, l)
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("the migration wrote again: %d event(s)", got-before)
	}
}
