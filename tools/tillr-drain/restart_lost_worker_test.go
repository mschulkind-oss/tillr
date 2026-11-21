package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestLandingRollRecoversLostWorker(t *testing.T) {
	for _, mode := range []string{"redispatch", "stop"} {
		t.Run(mode, func(t *testing.T) {
			testLandingRollLostWorker(t, mode)
		})
	}
}

func testLandingRollLostWorker(t *testing.T, mode string) {
	t.Helper()
	dir := scratch(t, "roll-lost-worker")
	pids, faces, fifo := filepath.Join(dir, "pids"), filepath.Join(dir, "faces"), filepath.Join(dir, "hold")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKER_PIDS", pids)
	t.Setenv("FACE_PIDS", faces)
	t.Setenv("HOLD_FIFO", fifo)
	t.Setenv("REAL_RUN_BIN", sibling(t, "run"))
	wrapper := stub(t, "run-face", `if [ "$1" = exec ]; then echo "$$ $TILLR_RUN_ID" >> "$FACE_PIDS"; fi
exec "$REAL_RUN_BIN" "$@"`)
	adapter := stub(t, "adapter", `if [ "$TILLR_ITEM_ID" = item_041 ]; then
    exec 3<>"$HOLD_FIFO"
    echo "$$ $TILLR_RUN_ID" >> "$WORKER_PIDS"
    read -r line <&3
    exit 1
fi
while [ ! -s "$WORKER_PIDS" ]; do sleep 0.02; done
`+committingAdapter)
	land := stub(t, "land", `while [ "$#" -gt 1 ] && [ "$1" != --head ]; do shift; done
[ "$1" = --head ] || exit 0
cd "$FIXTURE_ROOT" || exit 5
if ! git cherry-pick "$2" >&2; then exit 5; fi
printf '{"record_version":1,"kind":"land.record","id":"land-1","producer":{"system":"tillr","utility":"land","version":"0.1.0"},"created_at":"2026-11-21T00:00:00Z","payload":{"trunk_after":"%s"}}\n' "$(git rev-parse HEAD)"`)
	f := newLoopFixture(t, adapter)
	t.Setenv("FIXTURE_ROOT", f.root)
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "other.txt"), fencedItemRecord("item_042", "in-queue", "candidate.txt"))
	fixtureTerminalEvidence(t, f.store, "item_042")
	s := &rollScenario{f: f, exe: filepath.Join(dir, "tillr-drain"), out: filepath.Join(dir, "loop.out")}
	copyFile(t, bin, s.exe, nil)
	copyFile(t, bin, s.exe+".next", []byte("\n# post-landing build\n"))
	t.Setenv("ROLL_BINARY", s.exe)
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[roll]\npaths = ['candidate.txt']\ncommand = 'mv \"$ROLL_BINARY.next\" \"$ROLL_BINARY\"'\n")
	git(t, f.root, "add", ".tillr.toml")
	git(t, f.root, "commit", "-m", "configure roll")
	policy := filepath.Join(dir, "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	out, err := os.Create(s.out)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	s.cmd = exec.Command(s.exe, "loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", "2", "--policy", policy, "--run-bin", wrapper, "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"), "--rolling=", "--gate", always(t, "gate", 0), "--land", land, "--verdict", always(t, "verdict", 0))
	s.cmd.Stdout, s.cmd.Stderr = out, out
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- s.cmd.Wait() }()
	defer s.cmd.Process.Kill()
	t.Cleanup(func() { exec.Command(bin, "stop", "--end-runs", "--store", f.store, "--root", f.root).Run() })
	s.waitFor(t, "in-place restart after the matching landing", func() bool {
		return strings.Contains(s.output(t), "restarted in place onto build")
	})
	// Adoption reports before registering its session; wait for registration so
	// stop targets the new session, not the just-ended one.
	db, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.waitFor(t, "replacement session registration", func() bool {
		var registered bool
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM drain_sessions WHERE ended_at IS NULL)`).Scan(&registered)
		}); err != nil {
			t.Fatal(err)
		}
		return registered
	})
	first := strings.Fields(lines(t, pids)[0])
	worker, err := strconv.Atoi(first[0])
	if err != nil || !alive(worker) {
		t.Fatalf("the original worker did not survive exec: %v", first)
	}
	face := 0
	for _, line := range lines(t, faces) {
		fields := strings.Fields(line)
		if fields[1] == first[1] {
			face, _ = strconv.Atoi(fields[0])
		}
	}
	if face <= 0 || !alive(face) {
		t.Fatalf("the original run face did not survive exec: %d", face)
	}
	// Freeze planning, not the worker: this lets why observe the missing processes
	// before recovery can change the store. Kill both fixture PIDs with no finish record.
	// Take the writer while freezing so the loop cannot be stopped mid-write,
	// which would prevent the stop CLI from recording its request.
	if err := db.WriteTx(context.Background(), func(*store.Tx) error {
		if err := s.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
			return err
		}
		s.waitFor(t, "loop to stop for the observation", func() bool {
			return strings.HasPrefix(procStatus(t, s.cmd.Process.Pid, "State"), "T")
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{face, worker} {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
	}
	s.waitFor(t, "both fixture processes to go away or become zombies", func() bool {
		for _, pid := range []int{face, worker} {
			start, err := presence.ProcessStart(pid)
			if err != nil && !presence.ProcessGone(err) {
				t.Fatal(err)
			}
			if start != "" {
				return false
			}
		}
		return true
	})
	why, err := exec.Command(bin, "why", "item_041", "--json", "--root", f.root, "--store", f.store, "--policy", policy).CombinedOutput()
	if err != nil {
		t.Fatalf("why: %v: %s", err, why)
	}
	var row drain.WhyRow
	if err := json.Unmarshal(why, &row); err != nil {
		t.Fatalf("why row: %v: %s", err, why)
	}
	if row.ItemID != "item_041" || row.Position != "held" || row.Reason != "run-awaits-recovery" || row.Decision.RunRequest == nil || row.Decision.RunRequest.RunID != first[1] {
		t.Errorf("why did not show the original run awaiting recovery: %s", why)
	}
	if mode == "stop" {
		// Queue SIGTERM while the replacement cannot dispatch; the CLI then waits
		// for the session to end, and we also require the OS process to exit.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, bin, "stop", "--root", f.root, "--store", f.store)
		stopDone := make(chan error, 1)
		go func() {
			raw, err := stop.CombinedOutput()
			if err != nil {
				err = fmt.Errorf("stop: %w: %s", err, raw)
			}
			stopDone <- err
		}()
		s.waitFor(t, "stop's pending SIGTERM", func() bool {
			if ctx.Err() != nil {
				t.Fatalf("stop did not reach the replacement loop: %v", ctx.Err())
			}
			bits, _ := strconv.ParseUint(procStatus(t, s.cmd.Process.Pid, "ShdPnd"), 16, 64)
			return bits&(1<<(syscall.SIGTERM-1)) != 0
		})
		if err := s.cmd.Process.Signal(syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
		if err := <-stopDone; err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-exited:
			if err != nil {
				t.Fatalf("loop: %v\n%s", err, s.output(t))
			}
		case <-ctx.Done():
			t.Fatalf("stop returned but left the loop running\n%s", s.output(t))
		}
		return
	}
	if err := s.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	s.waitFor(t, "replacement worker after the run vanished", func() bool { return len(lines(t, pids)) > 1 })
	second := strings.Fields(lines(t, pids)[1])
	pid, err := strconv.Atoi(second[0])
	if err != nil || !alive(pid) || second[1] == first[1] {
		t.Fatalf("expected a new live run, got %v after %v", second, first)
	}
	var cause string
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT end_cause FROM runs WHERE run_id = ?`, first[1]).Scan(&cause)
	}); err != nil || cause != run.EndLeaseReclaimed {
		t.Fatalf("the original run was not reclaimed: cause %q, error %v", cause, err)
	}
}

func procStatus(t *testing.T, pid int, field string) string {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if val, ok := strings.CutPrefix(line, field+":"); ok {
			return strings.TrimSpace(val)
		}
	}
	t.Fatalf("process %d has no %s status field", pid, field)
	return ""
}
