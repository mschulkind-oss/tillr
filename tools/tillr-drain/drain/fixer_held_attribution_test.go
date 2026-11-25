package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type fixerAttachedRow struct {
	Seq        int64
	ObjectType string
	ObjectID   string
	Payload    map[string]any
}

func readFixerAttachedRows(t *testing.T, f refusalLoopFixture) []fixerAttachedRow {
	t.Helper()
	var rows []fixerAttachedRow
	fixtureRead(t, f, func(r *store.Reader) error {
		q, err := r.Query(`SELECT seq, object_type, COALESCE(object_id, ''), payload FROM events WHERE kind = ? ORDER BY seq`, FixerAttachedKind)
		if err != nil {
			return err
		}
		defer q.Close()
		for q.Next() {
			var row fixerAttachedRow
			var raw string
			if err := q.Scan(&row.Seq, &row.ObjectType, &row.ObjectID, &raw); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(raw), &row.Payload); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return q.Err()
	})
	return rows
}

func TestKeptCandidateFixerAttachedAfterGateVerdict(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t, "item-new-fixer", "item-reused-fixer", "item-overlap-fixer")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)

	clock := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	}
	steps := &testFixSteps{
		obstacleRequestSteps: obstacleRequestSteps{t: t, f: f},
		replays: map[string]ReplayOutcome{
			base: {Rev: base, Known: true, Red: []string{"go-test-tillr"}},
		},
	}
	var reportBuf bytes.Buffer
	l := f.loop(t, steps, &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath})
	l.cfg.Now = nowFn
	l.cfg.Report = &reportBuf
	ps := &ProcessSteps{Root: f.root, Store: f.storePath, Now: nowFn}

	runVerifyFor := func(itemID, runID, laneID, sha, checkName string) (int64, fixerAttachedRow) {
		t.Helper()
		steps.replays[sha] = ReplayOutcome{Rev: sha, Known: true, Red: []string{checkName}}
		steps.gateFn = func(c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateFailed,
				FailingChecks: []string{checkName},
				finish: func(cur GateOutcome) error {
					return ps.recordGateJudgment(ctx, c, gateStepJudgment{
						TickID:   c.TickID,
						Run:      runRef{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID},
						Outcome:  cur.Outcome,
						ExitCode: 1,
					})
				},
			}, nil
		}
		cand := Candidate{
			ItemID:   itemID,
			RunID:    runID,
			LaneID:   laneID,
			Worktree: l.lanePath(laneID),
			TickID:   "tick-1",
			BaseSHA:  sha,
			HeadSHA:  sha,
		}
		if _, _, err := l.verify(ctx, &cand, &LoopResult{}, nil); err != nil {
			t.Fatalf("verify(%s): %v\nreport:\n%s", itemID, err, reportBuf.String())
		}
		var verdictSeq int64
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(
				`SELECT COALESCE(MAX(seq), 0) FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ?`,
				GateVerdictKind, runID,
			).Scan(&verdictSeq)
		})
		if verdictSeq == 0 {
			t.Fatalf("expected gate.verdict for %s", runID)
		}
		rows := readFixerAttachedRows(t, f)
		var matched []fixerAttachedRow
		for _, row := range rows {
			if row.ObjectID == runID {
				matched = append(matched, row)
			}
		}
		if len(matched) != 1 {
			t.Fatalf("expected 1 kept-candidate drain.fixer-attached for %s, got %d (%+v)", runID, len(matched), matched)
		}
		if matched[0].Seq <= verdictSeq {
			t.Fatalf("expected drain.fixer-attached seq (%d) > gate.verdict seq (%d)", matched[0].Seq, verdictSeq)
		}
		return verdictSeq, matched[0]
	}

	// 1. Newly filed fixer-main-<sha>.
	_, row1 := runVerifyFor("item-new-fixer", "run-item-new-fixer-a1", "lane-1", base, "go-test-tillr")
	fixer1 := TrunkFixerID(base)
	if row1.ObjectType != "run" || row1.Payload["item_id"] != "item-new-fixer" || row1.Payload["run_id"] != "run-item-new-fixer-a1" ||
		row1.Payload["lane_id"] != "lane-1" || row1.Payload["fixer_item"] != fixer1 || row1.Payload["red_sha"] != base ||
		row1.Payload["kept_candidate"] != true {
		t.Fatalf("unexpected kept-candidate payload (new fixer): %+v", row1)
	}

	// Idempotent when called again after the same gate verdict.
	if err := l.recordKeptCandidateFixerAttached(ctx, Candidate{
		ItemID: "item-new-fixer", RunID: "run-item-new-fixer-a1", LaneID: "lane-1",
	}, trunkFixerHold{fixerID: fixer1, redSHA: base, failingChecks: []string{"go-test-tillr"}}); err != nil {
		t.Fatalf("second recordKeptCandidateFixerAttached: %v", err)
	}
	var dupCount int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`, FixerAttachedKind, "run-item-new-fixer-a1").Scan(&dupCount)
	})
	if dupCount != 1 {
		t.Fatalf("expected idempotent single drain.fixer-attached, got %d", dupCount)
	}

	// 2. Reused exact fixer-main-<sha> (same check, same sha).
	_, row2 := runVerifyFor("item-reused-fixer", "run-item-reused-fixer-a1", "lane-2", base, "go-test-tillr")
	if row2.Payload["item_id"] != "item-reused-fixer" || row2.Payload["fixer_item"] != fixer1 || row2.Payload["kept_candidate"] != true {
		t.Fatalf("unexpected kept-candidate payload (reused exact fixer): %+v", row2)
	}

	// 3. Check-overlap attachment onto an open older fixer-main-* at a new tip.
	if err := os.WriteFile(filepath.Join(f.root, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "add", "second.txt")
	runGit(t, f.root, "commit", "-qm", "advance trunk")
	newTip := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, l.lanePath("lane-3"), "reset", "--hard", newTip)

	_, row3 := runVerifyFor("item-overlap-fixer", "run-item-overlap-fixer-a1", "lane-3", newTip, "go-test-tillr")
	if row3.Payload["item_id"] != "item-overlap-fixer" || row3.Payload["fixer_item"] != fixer1 || row3.Payload["kept_candidate"] != true {
		t.Fatalf("unexpected kept-candidate overlap row: %+v", row3)
	}
	rows := readFixerAttachedRows(t, f)
	var sawFixerAttach bool
	for _, row := range rows {
		if row.ObjectID == fixer1 {
			sawFixerAttach = true
			if row.ObjectType != "run" || row.Payload["item_id"] != fixer1 || row.Payload["kept_candidate"] != false {
				t.Fatalf("unexpected fixer-scoped attachment row: %+v", row)
			}
		}
	}
	if !sawFixerAttach {
		t.Fatalf("expected fixer-scoped drain.fixer-attached for %s, rows=%+v", fixer1, rows)
	}
}

func TestFixerAttachedAbsentOnOpenTestFixAndDefectAttachmentShape(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	l.cfg.Now = func() time.Time { return time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC) }

	// 1. holdForOpenTestFix must leave candidate.trunkFixer nil and emit no drain.fixer-attached.
	steps := &testFixSteps{obstacleRequestSteps: obstacleRequestSteps{t: t, f: f}}
	l.cfg.Steps = steps
	cand := Candidate{ItemID: "item-held-by-test", RunID: "run-item-held-by-test-a1", LaneID: "lane-1"}
	if _, err := l.holdForOpenTestFix(ctx, &cand, GateOutcome{Outcome: GateFailed, FailingChecks: []string{"go-test-tillr"}}, "TestFoo failed"); err != nil {
		t.Fatalf("holdForOpenTestFix: %v", err)
	}
	if cand.trunkFixer != nil {
		t.Fatalf("expected candidate.trunkFixer == nil on open-test-fix hold, got %+v", cand.trunkFixer)
	}
	if rows := readFixerAttachedRows(t, f); len(rows) != 0 {
		t.Fatalf("expected 0 drain.fixer-attached rows on open-test-fix hold, got %+v", rows)
	}

	// 2. Worker OBSTACLE: trunk via routeObstacle must emit no kept_candidate: true drain.fixer-attached.
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)
	steps.replays = map[string]ReplayOutcome{base: {Rev: base, Known: true, Red: []string{"go-test-tillr"}}}
	l.routeObstacle(ctx,
		Decision{ItemID: "item-obstacle-trunk", LaneID: "lane-2"},
		run.Request{RunID: "run-item-obstacle-trunk-a1", ItemID: "item-obstacle-trunk", LaneID: "lane-2"},
		"lane-2", "tick-1", base,
		Obstacle{Kind: ObstacleTrunk, Fields: map[string]string{"CHECKS": "go-test-tillr", "BASE": base}},
		&LoopResult{},
	)
	for _, row := range readFixerAttachedRows(t, f) {
		if row.Payload["kept_candidate"] == true {
			t.Fatalf("expected no kept_candidate:true drain.fixer-attached on OBSTACLE: trunk, got %+v", row)
		}
	}

	// 3. fileDefectItem repeat attachment keeps ObjectID = defectID and adds item_id + kept_candidate: false.
	obs := Obstacle{Kind: ObstacleTooling, Fields: map[string]string{"COMMAND": "go test", "EVIDENCE": "boom", "PATHS": "tools/gate/done.py"}}
	defectID1, err := l.fileDefectItem(ctx, "item-a", obs)
	if err != nil {
		t.Fatalf("first fileDefectItem: %v", err)
	}
	defectID2, err := l.fileDefectItem(ctx, "item-b", obs)
	if err != nil {
		t.Fatalf("second fileDefectItem: %v", err)
	}
	if defectID1 != defectID2 {
		t.Fatalf("expected repeat defect to attach to %s, got %s", defectID1, defectID2)
	}
	var defectRows []fixerAttachedRow
	for _, row := range readFixerAttachedRows(t, f) {
		if row.ObjectID == defectID1 {
			defectRows = append(defectRows, row)
		}
	}
	if len(defectRows) != 1 {
		t.Fatalf("expected 1 defect drain.fixer-attached row, got %+v", defectRows)
	}
	if defectRows[0].Payload["item_id"] != defectID1 || defectRows[0].Payload["kept_candidate"] != false {
		t.Fatalf("unexpected defect attachment row: %+v", defectRows[0])
	}
}

func TestGateJudgmentEpochAdvancesAcrossTrunkRedHolds(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	ps := &ProcessSteps{Root: f.root, Store: f.storePath, Now: l.cfg.Now}

	const runID = "run-item-epoch-a1"
	const itemID = "item-epoch"
	cand := Candidate{ItemID: itemID, RunID: runID, LaneID: "lane-1", TickID: "tick-1"}

	// First gate.verdict before hold.
	if err := ps.recordGateJudgment(ctx, cand, gateStepJudgment{
		TickID: "tick-1", Run: runRef{RunID: runID, ItemID: itemID, LaneID: "lane-1"}, Outcome: GateFailed, ExitCode: 1,
	}); err != nil {
		t.Fatalf("first recordGateJudgment: %v", err)
	}

	// Non-kept-candidate drain.fixer-attached must NOT advance gateJudgmentEpoch.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, FixerAttachedKind, runID, map[string]any{"item_id": runID, "kept_candidate": false}, l.cfg.Now().UTC())
	})
	var epochBefore int64
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		epochBefore, err = gateJudgmentEpoch(r, runID)
		return err
	})
	if epochBefore != 0 {
		t.Fatalf("expected gateJudgmentEpoch == 0 for kept_candidate:false, got %d", epochBefore)
	}

	// Kept-candidate drain.fixer-attached advances gateJudgmentEpoch so a post-hold gate.verdict is recorded.
	if err := l.recordKeptCandidateFixerAttached(ctx, cand, trunkFixerHold{
		fixerID: "fixer-main-111111111111", redSHA: "111111111111", failingChecks: []string{"go-test-tillr"},
	}); err != nil {
		t.Fatalf("recordKeptCandidateFixerAttached: %v", err)
	}
	if err := ps.recordGateJudgment(ctx, cand, gateStepJudgment{
		TickID: "tick-2", Run: runRef{RunID: runID, ItemID: itemID, LaneID: "lane-1"}, Outcome: GateFailed, ExitCode: 1,
	}); err != nil {
		t.Fatalf("second recordGateJudgment after kept_candidate fixer-attached: %v", err)
	}

	// trunk-already-red drain.refusal also advances gateJudgmentEpoch so a third gate.verdict is recorded.
	if err := l.recordTrunkRefusal(ctx, cand, "222222222222", []string{"go-test-tillr"}, "fixer-main-222222222222"); err != nil {
		t.Fatalf("recordTrunkRefusal: %v", err)
	}
	if err := ps.recordGateJudgment(ctx, cand, gateStepJudgment{
		TickID: "tick-3", Run: runRef{RunID: runID, ItemID: itemID, LaneID: "lane-1"}, Outcome: GatePassed, ExitCode: 0,
	}); err != nil {
		t.Fatalf("third recordGateJudgment after trunk-already-red refusal: %v", err)
	}

	var verdictCount int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`, GateVerdictKind, runID).Scan(&verdictCount)
	})
	if verdictCount != 3 {
		t.Fatalf("expected 3 gate.verdict events across two trunk-red holds, got %d", verdictCount)
	}
}
