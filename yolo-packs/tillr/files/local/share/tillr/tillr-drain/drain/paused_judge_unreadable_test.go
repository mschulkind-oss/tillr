package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func recordLegacyPausedJudgeUnreadableHold(t *testing.T, ctx context.Context, f refusalLoopFixture, l *Loop, mode string) string {
	t.Helper()
	var work *pendingWork
	fixtureRead(t, f, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		work = works["item-a"]
		return nil
	})
	if work == nil {
		t.Fatal("missing pending work for item-a after pause")
	}

	holdResult := &LoopResult{}
	var pausedRunID string
	switch mode {
	case "evaluate":
		c, err := l.evaluatorCandidate(ctx, work.Decision)
		if err != nil {
			t.Fatalf("evaluatorCandidate: %v", err)
		}
		pausedRunID = work.Decision.RunRequest.RunID
		detail := "open " + filepath.Join(run.DurableDir(l.laneDirPath(c.LaneID), pausedRunID), "eval.output") + ": no such file or directory"
		if err := l.holdJudge(ctx, c, pausedRunID, ReasonJudgeUnreadable, detail, holdResult); err != nil {
			t.Fatalf("holdJudge: %v", err)
		}
		if err := l.finishEvaluatorHold(ctx, work.Decision, c); err != nil {
			t.Fatalf("finishEvaluatorHold: %v", err)
		}
	case "judge":
		if work.Candidate == nil {
			t.Fatalf("expected candidate on item-a pending work, got %+v", work)
		}
		c := *work.Candidate
		pausedRunID = judgeRunID(c.RunID)
		detail := "open " + filepath.Join(run.DurableDir(l.laneDirPath(c.LaneID), pausedRunID), "eval.output") + ": no such file or directory"
		if err := l.holdJudge(ctx, c, pausedRunID, ReasonJudgeUnreadable, detail, holdResult); err != nil {
			t.Fatalf("holdJudge: %v", err)
		}
		if stop := l.abandon(ctx, c.LaneID, c.RunID, c.HeadSHA); stop != "" {
			t.Fatalf("abandon: %s", stop)
		}
		if err := l.markExamined(ctx, work.Decision); err != nil {
			t.Fatalf("markExamined: %v", err)
		}
	default:
		t.Fatalf("unknown mode %q", mode)
	}
	if err := l.releaseExamination(ctx, work.Decision); err != nil {
		t.Fatalf("releaseExamination: %v", err)
	}

	// Simulate another item having reused the released lane in the meantime.
	laneDir := l.laneDirPath("lane-2")
	writeForkFile(t, filepath.Join(laneDir, BriefFile), "other item brief\n")
	writeForkFile(t, filepath.Join(laneDir, SessionIDFile), "00000000-0000-4000-8000-000000000099\n")

	fixtureRead(t, f, func(r *store.Reader) error {
		var latestID, latestState string
		if err := r.QueryRow(`SELECT run_id, state FROM runs WHERE item_id='item-a' ORDER BY event_seq DESC, started_at DESC, rowid DESC LIMIT 1`).Scan(&latestID, &latestState); err != nil {
			return err
		}
		if latestID != pausedRunID || latestState != run.StatePaused {
			t.Fatalf("latest run = (%q, %q), want (%q, %q)", latestID, latestState, pausedRunID, run.StatePaused)
		}
		var step string
		if err := r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.step'),'') FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, RefusalKind, pausedRunID).Scan(&step); err != nil {
			return err
		}
		if step != ReasonJudgeUnreadable {
			t.Fatalf("refusal step = %q, want %q", step, ReasonJudgeUnreadable)
		}
		return nil
	})
	return pausedRunID
}

