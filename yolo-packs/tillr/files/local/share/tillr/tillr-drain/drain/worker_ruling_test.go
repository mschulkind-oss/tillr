package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The owner ruled on 2026-11-17 that an edit to an item's block or fence is no fact for its
// work: an in-flight workflow is never settled, released or invalidated because a hash of
// the block or of the fence changed (audit finding F-07).
func TestAnInFlightWorkflowIsNotInvalidatedByABlockOrFenceEdit(t *testing.T) {
	for _, edit := range []string{
		`UPDATE item_criteria SET text = 'revised criterion' WHERE item_id = 'item-a'`,
		`UPDATE items SET title = 'a reworded title' WHERE item_id = 'item-a'`,
		`UPDATE items SET allowed_paths = '["tools/changed/"]' WHERE item_id = 'item-a'`,
	} {
		t.Run(edit, func(t *testing.T) {
			f, l, _ := restartGraphFixture(t)
			result := &LoopResult{}
			for i := 0; i < 5; i++ {
				graphPass(t, l, result)
			}
			if w := pendingForTest(t, f, "item-a"); w == nil || w.Done {
				t.Fatal("fixture has no in-flight workflow")
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(edit)
				return err
			})
			if d := mustPlan(t, l)[0].decision; d.WorkflowRelease || d.Action == ActionSettle && d.To == "held" {
				t.Fatalf("an edit to the block or fence settled the in-flight workflow: %+v", d)
			}
		})
	}
}

// finishRefused completes a planned dispatch whose run ended refused at step: for a gate
// refusal its fast verdict names checkID failing with output; for any other step detail is
// the refusal's words. The examination is recorded through markExamined, which is where an
// identical repeat is routed.
func finishRefused(t *testing.T, f refusalLoopFixture, l *Loop, p plannedApplication, step, detail, checkID, output string) {
	t.Helper()
	ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	d := p.decision
	id := d.RunRequest.RunID
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: id, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit})
	})
	if step == RefusalGate {
		dir := RunDir(l.cfg.Root, id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		logPath := filepath.Join(dir, checkID+".log")
		if err := os.WriteFile(logPath, []byte(output), 0600); err != nil {
			t.Fatal(err)
		}
		verdict := mustJSON(map[string]any{"payload": map[string]any{"result": "fail", "checks": []map[string]any{{"check_id": checkID, "result": "fail", "log_ref": logPath}, {"check_id": "passing", "result": "pass"}}}})
		if err := os.WriteFile(filepath.Join(dir, GateFastVerdictFile), []byte(verdict), 0600); err != nil {
			t.Fatal(err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendEffect(tx, GateVerdictKind, id, map[string]any{"result": GateFailed}, l.cfg.Now())
		})
	}
	if err := l.refuse(context.Background(), Candidate{ItemID: d.ItemID, RunID: id, LaneID: d.LaneID, RefusalDetail: detail}, &LoopResult{}, step); err != nil {
		t.Fatal(err)
	}
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func itemConflict(t *testing.T, f refusalLoopFixture) item.Conflict {
	t.Helper()
	var c item.Conflict
	fixtureWrite(t, f, func(tx *store.Tx) error { cs, err := item.ReadConflicts(tx.Reader()); c = cs["item-a"]; return err })
	return c
}

func designSeatFixture(t *testing.T) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref = 'docs/work.md#work' WHERE item_id = 'item-a'`)
		return err
	})
	return f, l
}

// A gate refusal, a run that committed nothing, a fence escape and a refused landing go
// back to the item's worker with the evidence, the first time and every time: the next pass
// dispatches the item with the ending in its decision, uncounted, and never holds it for
// the owner (audit findings F-02 and F-03).
func TestAFailureReturnsToItsWorkerWithTheEvidence(t *testing.T) {
	for _, step := range []string{RefusalGate, RefusalNoOp, RefusalFence, RefusalLand} {
		t.Run(step, func(t *testing.T) {
			f, l := designSeatFixture(t)
			first := mustPlan(t, l)[0]
			finishRefused(t, f, l, first, step, "refused: tools/outside/x", "documents", "doc.md:12: dead anchor")
			d := mustPlan(t, l)[0].decision
			if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Returned == nil {
				t.Fatalf("the failure did not return to its worker: %+v", d)
			}
			r := *d.Returned
			if r.RunID != first.decision.RunRequest.RunID || r.Cause != step || r.Detail != "refused: tools/outside/x" {
				t.Fatalf("the returned ending lost its evidence: %+v", r)
			}
			if step == RefusalGate && (len(r.Checks) != 1 || r.Checks[0].CheckID != "documents" || r.Checks[0].LogRef == "") {
				t.Fatalf("the failing gate checks were not carried: %+v", r.Checks)
			}
			brief := returnedBrief(r)
			for _, want := range []string{r.RunID, "refused: tools/outside/x", "refs/tillr/runs/" + r.RunID} {
				if !strings.Contains(brief, want) {
					t.Fatalf("the brief does not carry %q:\n%s", want, brief)
				}
			}
			if c := itemConflict(t, f); c.ConflictID != "" {
				t.Fatalf("a first failure raised a conflict: %+v", c)
			}
		})
	}
}

// Five different failures are each returned the way the first was, and the sixth run can
// land: nothing counts them (the graph design's item "Route a gate refusal back to the
// session that wrote the work", criterion c3).
func TestFiveDifferentFailuresAreEachReturned(t *testing.T) {
	f, l := designSeatFixture(t)
	p := mustPlan(t, l)[0]
	for i := 0; i < 5; i++ {
		finishRefused(t, f, l, p, RefusalGate, "", "check", fmt.Sprintf("failure number %c", 'a'+i))
		if c := itemConflict(t, f); c.ConflictID != "" {
			t.Fatalf("failure %d raised a conflict: %+v", i+1, c)
		}
		p = mustPlan(t, l)[0]
		if p.decision.Action != ActionDispatch || p.decision.Reason != ReasonReturned {
			t.Fatalf("failure %d was not returned: %+v", i+1, p.decision)
		}
	}
}

// An identical repeat — the same producer, cause and evidence as an earlier ending — is not
// sent to the worker again: the conflict goes to the design seat with both records.
func TestAnIdenticalRepeatGoesToTheDesignSeat(t *testing.T) {
	for _, step := range []string{RefusalGate, RefusalFence} {
		t.Run(step, func(t *testing.T) {
			f, l := designSeatFixture(t)
			first := mustPlan(t, l)[0]
			// The same failure, differing only by the run's own id and a duration.
			finishRefused(t, f, l, first, step, "escaped "+first.decision.RunRequest.RunID+"/x", "documents", first.decision.RunRequest.RunID+": dead anchor after 12 ms")
			second := mustPlan(t, l)[0]
			finishRefused(t, f, l, second, step, "escaped "+second.decision.RunRequest.RunID+"/x", "documents", second.decision.RunRequest.RunID+": dead anchor after 30 ms")
			c := itemConflict(t, f)
			if c.Status != "raised" || c.WaitingOn != "design" || !strings.HasPrefix(c.Text, IdenticalRepeatPrefix) {
				t.Fatalf("the identical repeat did not go to the design seat: %+v", c)
			}
			for _, want := range []string{first.decision.RunRequest.RunID, second.decision.RunRequest.RunID} {
				if !strings.Contains(c.Text, want) {
					t.Fatalf("the conflict lacks %s: %s", want, c.Text)
				}
			}
			d := mustPlan(t, l)[0].decision
			if d.Seat != "design" || d.BlockConflict == nil {
				t.Fatalf("the repeat was not dispatched to the design seat: %+v", d)
			}
			// Replaying the examination after a restart raises nothing twice.
			if err := l.markExamined(context.Background(), second.decision); err != nil {
				t.Fatal(err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var n int
				err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'item.conflict'`).Scan(&n)
				if n != 1 {
					t.Errorf("%d conflict events, want 1", n)
				}
				return err
			})
		})
	}
}

