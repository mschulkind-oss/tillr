package drain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A held candidate whose lane was released, then reassigned to a second lane and held there
// again, must release that second lane too. The reassignment's durable record is the whole
// pending state, and its lane is occupied again: replaying it must not keep the earlier
// release's lane_released, or the second hold frees the lease without resetting the lane and
// the planner moves the pinned commit to a third lane, stranding a copy on every lane it
// passed through (the 2026-11-17 lane-holds-unlanded-change lanes).
func TestReassignedHeldCandidateReleasesItsSecondLane(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	sibling := mustPlan(t, l)[1]
	ok, _, err := l.applyPlan(context.Background(), sibling, c.BaseSHA, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("take old lane: %v/%v", ok, err)
	}
	clearFault := func() {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			faults, err := ReadItemFaults(tx.Reader())
			if err != nil {
				return err
			}
			return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
		})
	}
	clearFault()
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	p := mustPlan(t, l)[0]
	if p.decision.Action != ActionJudge || p.decision.LaneID != "lane-2" {
		t.Fatalf("retry did not choose the free lane: %+v", p.decision)
	}
	// The gate faults again on the second lane: the item is held a second time.
	examineHeldCandidate(t, l)

	trunk := runGit(t, f.root, "rev-parse", TrunkRef)
	if got := runGit(t, l.lanePath("lane-2"), "rev-parse", "HEAD"); got != trunk {
		t.Fatalf("second hold left lane-2 holding %s, want it reset to trunk %s", got, trunk)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("release lost the pinned candidate: %s", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		w := works[c.ItemID]
		if w == nil || !w.LaneReleased || w.Decision.LaneID != "lane-2" {
			t.Errorf("second lane's release is not recorded: %+v", w)
		}
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND json_extract(payload,'$.released') AND json_extract(payload,'$.candidate.lane_id')='lane-2'`, candidateLaneKind, c.RunID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("lane-2 release receipts = %d, want 1", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Replaying a reassignment reads the assigned lane as occupied, whatever the earlier release
// said: the payload omits a false lane_released.
func TestReplayedAssignmentOccupiesItsLane(t *testing.T) {
	_, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	s, err := store.Open(context.Background(), l.cfg.Store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		w := works[c.ItemID]
		if w == nil || !w.LaneReleased {
			t.Fatalf("fixture: candidate not released: %+v", w)
		}
		d := w.Decision
		req := *d.RunRequest
		d.LaneID, d.RunRequest = "lane-2", &req
		if err := l.assignPinnedCandidate(tx, w, &d, l.cfg.Now()); err != nil {
			return err
		}
		works, err = readPending(tx.Reader())
		if err != nil {
			return err
		}
		w = works[c.ItemID]
		if w.LaneReleased || !w.NeedsStage || w.Decision.LaneID != "lane-2" || w.Candidate.LaneID != "lane-2" {
			t.Errorf("replayed assignment: released=%v needs_stage=%v lane=%s candidate lane=%s", w.LaneReleased, w.NeedsStage, w.Decision.LaneID, w.Candidate.LaneID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// insertLanding records a land.landing the way the lander's record lands in the store, without
// the land utility's registry.
func insertLanding(t *testing.T, f refusalLoopFixture, runID, headSHA, result string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (1,'land.landing','item','item-x',json_object('run_id',?,'head_sha',?,'result',?))`, runID, headSHA, result)
		return err
	})
}

