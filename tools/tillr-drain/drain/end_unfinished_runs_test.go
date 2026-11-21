package drain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func insertParallelClaim(t *testing.T, tx *store.Tx, itemID, laneID, groupID string, attempt int, readerIDs []string, at time.Time) {
	t.Helper()
	atMS := at.UTC().UnixMilli()
	if _, err := tx.Exec(
		`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES(?, 'leased', ?, NULL)
		 ON CONFLICT(lane_id) DO UPDATE SET state='leased', run_id=excluded.run_id, leased_until=NULL`,
		laneID, groupID,
	); err != nil {
		t.Fatal(err)
	}
	for i, rid := range readerIDs {
		if _, err := tx.Exec(
			`INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled) VALUES(?, ?, ?, ?, 0)`,
			rid, groupID, itemID, laneID,
		); err != nil {
			t.Fatal(err)
		}
		nodeID := "read-" + string(rune('a'+i))
		if _, err := tx.Exec(
			`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome, seat) VALUES(?, ?, ?, ?, 'running', 'scout')`,
			itemID, attempt, nodeID, rid,
		); err != nil {
			t.Fatal(err)
		}
	}
	_ = atMS
}

// 1a. Item landing (completeItem) ends earlier unfinished read_only_runs, node_runs,
// quota_pauses, and paused/non-terminal runs rows — reproducing the 4-reader
// item-doc-triage lane-4 shape.
func TestItemLandingEndsEarlierUnfinishedParallelRuns(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-doc-triage")
	at := l.cfg.Now().UTC()
	atStr := at.Format(time.RFC3339)

	oldGroup := "run-item-doc-triage-a2"
	oldReaders := []string{
		"run-item-doc-triage-a2-n9",
		"run-item-doc-triage-a2-n37",
		"run-item-doc-triage-a2-n38",
		"run-item-doc-triage-a2-n39",
	}
	finalRun := "run-item-doc-triage-a3"

	fixtureWrite(t, f, func(tx *store.Tx) error {
		insertParallelClaim(t, tx, "item-doc-triage", "lane-4", oldGroup, 2, oldReaders, at)
		// One of the old readers also had a paused runs row and a quota_pauses row.
		if err := applyTerminal(tx, run.Record{
			RunID:        oldReaders[0],
			ItemID:       "item-doc-triage",
			LaneID:       "lane-4",
			Backend:      "claude",
			State:        run.StatePaused,
			EndCause:     run.EndSignal,
			StartedAt:    atStr,
			FinishedAt:   atStr,
			ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		// Re-open read_only_runs.settled=0 and lane lease as they were before landing.
		if _, err := tx.Exec(`UPDATE read_only_runs SET settled=0 WHERE run_id=?`, oldReaders[0]); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO quota_pauses(run_id, item_id, lane_id, backend, state, paused_at) VALUES(?, 'item-doc-triage', 'lane-4', 'claude', 'waiting', ?)`, oldReaders[0], at.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET state='leased', run_id=? WHERE lane_id='lane-4'`, oldGroup); err != nil {
			return err
		}
		// Newer attempt run on lane-1 that actually landed the item.
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES('lane-1', 'idle')`); err != nil {
			return err
		}
		return applyTerminal(tx, run.Record{
			RunID:        finalRun,
			ItemID:       "item-doc-triage",
			LaneID:       "lane-1",
			Backend:      "claude",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			EndCause:     run.EndNormalExit,
			StartedAt:    at.Add(time.Minute).Format(time.RFC3339),
			FinishedAt:   at.Add(2 * time.Minute).Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		})
	})

	if err := l.completeItem(ctx, Candidate{ItemID: "item-doc-triage", RunID: finalRun, LaneID: "lane-1"}); err != nil {
		t.Fatal(err)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		var unsettled, runningNodes, pauses, nonTermRuns int
		if err := r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE item_id='item-doc-triage' AND settled=0`).Scan(&unsettled); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-doc-triage' AND outcome='running'`).Scan(&runningNodes); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM quota_pauses WHERE item_id='item-doc-triage'`).Scan(&pauses); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE item_id='item-doc-triage' AND state NOT IN ('exited','failed-to-start','timed-out')`).Scan(&nonTermRuns); err != nil {
			return err
		}
		if unsettled != 0 || runningNodes != 0 || pauses != 0 || nonTermRuns != 0 {
			t.Fatalf("unfinished rows remain: unsettled=%d runningNodes=%d pauses=%d nonTermRuns=%d", unsettled, runningNodes, pauses, nonTermRuns)
		}
		var laneState string
		if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-4'`).Scan(&laneState); err != nil {
			return err
		}
		if laneState != "idle" {
			t.Fatalf("lane-4 state=%q, want idle", laneState)
		}
		// Never-started readers must NOT have fake runs rows synthesized.
		var synthesized int
		if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id IN (?,?,?)`, oldReaders[1], oldReaders[2], oldReaders[3]).Scan(&synthesized); err != nil {
			return err
		}
		if synthesized != 0 {
			t.Fatalf("never-started claims synthesized %d runs rows, want 0", synthesized)
		}
		return nil
	})
}

// 1b. resolveElsewhere and settleTerminalItem also end unfinished run records for the item.
func TestTerminalSettlementsEndUnfinishedRuns(t *testing.T) {
	ctx := context.Background()

	t.Run("resolveElsewhere", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		at := l.cfg.Now().UTC()
		fixtureWrite(t, f, func(tx *store.Tx) error {
			insertParallelClaim(t, tx, "item-a", "lane-2", "run-item-a-a1", 1, []string{"run-item-a-a1-n1"}, at)
			return nil
		})
		if err := l.resolveElsewhere(ctx, Candidate{ItemID: "item-a", RunID: "run-item-a-a2", HeadSHA: runGit(t, f.root, "rev-parse", TrunkRef)}); err != nil {
			t.Fatal(err)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			var unsettled, running int
			_ = r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE item_id='item-a' AND settled=0`).Scan(&unsettled)
			_ = r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND outcome='running'`).Scan(&running)
			if unsettled != 0 || running != 0 {
				t.Fatalf("unsettled=%d running=%d, want 0", unsettled, running)
			}
			return nil
		})
	})

	t.Run("settleTerminalItem", func(t *testing.T) {
		ptf := newPendingTerminalFixture(t, false, "")
		f := ptf.f
		finishByHand(t, f, StateLanded)
		l := ptf.replacement(t, &terminalSteps{})
		at := l.cfg.Now().UTC()
		fixtureWrite(t, f, func(tx *store.Tx) error {
			insertParallelClaim(t, tx, "item-a", "lane-3", "run-item-a-a0", 0, []string{"run-item-a-a0-n1"}, at)
			return nil
		})
		var works map[string]*pendingWork
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			works, err = readPending(r)
			return err
		})
		if err := l.settleTerminalItem(ctx, works["item-a"], &itemTerminalError{ItemID: "item-a", State: StateLanded, Step: "plan"}); err != nil {
			t.Fatal(err)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			var unsettled, running int
			_ = r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE item_id='item-a' AND settled=0`).Scan(&unsettled)
			_ = r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND outcome='running'`).Scan(&running)
			if unsettled != 0 || running != 0 {
				t.Fatalf("unsettled=%d running=%d, want 0", unsettled, running)
			}
			return nil
		})
	})
}

// 2a. New attempt in applyPlan ends superseded paused/running runs and preserves
// readPriorRuns ordering (the superseded run never jumps ahead of newer finished runs).
func TestApplyPlanSupersessionEndsOlderUnfinishedRunsWithoutReorderingPriors(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	at := l.cfg.Now().UTC()
	atStr := at.Format(time.RFC3339)

	oldPaused := "run-item-a-a1"
	newerFinished := "run-item-a-a2"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES('lane-2', 'idle'), ('lane-3', 'idle')`); err != nil {
			return err
		}
		if err := applyTerminal(tx, run.Record{
			RunID:        oldPaused,
			ItemID:       "item-a",
			LaneID:       "lane-2",
			Backend:      "claude",
			State:        run.StatePaused,
			EndCause:     run.EndSignal,
			StartedAt:    atStr,
			FinishedAt:   atStr,
			ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled) VALUES(?, ?, 'item-a', 'lane-2', 0)`, oldPaused, oldPaused); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 1, 'build', ?, 'running')`, oldPaused); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO quota_pauses(run_id, item_id, lane_id, backend, state, paused_at) VALUES(?, 'item-a', 'lane-2', 'claude', 'waiting', ?)`, oldPaused, at.UnixMilli()); err != nil {
			return err
		}
		// Newer attempt a2 finished with infrastructure failure; a3 must wait for recovery.
		if err := applyTerminal(tx, run.Record{
			RunID:        newerFinished,
			ItemID:       "item-a",
			LaneID:       "lane-3",
			Backend:      "fixture",
			State:        run.StateFailedToStart,
			EndCause:     run.EndFailedToStart,
			StartedAt:    at.Add(time.Minute).Format(time.RFC3339),
			FinishedAt:   at.Add(2 * time.Minute).Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		return nil
	})

	// Record recovery of a2's backend before planning its next dispatch.
	if err := ProbeBackend(ctx, f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	plans := mustPlan(t, l)
	if len(plans) == 0 || plans[0].decision.Action != ActionDispatch || plans[0].decision.Attempt != 3 ||
		plans[0].decision.RunRequest == nil || plans[0].decision.RunRequest.Backend != "fixture" {
		t.Fatalf("expected attempt 3 on the recovered backend, got %+v", plans)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(ctx, plans[0], base, at.Add(3*time.Minute))
	if err != nil || !ok {
		t.Fatalf("applyPlan failed: ok=%v err=%v", ok, err)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM runs WHERE run_id=?`, oldPaused).Scan(&state); err != nil {
			return err
		}
		if state != run.StateTimedOut {
			t.Fatalf("superseded run state=%q, want %q", state, run.StateTimedOut)
		}
		var settled int
		if err := r.QueryRow(`SELECT settled FROM read_only_runs WHERE run_id=?`, oldPaused).Scan(&settled); err != nil {
			return err
		}
		if settled != 1 {
			t.Fatalf("read_only_runs settled=%d, want 1", settled)
		}
		var pauses int
		if err := r.QueryRow(`SELECT COUNT(*) FROM quota_pauses WHERE run_id=?`, oldPaused).Scan(&pauses); err != nil {
			return err
		}
		if pauses != 0 {
			t.Fatalf("quota_pauses count=%d, want 0", pauses)
		}
		priors, err := readPriorRuns(r)
		if err != nil {
			return err
		}
		got := priors["item-a"]
		if len(got) != 2 || got[0].RunID != oldPaused || got[1].RunID != newerFinished {
			t.Fatalf("priors order = %+v, want [%s, %s]", got, oldPaused, newerFinished)
		}
		if got[0].Class != AttemptUnjudged {
			t.Fatalf("superseded prior class = %q, want %q", got[0].Class, AttemptUnjudged)
		}
		_, endings, payloads, err := readRunJudgmentsAndEndings(r)
		if err != nil {
			return err
		}
		if endings[oldPaused] >= endings[newerFinished] {
			t.Fatalf("superseded ending seq %d >= newer finished seq %d", endings[oldPaused], endings[newerFinished])
		}
		var termRec run.Record
		if err := json.Unmarshal(payloads[oldPaused], &termRec); err != nil {
			return err
		}
		if termRec.State != run.StateTimedOut || termRec.EndCause != run.EndLeaseReclaimed {
			t.Fatalf("superseded finish payload = %+v, want timed-out / lease-reclaimed", termRec)
		}
		return nil
	})
}

