package drain

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type stallSteps struct {
	dirtyPathSteps
	twice  bool
	onGate func(Candidate) (GateOutcome, bool)
}

func (s *stallSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	if s.onGate != nil {
		if out, ok := s.onGate(c); ok {
			return out, nil
		}
	}
	if s.gates == 1 || s.twice {
		return GateOutcome{Outcome: GateStalled, StalledChecks: []StallObservation{{CheckID: "documents", LastOutputLine: "heartbeat", CPUTimeMS: int64(s.gates), SilentSamples: 60, DurationMS: 60000}}}, nil
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func stalledFixture(t *testing.T, twice bool) (refusalLoopFixture, *Loop, *stallSteps, Candidate) {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &stallSteps{dirtyPathSteps: dirtyPathSteps{t: t, root: f.root, store: f.storePath}, twice: twice}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["base.txt"]'`)
		return err
	})
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch: %v %v", ok, err)
	}
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "base.txt\n")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "candidate\n")
	runGit(t, lane, "add", "base.txt")
	runGit(t, lane, "commit", "-qm", "candidate")
	c := Candidate{ItemID: "item-a", RunID: p.decision.RunRequest.RunID, LaneID: "lane-1", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD"), Request: p.decision.RunRequest}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}})
	})
	if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	return f, l, steps, c
}

func TestGateStallRetainsCandidateAndRetriesAfterFewerGatesAcrossRestart(t *testing.T) {
	f, l, steps, c := stalledFixture(t, false)
	l.gatesInFlight = 2 // Two siblings plus this candidate at its stall.
	var result LoopResult
	_, _, err := l.Verify(context.Background(), c, &result, nil)
	if !errors.Is(err, errGateStallHeld) {
		t.Fatalf("first stall: %v", err)
	}
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	replacement.gatesInFlight = 3
	if d := mustPlan(t, replacement)[0].decision; d.Action != ActionHold || d.Reason != ReasonGateStallRerun {
		t.Fatalf("lost wait: %+v", d)
	}
	replacement.gatesInFlight = 2
	if d := mustPlan(t, replacement)[0].decision; d.Action != ActionJudge {
		t.Fatalf("did not reopen on first fewer-gate pass: %+v", d)
	}
	if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("retry: %s %v", stop, err)
	}
	drainOwned(t, replacement, &result)
	if steps.gates != 2 || steps.lands != 1 || result.Rejected != 0 {
		t.Fatalf("retry changed candidate or refused: gates=%d lands=%d result=%+v", steps.gates, steps.lands, result)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, dispatchStartedKind).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("worker dispatches %d", n)
		}
		return nil
	})
}

func TestSecondGateStallFaultNamesBothObservationsWithoutRefusal(t *testing.T) {
	f, l, steps, c := stalledFixture(t, true)
	var result LoopResult
	_, _, err := l.Verify(context.Background(), c, &result, nil)
	if !errors.Is(err, errGateStallHeld) {
		t.Fatalf("first stall %v", err)
	}
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("retry %s %v", stop, err)
	}
	drainOwned(t, replacement, &result)
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault := faults[c.ItemID]
		if fault.Reason != ReasonGateStalled || !strings.Contains(fault.Detail, "cpu_time_ms\":1") || !strings.Contains(fault.Detail, "cpu_time_ms\":2") {
			t.Errorf("fault: %+v", fault)
		}
		return nil
	})
	if steps.gates != 2 || steps.lands != 0 || result.Rejected != 0 {
		t.Fatalf("stall treated as failure: %+v", result)
	}
}

func TestRecordedSecondStallRecoversFaultWithoutAThirdGate(t *testing.T) {
	f, l, steps, c := stalledFixture(t, true)
	var result LoopResult
	if _, _, err := l.Verify(context.Background(), c, &result, nil); !errors.Is(err, errGateStallHeld) {
		t.Fatal(err)
	}
	// Crash after ProcessSteps recorded the second stalled verdict, before verify
	// could append the fault. The existing pending history must finish that effect.
	payload := map[string]any{"result": GateStalled, "checks": []StallObservation{{CheckID: "documents", Result: GateStalled, CPUTimeMS: 99, SilentSamples: 60}}}
	if err := appendJudgment(context.Background(), f.storePath, GateVerdictKind, "run", c.RunID, payload, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("recovery %s %v", stop, err)
	}
	drainOwned(t, replacement, &result)
	if steps.gates != 1 {
		t.Errorf("recorded second stall was gated again: %d", steps.gates)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err == nil && (faults[c.ItemID].Reason != ReasonGateStalled || !strings.Contains(faults[c.ItemID].Detail, "cpu_time_ms\":99")) {
			t.Errorf("lost second observation: %+v", faults)
		}
		return err
	})
}

