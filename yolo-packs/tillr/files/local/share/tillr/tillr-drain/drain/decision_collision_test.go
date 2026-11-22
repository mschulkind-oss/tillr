package drain

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The design dispatch for a raised conflict names the attempt its run id names. Measured
// 2026-11-18: an item whose earlier run was judged (so its prior-run count skips that run)
// planned run a13 under attempt 12, whose decision id was the a12 dispatch's, and the store's
// refusal of the second row faulted the loop at every start.
func TestAConflictsDesignDispatchNamesTheAttemptOfItsRun(t *testing.T) {
	lanes := []string{"lane-1"}
	priors := make([]priorRun, 11)
	for i := range priors {
		priors[i] = priorRun{RunID: attemptRunID("item-a", i+1)}
	}
	p := planner{
		tick:     Pass{AvailableLanes: &lanes},
		policy:   &Policy{Seats: map[string]string{"build": "fixture", "design": "designer"}, Backends: map[string]Backend{"fixture": {Adapter: "fixture"}, "designer": {Adapter: "fixture"}}},
		priors:   map[string][]priorRun{"item-a": priors},
		attempts: map[string]int{"item-a": 13},
		held:     map[string]bool{}, assigned: map[string]bool{},
	}
	d := p.conflictDecision(itemRow{ItemID: "item-a"}, item.Conflict{ConflictID: "conflict-run-item-a-a12", ItemID: "item-a", RunID: "run-item-a-a12", BodyRef: "docs/x.md#a", Text: "# run report", WaitingOn: "design", Status: "raised"})
	if d.Action != ActionDispatch || d.RunRequest == nil {
		t.Fatalf("no design dispatch: %+v", d)
	}
	if d.RunRequest.RunID != "run-item-a-a13" || d.Attempt != 13 || !strings.Contains(d.DecisionID, "-a13-") {
		t.Fatalf("run %s, attempt %d, decision %s: the decision must name its run's attempt", d.RunRequest.RunID, d.Attempt, d.DecisionID)
	}
}

// A landing repair carries its parent's run ID plus a suffix, but settles under its
// own attempt. A design redispatch must not use that attempt for a second settlement.
func TestConflictDesignSettlesAfterAnsweredWorkerAndLandingRepair(t *testing.T) {
	ctx := context.Background()
	f, l := designSeatFixture(t)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	at := l.cfg.Now()
	const id = "item-a"
	finish := func(tx *store.Tx, runID, lane string) error {
		return applyTerminal(tx, run.Record{RunID: runID, ItemID: id, LaneID: lane, Backend: "fixture",
			State: run.StateExited, ExitCode: code(0), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit})
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i := 1; i <= 10; i++ {
			if err := finish(tx, attemptRunID(id, i), "lane-1"); err != nil {
				return err
			}
		}
		return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-first", ItemID: id, RunID: attemptRunID(id, 10),
			BodyRef: "docs/work.md#work", Text: "The conflict is answered.", Status: "answered", WaitingOn: "build"}, at)
	})
	claim := func(p plannedApplication) *pendingWork {
		t.Helper()
		ok, work, err := l.applyPlan(ctx, p, base, at)
		if err != nil || !ok || work == nil {
			t.Fatalf("apply %s: ok=%v work=%v err=%v; next reason=%s", p.decision.DecisionID, ok, work, err, decisionFor(t, l, id).decision.Reason)
		}
		return work
	}
	settle := func(req *run.Request) Decision {
		t.Helper()
		fixtureWrite(t, f, func(tx *store.Tx) error { return finish(tx, req.RunID, req.LaneID) })
		p := decisionFor(t, l, id)
		if d := p.decision; d.Action != ActionSettle || d.To != "candidate" || d.RunRequest == nil || d.RunRequest.RunID != req.RunID {
			t.Fatalf("not settling %s to candidate: %+v", req.RunID, d)
		}
		claim(p)
		return p.decision
	}

	p := decisionFor(t, l, id)
	if d := p.decision; d.Action != ActionDispatch || d.Reason != ReasonConflictAnswered || d.Attempt != 11 {
		t.Fatalf("not the answered worker's dispatch: %+v", d)
	}
	worker := claim(p).Decision
	settle(worker.RunRequest)
	// Start where the squash-conflict handler hands the lane to its inline repair.
	// The worker has been judged; its row counts for repair allocation, not for priors.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, judgeRequestKind, worker.RunRequest.RunID, struct{}{}, at)
	})
	ordinal, err := l.nextItemAttempt(ctx, id)
	if err != nil || ordinal != 12 {
		t.Fatalf("repair attempt = %d, err=%v", ordinal, err)
	}
	req := *worker.RunRequest
	req.RunID = conflictFixRunID(req.RunID)
	repair := Decision{ItemID: id, LaneID: worker.LaneID, From: "candidate", To: "running", Action: ActionDispatch,
		Reason: ReasonConflictFix, Seat: worker.Seat, Attempt: ordinal, RunRequest: &req}
	identifyDecision(&repair)
	if err := l.handoffRepair(ctx, Candidate{ItemID: id, RunID: worker.RunRequest.RunID}, repair, base); err != nil {
		t.Fatal(err)
	}
	repaired := settle(&req)
	if err := l.markExamined(ctx, repair); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-repair", ItemID: id, RunID: req.RunID,
			BodyRef: "docs/work.md#work", Text: "The repair conflicted again.", Status: "raised", WaitingOn: "design"}, at)
	})

	p = decisionFor(t, l, id)
	if d := p.decision; d.Action != ActionDispatch || d.Reason != ReasonConflictDesign {
		t.Fatalf("not the design redispatch: %+v", d)
	}
	design := claim(p).Decision
	settled := settle(design.RunRequest)
	if design.Attempt != 13 || settled.DecisionID == repaired.DecisionID {
		t.Fatalf("design reused the repair attempt: repair=%s design=%s", repaired.DecisionID, settled.DecisionID)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		for _, d := range []Decision{repaired, settled} {
			var runID string
			if err := r.QueryRow(`SELECT run_id FROM dispatches WHERE decision_id=? AND action='settle'`, d.DecisionID).Scan(&runID); err != nil {
				return err
			}
			if runID != d.RunRequest.RunID {
				t.Errorf("%s records %s, want %s", d.DecisionID, runID, d.RunRequest.RunID)
			}
		}
		faults, err := ReadItemFaults(r)
		if err == nil && faults[id].Seq != 0 {
			t.Errorf("settlement left an item fault: %+v", faults[id])
		}
		return err
	})
}

