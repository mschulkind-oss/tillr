package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func deadOwnerClaim(t *testing.T, f refusalLoopFixture, l *Loop, p plannedApplication) {
	t.Helper()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	owner := *work.Owner
	owner.PID = 2147483647
	work.Owner = &owner
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, p.decision.RunRequest.RunID, work, l.cfg.Now())
	})
}

func TestReplacementRecoversUnboundedDeadLoopWithoutTerminalRecord(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	if p.decision.RunRequest.TimeoutMS != 0 {
		t.Fatal("test must use an unbounded run")
	}
	deadOwnerClaim(t, f, l, p)
	if _, err := pipelineStart(f.root, p.decision.RunRequest.RunID, "graph"); err != nil {
		t.Fatal(err)
	}
	next := f.loop(t, &recordingSteps{}, &itemContentStager{})
	d := mustPlan(t, next)[0].decision
	if d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop") {
		t.Fatalf("dead unbounded run did not recover and re-dispatch in Pass 1: %+v", d)
	}
	// The original dispatch and the replacement's reap each applied a transition.
	// Recovering a dead owner must consume a generation even without a new dispatch.
	if got := storeRows(t, f.storePath)["drain_generations"]; got != 2 {
		t.Fatalf("dead-owner recovery allocated %d generations, want 2", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rec, err := terminalRecord(r, p.decision.RunRequest.RunID)
		if err != nil {
			return err
		}
		if rec == nil {
			t.Fatal("recovery did not close the claim")
		}
		if rec.SpendStatus != "unreported" || !strings.Contains(rec.SpendNote, "no terminal record") {
			t.Fatalf("reaped face has no spend reason: %+v", rec)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := os.ReadFile(filepath.Join(RunDir(f.root, p.decision.RunRequest.RunID), "pipeline.jsonl"))
	if readErr != nil || strings.Count(string(raw), "\n") != 2 || strings.Count(string(raw), `"finished_at_ms":null`) != 1 {
		t.Fatalf("replacement did not close open graph: %s %v", raw, readErr)
	}
	var measured LoopResult
	if err := next.snapshotSpend(&measured); err != nil {
		t.Fatal(err)
	}
	if measured.RecordedSpend.Unreported != 1 || measured.RecordedSpend.CostUnreported != 1 {
		t.Fatalf("reaped run not counted unreported: %+v", measured.RecordedSpend)
	}
}

func TestThreeDeadSupervisorsDoNotSpendAnyAttemptBudget(t *testing.T) {
	f, l := passFixture(t, "item-a")
	for attempt := 1; attempt <= 3; attempt++ {
		p := mustPlan(t, l)[0]
		if p.decision.Action != ActionDispatch || p.decision.Attempt != attempt || p.decision.Readmitted || p.decision.RetryID != "" {
			t.Fatalf("dispatch %d: %+v", attempt, p.decision)
		}
		deadOwnerClaim(t, f, l, p)
		l = f.loop(t, &recordingSteps{}, &itemContentStager{})
	}
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionDispatch || d.Attempt != 4 || d.Readmitted || d.RetryID != "" || d.Reason == ReasonFailover {
		t.Fatalf("fourth crash dispatch spent a budget: %+v", d)
	}
}

func TestDeadExaminationReleasesOnlyItsLeaseAndKeepsTheTerminalResult(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	owner, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	owner.PID = 2147483647
	fixtureWrite(t, f, func(tx *store.Tx) error {
		id := p.decision.RunRequest.RunID
		rec := run.Record{RunID: id, ItemID: "item-a", LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationOwnerKind, id, owner, l.cfg.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, id, p.decision.LaneID)
		return err
	})
	next := f.loop(t, &recordingSteps{}, &itemContentStager{})
	if d := mustPlan(t, next)[0].decision; d.Action != ActionSettle {
		t.Fatalf("dead examination blocked replacement: %+v", d)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if works["item-a"].Orphaned || works["item-a"].Examiner != nil {
			t.Fatal("examination recovery discarded the result or retained its owner")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeadOwnerDoesNotExemptAnIndependentlySignaledTerminalRun(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: p.decision.RunRequest.RunID, ItemID: "item-a", LaneID: p.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(-1), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndSignal})
	})
	_ = mustPlan(t, f.loop(t, &recordingSteps{}, &itemContentStager{}))
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		interrupted, _, err := readInterruptedRuns(r)
		if err != nil {
			return err
		}
		if interrupted[p.decision.RunRequest.RunID] {
			t.Fatal("dead supervisor was treated as proof it orphaned the terminal EndSignal run")
		}
		priors, err := readPriorRuns(r)
		if err != nil {
			return err
		}
		if priors["item-a"][0].Class != AttemptInterrupted {
			t.Fatalf("EndSignal class = %q, want %q", priors["item-a"][0].Class, AttemptInterrupted)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	f2, l2 := passFixture(t, "item-a")
	p2 := mustPlan(t, l2)[0]
	deadOwnerClaim(t, f2, l2, p2)
	fixtureWrite(t, f2, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: p2.decision.RunRequest.RunID, ItemID: "item-a", LaneID: p2.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(-1), StartedAt: l2.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l2.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}})
	})
	_ = mustPlan(t, f2.loop(t, &recordingSteps{}, &itemContentStager{}))
	s2 := openFixture(t, f2.storePath)
	defer s2.Close()
	if err := s2.ReadTx(context.Background(), func(r *store.Reader) error {
		interrupted, _, err := readInterruptedRuns(r)
		if err != nil {
			return err
		}
		if interrupted[p2.decision.RunRequest.RunID] {
			t.Fatal("dead supervisor was treated as proof it orphaned the terminal crashed run")
		}
		priors, err := readPriorRuns(r)
		if err != nil {
			return err
		}
		if priors["item-a"][0].Class != AttemptInfrastructure {
			t.Fatalf("crashed run class = %q, want %q", priors["item-a"][0].Class, AttemptInfrastructure)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDeadLoopNeverReclaimsASurvivingWorker(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	cmd := exec.Command("sleep", "60")
	cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+p.decision.RunRequest.RunID, "TILLR_LOOP_STORE="+l.cfg.Store)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	alias := filepath.Join(f.root, "store-alias.sqlite3")
	if err := os.Symlink(f.storePath, alias); err != nil {
		t.Fatal(err)
	}
	if live, err := liveRunIDs(alias); err != nil || !live[p.decision.RunRequest.RunID] {
		t.Fatalf("store alias hid surviving worker: %v %v", live, err)
	}
	replacement := f.loop(t, &recordingSteps{}, &itemContentStager{})
	if d := mustPlan(t, replacement)[0].decision; d.Action != ActionHold || d.Reason != "run-live" {
		t.Fatalf("live worker reclaimed: %+v", d)
	}
}

func TestProcessExitDuringScanIsNotARecoveryFault(t *testing.T) {
	for _, err := range []error{os.ErrNotExist, syscall.ESRCH, fmt.Errorf("reading environment: %w", syscall.ESRCH)} {
		if !processGone(err) {
			t.Fatalf("exited process treated as fault: %v", err)
		}
	}
	if processGone(os.ErrPermission) {
		t.Fatal("relevant permission refusal swallowed")
	}
}

func TestProcessScanSkipsForeignUIDBeforeReadingPrivateEnvironment(t *testing.T) {
	// This directory deliberately has no readable environment file. A different
	// UID's process is excluded before attempting that read, even as root.
	dir := scratchUnderDurable(t)
	if env, err := runProcessEnvironment(dir, uint32(os.Geteuid()+1)); err != nil || env != nil {
		t.Fatalf("foreign process inspected: %q %v", env, err)
	}
	if _, err := runProcessEnvironment(dir, uint32(os.Geteuid())); err == nil {
		t.Fatal("relevant unreadable process did not fail closed")
	}
}

func TestOwnerIdentityDoesNotMistakeALiveOrForeignProcessForDead(t *testing.T) {
	owner, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	if dead, err := ownerDead(owner, owner); err != nil || dead {
		t.Fatalf("live owner: %v %v", dead, err)
	}
	other := *owner
	other.Domain = "another namespace"
	if dead, err := ownerDead(&other, owner); err != nil || dead {
		t.Fatalf("foreign owner: %v %v", dead, err)
	}
	otherUID := *owner
	otherUID.UID++
	if dead, err := ownerDead(&otherUID, owner); err != nil || dead {
		t.Fatalf("foreign UID owner: %v %v", dead, err)
	}
	reused := *owner
	reused.Start = "different process"
	if dead, err := ownerDead(&reused, owner); err != nil || !dead {
		t.Fatalf("PID reuse: %v %v", dead, err)
	}
}