// The live store holds items the removed rules parked: a no-op whose input-change
// re-admission also ended no-op (`readmission-exhausted`), and refusals recorded before
// evidence keys existed. At the next pass under the new rules each is returned to its
// worker without hand repair, and an unkeyed ending is never read as a repeat.
func TestItemsTheRemovedRulesParkedAreReturnedAtTheNextPass(t *testing.T) {
	f, l := designSeatFixture(t)
	legacy := func(p plannedApplication) {
		ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("apply: %v %v", ok, err)
		}
		id := p.decision.RunRequest.RunID
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := applyTerminal(tx, run.Record{RunID: id, ItemID: "item-a", LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
				return err
			}
			// A refusal as the store recorded it before evidence keys existed.
			return appendEffect(tx, RefusalKind, id, map[string]any{"step": RefusalNoOp, "item_id": "item-a"}, l.cfg.Now())
		})
	}
	first := mustPlan(t, l)[0]
	legacy(first)
	if err := l.markExamined(context.Background(), first.decision); err != nil {
		t.Fatal(err)
	}
	// The removed rule's re-admitted second dispatch, as the store recorded it.
	second := mustPlan(t, l)[0]
	second.decision.Readmitted, second.decision.Reason, second.decision.Returned = true, "inputs-changed: tillr-build", nil
	legacy(second)
	if err := l.markExamined(context.Background(), second.decision); err != nil {
		t.Fatal(err)
	}
	if c := itemConflict(t, f); c.ConflictID != "" {
		t.Fatalf("an unkeyed legacy ending was read as a repeat: %+v", c)
	}
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Returned == nil || d.Returned.Cause != RefusalNoOp {
		t.Fatalf("the parked item was not returned: %+v", d)
	}
}

// An item the removed one-fix-run rule parked as conflict-exhausted — its latest run, a
// conflict-fix run, ended in a second squash conflict — is returned at the next pass with
// the conflict as its evidence, with no migration step: holds are derived on every pass.
func TestAnItemParkedAsConflictExhaustedIsReturnedAtTheNextPass(t *testing.T) {
	f, l := designSeatFixture(t)
	p := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	id := p.decision.RunRequest.RunID
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{RunID: id, ItemID: "item-a", LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
			return err
		}
		// The second conflict as the removed rule recorded it: no fix run, no evidence key.
		return appendEffect(tx, RefusalKind, id, map[string]any{"step": RefusalConflict, "item_id": "item-a", "conflict_paths": []string{"CONFLICT (content): Merge conflict in base.txt"}}, l.cfg.Now())
	})
	if err := l.markExamined(context.Background(), p.decision); err != nil {
		t.Fatal(err)
	}
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Returned == nil || d.Returned.Cause != RefusalConflict {
		t.Fatalf("the parked conflict was not returned: %+v", d)
	}
}
