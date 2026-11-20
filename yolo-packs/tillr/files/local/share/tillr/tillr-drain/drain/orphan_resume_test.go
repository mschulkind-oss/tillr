package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// pausedThenResumedAndKilled builds the state four lanes sat in for nine to twelve hours on
// 2026-11-19: a run a loop restart paused, whose continuation was claimed (run.resume,
// which clears the dispatch owner and leases the lane with no expiry) and then died with
// its loop, so the run's last event is the resume, its row still reads paused, and no
// process exists.
func pausedThenResumedAndKilled(t *testing.T, f refusalLoopFixture, l *Loop) Decision {
	t.Helper()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch claim: %v %v", ok, err)
	}
	d := p.decision
	id := d.RunRequest.RunID
	at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{RunID: id, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StatePaused, StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndRestart}); err != nil {
			return err
		}
		body, err := json.Marshal(map[string]any{"request": d.RunRequest, "run_id": id, "item_id": d.ItemID, "lane_id": d.LaneID, "backend": d.RunRequest.Backend, "session_id": "11111111-2222-3333-4444-555555555555", "resumed_at": at})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event.Event{Kind: run.ResumeKind, ObjectType: "run", ObjectID: &id, Payload: body, At: l.cfg.Now().UTC()}); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, id, d.LaneID)
		return err
	})
	return d
}

// Before the fix the item planned as a no-lane hold, forever: its lane was leased to the
// run itself, the resume had cleared the owner whose death would release it, and the run's
// terminal row made the reaper treat it as finished.
func TestAResumedRunWhoseFaceDiedWithoutAnEndIsSettledAndResumedByTheNextPass(t *testing.T) {
	f, l := passFixture(t, "item-a")
	d := pausedThenResumedAndKilled(t, f, l)
	if state, runID, expiry := laneRow(t, f, d.LaneID); state != run.LaneStateLeased || runID != d.RunRequest.RunID || expiry != "" {
		t.Fatalf("fixture: lane is %s/%s/%s", state, runID, expiry)
	}
	plans := mustPlan(t, l)
	if len(plans) == 0 || plans[0].decision.Action != ActionSettle {
		t.Fatalf("the next pass did not move the paused run: %+v", plans)
	}
	if state, runID, _ := laneRow(t, f, d.LaneID); state != run.LaneStateIdle || runID != "" {
		t.Fatalf("lane after the pass is %s/%s, want idle", state, runID)
	}
	if causes := laneReleases(t, f, d.RunRequest.RunID); len(causes) != 1 || causes[0] != laneReleaseReap {
		t.Fatalf("lane releases %v, want one reap", causes)
	}
}

// A continuation this loop is still running keeps its lease: the face is not gone.
func TestAResumedRunThisLoopIsStillRunningKeepsItsLane(t *testing.T) {
	f, l := passFixture(t, "item-a")
	d := pausedThenResumedAndKilled(t, f, l)
	l.active[d.RunRequest.RunID] = true
	mustPlan(t, l)
	if state, runID, _ := laneRow(t, f, d.LaneID); state != run.LaneStateLeased || runID != d.RunRequest.RunID {
		t.Fatalf("an active continuation lost its lane: %s/%s", state, runID)
	}
}

// A resume that a finish followed is an ended segment, not an orphaned one.
func TestAResumeFollowedByItsFinishIsNotReclaimed(t *testing.T) {
	f, l := passFixture(t, "item-a")
	d := pausedThenResumedAndKilled(t, f, l)
	at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit})
	})
	mustPlan(t, l)
	if causes := laneReleases(t, f, d.RunRequest.RunID); len(causes) != 0 && strings.Contains(strings.Join(causes, ","), laneReleaseReap) {
		t.Fatalf("reaped a finished segment: %v", causes)
	}
}
