package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/store"
)

func TestExamineCoordinatorClaimsOnRunExitedWithoutQueuePass(t *testing.T) {
	// Keep XDG_RUNTIME_DIR short (under /tmp, independent of deep TMPDIR) so
	// AF_UNIX sun_path stays well under Linux's 108-byte UNIX_PATH_MAX.
	runtimeDir, err := os.MkdirTemp("/tmp", "tillr-exam-notify-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	l.cfg.Now = func() time.Time { return time.Now().UTC() }

	// Block the fallback poll timer forever so only run.exited can wake the coordinator.
	neverTick := make(chan time.Time)
	l.examineTimer = func(time.Duration) (<-chan time.Time, func()) {
		return neverTick, func() {}
	}

	relayCtx, relayCancel := context.WithCancel(context.Background())
	relayDone := make(chan error, 1)
	go func() {
		relayDone <- notify.Serve(relayCtx, notify.ServeConfig{
			StorePath: f.storePath,
			IdleExit:  time.Minute,
		})
	}()
	t.Cleanup(func() {
		relayCancel()
		<-relayDone
	})

	// Subscribe to candidate.ready and agent.slot-freed to observe post-examination publication.
	subCtx, subCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer subCancel()
	var sub *notify.Subscription
	for {
		sub, err = notify.Subscribe(subCtx, f.storePath, notify.TopicCandidateReady, notify.TopicAgentSlotFreed)
		if err == nil {
			break
		}
		if subCtx.Err() != nil {
			t.Fatalf("subscribing to relay: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer func() { _ = sub.Close() }()

	// Dispatch item-a onto lane-1 and commit a candidate change, leaving the run running.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]' WHERE item_id = 'item-a'`)
		return err
	})
	plans := mustPlan(t, l)
	var dispatchPlan plannedApplication
	for _, p := range plans {
		if p.decision.ItemID == "item-a" && p.decision.Action == ActionDispatch {
			dispatchPlan = p
		}
	}
	if dispatchPlan.decision.RunRequest == nil {
		t.Fatalf("expected dispatch plan for item-a, got %+v", plans)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	at := time.Now().UTC()
	var generation string
	ok, work, err := l.applyPlanGeneration(context.Background(), dispatchPlan, base, at, &generation)
	if !ok || err != nil {
		t.Fatalf("applyPlanGeneration: ok=%t err=%v", ok, err)
	}
	d := work.Decision
	lane := l.lanePath(d.LaneID)
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "slice 3 candidate\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate")

	// Track planDefinition calls: examination must not call planDefinition.
	var planCalls atomic.Int64
	planDefinitionCalls.Store(&planCalls)
	t.Cleanup(func() { planDefinitionCalls.Store(nil) })

	// Start ONLY the notify listener and the examine coordinator — no queue pass loop!
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopTrigger := l.startTriggerNotify(ctx)
	t.Cleanup(stopTrigger)

	// Give the notify subscriber a moment to connect to the relay.
	time.Sleep(50 * time.Millisecond)

	var result LoopResult
	stopExamine := l.startExamineCoordinator(ctx, &result)
	t.Cleanup(stopExamine)

	// Simulate tillr-run committing the terminal run record, which publishes run.exited post-commit.
	exitAt := time.Now().UTC()
	code := 0
	rec := run.Record{
		RunID:      d.RunRequest.RunID,
		ItemID:     d.ItemID,
		LaneID:     d.LaneID,
		Backend:    d.RunRequest.Backend,
		State:      run.StateExited,
		ExitCode:   &code,
		StartedAt:  at.Format(time.RFC3339Nano),
		FinishedAt: exitAt.Format(time.RFC3339Nano),
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, rec)
	})

	// Wait for the examine coordinator to complete examination without any queue pass.
	select {
	case c := <-l.completions:
		if !c.fromExamineCoordinator {
			t.Fatalf("expected completion from examine coordinator, got %+v", c)
		}
		if c.outcome.err != nil {
			t.Fatalf("examine coordinator error: %v", c.outcome.err)
		}
		if c.itemID != d.ItemID || c.id != d.RunRequest.RunID {
			t.Fatalf("unexpected completion: item=%q run=%q, want (%q, %q)", c.itemID, c.id, d.ItemID, d.RunRequest.RunID)
		}
	case <-time.After(hangGuard):
		t.Fatal("examine coordinator did not claim and examine run on run.exited without a queue pass")
	}

	// Slice 3 settlement does not call planDefinition; Slice 4's claimSettledCandidateJudge
	// (called after settle in examineClaimed) is the only remaining caller (at most 1).
	if got := planCalls.Load(); got > 1 {
		t.Fatalf("planDefinition called %d times during examination+judge, want <= 1 (0 in settlement, 1 in Slice 4 judge claim)", got)
	}

	// Verify candidate.ready was persisted.
	rs, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var hasCandidate bool
	if err := rs.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, candidateReadyKind, d.RunRequest.RunID).Scan(&n); err != nil {
			return err
		}
		hasCandidate = n > 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !hasCandidate {
		t.Fatalf("expected %s event for %s", candidateReadyKind, d.RunRequest.RunID)
	}

	// Verify handoff.jsonl recorded claim_worker_settlement and measure worker exit -> claim_worker_settlement end.
	var (
		foundSpan      bool
		exitToClaimEnd int64
		claimDur       int64
	)
	for _, h := range readHandoffTimings(t, f.root, d.RunRequest.RunID) {
		if h.Phase != "examination" {
			continue
		}
		for _, span := range h.Phases {
			if span.Phase == "claim_worker_settlement" {
				foundSpan = true
				exitToClaimEnd = span.EndMS - exitAt.UnixMilli()
				claimDur = span.EndMS - span.StartMS
			}
		}
	}
	if !foundSpan {
		t.Fatalf("expected claim_worker_settlement span in handoff.jsonl for %s", d.RunRequest.RunID)
	}
	t.Logf("handoff.jsonl: worker_exit_to_claim_worker_settlement_end=%dms (claim_duration=%dms; baseline 5400-13600ms)", exitToClaimEnd, claimDur)
	if exitToClaimEnd > 2000 {
		t.Fatalf("worker exit to claim_worker_settlement end = %dms, expected well under 5400ms baseline", exitToClaimEnd)
	}

	// Confirm candidate.ready arrived on the relay.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	gotCandidateReady := false
	for !gotCandidateReady {
		frame, err := sub.Next(waitCtx)
		if err != nil {
			t.Fatalf("waiting for %s on relay: %v", notify.TopicCandidateReady, err)
		}
		if frame.Topic == notify.TopicCandidateReady && frame.RunID == d.RunRequest.RunID {
			gotCandidateReady = true
		}
	}
}

