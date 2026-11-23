package drain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestFastStartRespectsHigherPlacedLaneReleasedCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	at := l.cfg.Now().UTC()
	req := l.cfg.Policy.request("item-a", "lane-4", "run-item-a-a2-n1", "fixture")
	startDecision := Decision{
		ItemID:     "item-a",
		LaneID:     "lane-4",
		Action:     ActionRetry,
		Reason:     "operator-retry",
		From:       "queued",
		To:         "running",
		Attempt:    2,
		Seat:       "build",
		RunRequest: req,
		PassID:     "gen-11750",
	}
	identifyDecision(&startDecision)
	cand := Candidate{
		ItemID:  "item-a",
		LaneID:  "lane-4",
		RunID:   req.RunID,
		BaseSHA: "base-sha",
		HeadSHA: "head-sha",
		Request: req,
		Seat:    "build",
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank='00m', express=1 WHERE item_id='item-a'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET rank='02m', express=1 WHERE item_id='item-b'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES ('lane-1', 'idle') ON CONFLICT(lane_id) DO UPDATE SET state='idle', run_id=NULL, leased_until=NULL`); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, req.RunID, pendingWork{Decision: startDecision, BaseSHA: "base-sha"}, at.Add(-time.Hour)); err != nil {
			return err
		}
		exit := 0
		stamp := at.Add(-50 * time.Minute).Format(time.RFC3339Nano)
		if err := applyTerminal(tx, run.Record{
			RunID: req.RunID, ItemID: "item-a", LaneID: "lane-4", Backend: req.Backend,
			State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateReadyKind, req.RunID, cand, at.Add(-45*time.Minute)); err != nil {
			return err
		}
		hold := gateStallHold{Candidate: cand, HeadTree: "tree-sha", GatesInFlight: 4}
		if err := appendEffect(tx, gateStallHeldKind, req.RunID, hold, at.Add(-40*time.Minute)); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateLaneKind, req.RunID, candidateLane{Candidate: cand, Released: true}, at.Add(-39*time.Minute)); err != nil {
			return err
		}
		return appendEffect(tx, examinationReleasedKind, req.RunID, struct{}{}, at.Add(-38*time.Minute))
	})

	refresh := proposalFollower(t, l)
	snap := refresh(at)

	// Place-1 item-a is a lane-released candidate whose stall rerun is eligible
	// (0 or 1 gates in flight < 4). Fast-start must not hand lane-1 to place-2 item-b.
	if entry, ok := snap.nextFreshStart(); ok {
		t.Fatalf("nextFreshStart() = (%s on %s), want none while place-1 lane-released candidate item-a needs lane-1", entry.decision.ItemID, entry.decision.LaneID)
	}
	if entry, ok := snap.nextFreshStartWithGates(1); ok {
		t.Fatalf("nextFreshStartWithGates(1) = (%s on %s), want none while place-1 lane-released candidate item-a needs lane-1", entry.decision.ItemID, entry.decision.LaneID)
	}
	// When the gate-stall hold is still active (4 gates in flight >= 4), item-a cannot
	// rerun yet, so place-2 item-b may take lane-1.
	if entry, ok := snap.nextFreshStartWithGates(4); !ok || entry.decision.ItemID != "item-b" || entry.decision.LaneID != "lane-1" {
		t.Fatalf("nextFreshStartWithGates(4) = (%+v, %v), want item-b on lane-1", entry.decision, ok)
	}

	// With a second free lane, place-1 item-a reserves lane-1 and place-2 item-b takes lane-2.
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES ('lane-2', 'idle') ON CONFLICT(lane_id) DO UPDATE SET state='idle', run_id=NULL, leased_until=NULL`)
		return err
	})
	twoLanes := proposalFollower(t, l)(at)
	if entry, ok := twoLanes.nextFreshStart(); !ok || entry.decision.ItemID != "item-b" || entry.decision.LaneID != "lane-2" {
		t.Fatalf("two-lane nextFreshStart() = (%+v, %v), want item-b on lane-2 (leaving lane-1 for item-a)", entry.decision, ok)
	}

	// Confirm the planner itself gives lane-1 to place-1 item-a (judge) and holds item-b (no-lane).
	st, err := store.OpenRead(t.Context(), f.storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	available := []string{"lane-1"}
	var got map[string]Decision
	if err := st.ReadTx(t.Context(), func(r *store.Reader) error {
		envs, err := Plan(r, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &available, GatesInFlight: 1, Build: &l.build}, l.cfg.Policy, &VantageQuestions{Root: f.root, Context: t.Context()})
		if err != nil {
			return err
		}
		got = map[string]Decision{}
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			got[d.ItemID] = d
		}
		return nil
	}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if d := got["item-a"]; d.Action != ActionJudge || d.LaneID != "lane-1" {
		t.Fatalf("Plan item-a = (%s, %s, %s), want judge on lane-1", d.Action, d.Reason, d.LaneID)
	}
	if d := got["item-b"]; d.Action != ActionHold || d.Reason != ReasonNoLane {
		t.Fatalf("Plan item-b = (%s, %s, %s), want hold/no-lane", d.Action, d.Reason, d.LaneID)
	}
}

func TestFastStartOrdinaryLaneReleasedCandidateDoesNotBlockExpressLane(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "express-1"}
	at := l.cfg.Now().UTC()
	req := l.cfg.Policy.request("item-a", "lane-4", "run-item-a-a1-n1", "fixture")
	startDecision := Decision{
		ItemID:     "item-a",
		LaneID:     "lane-4",
		Action:     ActionDispatch,
		Reason:     "ready",
		From:       "queued",
		To:         "running",
		Attempt:    1,
		Seat:       "build",
		RunRequest: req,
	}
	identifyDecision(&startDecision)
	cand := Candidate{
		ItemID:  "item-a",
		LaneID:  "lane-4",
		RunID:   req.RunID,
		BaseSHA: "base-sha",
		HeadSHA: "head-sha",
		Request: req,
		Seat:    "build",
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank='00m', express=0 WHERE item_id='item-a'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET rank='02m', express=1 WHERE item_id='item-b'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id) VALUES ('lane-1', 'leased', 'foreign'), ('express-1', 'idle', NULL)
			ON CONFLICT(lane_id) DO UPDATE SET state=excluded.state, run_id=excluded.run_id`); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, req.RunID, pendingWork{Decision: startDecision, BaseSHA: "base-sha"}, at.Add(-time.Hour)); err != nil {
			return err
		}
		exit := 0
		stamp := at.Add(-50 * time.Minute).Format(time.RFC3339Nano)
		if err := applyTerminal(tx, run.Record{
			RunID: req.RunID, ItemID: "item-a", LaneID: "lane-4", Backend: req.Backend,
			State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateReadyKind, req.RunID, cand, at.Add(-45*time.Minute)); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateLaneKind, req.RunID, candidateLane{Candidate: cand, Released: true}, at.Add(-40*time.Minute)); err != nil {
			return err
		}
		return appendEffect(tx, examinationReleasedKind, req.RunID, struct{}{}, at.Add(-39*time.Minute))
	})

	snap := proposalFollower(t, l)(at)
	if entry, ok := snap.nextFreshStart(); !ok || entry.decision.ItemID != "item-b" || entry.decision.LaneID != "express-1" {
		t.Fatalf("nextFreshStart() = (%+v, %v), want express item-b on express-1", entry.decision, ok)
	}
}