// 2b. cancelStaging removes never-started node_runs(outcome='running'), and
// awaitBuildReader ends paused runs when replacing a reader.
func TestCancelStagingAndAwaitBuildReaderCleanup(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	at := l.cfg.Now().UTC()

	// cancelStaging cleanup of node_runs.
	d := Decision{
		DecisionID: "dec-cancel",
		ItemID:     "item-a",
		LaneID:     "lane-1",
		Attempt:    1,
		RunRequest: &run.Request{RunID: "run-item-a-a1-n1", ItemID: "item-a", Backend: "claude"},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES('lane-1', 'idle')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled) VALUES('run-item-a-a1-n1', 'run-item-a-a1-n1', 'item-a', 'lane-1', 0)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-a', 1, 'read-1', 'run-item-a-a1-n1', 'running')`); err != nil {
			return err
		}
		return nil
	})
	if err := l.cancelStaging(ctx, d); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		_ = r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE run_id='run-item-a-a1-n1'`).Scan(&count)
		if count != 0 {
			t.Fatalf("node_runs count=%d after cancelStaging, want 0", count)
		}
		return nil
	})

	// awaitBuildReader ends a paused run when len(live) == 0.
	pausedReader := "run-item-a-a2-n1"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{
			RunID:        pausedReader,
			ItemID:       "item-a",
			LaneID:       "lane-1",
			Backend:      "claude",
			State:        run.StatePaused,
			EndCause:     run.EndSignal,
			StartedAt:    at.Format(time.RFC3339),
			FinishedAt:   at.Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled) VALUES(?, 'grp-2', 'item-a', 'lane-1', 0)`, pausedReader)
		return err
	})
	if err := l.awaitBuildReader(ctx, pausedReader); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		var settled int
		if err := r.QueryRow(`SELECT state FROM runs WHERE run_id=?`, pausedReader).Scan(&state); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT settled FROM read_only_runs WHERE run_id=?`, pausedReader).Scan(&settled); err != nil {
			return err
		}
		if state != run.StateTimedOut || settled != 1 {
			t.Fatalf("awaitBuildReader: state=%q settled=%d, want timed-out / 1", state, settled)
		}
		return nil
	})
}

// 2c. Operator retry supersedeCandidate ends unfinished run records up through the retried attempt.
func TestSupersedeCandidateEndsUnfinishedRuns(t *testing.T) {
	ctx := context.Background()
	f, l, c := heldCandidateFixture(t)
	retryID := authorizeFixtureRetry(t, f, l, c)
	_ = retryID

	at := l.cfg.Now().UTC()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled) VALUES(?, ?, ?, ?, 0)`, c.RunID, c.RunID, c.ItemID, c.LaneID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES(?, 1, 'build', ?, 'running')`, c.ItemID, c.RunID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO quota_pauses(run_id, item_id, lane_id, backend, state, paused_at) VALUES(?, ?, ?, 'claude', 'waiting', ?)`, c.RunID, c.ItemID, c.LaneID, at.UnixMilli()); err != nil {
			return err
		}
		return nil
	})
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "gate erred"); err != nil {
		t.Fatal(err)
	}
	if err := l.supersedeRetriedCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var unsettled, running, pauses int
		_ = r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE item_id=? AND settled=0`, c.ItemID).Scan(&unsettled)
		_ = r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id=? AND outcome='running'`, c.ItemID).Scan(&running)
		_ = r.QueryRow(`SELECT COUNT(*) FROM quota_pauses WHERE item_id=?`, c.ItemID).Scan(&pauses)
		if unsettled != 0 || running != 0 || pauses != 0 {
			t.Fatalf("unsettled=%d running=%d pauses=%d, want 0", unsettled, running, pauses)
		}
		return nil
	})
}