func TestExamineCoordinatorFallbackPollWithNotificationsOff(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.NoNotify = true
	l.cfg.StorePollInterval = DefaultFallbackPollInterval
	at := l.cfg.Now().UTC()
	l.cfg.Now = func() time.Time { return at }

	timerCh := make(chan time.Time, 1)
	l.examineTimer = func(d time.Duration) (<-chan time.Time, func()) {
		if d != DefaultFallbackPollInterval {
			t.Errorf("examineTimer interval = %v, want %v", d, DefaultFallbackPollInterval)
		}
		return timerCh, func() {}
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]' WHERE item_id = 'item-a'`)
		return err
	})
	plans := mustPlan(t, l)
	var dispatchPlan plannedApplication
	for _, p := range plans {
		if p.decision.ItemID == "item-a" && p.decision.Action == ActionDispatch {
			dispatchPlan = p
		}
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	var generation string
	ok, work, err := l.applyPlanGeneration(context.Background(), dispatchPlan, base, at, &generation)
	if !ok || err != nil {
		t.Fatalf("applyPlanGeneration: ok=%t err=%v", ok, err)
	}
	d := work.Decision
	lane := l.lanePath(d.LaneID)
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "fallback poll candidate\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var result LoopResult
	stopExamine := l.startExamineCoordinator(ctx, &result)
	t.Cleanup(stopExamine)

	// Wait for the initial startup scan to settle before committing the terminal run.
	time.Sleep(30 * time.Millisecond)

	code := 0
	rec := run.Record{
		RunID:      d.RunRequest.RunID,
		ItemID:     d.ItemID,
		LaneID:     d.LaneID,
		Backend:    d.RunRequest.Backend,
		State:      run.StateExited,
		ExitCode:   &code,
		StartedAt:  at.Format(time.RFC3339Nano),
		FinishedAt: at.Add(time.Second).Format(time.RFC3339Nano),
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, rec)
	})

	// With notifications off and no timer tick yet, the run must remain unclaimed.
	select {
	case c := <-l.completions:
		t.Fatalf("unexpected completion before fallback poll tick: %+v", c)
	case <-time.After(50 * time.Millisecond):
	}

	// Fire the 15s fallback poll tick.
	timerCh <- at.Add(DefaultFallbackPollInterval)

	select {
	case c := <-l.completions:
		if !c.fromExamineCoordinator || c.outcome.err != nil {
			t.Fatalf("unexpected fallback poll completion: %+v", c)
		}
		if c.id != d.RunRequest.RunID {
			t.Fatalf("completion run_id = %q, want %q", c.id, d.RunRequest.RunID)
		}
	case <-time.After(hangGuard):
		t.Fatal("examine coordinator did not claim run on fallback poll with notifications off")
	}
}