func TestGateStalledFaultClearRetainsTheCandidateAndAttempt(t *testing.T) {
	f, l, steps, c := stalledFixture(t, true)
	var result LoopResult
	if _, _, err := l.Verify(context.Background(), c, &result, nil); !errors.Is(err, errGateStallHeld) {
		t.Fatal(err)
	}
	if _, _, err := l.Verify(context.Background(), c, &result, nil); !errors.Is(err, errItemFaultHeld) {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	d := mustPlan(t, replacement)[0].decision
	if d.Action != ActionJudge || d.Attempt != 1 || d.RunRequest.RunID != c.RunID {
		t.Fatalf("clear replaced attempt: %+v", d)
	}
	steps.twice = false
	if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("cleared retry: %s %v", stop, err)
	}
	drainOwned(t, replacement, &result)
	if steps.gates != 3 || steps.lands != 1 || result.Rejected != 0 {
		t.Fatalf("clear did not land retained candidate: gates=%d lands=%d %+v", steps.gates, steps.lands, result)
	}
}

func TestSecondStallUsesHeadTreeRatherThanCommitIdentity(t *testing.T) {
	f, l, _, c := stalledFixture(t, true)
	first := GateOutcome{Outcome: GateStalled, StalledChecks: []StallObservation{{CheckID: "documents", CPUTimeMS: 1}}}
	if err := l.holdGateStall(context.Background(), c, first, 2); !errors.Is(err, errGateStallHeld) {
		t.Fatal(err)
	}
	tree := runGit(t, c.Worktree, "rev-parse", c.HeadSHA+"^{tree}")
	c.HeadSHA = runGit(t, c.Worktree, "commit-tree", tree, "-p", c.BaseSHA, "-m", "same candidate tree, new commit")
	second := GateOutcome{Outcome: GateStalled, StalledChecks: []StallObservation{{CheckID: "documents", CPUTimeMS: 2}}}
	if err := l.holdGateStall(context.Background(), c, second, 1); !errors.Is(err, errItemFaultHeld) {
		t.Fatalf("same tree treated as new candidate: %v", err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err == nil && faults[c.ItemID].Reason != ReasonGateStalled {
			t.Errorf("missing second-stall fault: %+v", faults)
		}
		return err
	})
}