// A planned decision whose id the store already holds with other columns holds that one
// item with a named fault, and the loop goes on: it is never a loop fault.
func TestADecisionCollisionHoldsTheItemAndNotTheLoop(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	holdWALKeeper(t, f.storePath)
	base := commitBlock(t, f.root, "item-a", "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch {
		t.Fatalf("no dispatch planned: %+v", p.decision)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at) VALUES (?, ?, 'lane-other', 'run-other', 'dispatch', 'earlier', 0)`, p.decision.DecisionID, p.decision.ItemID)
		return err
	})
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || ok || work != nil {
		t.Fatalf("a decision collision reached the loop: ok=%v work=%v err=%v", ok, work, err)
	}
	next := decisionFor(t, l, "item-a")
	if next.decision.Action != ActionHold || next.decision.Reason != ReasonDecisionCollision {
		t.Fatalf("the item is not held for the collision: %+v", next.decision)
	}
}

func TestFirstNodeSettlesAfterQuotaReopens(t *testing.T) {
	for _, collide := range []bool{false, true} {
		t.Run(fmt.Sprintf("inject_collision=%t", collide), func(t *testing.T) {
			ctx := context.Background()
			f, l, _ := workflowLoopFixture(t, "")
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			at := l.cfg.Now().UTC().Truncate(time.Second)
			reset := at.Add(time.Hour)
			l.cfg.Now = func() time.Time { return at }
			finish := func(wantRun string, exit int, log string) plannedApplication {
				t.Helper()
				p := decisionFor(t, l, "item-a")
				if d := p.decision; d.Action != ActionDispatch || d.RunRequest == nil || d.RunRequest.RunID != wantRun || d.Attempt != 1 || d.NodeIndex != 0 || d.LaneID != "lane-1" {
					t.Fatalf("not the expected first node (%s): %+v", wantRun, d)
				}
				ok, work, err := l.applyPlan(ctx, p, base, at)
				if err != nil || !ok || work == nil {
					t.Fatalf("claim %s: ok=%v err=%v", wantRun, ok, err)
				}
				staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, work.BaseSHA, &LoopResult{})
				if err != nil || len(staged) != 1 {
					t.Fatalf("stage %s: %+v / %v", wantRun, staged, err)
				}
				d := staged[0]
				path := filepath.Join(f.root, wantRun+".report")
				writeForkFile(t, path, log)
				rec := run.Record{RunID: wantRun, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend,
					State: run.StateExited, ExitCode: code(exit), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
					ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
				fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
				p = decisionFor(t, l, "item-a")
				if d := p.decision; d.Action != ActionSettle || d.RunRequest == nil || d.RunRequest.RunID != wantRun {
					t.Fatalf("not settling %s: %+v", wantRun, d)
				}
				return p
			}
			settle := func(p plannedApplication) {
				t.Helper()
				ok, work, err := l.applyPlan(ctx, p, base, at)
				if err != nil || !ok || work == nil {
					t.Fatalf("claim settlement %s: ok=%v err=%v; next: %+v", p.decision.DecisionID, ok, err, decisionFor(t, l, "item-a").decision)
				}
				out := l.examineClaimed(ctx, p.decision, work, at, &PassTiming{}, nil).outcome
				if out.err != nil || out.stop != "" {
					t.Fatalf("settle %s: %+v", p.decision.DecisionID, out)
				}
			}
			first := finish("run-item-a-a1-n1", 2, fmt.Sprintf("# run report\nrate limit kind: five-hour\nstatus: rejected\nfraction: 1\nreset: %d\n---\nquota exhausted\n", reset.Unix()))
			settle(first)
			if d := decisionFor(t, l, "item-a").decision; d.Reason != ReasonBackendWindow || d.LaneID != "" || !strings.Contains(d.BackendHoldCause, first.decision.RunRequest.RunID) {
				t.Fatalf("quota refusal did not release lane and name its run: %+v", d)
			}
			at = reset.Add(time.Second)
			second := finish("run-item-a-a1-n1-fresh", 1, "The retried step ran and reported failure.\nRESULT: failed\n")
			if !collide {
				settle(second)
				fixtureRead(t, f, func(r *store.Reader) error {
					var outcome string
					if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, second.decision.RunRequest.RunID).Scan(&outcome); err != nil {
						return err
					}
					if outcome != "failed" {
						t.Errorf("node outcome = %q, want failed", outcome)
					}
					return nil
				})
				if d := decisionFor(t, l, "item-b").decision; d.Action != ActionDispatch || d.LaneID != "lane-1" {
					t.Fatalf("settled node did not free its lane: %+v", d)
				}
				return
			}
			// Supply a genuine conflicting row, independently of the old quota bug.
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO dispatches (decision_id, item_id, action, reason, dispatched_at) VALUES (?, 'item-a', 'settle', 'different', 0)`, second.decision.DecisionID)
				return err
			})
			var report bytes.Buffer
			l.cfg.Report = &report
			if ok, work, err := l.applyPlan(ctx, second, base, at); ok || work != nil || err != nil {
				t.Fatalf("collision escaped its item: ok=%v work=%+v err=%v", ok, work, err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				faults, err := ReadItemFaults(r)
				if err != nil {
					return err
				}
				fault := faults["item-a"]
				answer, err := Why(r, Pass{Now: at.Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes, StorePath: f.storePath}, l.cfg.Policy, nil)
				if err != nil {
					return err
				}
				command := fmt.Sprintf("tillr drain clear-fault --store '%s' --item 'item-a' --fault %d", f.storePath, fault.Seq)
				if !strings.Contains(report.String(), command) {
					t.Errorf("loop report omits %q: %s", command, &report)
				}
				for _, row := range answer.Rows {
					if row.ItemID != "item-a" {
						continue
					}
					if fault.Seq == 0 || row.Reason != ReasonDecisionCollision || !strings.Contains(row.Why, command) || !strings.Contains(row.Mover, command) ||
						(row.LaneStatus != "released" && !strings.Contains(row.LaneReason, second.decision.RunRequest.RunID)) {
						t.Errorf("expected collision hold with lane explanation and %q: %+v", command, row)
					}
					return nil
				}
				t.Fatal("why omitted item-a")
				return nil
			})
		})
	}
}