func TestExaminationSettlesByCASWithoutPlanDefinitionForAllNodeKinds(t *testing.T) {
	t.Run("commit", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		l.cfg.Lanes = []string{"lane-1"}
		at := l.cfg.Now().UTC()
		l.cfg.Now = func() time.Time { return at }

		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]' WHERE item_id = 'item-a'`)
			return err
		})
		plans := mustPlan(t, l)
		var dp plannedApplication
		for _, p := range plans {
			if p.decision.ItemID == "item-a" {
				dp = p
			}
		}
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		var gen string
		ok, work, err := l.applyPlanGeneration(context.Background(), dp, base, at, &gen)
		if !ok || err != nil {
			t.Fatalf("applyPlanGeneration: ok=%t err=%v", ok, err)
		}
		d := work.Decision
		lane := l.lanePath(d.LaneID)
		writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "candidate.txt\n")
		writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), BaseSHAFile), base+"\n")
		writeForkFile(t, filepath.Join(lane, "candidate.txt"), "commit node\n")
		runGit(t, lane, "add", "candidate.txt")
		runGit(t, lane, "commit", "-qm", "commit node")

		code := 0
		rec := run.Record{
			RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID,
			Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: &code,
			StartedAt: at.Format(time.RFC3339Nano), FinishedAt: at.Add(time.Second).Format(time.RFC3339Nano),
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, rec)
		})

		var planCalls atomic.Int64
		planDefinitionCalls.Store(&planCalls)
		t.Cleanup(func() { planDefinitionCalls.Store(nil) })

		claim, err := l.claimWorkerSettlement(context.Background(), d.ItemID, d.RunRequest.RunID, at.Add(time.Second))
		if err != nil || !claim.claimed {
			t.Fatalf("first claimWorkerSettlement: claimed=%t err=%v", claim.claimed, err)
		}
		// Second racing CAS on the same run must lose.
		claimAgain, err := l.claimWorkerSettlement(context.Background(), d.ItemID, d.RunRequest.RunID, at.Add(time.Second))
		if err != nil {
			t.Fatalf("second claimWorkerSettlement error: %v", err)
		}
		if claimAgain.claimed {
			t.Fatal("expected second racing claimWorkerSettlement to return claimed=false")
		}

		timing := &PassTiming{TickID: claim.tickID, AtMS: at.UnixMilli()}
		var result LoopResult
		out := l.settle(context.Background(), claim.work.Decision, rec, "", claim.work.BaseSHA, claim.decision.RunRequest.CorrelationID, at.Add(time.Second), &result, timing, nil, true)
		if out.err != nil {
			t.Fatalf("settle commit node: %v", out.err)
		}
		if got := planCalls.Load(); got != 0 {
			t.Fatalf("planDefinition called %d times for commit node, want 0", got)
		}
	})

	t.Run("no_commit", func(t *testing.T) {
		f, l, _ := exitFixture(t, fmt.Sprintf(exitOne, "no_commit"), false, 0)
		at := l.cfg.Now().UTC()
		l.cfg.Now = func() time.Time { return at }

		plans := mustPlan(t, l)
		var dp plannedApplication
		for _, p := range plans {
			if p.decision.Action == ActionDispatch {
				dp = p
				break
			}
		}
		if dp.decision.RunRequest == nil {
			t.Fatalf("expected dispatch plan for no_commit item, got %+v", plans)
		}
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		var gen string
		ok, work, err := l.applyPlanGeneration(context.Background(), dp, base, at, &gen)
		if !ok || err != nil {
			t.Fatalf("applyPlanGeneration: ok=%t err=%v", ok, err)
		}
		d := work.Decision
		runGit(t, l.lanePath(d.LaneID), "reset", "--hard", base)
		writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), BaseSHAFile), base+"\n")
		reportPath := filepath.Join(l.laneDirPath(d.LaneID), "report.md")
		writeForkFile(t, reportPath, "RESULT: done\n")

		code := 0
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, run.Record{
				RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID,
				Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: &code,
				StartedAt:    at.Format(time.RFC3339Nano),
				FinishedAt:   at.Add(time.Second).Format(time.RFC3339Nano),
				ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
			})
		})

		var planCalls atomic.Int64
		planDefinitionCalls.Store(&planCalls)
		t.Cleanup(func() { planDefinitionCalls.Store(nil) })

		claim, err := l.claimWorkerSettlement(context.Background(), d.ItemID, d.RunRequest.RunID, at.Add(time.Second))
		if err != nil || !claim.claimed {
			t.Fatalf("first claimWorkerSettlement: claimed=%t err=%v", claim.claimed, err)
		}
		claimAgain, err := l.claimWorkerSettlement(context.Background(), d.ItemID, d.RunRequest.RunID, at.Add(time.Second))
		if err != nil {
			t.Fatalf("second claimWorkerSettlement error: %v", err)
		}
		if claimAgain.claimed {
			t.Fatal("expected second racing claimWorkerSettlement to return claimed=false")
		}

		timing := &PassTiming{TickID: claim.tickID, AtMS: at.UnixMilli()}
		comp := l.examineClaimed(context.Background(), claim.decision, claim.work, at.Add(time.Second), timing, nil)
		if comp.outcome.err != nil {
			t.Fatalf("examineClaimed no_commit node: %v", comp.outcome.err)
		}
		if got := planCalls.Load(); got != 0 {
			t.Fatalf("planDefinition called %d times for no_commit node, want 0", got)
		}

		// Verify the no_commit item completed without a candidate.
		rs, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rs.Close() }()
		var state string
		if err := rs.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, d.ItemID).Scan(&state)
		}); err != nil {
			t.Fatal(err)
		}
		if state != StateLanded {
			t.Fatalf("no_commit item state = %q, want %q", state, StateLanded)
		}
	})

	t.Run("conflict_fix", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		l.cfg.Lanes = []string{"lane-1"}
		at := l.cfg.Now().UTC()
		l.cfg.Now = func() time.Time { return at }

		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]' WHERE item_id = 'item-a'`)
			return err
		})
		plans := mustPlan(t, l)
		var dp plannedApplication
		for _, p := range plans {
			if p.decision.ItemID == "item-a" {
				dp = p
			}
		}
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		var gen string
		ok, work, err := l.applyPlanGeneration(context.Background(), dp, base, at, &gen)
		if !ok || err != nil {
			t.Fatalf("applyPlanGeneration: ok=%t err=%v", ok, err)
		}
		worker := work.Decision
		code := 0
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := applyTerminal(tx, run.Record{
				RunID: worker.RunRequest.RunID, ItemID: worker.ItemID, LaneID: worker.LaneID,
				Backend: worker.RunRequest.Backend, State: run.StateExited, ExitCode: &code,
				StartedAt: at.Format(time.RFC3339Nano), FinishedAt: at.Add(time.Second).Format(time.RFC3339Nano),
			}); err != nil {
				return err
			}
			// Mark the worker examined and judged so we can hand off to a -cfix repair run.
			if err := appendEffect(tx, examinationDoneKind, worker.RunRequest.RunID, struct{}{}, at.Add(time.Second)); err != nil {
				return err
			}
			return appendEffect(tx, judgeRequestKind, worker.RunRequest.RunID, struct{}{}, at.Add(time.Second))
		})

		ordinal, err := l.nextItemAttempt(context.Background(), worker.ItemID)
		if err != nil {
			t.Fatal(err)
		}
		req := *worker.RunRequest
		req.RunID = conflictFixRunID(req.RunID)
		repair := Decision{
			ItemID:     worker.ItemID,
			LaneID:     worker.LaneID,
			From:       "candidate",
			To:         "running",
			Action:     ActionDispatch,
			Reason:     ReasonConflictFix,
			Seat:       worker.Seat,
			Attempt:    ordinal,
			RunRequest: &req,
		}
		identifyDecision(&repair)
		if err := l.handoffRepair(context.Background(), Candidate{ItemID: worker.ItemID, RunID: worker.RunRequest.RunID}, repair, base); err != nil {
			t.Fatalf("handoffRepair: %v", err)
		}

		lane := l.lanePath(repair.LaneID)
		writeForkFile(t, filepath.Join(l.laneDirPath(repair.LaneID), FenceFile), "candidate.txt\n")
		writeForkFile(t, filepath.Join(l.laneDirPath(repair.LaneID), BaseSHAFile), base+"\n")
		writeForkFile(t, filepath.Join(lane, "candidate.txt"), "conflict fix resolved\n")
		runGit(t, lane, "add", "candidate.txt")
		runGit(t, lane, "commit", "-qm", "conflict fix")

		repairRec := run.Record{
			RunID: req.RunID, ItemID: repair.ItemID, LaneID: repair.LaneID,
			Backend: req.Backend, State: run.StateExited, ExitCode: &code,
			StartedAt:  at.Add(2 * time.Second).Format(time.RFC3339Nano),
			FinishedAt: at.Add(3 * time.Second).Format(time.RFC3339Nano),
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, repairRec)
		})

		var planCalls atomic.Int64
		planDefinitionCalls.Store(&planCalls)
		t.Cleanup(func() { planDefinitionCalls.Store(nil) })

		claim, err := l.claimWorkerSettlement(context.Background(), repair.ItemID, req.RunID, at.Add(3*time.Second))
		if err != nil || !claim.claimed {
			t.Fatalf("first claimWorkerSettlement(-cfix): claimed=%t err=%v", claim.claimed, err)
		}
		claimAgain, err := l.claimWorkerSettlement(context.Background(), repair.ItemID, req.RunID, at.Add(3*time.Second))
		if err != nil {
			t.Fatalf("second claimWorkerSettlement(-cfix) error: %v", err)
		}
		if claimAgain.claimed {
			t.Fatal("expected second racing claimWorkerSettlement(-cfix) to return claimed=false")
		}

		timing := &PassTiming{TickID: claim.tickID, AtMS: at.Add(3 * time.Second).UnixMilli()}
		var result LoopResult
		out := l.settle(context.Background(), claim.work.Decision, repairRec, "", claim.work.BaseSHA, claim.decision.RunRequest.CorrelationID, at.Add(3*time.Second), &result, timing, nil, true)
		if out.err != nil {
			t.Fatalf("settle -cfix node: %v", out.err)
		}
		if got := planCalls.Load(); got != 0 {
			t.Fatalf("planDefinition called %d times for -cfix node, want 0", got)
		}

		// Verify candidate.ready was recorded for the -cfix run.
		rs, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rs.Close() }()
		var raw string
		if err := rs.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, candidateReadyKind, req.RunID).Scan(&raw)
		}); err != nil {
			t.Fatalf("expected %s event for -cfix run %s: %v", candidateReadyKind, req.RunID, err)
		}
		var cand Candidate
		if err := json.Unmarshal([]byte(raw), &cand); err != nil {
			t.Fatal(err)
		}
		if cand.RunID != req.RunID {
			t.Fatalf("candidate run_id = %q, want %q", cand.RunID, req.RunID)
		}
	})
}
