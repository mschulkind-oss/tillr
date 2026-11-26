package drain

import (
	"context"
	"os/exec"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// deadOwner is the identity of a process in this domain that has exited.
func deadOwner(t *testing.T) LoopOwner {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	start, err := processStart(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	owner := *here
	owner.PID, owner.Start = cmd.Process.Pid, start
	return owner
}

// A pass reaps a dead loop's claim from one process-table scan taken before its
// write transaction, not from a scan per run (and per examiner) inside it. The
// outcome is unchanged: the dead examiner's lease on the lane is released.
func TestAPassReapsDeadClaimsFromOneScanOutsideTheWriter(t *testing.T) {
	f, l := passFixture(t, "item-a")
	id := claimRun(t, f, l)
	dead := deadOwner(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		work := *works["item-a"]
		work.Owner = &dead
		if err := appendEffect(tx, dispatchStartedKind, id, work, l.cfg.Now()); err != nil {
			return err
		}
		return appendEffect(tx, examinationOwnerKind, id, &dead, l.cfg.Now())
	})
	before := procScans.Load()
	if _, _, err := l.passPlans(context.Background(), l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	if n := procScans.Load() - before; n != 1 {
		t.Fatalf("a pass scanned the process table %d time(s), want 1", n)
	}
	var state string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return tx.QueryRow(`SELECT state FROM lanes WHERE run_id IS NULL AND lane_id = 'lane-1'`).Scan(&state)
	})
	if state == "" {
		t.Fatal("the dead examiner's lease was not released")
	}
	// Inside the writer, the reap only reads the evidence it was handed.
	before = procScans.Load()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return l.reapDeadLoopRuns(context.Background(), tx, l.cfg.Now(), reapEvidence{Dead: map[LoopOwner]bool{dead: true}, Live: map[string]bool{}})
	})
	if n := procScans.Load() - before; n != 0 {
		t.Fatalf("the reap scanned the process table %d time(s) inside the writer", n)
	}
}