func TestJudgeUnreadableHoldOverPausedRunReleasesAndResumesWithoutOperatorCommand(t *testing.T) {
	for _, mode := range []string{"evaluate", "judge"} {
		t.Run(mode, func(t *testing.T) {
			f, l, base := judgeRouteFixture(t, "pass")
			steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: mode}
			l.cfg.Steps = steps
			l.cfg.Lanes = []string{"lane-2"}
			l.cfg.StorePollInterval = 5 * time.Millisecond

			if mode == "evaluate" {
				writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
				runGit(t, f.root, "add", "config/tillr/workflows")
				runGit(t, f.root, "commit", "-qm", "evaluator workflow")
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`)
					return err
				})
			}

			ctx := context.Background()
			setupResult := &LoopResult{}
			for i := 0; i < 6 && !steps.pausedOnce; i++ {
				if _, err := l.pass(ctx, setupResult); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, setupResult)
			}
			if !steps.pausedOnce {
				t.Fatalf("target %s never paused; runs=%+v", mode, steps.runs)
			}

			pausedRunID := recordLegacyPausedJudgeUnreadableHold(t, ctx, f, l, mode)

			wantAction := ActionSettle
			if mode == "judge" {
				wantAction = ActionJudge
			}
			// Starting directly from the recorded judge-unreadable hold over a paused
			// run with a free lane, the very next plan already treats it as paused work.
			immediateDec := decisionFor(t, l, "item-a").decision
			if immediateDec.Action != wantAction {
				t.Fatalf("next plan over paused judge-unreadable hold: got %+v, want action %s", immediateDec, wantAction)
			}

			// While rotated down to a single busy lane-1, the item must wait on no-lane
			// as paused work instead of staying held as judge-unreadable.
			l.cfg.Lanes = []string{"lane-1"}
			at := l.cfg.Now()
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES ('lane-1', 'leased', 'busy-run', ?)
					ON CONFLICT(lane_id) DO UPDATE SET state='leased', run_id='busy-run', leased_until=excluded.leased_until`,
					at.Add(time.Hour).UnixMilli()); err != nil {
					return err
				}
				_, err := tx.Exec(`UPDATE items SET state='in-queue', rank='0', workflow='', judge_mode='' WHERE item_id='item-b'`)
				return err
			})

			blockedDec := decisionFor(t, l, "item-a").decision
			if blockedDec.Action != ActionHold || blockedDec.Reason != ReasonNoLane {
				t.Fatalf("while lane-1 is busy: got %+v, want hold/%s (not %s)", blockedDec, ReasonNoLane, ReasonJudgeUnreadable)
			}
			lanes := []string{"lane-1"}
			fixtureRead(t, f, func(r *store.Reader) error {
				answer, err := Why(r, Pass{AvailableLanes: &lanes, Now: at.Format(time.RFC3339Nano), Build: &l.build}, l.cfg.Policy, &VantageQuestions{Root: f.root})
				if err != nil {
					return err
				}
				for _, row := range answer.Rows {
					if row.ItemID == "item-a" {
						if row.Reason != ReasonNoLane || !slices.Contains(row.WaitingOn, "lane-1") {
							t.Fatalf("unexpected why row while lane-1 busy: %+v", row)
						}
					}
				}
				return nil
			})

			// Once lane-1 frees, the next plan treats the paused run as paused work
			// (ActionSettle / ActionJudge) and resumes it without any operator command.
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE lanes SET state='idle', run_id=NULL, leased_until=NULL WHERE lane_id='lane-1'`)
				return err
			})

			readyDec := decisionFor(t, l, "item-a").decision
			if readyDec.Action != wantAction {
				t.Fatalf("next plan after lane freed: got %+v, want action %s", readyDec, wantAction)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				answer, err := Why(r, Pass{AvailableLanes: &lanes, Now: at.Format(time.RFC3339Nano), Build: &l.build}, l.cfg.Policy, &VantageQuestions{Root: f.root})
				if err != nil {
					return err
				}
				for _, row := range answer.Rows {
					if row.ItemID == "item-a" {
						if row.Reason == ReasonJudgeUnreadable || row.Class != "moving" {
							t.Fatalf("why still held item-a instead of treating as paused work: %+v", row)
						}
					}
				}
				return nil
			})

			resumeResult := &LoopResult{}
			for i := 0; i < 8; i++ {
				if _, err := l.pass(ctx, resumeResult); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, resumeResult)
				var state string
				fixtureRead(t, f, func(r *store.Reader) error {
					return r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state)
				})
				if state == "landed" {
					break
				}
			}

			if resumeResult.RefusedSteps[ReasonJudgeUnreadable] != 0 {
				t.Fatalf("judge-unreadable recorded again on resume: %+v", resumeResult.RefusedSteps)
			}
			if len(steps.resumedReqs) != 1 || !steps.resumedReqs[0].Resume || steps.resumedReqs[0].RunID != pausedRunID {
				t.Fatalf("expected 1 resumed continuation of %s, got %+v", pausedRunID, steps.resumedReqs)
			}
			if steps.pauseSession == "" || steps.resumeSession != steps.pauseSession {
				t.Fatalf("continuation lost judge session: paused=%q resumed=%q", steps.pauseSession, steps.resumeSession)
			}

			fixtureRead(t, f, func(r *store.Reader) error {
				var state string
				if err := r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
					return err
				}
				if state != "landed" {
					t.Fatalf("item-a state=%q, want landed", state)
				}
				return nil
			})
		})
	}
}

func TestJudgeUnreadablePausedRunSynthesizedWithoutPendingHistory(t *testing.T) {
	f, l, _ := judgeRouteFixture(t, "pass")
	l.cfg.Lanes = []string{"lane-1"}
	baseSHA := runGit(t, f.root, "rev-parse", TrunkRef)
	const pausedRunID = "run-item-a-a1-judge"
	const writerRunID = "run-item-a-a1"

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{
			RunID:      pausedRunID,
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "fixture",
			State:      run.StatePaused,
			StartedAt:  "2026-11-23T05:20:00Z",
			FinishedAt: "2026-11-23T05:21:00Z",
			EndCause:   run.EndRestart,
		}); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"item_id":       "item-a",
			"lane_id":       "lane-1",
			"step":          ReasonJudgeUnreadable,
			"detail":        "missing eval.output",
			"base_sha":      baseSHA,
			"head_sha":      baseSHA,
			"writer_run_id": writerRunID,
		})
		if err != nil {
			return err
		}
		runID := pausedRunID
		_, err = tx.Append(event.Event{
			At:         l.cfg.Now(),
			Kind:       RefusalKind,
			ObjectType: "run",
			ObjectID:   &runID,
			Payload:    payload,
		})
		return err
	})

	dec := decisionFor(t, l, "item-a").decision
	if dec.Action == ActionHold && dec.Reason == ReasonJudgeUnreadable {
		t.Fatalf("synthesized paused judge-unreadable item remained held: %+v", dec)
	}
	if dec.Action != ActionJudge {
		t.Fatalf("synthesized paused judge-unreadable decision = %+v, want %s", dec, ActionJudge)
	}
}

func TestJudgeUnreadablePausedRunOutOfScopeCasesStayHeld(t *testing.T) {
	t.Run("exited-judge-unreadable-stays-held", func(t *testing.T) {
		f, l, base := judgeRouteFixture(t, "pass")
		steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: "evaluate"}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-2"}
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
		runGit(t, f.root, "add", "config/tillr/workflows")
		runGit(t, f.root, "commit", "-qm", "evaluator workflow")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`)
			return err
		})

		ctx := context.Background()
		setupResult := &LoopResult{}
		for i := 0; i < 6 && !steps.pausedOnce; i++ {
			if _, err := l.pass(ctx, setupResult); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, setupResult)
		}
		pausedRunID := recordLegacyPausedJudgeUnreadableHold(t, ctx, f, l, "evaluate")
		zero := 0
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, run.Record{
				RunID:      pausedRunID,
				ItemID:     "item-a",
				LaneID:     "lane-2",
				Backend:    "fixture",
				State:      run.StateExited,
				ExitCode:   &zero,
				StartedAt:  "2026-11-23T05:20:00Z",
				FinishedAt: "2026-11-23T05:25:00Z",
				EndCause:   run.EndNormalExit,
			})
		})

		got := decisionFor(t, l, "item-a").decision
		if got.Action != ActionHold || got.Reason != ReasonJudgeUnreadable {
			t.Fatalf("exited judge-unreadable changed behavior: got %+v, want hold/%s", got, ReasonJudgeUnreadable)
		}
	})

	t.Run("quota-paused-run-not-treated-as-restart-paused-work", func(t *testing.T) {
		f, l, base := judgeRouteFixture(t, "pass")
		steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: "evaluate"}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-2"}
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
		runGit(t, f.root, "add", "config/tillr/workflows")
		runGit(t, f.root, "commit", "-qm", "evaluator workflow")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`)
			return err
		})

		ctx := context.Background()
		setupResult := &LoopResult{}
		for i := 0; i < 6 && !steps.pausedOnce; i++ {
			if _, err := l.pass(ctx, setupResult); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, setupResult)
		}
		pausedRunID := recordLegacyPausedJudgeUnreadableHold(t, ctx, f, l, "evaluate")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO quota_pauses(run_id, item_id, lane_id, backend, state, paused_at) VALUES (?, 'item-a', 'lane-2', 'fixture', 'waiting', ?)`, pausedRunID, l.cfg.Now().UnixMilli())
			return err
		})

		got := decisionFor(t, l, "item-a").decision
		if got.Action != ActionHold || got.Reason != ReasonJudgeUnreadable {
			t.Fatalf("quota-paused judge-unreadable reopened out of scope: got %+v, want hold/%s", got, ReasonJudgeUnreadable)
		}
	})

	t.Run("other-hold-reason-over-paused-run-stays-held", func(t *testing.T) {
		f, l, base := judgeRouteFixture(t, "pass")
		steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: "evaluate"}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-2"}
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
		runGit(t, f.root, "add", "config/tillr/workflows")
		runGit(t, f.root, "commit", "-qm", "evaluator workflow")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`)
			return err
		})

		ctx := context.Background()
		setupResult := &LoopResult{}
		for i := 0; i < 6 && !steps.pausedOnce; i++ {
			if _, err := l.pass(ctx, setupResult); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, setupResult)
		}
		var work *pendingWork
		fixtureRead(t, f, func(r *store.Reader) error {
			works, err := readPending(r)
			work = works["item-a"]
			return err
		})
		c, err := l.evaluatorCandidate(ctx, work.Decision)
		if err != nil {
			t.Fatal(err)
		}
		holdResult := &LoopResult{}
		pausedRunID := work.Decision.RunRequest.RunID
		if err := l.holdJudge(ctx, c, pausedRunID, ReasonJudgeDefectiveMismatch, "mismatch", holdResult); err != nil {
			t.Fatal(err)
		}
		if err := l.finishEvaluatorHold(ctx, work.Decision, c); err != nil {
			t.Fatal(err)
		}
		if err := l.releaseExamination(ctx, work.Decision); err != nil {
			t.Fatal(err)
		}

		got := decisionFor(t, l, "item-a").decision
		if got.Action != ActionHold || got.Reason != ReasonJudgeDefectiveMismatch {
			t.Fatalf("other hold reason over paused run changed behavior: got %+v, want hold/%s", got, ReasonJudgeDefectiveMismatch)
		}
	})
}

func TestStartStoreSyncPreservesPendingPointerUntilPausedJudgeUnreadableChanges(t *testing.T) {
	f, l, base := judgeRouteFixture(t, "pass")
	steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: "evaluate"}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-2"}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`)
		return err
	})

	ctx := context.Background()
	var syncer startStoreSync
	defer syncer.close()

	at := l.cfg.Now()
	seed, err := syncer.syncStore(ctx, l.cfg.Store, at, l.cfg.Policy, startSnapshot{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Clock-only quota probe refresh with no paused judge-unreadable hold must preserve pending pointer.
	afterProbe, err := syncer.syncStore(ctx, l.cfg.Store, at.Add(QuotaProbeInterval+time.Second), l.cfg.Policy, seed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if afterProbe.pending != seed.pending {
		t.Fatal("quota probe refresh cloned pending without any pending or paused-judge change")
	}

	setupResult := &LoopResult{}
	for i := 0; i < 6 && !steps.pausedOnce; i++ {
		if _, err := l.pass(ctx, setupResult); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, setupResult)
	}
	pausedRunID := recordLegacyPausedJudgeUnreadableHold(t, ctx, f, l, "evaluate")

	at = at.Add(2 * QuotaProbeInterval)
	reopenedSnap, err := syncer.syncStore(ctx, l.cfg.Store, at, l.cfg.Policy, afterProbe, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := reopenedSnap.pending.works["item-a"]
	if w == nil || w.Done {
		t.Fatalf("startStoreSync did not reopen paused judge-unreadable item-a: %+v", w)
	}

	// Unrelated item update while item-a remains reopened must preserve reopenedSnap.pending pointer.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		id := "item-b"
		if _, err := tx.Exec(`UPDATE items SET state='in-queue', updated_at=updated_at+1 WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Append(event.Event{
			At:         at.Add(time.Second),
			Kind:       "item.update",
			ObjectType: "item",
			ObjectID:   &id,
			Payload:    []byte(`{"state":"in-queue"}`),
		})
		return err
	})
	afterItemUpdate, err := syncer.syncStore(ctx, l.cfg.Store, at.Add(2*time.Second), l.cfg.Policy, reopenedSnap, nil)
	if err != nil {
		t.Fatal(err)
	}
	if afterItemUpdate.pending != reopenedSnap.pending {
		t.Fatal("unrelated item update cloned pending when reopened paused-judge state was unchanged")
	}

	// Once the paused run transitions to exited, startStoreSync reverts the reopened work.
	zero := 0
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:      pausedRunID,
			ItemID:     "item-a",
			LaneID:     "lane-2",
			Backend:    "fixture",
			State:      run.StateExited,
			ExitCode:   &zero,
			StartedAt:  "2026-11-23T05:20:00Z",
			FinishedAt: "2026-11-23T05:25:00Z",
			EndCause:   run.EndNormalExit,
		})
	})
	revertedSnap, err := syncer.syncStore(ctx, l.cfg.Store, at.Add(3*time.Second), l.cfg.Policy, afterItemUpdate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if revertedSnap.pending == afterItemUpdate.pending {
		t.Fatal("expected pending clone when paused run exited and reverted reopened work")
	}
	if rw := revertedSnap.pending.works["item-a"]; rw == nil || !rw.Done {
		t.Fatalf("expected item-a work reverted to Done after run exited, got %+v", rw)
	}
}

func TestPendingHistoryCacheReopensPausedJudgeUnreadableWithoutMutatingCachedBase(t *testing.T) {
	f, l, base := judgeRouteFixture(t, "pass")
	steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: "evaluate"}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-2"}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`)
		return err
	})

	ctx := context.Background()
	setupResult := &LoopResult{}
	for i := 0; i < 6 && !steps.pausedOnce; i++ {
		if _, err := l.pass(ctx, setupResult); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, setupResult)
	}
	pausedRunID := recordLegacyPausedJudgeUnreadableHold(t, ctx, f, l, "evaluate")

	var reopenedSnap *pendingHistoryState
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		reopenedSnap, err = readPendingHistoryState(r)
		if err != nil {
			return err
		}
		sameTx, err := readPendingHistoryState(r)
		if err != nil {
			return err
		}
		if sameTx != reopenedSnap {
			t.Fatal("second readPendingHistoryState in same ReadTx returned different pointer")
		}
		return nil
	})
	if w := reopenedSnap.works["item-a"]; w == nil || w.Done || !w.ReopenedPausedJudge {
		t.Fatalf("expected reopened paused-judge work for item-a, got %+v", w)
	}

	beforeFolded := pendingEventsFolded.Load()
	fixtureRead(t, f, func(r *store.Reader) error {
		again, err := readPendingHistoryState(r)
		if err != nil {
			return err
		}
		if w := again.works["item-a"]; w == nil || w.Done || !w.ReopenedPausedJudge {
			t.Fatalf("second ReadTx lost reopened paused-judge work: %+v", w)
		}
		return nil
	})
	if folded := pendingEventsFolded.Load() - beforeFolded; folded != 0 {
		t.Fatalf("second ReadTx folded %d events, want 0", folded)
	}

	// Non-event SQL change (quota_pauses row) immediately reverts reopening on the
	// next ReadTx without mutating the previously returned snapshot.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO quota_pauses(run_id, item_id, lane_id, backend, state, paused_at) VALUES (?, 'item-a', 'lane-2', 'fixture', 'waiting', ?)`, pausedRunID, l.cfg.Now().UnixMilli())
		return err
	})
	var afterQuota *pendingHistoryState
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		afterQuota, err = readPendingHistoryState(r)
		return err
	})
	if w := afterQuota.works["item-a"]; w == nil || !w.Done || w.ReopenedPausedJudge {
		t.Fatalf("expected Done work after quota_pauses insert, got %+v", w)
	}
	if w := reopenedSnap.works["item-a"]; w == nil || w.Done || !w.ReopenedPausedJudge {
		t.Fatalf("prior cached snapshot was mutated: %+v", w)
	}
}
