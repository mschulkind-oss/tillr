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
	next := f.loop(t, &recordingSteps{}, &itemContentStager{})
	settlement := mustPlan(t, next)[0].decision
	if settlement.Action != ActionSettle {
		t.Fatalf("dead unbounded run did not settle: %+v", settlement)
	}
	// The original dispatch and the replacement's reap each applied a transition.
	// Recovering a dead owner must consume a generation even without a new dispatch.
	if got := storeRows(t, f.storePath)["drain_generations"]; got != 2 {
		t.Fatalf("dead-owner recovery allocated %d generations, want 2", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	var work *pendingWork
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		work = works["item-a"]
		rec, err := terminalRecord(r, p.decision.RunRequest.RunID)
		if err != nil {
			return err
		}
		if rec == nil {
			t.Fatal("recovery did not close the claim")
		}
		outcome := next.settle(context.Background(), work.Decision, *rec, "", work.BaseSHA, "tick", next.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, true)
		return outcome.err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := next.releaseExamination(context.Background(), p.decision); err != nil {
		t.Fatal(err)
	}
	d := mustPlan(t, next)[0].decision
	if d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop") {
		t.Fatalf("recovered no-commit run not re-admitted: %+v", d)
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
	if live, err := liveRunProcess(alias, p.decision.RunRequest.RunID); err != nil || !live {
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