// 3. One-time loop repair repairs pre-existing stale records across landed items and
// superseded attempts on the first pass, records drain.stale-runs-repaired, leaves
// legitimately active paused runs untouched, and does NOT run again on a new Loop.
func TestRepairStaleRunRecordsRunsOnceAndPreservesActivePausedRuns(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-landed", "item-active-paused", "item-paused-reader", "item-ready-retry")
	at := l.cfg.Now().UTC()
	atStr := at.Format(time.RFC3339)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES('lane-2','idle'),('lane-3','idle'),('lane-4','idle'),('lane-5','idle')`); err != nil {
			return err
		}
		// 1) Landed item with 4 stale read_only_runs, node_runs, and stranded lease on lane-4.
		if _, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='item-landed'`, StateLanded); err != nil {
			return err
		}
		insertParallelClaim(t, tx, "item-landed", "lane-4", "run-item-landed-a2", 2, []string{
			"run-item-landed-a2-n9", "run-item-landed-a2-n37", "run-item-landed-a2-n38", "run-item-landed-a2-n39",
		}, at)

		// 2) Active quota-paused run (!work.Done) must remain paused.
		dActive := Decision{
			DecisionID: "dec-active-paused",
			ItemID:     "item-active-paused",
			LaneID:     "lane-2",
			Attempt:    1,
			RunRequest: &run.Request{RunID: "run-active-paused-a1", ItemID: "item-active-paused", Backend: "claude"},
		}
		if err := appendEffect(tx, dispatchStartedKind, dActive.RunRequest.RunID, &pendingWork{Decision: dActive}, at); err != nil {
			return err
		}
		if err := applyTerminal(tx, run.Record{
			RunID: "run-active-paused-a1", ItemID: "item-active-paused", LaneID: "lane-2",
			Backend: "claude", State: run.StatePaused, EndCause: run.EndSignal,
			StartedAt: atStr, FinishedAt: atStr, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO quota_pauses(run_id, item_id, lane_id, backend, state, paused_at) VALUES('run-active-paused-a1', 'item-active-paused', 'lane-2', 'claude', 'waiting', ?)`, at.UnixMilli()); err != nil {
			return err
		}

		// 3) Paused parallel reader (work.Done == true) with no newer attempt must remain paused.
		dReader := Decision{
			DecisionID: "dec-paused-reader",
			ItemID:     "item-paused-reader",
			LaneID:     "lane-3",
			Attempt:    1,
			RunRequest: &run.Request{RunID: "run-paused-reader-a1-n1", ItemID: "item-paused-reader", Backend: "claude"},
		}
		if err := appendEffect(tx, dispatchStartedKind, dReader.RunRequest.RunID, &pendingWork{Decision: dReader, Done: true}, at); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationDoneKind, dReader.RunRequest.RunID, struct{}{}, at); err != nil {
			return err
		}
		if err := applyTerminal(tx, run.Record{
			RunID: "run-paused-reader-a1-n1", ItemID: "item-paused-reader", LaneID: "lane-3",
			Backend: "claude", State: run.StatePaused, EndCause: run.EndSignal,
			StartedAt: atStr, FinishedAt: atStr, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}

		// 4) Ready workflow node continuation (e.g. interrupted/failover node restart) keeps outcome='running'.
		dReady := Decision{
			DecisionID:      "dec-ready-retry",
			ItemID:          "item-ready-retry",
			LaneID:          "lane-5",
			Attempt:         1,
			WorkflowRestart: true,
			NodeIndex:       0,
			Workflow:        &Workflow{Stages: []WorkflowStage{{ID: "build", Seat: "build", Access: "write"}}},
			RunRequest:      &run.Request{RunID: "run-ready-retry-a1-n1-interrupted", ItemID: "item-ready-retry", NodeID: "build", Backend: "claude"},
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES('item-ready-retry', 1, 'build', 'run-ready-retry-a1-n1', 'running')`); err != nil {
			return err
		}
		if err := appendEffect(tx, nodeAdvancedKind, dReady.RunRequest.RunID, pendingWork{Decision: dReady, Ready: true, ParentRunID: "run-ready-retry-a1-n1"}, at); err != nil {
			return err
		}
		return nil
	})

	if err := l.repairStaleRunRecords(ctx); err != nil {
		t.Fatal(err)
	}

	var repairEventCount int
	fixtureRead(t, f, func(r *store.Reader) error {
		var unsettled, running int
		_ = r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE item_id='item-landed' AND settled=0`).Scan(&unsettled)
		_ = r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-landed' AND outcome='running'`).Scan(&running)
		if unsettled != 0 || running != 0 {
			t.Fatalf("landed item still has unsettled=%d running=%d", unsettled, running)
		}
		var laneState string
		_ = r.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-4'`).Scan(&laneState)
		if laneState != "idle" {
			t.Fatalf("lane-4 state=%q, want idle", laneState)
		}
		var activeState, readerState, readyOutcome string
		_ = r.QueryRow(`SELECT state FROM runs WHERE run_id='run-active-paused-a1'`).Scan(&activeState)
		_ = r.QueryRow(`SELECT state FROM runs WHERE run_id='run-paused-reader-a1-n1'`).Scan(&readerState)
		_ = r.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-ready-retry' AND attempt=1 AND node_id='build'`).Scan(&readyOutcome)
		if activeState != run.StatePaused || readerState != run.StatePaused || readyOutcome != "running" {
			t.Fatalf("active runs were altered: active=%q reader=%q readyOutcome=%q", activeState, readerState, readyOutcome)
		}
		_ = r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, staleRunsRepairedKind).Scan(&repairEventCount)
		if repairEventCount != 1 {
			t.Fatalf("repair event count=%d, want 1", repairEventCount)
		}
		var payload []byte
		_ = r.QueryRow(`SELECT payload FROM events WHERE kind=?`, staleRunsRepairedKind).Scan(&payload)
		var summary staleRunsRepaired
		if err := json.Unmarshal(payload, &summary); err != nil {
			return err
		}
		if len(summary.SettledReadOnly) != 4 || len(summary.EndedNodeRuns) != 4 || len(summary.ReleasedLanes) != 1 {
			t.Fatalf("unexpected repair summary: %+v", summary)
		}
		return nil
	})

	// New Loop on the same store must NOT run repair again even if new rows appear later.
	l2, err := NewLoop(l.cfg)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		insertParallelClaim(t, tx, "item-landed", "lane-4", "run-item-landed-a99", 99, []string{"run-item-landed-a99-n1"}, at)
		return nil
	})
	if err := l2.repairStaleRunRecords(ctx); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count, unsettled int
		_ = r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, staleRunsRepairedKind).Scan(&count)
		_ = r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE run_id='run-item-landed-a99-n1' AND settled=0`).Scan(&unsettled)
		if count != 1 || unsettled != 1 {
			t.Fatalf("second Loop re-ran one-time repair: events=%d unsettled=%d", count, unsettled)
		}
		return nil
	})
}
