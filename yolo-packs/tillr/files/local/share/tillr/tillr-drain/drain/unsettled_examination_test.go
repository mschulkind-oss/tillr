package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestUnsettledExaminationHoldsItemAndDoesNotReclaimOrStarveDispatch(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	now := l.cfg.Now().UTC()
	ref := filepath.Join(f.root, "policy.json")
	pol := Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"build": "pi", "review": "reviewer"},
		Backends: map[string]Backend{
			"pi":       {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
			"reviewer": {Adapter: "reviewer", Model: "model-reviewer", TimeoutSeconds: 60},
		},
	}
	l.cfg.Policy, l.cfg.PolicyRef = savePolicyFile(t, ref, pol), ref

	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps

	base := runGit(t, f.root, "rev-parse", TrunkRef)
	wf := &Workflow{
		Name:    "eval-wf",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "verify", Kind: "agent", Seat: "review", Access: "read_only", JudgeClasses: []string{"verification-only"}},
		},
		Exit: &WorkflowExit{
			RequiresNodes: []string{"verify"},
			EvaluatorSlot: "verify",
		},
	}
	evalRunID := "run-item-a-a1-n1"
	evalReq := l.cfg.Policy.Request("item-a", "lane-1", evalRunID, "reviewer")
	evalReq.NodeID = "verify"
	evalReq.Workflow = "eval-wf@v1"
	evalDec := Decision{
		ItemID:           "item-a",
		Attempt:          1,
		Action:           ActionDispatch,
		Reason:           "ready",
		From:             "ready",
		To:               "running",
		LaneID:           "lane-1",
		Seat:             "review",
		Workflow:         wf,
		SelectedWorkflow: "eval-wf@v1",
		WorkflowSource:   "item",
		NodeIndex:        0,
		NodeBaseSHA:      base,
		RunRequest:       evalReq,
	}
	stampDecisionRequest(evalDec, evalReq)
	identifyDecision(&evalDec)

	// Stage lane-1 and write an evaluator receipt whose next verify attempt
	// (evalRunID-restart) failed to start on the unavailable reviewer backend,
	// while the first verify run (evalRunID) itself exited (so
	// settlementHeldByBackendOrWait does not catch evalRunID).
	retryRunID := evalRunID + "-restart"
	retryReq := *evalReq
	retryReq.RunID = retryRunID
	lanePath := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), base+"\n")
	if err := writeJudgeReceipt(filepath.Join(RunDir(f.root, evalRunID), judgeReceiptFile), &JudgeReceipt{
		Request: retryReq,
		Options: item.JudgeOptions{
			ItemID: "item-a",
			Head:   base,
		},
	}); err != nil {
		t.Fatalf("writeJudgeReceipt: %v", err)
	}
	// Ensure lane-1 checkout exists at base.
	if head := runGit(t, lanePath, "rev-parse", "HEAD"); head != base {
		t.Fatalf("lane-1 head = %s, want %s", head, base)
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat = 'build' WHERE item_id IN ('item-a', 'item-b')`); err != nil {
			return err
		}
		if err := recordWorkflowSelection(tx, evalDec); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome, seat)
			VALUES('item-a', 1, 'verify', ?, 'running', 'review')`, evalRunID); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, evalRunID, pendingWork{
			Decision: evalDec,
			BaseSHA:  base,
		}, now.Add(-2*time.Minute)); err != nil {
			return err
		}
		// First verify run exited normally; the next verify attempt failed to start
		// on reviewer, so judgeCandidate returns errJudgeBackendHeld.
		exitCode := 0
		if err := applyTerminal(tx, run.Record{
			RunID:      evalRunID,
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "reviewer",
			State:      run.StateExited,
			ExitCode:   &exitCode,
			StartedAt:  now.Add(-2 * time.Minute).Format(time.RFC3339),
			FinishedAt: now.Add(-90 * time.Second).Format(time.RFC3339),
		}); err != nil {
			return err
		}
		return applyTerminal(tx, run.Record{
			RunID:      retryRunID,
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "reviewer",
			State:      run.StateFailedToStart,
			StartedAt:  now.Add(-80 * time.Second).Format(time.RFC3339),
			FinishedAt: now.Add(-70 * time.Second).Format(time.RFC3339),
		})
	})
	// Close reviewer via a quota window so judgeCandidate returns errJudgeBackendHeld.
	reset := now.Add(time.Hour).Truncate(time.Second)
	quotaReport := filepath.Join(f.root, "quota.report")
	writeForkFile(t, quotaReport, fmt.Sprintf("# run report\nrate limit kind: weekly\nfraction: 1\nstatus: rejected\nreset: %d\n---\n", reset.Unix()))
	quotaRec := run.Record{
		RunID:        "run-quota-probe",
		ItemID:       "item-b",
		LaneID:       "lane-2",
		Backend:      "reviewer",
		State:        run.StateExited,
		ExitCode:     code(1),
		StartedAt:    now.Format(time.RFC3339),
		FinishedAt:   now.Format(time.RFC3339),
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: quotaReport}},
	}
	if err := l.recordBackendWindow(context.Background(), quotaRec); err != nil {
		t.Fatalf("recordBackendWindow: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		close(steps.release)
		for len(l.active) > 0 {
			c := <-l.completions
			delete(l.active, c.id)
		}
	}()

	// First scan finds the run and examines it.
	found, err := l.findExaminableRuns(ctx)
	if err != nil {
		t.Fatalf("findExaminableRuns(1): %v", err)
	}
	if len(found) != 1 || found[0].runID != evalRunID {
		t.Fatalf("findExaminableRuns(1) = %+v, want [%s]", found, evalRunID)
	}
	var result LoopResult
	l.examinations.Add(1)
	l.runCoordinatedExamination(ctx, "item-a", evalRunID, &result)

	// Drain the examination completion (runCoordinatedExamination ran synchronously above).
	var comp runCompletion
	select {
	case comp = <-l.completions:
	default:
		t.Fatal("expected completion from runCoordinatedExamination")
	}

	// Criterion 6: The item must show a hold naming why it could not be settled,
	// and the run must not be claimed again until something about it changes.
	var faultSeq int64
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, ok := faults["item-a"]
		if !ok {
			var evs []string
			rows, _ := r.Query(`SELECT kind, object_id, CAST(payload AS TEXT) FROM events ORDER BY seq`)
			if rows != nil {
				for rows.Next() {
					var k, id, p string
					_ = rows.Scan(&k, &id, &p)
					evs = append(evs, k+"("+id+")="+p)
				}
				rows.Close()
			}
			t.Fatalf("expected item-a to hold a fault after unsettled examination, got none; comp=%+v result=%+v events=%v", comp, comp.result, evs)
		}
		faultSeq = fault.Seq
		if fault.Reason != ReasonSettleFailed {
			t.Errorf("fault.Reason = %q, want %q", fault.Reason, ReasonSettleFailed)
		}
		if !strings.Contains(fault.Detail, "review backend") || !strings.Contains(fault.Detail, "reviewer") {
			t.Errorf("fault.Detail = %q, want reason naming why it could not be settled", fault.Detail)
		}
		var claimed int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, examinationClaimedKind, evalRunID).Scan(&claimed); err != nil {
			return err
		}
		if claimed != 1 {
			t.Errorf("examination-claimed count = %d, want 1", claimed)
		}
		return nil
	})

	// Item's planned decision must show the hold.
	held := planFor(t, l, "item-a")
	if held.Action != ActionHold || held.Reason != ReasonSettleFailed {
		t.Fatalf("planFor(item-a) = %+v, want ActionHold/%s", held, ReasonSettleFailed)
	}

	// Second scan must NOT find the run again, and a direct claim must refuse.
	found, err = l.findExaminableRuns(ctx)
	if err != nil {
		t.Fatalf("findExaminableRuns(2): %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("findExaminableRuns(2) = %+v, want empty until run changes", found)
	}
	claim, err := l.claimWorkerSettlement(ctx, "item-a", evalRunID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("claimWorkerSettlement(2): %v", err)
	}
	if claim.claimed {
		t.Fatal("expected unsettled run not to be claimed again until it changes")
	}

	// Criterion 5: Ready work (item-b) is planned and dispatched on following passes.
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatalf("pass: %v", err)
	}
	select {
	case req := <-steps.requests:
		if req.ItemID != "item-b" {
			t.Fatalf("dispatched %q, want item-b", req.ItemID)
		}
	case <-time.After(hangGuard):
		t.Fatal("ready item-b was not dispatched on following pass")
	}

	// Once something changes (clearing the fault), the run is examinable again.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return ClearItemFault(tx, "item-a", faultSeq, now.Add(2*time.Minute))
	})
	found, err = l.findExaminableRuns(ctx)
	if err != nil {
		t.Fatalf("findExaminableRuns(after clear): %v", err)
	}
	if len(found) != 1 || found[0].runID != evalRunID {
		t.Fatalf("findExaminableRuns(after clear) = %+v, want [%s]", found, evalRunID)
	}
}