func TestGateStalledRetrySupersedesAndRebuildsOnCurrentTrunk(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c := stalledFixture(t, true)
	var gatedTrees []string
	var sawTrunkFix bool
	steps.onGate = func(cand Candidate) (GateOutcome, bool) {
		tree := runGit(t, cand.Worktree, "rev-parse", cand.HeadSHA+"^{tree}")
		gatedTrees = append(gatedTrees, tree)
		listed := runGit(t, cand.Worktree, "ls-tree", "--name-only", cand.HeadSHA)
		if strings.Contains(listed, "trunk-fix.txt") {
			sawTrunkFix = true
			return GateOutcome{Outcome: GatePassed}, true
		}
		return GateOutcome{
			Outcome: GateStalled,
			StalledChecks: []StallObservation{{
				CheckID:        "documents",
				LastOutputLine: "heartbeat",
				CPUTimeMS:      int64(steps.gates),
				SilentSamples:  60,
				DurationMS:     60000,
			}},
		}, true
	}

	// Two stalls on the old base hold the item as gate-stalled, with gate.verdict recorded
	// (as ProcessSteps.Gate does in production).
	oldTree := runGit(t, c.Worktree, "rev-parse", c.HeadSHA+"^{tree}")
	var result LoopResult
	if _, _, err := l.Verify(ctx, c, &result, nil); !errors.Is(err, errGateStallHeld) {
		t.Fatalf("first stall: %v", err)
	}
	if _, _, err := l.Verify(ctx, c, &result, nil); !errors.Is(err, errItemFaultHeld) {
		t.Fatalf("second stall: %v", err)
	}
	stalledVerdict := map[string]any{
		"result":   GateStalled,
		"tree_sha": oldTree,
		"checks":   []StallObservation{{CheckID: "documents", Result: GateStalled, CPUTimeMS: 1, SilentSamples: 60}},
	}
	if err := appendJudgment(ctx, f.storePath, GateVerdictKind, "run", c.RunID, stalledVerdict, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}

	// Trunk advances with a newer file (simulating a gate script fix landed after dispatch).
	writeForkFile(t, filepath.Join(f.root, "trunk-fix.txt"), "gate script fix\n")
	runGit(t, f.root, "add", "trunk-fix.txt")
	runGit(t, f.root, "commit", "-qm", "fix: repair gate script")
	newTrunk := runGit(t, f.root, "rev-parse", TrunkRef)

	// One designed act, recorded with who and why, acknowledges the stalled verdict and
	// authorizes superseding the stall-faulted candidate.
	var retry Retry
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var err error
		retry, err = AuthorizeRetry(ctx, tx, RetryRequest{
			ItemID:      c.ItemID,
			RunID:       c.RunID,
			By:          "operator",
			Why:         "rebuild on trunk after gate script fix",
			At:          l.cfg.Now(),
			Acknowledge: RetryAckGateFailed,
		})
		return err
	})
	fixtureRead(t, f, func(r *store.Reader) error {
		var by, why string
		if err := r.QueryRow(`SELECT requested_by, reason FROM retries WHERE retry_id=?`, retry.RetryID).Scan(&by, &why); err != nil {
			return err
		}
		if by != "operator" || why != "rebuild on trunk after gate script fix" {
			t.Errorf("retry attribution = (%q, %q)", by, why)
		}
		return nil
	})

	// On the next pass, supersedeRetriedCandidates pins the stalled candidate, resets the
	// lane to the current trunk, clears gate-stalled, and dispatches attempt 2 on newTrunk.
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	if err := replacement.supersedeRetriedCandidates(ctx); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if got := refSHA(t, f.root, "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("superseded candidate pin = %q, want %s", got, c.HeadSHA)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != newTrunk {
		t.Fatalf("lane HEAD = %s, want current trunk %s", got, newTrunk)
	}
	p := mustPlan(t, replacement)[0]
	if p.decision.Action != ActionRetry || p.decision.Attempt != 2 || p.decision.RetryID != retry.RetryID || p.decision.SupersedesRunID != c.RunID {
		t.Fatalf("plan after supersession = %+v, want ActionRetry attempt 2", p.decision)
	}
	if ok, _, err := replacement.applyPlan(ctx, p, newTrunk, replacement.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch retry: %v %v", ok, err)
	}

	// Attempt 2 builds on newTrunk (so its tree contains trunk-fix.txt).
	lane := replacement.lanePath("lane-1")
	writeForkFile(t, filepath.Join(replacement.laneDirPath("lane-1"), FenceFile), "base.txt\n")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "rebuilt candidate\n")
	runGit(t, lane, "add", "base.txt")
	runGit(t, lane, "commit", "-qm", "rebuilt candidate")
	rebuilt := Candidate{
		ItemID:   c.ItemID,
		RunID:    p.decision.RunRequest.RunID,
		LaneID:   "lane-1",
		Worktree: lane,
		BaseSHA:  newTrunk,
		HeadSHA:  runGit(t, lane, "rev-parse", "HEAD"),
		Request:  p.decision.RunRequest,
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:        rebuilt.RunID,
			ItemID:       rebuilt.ItemID,
			LaneID:       rebuilt.LaneID,
			Backend:      "fixture",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    replacement.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			FinishedAt:   replacement.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			ArtifactRefs: []run.ArtifactRef{},
		})
	})
	if err := appendJudgment(ctx, f.storePath, candidateReadyKind, "run", rebuilt.RunID, rebuilt, replacement.cfg.Now()); err != nil {
		t.Fatal(err)
	}

	if _, _, err := replacement.Verify(ctx, rebuilt, &result, nil); err != nil {
		t.Fatalf("verify rebuilt candidate: %v", err)
	}
	if !sawTrunkFix {
		t.Fatalf("next gate did not run on a tree containing trunk-fix.txt; gated trees=%v", gatedTrees)
	}
	if steps.gates != 3 || steps.lands != 1 {
		t.Fatalf("gates=%d lands=%d, want 3 gates (2 stalled + 1 pass) and 1 land", steps.gates, steps.lands)
	}
}