func laneCopyEvents(t *testing.T, f refusalLoopFixture) []string {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var out []string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind=? ORDER BY seq`, laneCopyClearedKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// A lane holding a commit a run pin retains, or one a recorded landing names, holds a copy:
// staging resets it and records why. Only a commit neither retains is refused.
func TestStagingResetsALaneCopyOfAPinnedOrLandedCandidate(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	stager := &LaneStager{Root: f.root, Store: f.storePath}
	spec := StageSpec{LaneID: "lane-1", BaseSHA: base}
	runGit(t, f.root, "checkout", "-qb", "lane-copy")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "pinned candidate")
	pinned := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", "refs/tillr/runs/run-pinned", pinned)
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err != nil {
		t.Fatalf("a pinned copy was refused: %v", err)
	}
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "squash-landed candidate, pin dropped")
	landed := runGit(t, f.root, "rev-parse", "HEAD")
	err := stager.refuseUnlandedCommit(context.Background(), spec, f.root)
	if refusal := asLaneRefusal(err); refusal == nil || refusal.Reason != ReasonLaneUnlandedChange {
		t.Fatalf("an unretained commit was not refused: %v", err)
	}
	insertLanding(t, f, "run-landed", landed, "refused")
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err == nil {
		t.Fatal("a refused landing cleared the only copy")
	}
	insertLanding(t, f, "run-landed", landed, "landed")
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err != nil {
		t.Fatalf("a landed copy was refused: %v", err)
	}
	got := laneCopyEvents(t, f)
	if len(got) != 2 || !strings.Contains(got[0], `"refs":["refs/tillr/runs/run-pinned"]`) || !strings.Contains(got[0], `"landed":false`) ||
		!strings.Contains(got[1], `"landed":true`) || !strings.Contains(got[1], `"run_id":"run-landed"`) {
		t.Fatalf("lane copy records: %v", got)
	}
}

// strandedLaneFixture is the 2026-11-17 state: item-a's held candidate is released and its
// old lane taken by a sibling, and lane-2 — the free lane the planner will assign it — still
// holds another commit, left behind by an earlier loop.
func strandedLaneFixture(t *testing.T, pin bool) (refusalLoopFixture, *Loop, Candidate, string) {
	t.Helper()
	f, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	sibling := mustPlan(t, l)[1]
	ok, _, err := l.applyPlan(context.Background(), sibling, c.BaseSHA, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("take old lane: %v/%v", ok, err)
	}
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	stale := l.lanePath("lane-2")
	runGit(t, stale, "commit", "--allow-empty", "-qm", "an earlier loop's candidate copy")
	sha := runGit(t, stale, "rev-parse", "HEAD")
	if pin {
		runGit(t, f.root, "update-ref", "refs/tillr/runs/run-earlier", sha)
		insertLanding(t, f, "run-earlier", sha, "landed")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	if p := mustPlan(t, l)[0]; p.decision.Action != ActionJudge || p.decision.LaneID != "lane-2" {
		t.Fatalf("retry did not choose lane-2: %+v", p.decision)
	}
	return f, l, c, sha
}

// The 18:45Z stop: the planner assigned a pinned candidate to a lane holding an earlier,
// already landed candidate, and staging refused it as a change nobody landed.
func TestPinnedCandidateStagesOverALandedCopy(t *testing.T) {
	f, l, c, _ := strandedLaneFixture(t, true)
	steps := &heldHeadSteps{}
	l.cfg.Steps = steps
	examineHeldCandidate(t, l)
	if steps.candidate.LaneID != "lane-2" || steps.candidate.HeadSHA != c.HeadSHA {
		t.Fatalf("candidate was not staged on lane-2: %+v", steps.candidate)
	}
	if got := laneCopyEvents(t, f); len(got) != 1 || !strings.Contains(got[0], `"lane_id":"lane-2"`) || !strings.Contains(got[0], `"landed":true`) {
		t.Fatalf("lane copy records: %v", got)
	}
}

// A lane holding a commit nothing retains keeps it, leaves rotation, and the pinned candidate
// is released for another lane: the loop does not stop on that lane's condition.
func TestPinnedCandidateRefusedByItsLaneLeavesTheLoopRunning(t *testing.T) {
	f, l, c, sha := strandedLaneFixture(t, false)
	p := mustPlan(t, l)[0]
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim examination: %v/%v", ok, err)
	}
	l.launchExamination(context.Background(), p.decision, work, l.cfg.Now(), &PassTiming{}, nil)
	var result LoopResult
	select {
	case done := <-l.completions:
		stop, err := l.takeCompletion(done, &result)
		if err != nil || stop != "" {
			t.Fatalf("one lane's condition stopped the loop: %q/%v", stop, err)
		}
	case <-time.After(hangGuard):
		t.Fatal("examination did not finish")
	}
	if got := runGit(t, l.lanePath("lane-2"), "rev-parse", "HEAD"); got != sha {
		t.Fatalf("the refused lane's only copy moved: %s", got)
	}
	if cond := l.laneConditions["lane-2"]; cond == nil || !cond.out || cond.reason != ReasonLaneUnlandedChange {
		t.Fatalf("lane-2 is not out of rotation: %+v", cond)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("candidate pin moved: %s", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if w := works[c.ItemID]; w == nil || !w.LaneReleased || w.Done {
			t.Errorf("candidate is not released for another lane: %+v", w)
		}
		var state string
		var runID *string
		if err := r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id='lane-2'`).Scan(&state, &runID); err != nil {
			return err
		}
		if runID != nil {
			t.Errorf("lane-2 is still reserved for %s", *runID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
