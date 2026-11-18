package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// stopClock is the injected "now" every test here judges heartbeats against.
var stopClock = time.UnixMilli(1_900_000_000_000).UTC()

func insertSession(t *testing.T, f refusalLoopFixture, id string, owner LoopOwner, nextPassAt time.Time) {
	t.Helper()
	raw, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner) VALUES (?, ?, ?, ?, ?)`,
			id, nextPassAt.Add(-time.Minute).UnixMilli(), nextPassAt.Add(-time.Second).UnixMilli(), nextPassAt.UnixMilli(), string(raw))
		return err
	})
}

func stopFlag(t *testing.T, f refusalLoopFixture, id string) int64 {
	t.Helper()
	var flag sql.NullInt64
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return tx.QueryRow(`SELECT stop_requested FROM drain_sessions WHERE session_id = ?`, id).Scan(&flag)
	})
	return flag.Int64
}

func foreignOwner(t *testing.T) LoopOwner {
	t.Helper()
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	other := *here
	other.Domain, other.PID = here.Domain+"-previous-jail", 12345
	return other
}

// localLoop is a live process in this domain standing in for a running loop.
func localLoop(t *testing.T) (LoopOwner, <-chan error) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	start, err := processStart(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	owner := *here
	owner.PID, owner.Start = cmd.Process.Pid, start
	return owner, exited
}

func claimRun(t *testing.T, f refusalLoopFixture, l *Loop) string {
	t.Helper()
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	return p.decision.RunRequest.RunID
}

func orphanedRuns(t *testing.T, f refusalLoopFixture) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		for _, w := range works {
			if w.Orphaned && w.Decision.RunRequest != nil {
				out[w.Decision.RunRequest.RunID] = true
			}
		}
		return err
	})
	return out
}

// A loop killed uncleanly in an earlier jail (another PID namespace) leaves an open
// session whose heartbeat has stopped. Stop leaves it alone, says so, and still
// stops the live local loop: a jail relaunch must not disable `tillr drain stop`.
func TestStopLeavesAStaleForeignSessionAndStopsTheLocalLoop(t *testing.T) {
	for _, end := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "end-runs"}[end], func(t *testing.T) {
			f, _ := passFixture(t, "item-a")
			insertSession(t, f, "dead-loop", foreignOwner(t), stopClock.Add(-time.Hour))
			owner, exited := localLoop(t)
			insertSession(t, f, "live-loop", owner, stopClock.Add(time.Second))
			report, err := Stop(context.Background(), StopConfig{Store: f.storePath, EndRuns: end, Now: func() time.Time { return stopClock }})
			if err != nil {
				t.Fatalf("stop refused: %v", err)
			}
			select {
			case <-exited:
			case <-time.After(hangGuard):
				t.Fatal("the live local loop was not signaled")
			}
			if stopFlag(t, f, "live-loop") == 0 {
				t.Fatal("the live local loop was not asked to stop")
			}
			if stopFlag(t, f, "dead-loop") != 0 {
				t.Fatal("a dead foreign session was flagged")
			}
			if !strings.Contains(strings.Join(report.Skipped, "\n"), "dead-loop") {
				t.Fatalf("the stale session was not reported: %q", report.Skipped)
			}
		})
	}
}

// A foreign session whose heartbeat is current is a live loop this domain cannot
// see or signal. The guard stays: it is not flagged and the stop says it failed.
// The local work is still done: --end-runs ends this domain's runs.
func TestStopNeverFlagsALiveForeignLoopButStillEndsLocalRuns(t *testing.T) {
	for _, end := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "end-runs"}[end], func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			id := claimRun(t, f, l)
			insertSession(t, f, "other-jail", foreignOwner(t), stopClock)
			_, err := Stop(context.Background(), StopConfig{Store: f.storePath, EndRuns: end, Now: func() time.Time { return stopClock }})
			if err == nil || !strings.Contains(err.Error(), "other-jail") {
				t.Fatalf("a live foreign loop was not reported as unstopped: %v", err)
			}
			if stopFlag(t, f, "other-jail") != 0 {
				t.Fatal("a live foreign loop was flagged")
			}
			if got := orphanedRuns(t, f)[id]; got != end {
				t.Fatalf("local run ended=%v, want %v", got, end)
			}
		})
	}
}

// One run owned by another domain does not stop EndRuns from ending the rest; it
// is reported and left exactly as it was.
func TestEndRunsEndsWhatItCanAndReportsAForeignRun(t *testing.T) {
	f, l := passFixture(t, "item-a")
	local := claimRun(t, f, l)
	foreignRun := "run-item-b-a1"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		work := *works["item-a"]
		req := *work.Decision.RunRequest
		req.RunID, req.ItemID = foreignRun, "item-b"
		work.Decision.ItemID, work.Decision.LaneID, work.Decision.RunRequest = "item-b", "lane-2", &req
		owner := foreignOwner(t)
		work.Owner = &owner
		return appendEffect(tx, dispatchStartedKind, foreignRun, work, l.cfg.Now())
	})
	skipped, err := EndRuns(context.Background(), f.storePath)
	if err != nil {
		t.Fatalf("one foreign run refused the whole call: %v", err)
	}
	ended := orphanedRuns(t, f)
	if !ended[local] || ended[foreignRun] {
		t.Fatalf("ended %v, want only %s", ended, local)
	}
	if !strings.Contains(strings.Join(skipped, "\n"), foreignRun) {
		t.Fatalf("the foreign run was not reported: %q", skipped)
	}
}