// A decision collision while settling one run holds that run's item with a named fault and
// the loop goes on; other settle errors keep their recorded rules.
func TestASettleErrorHoldsTheItemAndNotTheLoop(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	holdWALKeeper(t, f.storePath)
	base := commitBlock(t, f.root, "item-a", "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	result := &LoopResult{}
	stop, err := l.takeCompletion(runCompletion{id: "run-item-a-a1", itemID: "item-a", step: "settle", outcome: laneOutcome{err: &decisionCollisionError{ExitError: Internalf("the decision x is already recorded with different columns").(*ExitError), DecisionID: "x", ItemID: "item-a"}}}, result)
	if err != nil || stop != "" || result.StopItemID != "" {
		t.Fatalf("a settle error stopped the loop: stop=%q err=%v result=%+v", stop, err, result)
	}
	next := decisionFor(t, l, "item-a")
	if next.decision.Action != ActionHold || next.decision.Reason != ReasonDecisionCollision {
		t.Fatalf("the item is not held for its settle collision: %+v", next.decision)
	}
	// A settle error of another class keeps its recorded rule and ends the loop.
	if _, err := l.takeCompletion(runCompletion{id: "run-item-a-a2", itemID: "item-a", step: "run", outcome: laneOutcome{err: Internalf("reading the gate.plan payload failed")}}, &LoopResult{}); err == nil {
		t.Fatal("an unrecordable judgment was held on one item instead of failing the step")
	}
}
