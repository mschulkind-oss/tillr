package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// refusedLaneFixture is item-a on a loop whose lane-1 holds a change nobody landed, so
// staging refuses lane-1 and the item must be offered lane-2 in the same pass. The design
// seat is configured, and item-a has one terminal build run, a1.
func refusedLaneFixture(t *testing.T) (refusalLoopFixture, *Loop, *laneSteps) {
	t.Helper()
	f := newRefusalLoopFixture(t, "item-a")
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	steps := &laneSteps{}
	l := f.loop(t, steps, &laneRefusingStager{t: t, f: f, lane: "lane-1"})
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.SeatLists = map[string][]string{"design": {"fixture"}}
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	holdWALKeeper(t, f.storePath)
	rec := run.Record{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return f, l, steps
}

func recordItemAConflict(t *testing.T, f refusalLoopFixture, l *Loop, status, waiting string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-run-item-a-a1", ItemID: "item-a", RunID: "run-item-a-a1", BodyRef: "briefs/item-a.md", Text: "# run report\nthe block cannot be met", Status: status, WaitingOn: waiting, BlockedCriteria: []string{"c1"}}, l.cfg.Now())
	})
}

func onePassNoStop(t *testing.T, l *Loop) {
	t.Helper()
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("the pass stopped: %q/%v", stop, err)
	}
	for len(l.active) > 0 {
		c := <-l.completions
		if stop, err := l.takeCompletion(c, &result); err != nil || stop != "" {
			t.Fatalf("a run stopped the loop: %q/%v (%s)", stop, err, result.Detail)
		}
	}
}

// On 2026-11-17 six runs were claimed on lanes that then refused staging, and none of
// them ever started. The claim had already moved the item's conflict — a design-seat run
// marked it awaiting that run, a build against an answer closed it — and cancelling the
// staging left that move in place, so the in-pass re-plan held the item instead of
// offering it the next lane. A design run claimed on a refusing lane runs on the next one.
func TestADesignRunClaimedOnARefusingLaneRunsOnTheNextLane(t *testing.T) {
	f, l, steps := refusedLaneFixture(t)
	recordItemAConflict(t, f, l, "raised", "design")
	onePassNoStop(t, l)
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("the design run ran on %v, want lane-2", got)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.DesignRunID != "run-item-a-a2" {
		t.Fatalf("the conflict does not name the design run that ran: %+v", c)
	}
}

// The build that consumes a design answer is offered the next lane the same way.
func TestABuildAgainstAnAnswerClaimedOnARefusingLaneRunsOnTheNextLane(t *testing.T) {
	f, l, steps := refusedLaneFixture(t)
	recordItemAConflict(t, f, l, "answered", "build")
	onePassNoStop(t, l)
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("the build ran on %v, want lane-2", got)
	}
}

// legacyCancelledClaim claims item-a's next dispatch and then cancels it the way staging
// did before the fix: examined, its dispatch row deleted and its lane idled, with the
// conflict left as the claim moved it. It answers the cancelled run.
func legacyCancelledClaim(t *testing.T, f refusalLoopFixture, l *Loop) string {
	t.Helper()
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch {
		t.Fatalf("fixture: item-a is not dispatched: %+v", p.decision)
	}
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", err, ok)
	}
	d := work.Decision
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, examinationDoneKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM dispatches WHERE decision_id=? AND item_id=? AND run_id=?`, d.DecisionID, d.ItemID, d.RunRequest.RunID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=?`, "idle", d.LaneID, d.RunRequest.RunID)
		return err
	})
	return d.RunRequest.RunID
}

// The records those six cancelled claims left behind recover by themselves: the next pass
// recognizes a claimed run that was examined with no terminal record, undoes its conflict
// move, and dispatches the item again under the same run id, so no attempt is spent.
func TestACancelledClaimsRecordsAreRecognizedAndTheItemIsDispatchedAgain(t *testing.T) {
	for _, tc := range []struct{ status, waiting, want string }{
		{"raised", "design", "awaiting"},
		{"answered", "build", "closed"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			f, l, steps := refusedLaneFixture(t)
			recordItemAConflict(t, f, l, tc.status, tc.waiting)
			cancelled := legacyCancelledClaim(t, f, l)
			if c, _ := conflictOf(t, f, "item-a"); c.Status != tc.want {
				t.Fatalf("fixture: the claim left the conflict %s, want %s", c.Status, tc.want)
			}
			if d := decisionFor(t, l, "item-a").decision; d.Action != ActionHold {
				t.Fatalf("fixture: the legacy records do not hold the item: %+v", d)
			}
			onePassNoStop(t, l)
			if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
				t.Fatalf("item-a ran on %v after the cancelled claim, want lane-2", got)
			}
			s := openFixture(t, f.storePath)
			defer s.Close()
			var same, other int
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, dispatchStartedKind, cancelled).Scan(&same); err != nil {
					return err
				}
				return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_id != ?`, dispatchStartedKind, cancelled).Scan(&other)
			}); err != nil {
				t.Fatal(err)
			}
			// The legacy claim, the refused claim on lane-1, and the run on lane-2.
			if same != 3 || other != 0 {
				t.Fatalf("the re-dispatch did not reuse the cancelled run id %s: %d starts under it, %d under another id", cancelled, same, other)
			}
		})
	}
}