func TestDrainCompletionsDoesNotPostponePlanningWhenCompletionsKeepArriving(t *testing.T) {
	f, l := passFixture(t, "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps

	baseNow := l.cfg.Now().UTC()
	var injected atomic.Int64
	var inPass atomic.Bool
	l.cfg.Now = func() time.Time {
		// Each time finishRunStep asks for Now() while draining completions,
		// push another completion onto l.completions to simulate completions
		// arriving faster than they are consumed.
		if inPass.Load() && len(l.completions) == 0 && injected.Load() < 5 {
			n := injected.Add(1)
			l.completions <- runCompletion{
				id:                     "run-churn",
				itemID:                 "item-missing",
				step:                   ActionSettle,
				fromExamineCoordinator: true,
			}
			_ = n
		}
		return baseNow
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		close(steps.release)
		for len(l.active) > 0 {
			c := <-l.completions
			delete(l.active, c.id)
		}
	}()

	// Seed one initial completion before the pass starts.
	l.completions <- runCompletion{
		id:                     "run-churn-0",
		itemID:                 "item-missing",
		step:                   ActionSettle,
		fromExamineCoordinator: true,
	}

	inPass.Store(true)
	var result LoopResult
	drained, stop, err := l.drainCompletionsCount(ctx, &result)
	inPass.Store(false)
	if err != nil || stop != "" {
		t.Fatalf("drainCompletionsCount: stop=%q err=%v", stop, err)
	}
	// Only the 1 completion already queued at entry should have been drained;
	// the newly arriving completion must wait for the next pass.
	if drained != 1 {
		t.Fatalf("drainCompletionsCount drained %d completions, want 1 (newly arriving completions must not postpone planning)", drained)
	}
	if len(l.completions) == 0 {
		t.Fatal("expected newly arrived completion to remain queued for the next pass")
	}

	// And running a full pass with completions continuously arriving still
	// reaches planning and dispatches ready item-b.
	inPass.Store(true)
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatalf("pass: %v", err)
	}
	inPass.Store(false)

	select {
	case req := <-steps.requests:
		if req.ItemID != "item-b" {
			t.Fatalf("dispatched %q, want item-b", req.ItemID)
		}
	case <-time.After(hangGuard):
		t.Fatal("ready item-b was not dispatched while completions kept arriving")
	}
	_ = f
}
