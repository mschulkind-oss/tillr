package drain

import (
	"encoding/json"
	"path/filepath"
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

func TestWaitingCandidateGatesBeforeLaterReservedWorkerStart(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1"}
	at := l.cfg.Now().UTC()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	laneDir := l.laneDirPath("lane-1")
	sessionID := "33333333-3333-4333-8333-333333333333"
	writeForkFile(t, filepath.Join(laneDir, SessionIDFile), sessionID+"\n")
	writeForkFile(t, filepath.Join(laneDir, BaseSHAFile), base+"\n")

	reqA := l.cfg.Policy.request("item-a", "lane-4", "run-item-a-a2-n1", "fixture")
	startA := Decision{
		ItemID:     "item-a",
		LaneID:     "lane-4",
		Action:     ActionRetry,
		Reason:     "operator-retry",
		From:       "queued",
		To:         "running",
		Attempt:    2,
		Seat:       "build",
		RunRequest: reqA,
	}
	identifyDecision(&startA)
	candA := Candidate{
		ItemID:  "item-a",
		LaneID:  "lane-4",
		RunID:   reqA.RunID,
		BaseSHA: base,
		HeadSHA: "head-sha",
		Request: reqA,
		Seat:    "build",
	}

	reqB := l.cfg.Policy.request("item-b", "lane-1", "run-item-b-a1-n1", "fixture")
	wsB := &WorkerSession{
		SessionID: sessionID,
		LaneID:    "lane-1",
		LaneDir:   laneDir,
		BaseSHA:   base,
		Backend:   "fixture",
		Route:     reqB.Route(),
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank='00m', express=0 WHERE item_id='item-a'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET rank='02m', express=0 WHERE item_id='item-b'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES ('lane-1', 'idle') ON CONFLICT(lane_id) DO UPDATE SET state='idle', run_id=NULL, leased_until=NULL`); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, reqA.RunID, pendingWork{Decision: startA, BaseSHA: base}, at.Add(-time.Hour)); err != nil {
			return err
		}
		exit := 0
		stamp := at.Add(-50 * time.Minute).Format(time.RFC3339Nano)
		if err := applyTerminal(tx, run.Record{
			RunID: reqA.RunID, ItemID: "item-a", LaneID: "lane-4", Backend: reqA.Backend,
			State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateReadyKind, reqA.RunID, candA, at.Add(-45*time.Minute)); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateLaneKind, reqA.RunID, candidateLane{Candidate: candA, Released: true}, at.Add(-40*time.Minute)); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationReleasedKind, reqA.RunID, struct{}{}, at.Add(-39*time.Minute)); err != nil {
			return err
		}
		if err := applyTerminal(tx, run.Record{
			RunID: reqB.RunID, ItemID: "item-b", LaneID: "lane-1", Backend: reqB.Backend,
			State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		return appendEffect(tx, RefusalKind, reqB.RunID, refusalPayload{
			Step: RefusalGate, ItemID: "item-b", WorkerSession: wsB, Detail: "item-b failed gate",
		}, at.Add(-30*time.Minute))
	})

	planMap := func(available []string) map[string]Decision {
		t.Helper()
		st, err := store.OpenRead(t.Context(), f.storePath)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer st.Close()
		got := map[string]Decision{}
		if err := st.ReadTx(t.Context(), func(r *store.Reader) error {
			envs, err := Plan(r, Pass{Now: at.Format(time.RFC3339Nano), AvailableLanes: &available, Build: &l.build}, l.cfg.Policy, &VantageQuestions{Root: f.root, Context: t.Context()})
			if err != nil {
				return err
			}
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
		return got
	}

	// With only lane-1 free, place-1 waiting finished candidate item-a must gate on
	// lane-1 instead of letting place-2 returned worker item-b take lane-1 via its
	// worker-lane reservation.
	got := planMap([]string{"lane-1"})
	if d := got["item-a"]; d.Action != ActionJudge || d.LaneID != "lane-1" {
		t.Fatalf("Plan(lane-1) item-a = (%s, %s, %s), want judge on lane-1", d.Action, d.Reason, d.LaneID)
	}
	if d := got["item-b"]; d.Action != ActionHold || d.Reason != ReasonNoLane {
		t.Fatalf("Plan(lane-1) item-b = (%s, %s, %s), want hold/no-lane", d.Action, d.Reason, d.LaneID)
	}
	if entry, ok := proposalFollower(t, l)(at).nextFreshStart(); ok {
		t.Fatalf("nextFreshStart() = (%s on %s), want none while place-1 waiting candidate item-a needs lane-1", entry.decision.ItemID, entry.decision.LaneID)
	}

	// With a second free lane, item-a takes unreserved lane-2 and item-b keeps its
	// reserved warm session on lane-1.
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES ('lane-2', 'idle') ON CONFLICT(lane_id) DO UPDATE SET state='idle', run_id=NULL, leased_until=NULL`)
		return err
	})
	gotTwo := planMap([]string{"lane-1", "lane-2"})
	if d := gotTwo["item-a"]; d.Action != ActionJudge || d.LaneID != "lane-2" {
		t.Fatalf("Plan(lane-1,lane-2) item-a = (%s, %s, %s), want judge on lane-2", d.Action, d.Reason, d.LaneID)
	}
	if d := gotTwo["item-b"]; d.Action != ActionDispatch || d.LaneID != "lane-1" || d.WorkerSessionReason != "resumed-worker" {
		t.Fatalf("Plan(lane-1,lane-2) item-b = (%s, %s, %s, %s), want dispatch/resumed-worker on lane-1", d.Action, d.Reason, d.LaneID, d.WorkerSessionReason)
	}
	if entry, ok := proposalFollower(t, l)(at).nextFreshStart(); !ok || entry.decision.ItemID != "item-b" || entry.decision.LaneID != "lane-1" {
		t.Fatalf("two-lane nextFreshStart() = (%+v, %v), want item-b on lane-1", entry.decision, ok)
	}

	// When item-b is placed before item-a, item-b keeps lane-1 and item-a waits.
	l.cfg.Lanes = []string{"lane-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank='04m' WHERE item_id='item-a'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET rank='00m' WHERE item_id='item-b'`)
		return err
	})
	gotReversed := planMap([]string{"lane-1"})
	if d := gotReversed["item-b"]; d.Action != ActionDispatch || d.LaneID != "lane-1" {
		t.Fatalf("Plan(reversed) item-b = (%s, %s, %s), want dispatch on lane-1", d.Action, d.Reason, d.LaneID)
	}
	if d := gotReversed["item-a"]; d.Action != ActionHold || d.Reason != ReasonNoLane {
		t.Fatalf("Plan(reversed) item-a = (%s, %s, %s), want hold/no-lane", d.Action, d.Reason, d.LaneID)
	}
	if entry, ok := proposalFollower(t, l)(at).nextFreshStart(); !ok || entry.decision.ItemID != "item-b" || entry.decision.LaneID != "lane-1" {
		t.Fatalf("reversed nextFreshStart() = (%+v, %v), want item-b on lane-1", entry.decision, ok)
	}
}
