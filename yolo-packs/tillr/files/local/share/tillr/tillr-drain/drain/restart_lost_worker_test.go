package drain_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-drain/restart"
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

func TestLandingRollKeepsLiveWorkerAndDispatchesFreeLane(t *testing.T) {
	testLandingRollLostWorker(t, "live")
}

func TestLandingRollMigratesSchemaAndKeepsLiveWorker(t *testing.T) {
	testLandingRollLostWorker(t, "schema")
}

func TestLandingRollWithFailedStoreMigrationAlarmsAndKeepsOldBuild(t *testing.T) {
	t.Run("store event", func(t *testing.T) { testLandingRollLostWorker(t, "schema-blocked") })
	t.Run("store cannot log alarm", func(t *testing.T) { testLandingRollLostWorker(t, "schema-unlogged") })
}

func testLandingRollLostWorker(t *testing.T, mode string) {
	t.Helper()
	schemaRoll := strings.HasPrefix(mode, "schema")
	blockedSchema := schemaRoll && mode != "schema"
	dir := scratch(t, "roll-lost-worker")
	pids, faces, fifo := filepath.Join(dir, "pids"), filepath.Join(dir, "faces"), filepath.Join(dir, "hold")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKER_PIDS", pids)
	t.Setenv("FACE_PIDS", faces)
	t.Setenv("HOLD_FIFO", fifo)
	runBin, itemBin := sibling(t, "run"), sibling(t, "item")
	if schemaRoll {
		for _, binary := range []*string{&runBin, &itemBin} {
			live := filepath.Join(dir, filepath.Base(*binary))
			copyFile(t, *binary, live, nil)
			*binary = live
		}
	}
	t.Setenv("REAL_RUN_BIN", runBin)
	wrapper := stub(t, "run-face", `if [ "$1" = exec ]; then echo "$$ $TILLR_RUN_ID" >> "$FACE_PIDS"; fi
exec "$REAL_RUN_BIN" "$@"`)
	adapter := stub(t, "adapter", `if [ "$TILLR_ITEM_ID" = item_041 ]; then
    exec 3<>"$HOLD_FIFO"
    echo "$$ $TILLR_RUN_ID" >> "$WORKER_PIDS"
    read -r line <&3
    [ "$line" = finish ] || exit 1
    echo done > other.txt
    git add other.txt
    git commit -qm 'finish original work'
    printf 'RESULT: done\n'
    exit 0
fi
while [ ! -s "$WORKER_PIDS" ]; do sleep 0.02; done
`+committingAdapter)
	if mode == "live" || mode == "schema" {
		writeFile(t, filepath.Join(filepath.Dir(adapter), "pause-kind"), run.PauseAtGap+"\n")
	}
	land := stub(t, "land", `while [ "$#" -gt 1 ] && [ "$1" != --head ]; do shift; done
[ "$1" = --head ] || exit 0
cd "$FIXTURE_ROOT" || exit 5
if ! git cherry-pick "$2" >&2; then exit 5; fi
printf '{"record_version":1,"kind":"land.record","id":"land-1","producer":{"system":"tillr","utility":"land","version":"0.1.0"},"created_at":"2026-11-21T00:00:00Z","payload":{"trunk_after":"%s"}}\n' "$(git rev-parse HEAD)"`)
	f := newLoopFixture(t, adapter)
	if schemaRoll {
		f.store = filepath.Join(f.root, ".tillr", "state.db")
	}
	t.Setenv("FIXTURE_ROOT", f.root)
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "other.txt"), fencedItemRecord("item_042", "in-queue", "candidate.txt"))
	fixtureTerminalEvidence(t, f.store, "item_042")
	if blockedSchema {
		blockSchemaMigration(t, f.store, mode == "schema-unlogged")
	}
	if mode == "live" || mode == "schema" {
		// Observe even a lease change that does not emit a run.release event.
		ctx := context.Background()
		db, err := store.Open(ctx, f.store, true)
		if err != nil {
			t.Fatal(err)
		}
		err = db.WriteTx(ctx, func(tx *store.Tx) error {
			if _, err := tx.Exec(`CREATE TABLE fixture_lease_releases (run_id TEXT)`); err != nil {
				return err
			}
			_, err := tx.Exec(`CREATE TRIGGER fixture_watch_lease AFTER UPDATE ON lanes
				WHEN OLD.state='leased' AND (NEW.state<>'leased' OR NEW.run_id IS NOT OLD.run_id)
				BEGIN INSERT INTO fixture_lease_releases VALUES (OLD.run_id); END`)
			return err
		})
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := &rollScenario{f: f, exe: filepath.Join(dir, "tillr-drain"), out: filepath.Join(dir, "loop.out")}
	copyFile(t, drainBin(t), s.exe, nil)
	t.Setenv("ROLL_BINARY", s.exe)
	command := `mv "$ROLL_BINARY.next" "$ROLL_BINARY"`
	if schemaRoll {
		command = schemaRollFixture(t, f, s.exe)
	} else {
		copyFile(t, drainBin(t), s.exe+".next", []byte("\n# post-landing build\n"))
	}
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[roll]\npaths = ['candidate.txt']\ncommand = '"+command+"'\n")
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
	s.cmd = exec.Command(s.exe, "loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", "2", "--policy", policy, "--run-bin", wrapper, "--item-bin", itemBin, "--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), "--rolling=", "--gate", always(t, "gate", 0), "--land", land, "--verdict", always(t, "verdict", 0))
	s.cmd.Stdout, s.cmd.Stderr = out, out
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- s.cmd.Wait() }()
	defer s.cmd.Process.Kill()
	t.Cleanup(func() { exec.Command(s.exe, "stop", "--end-runs", "--store", f.store, "--root", f.root).Run() })
	if blockedSchema {
		if err := waitForStoreRollAlarm(func() string { return s.output(t) }, exited); err != nil {
			t.Fatal(err)
		}
		assertBlockedSchemaRoll(t, s, mode == "schema-unlogged")
		return
	}
	s.waitFor(t, "in-place restart after the matching landing", func() bool {
		out := s.output(t)
		if mode == "schema" && strings.Contains(out, "post-landing roll at") && strings.Contains(out, "failed:") {
			t.Fatalf("automatic roll failed:\n%s", out)
		}
		return strings.Contains(out, "restarted in place onto build")
	})
	// Adoption reports before the new session and lanes are ready. Use the
	// first pass's sync as the barrier before attempting the stop protocol.
	s.waitFor(t, "the replacement loop's first pass", func() bool {
		_, after, _ := strings.Cut(s.output(t), "restarted in place onto build")
		return strings.Contains(after, "loop: synced documents at ")
	})
	db, err := store.Open(context.Background(), f.store, mode != "schema")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if mode == "schema" {
		version, err := db.SchemaVersion()
		if err != nil || version != store.SchemaVersion+1 {
			t.Fatalf("schema after roll: %d, %v", version, err)
		}
		if file, err := os.ReadFile(filepath.Join(filepath.Dir(s.exe), "tillr-view")); err != nil || string(file) != "#!/bin/sh\nexit 0\n" {
			t.Fatalf("sibling was not installed after migration: %q, %v", file, err)
		}
		if file, err := os.ReadFile(filepath.Join(filepath.Dir(s.exe), "tillr-watch")); err != nil || string(file) != "#!/bin/sh\nexit 0\n" {
			t.Fatalf("watch was not installed after migration: %q, %v", file, err)
		}
		if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
			var exists bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='fixture_roll_upgrade')`).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				t.Error("replacement did not install the new schema's DDL")
			}
			build, err := restart.ReadBuild(r, restart.RoleDrain)
			if err != nil {
				return err
			}
			if build == nil || build.PID != s.cmd.Process.Pid || build.Digest != drain.DiskDigest(s.exe) || build.Digest == drain.DiskDigest(drainBin(t)) {
				t.Errorf("new build did not take over in place: %+v", build)
			}
			view, err := restart.ReadState(r, restart.RoleView)
			if err == nil && len(view.Pending) != 1 {
				t.Errorf("view should receive one deferred restart request: %+v", view)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
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
	if mode == "live" || mode == "schema" {
		assertLiveRoll(t, s, db, first[1], fifo)
		return
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
	why, err := exec.Command(drainBin(t), "why", "item_041", "--json", "--root", f.root, "--store", f.store, "--policy", policy).CombinedOutput()
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
		stop := exec.CommandContext(ctx, drainBin(t), "stop", "--root", f.root, "--store", f.store)
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

func blockSchemaMigration(t *testing.T, path string, refuseEvent bool) {
	t.Helper()
	db, err := store.Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(fmt.Sprintf(`CREATE TRIGGER fixture_block_schema BEFORE INSERT ON schema_migrations
			WHEN NEW.version > %d BEGIN SELECT RAISE(ABORT, 'fixture refuses new schema'); END`, store.SchemaVersion))
		if err == nil && refuseEvent {
			_, err = tx.Exec(`CREATE TRIGGER fixture_block_alarm BEFORE INSERT ON events
				WHEN NEW.kind = 'drain.roll-failed' BEGIN SELECT RAISE(ABORT, 'fixture refuses alarm insert'); END`)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func assertBlockedSchemaRoll(t *testing.T, s *rollScenario, fallback bool) {
	t.Helper()
	if len(buildLines(s.output(t))) != 1 || drain.DiskDigest(s.exe) != drain.DiskDigest(drainBin(t)) {
		t.Fatalf("the roll installed or started a refused build:\n%s", s.output(t))
	}
	ctx := context.Background()
	db, err := store.OpenRead(ctx, s.f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if version, err := db.SchemaVersion(); err != nil || version != store.SchemaVersion {
		t.Fatalf("failed preflight changed schema: %d, %v", version, err)
	}
	var active []alarms.Alarm
	if fallback {
		alarm, err := alarms.ReadStoreRollFallback(s.f.store)
		if err != nil || alarm == nil {
			t.Fatalf("no durable alarm after SQLite refused the failure event: %v", err)
		}
		active = append(active, *alarm)
	} else if err := db.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		active, err = alarms.Read(r, time.Now(), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Kind != "store-roll-blocked" ||
		!strings.Contains(active[0].String(), "operator: resolve the reported store problem") ||
		!strings.Contains(active[0].Cause, "fixture refuses new schema") {
		t.Errorf("store roll alarms: %+v", active)
	}
}

func assertLiveRoll(t *testing.T, s *rollScenario, db *store.Store, id, fifo string) {
	t.Helper()
	ctx := context.Background()
	raw, err := json.Marshal(map[string]any{"kind": "fixture.seed", "payload": map[string]any{
		"records": rawRecords([]string{fencedItemRecord("item_043", "in-queue", "candidate.txt")}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Seed(ctx, raw); err != nil {
		t.Fatal(err)
	}
	s.waitFor(t, "the replacement loop to dispatch a new item while the worker is blocked", func() bool {
		var started bool
		if err := db.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND json_extract(payload,'$.item_id')='item_043')`, run.AllocateKind).Scan(&started)
		}); err != nil {
			t.Fatal(err)
		}
		return started
	})
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		var leased bool
		var pauses, finishes, releases int
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes WHERE run_id=? AND state='leased'),
			(SELECT count(*) FROM events WHERE object_id=? AND kind=?),
			(SELECT count(*) FROM events WHERE object_id=? AND kind=?),
			(SELECT count(*) FROM fixture_lease_releases WHERE run_id=?)`,
			id, id, run.PauseRequestKind, id, run.FinishKind, id).Scan(&leased, &pauses, &finishes, &releases); err != nil {
			return err
		}
		if !leased || pauses != 0 || finishes != 0 || releases != 0 {
			t.Fatalf("replacement disturbed live run %s: leased=%v pauses=%d finishes=%d releases=%d\n%s", id, leased, pauses, finishes, releases, s.output(t))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.WriteString("finish\n")
	writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	s.waitFor(t, "the replacement loop to account for the original worker's finish", func() bool {
		return strings.Contains(s.output(t), id+" on fixture spent")
	})
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		var allocations, finishes int
		var cause string
		if err := r.QueryRow(`SELECT end_cause,
			(SELECT count(*) FROM events WHERE kind=? AND json_extract(payload,'$.item_id')='item_041'),
			(SELECT count(*) FROM events WHERE kind=? AND object_id=?) FROM runs WHERE run_id=?`,
			run.AllocateKind, run.FinishKind, id, id).Scan(&cause, &allocations, &finishes); err != nil {
			return err
		}
		if cause != run.EndNormalExit || allocations != 1 || finishes != 1 {
			t.Errorf("run %s: cause=%s allocations=%d finishes=%d", id, cause, allocations, finishes)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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

// waitForStoreRollAlarm polls the loop's output until the store roll alarm is logged or the
// loop exits. No wall-clock limit is applied: a busy machine makes the roll slower rather
// than failing the test.
func waitForStoreRollAlarm(output func() string, exited <-chan error) error {
	const alarm = "alarm started: ALARM store-roll-blocked (drain)"
	for {
		out := output()
		if strings.Contains(out, alarm) {
			return nil
		}
		select {
		case err := <-exited:
			out = output()
			if strings.Contains(out, alarm) {
				return nil
			}
			return fmt.Errorf("loop exited before raising the store roll alarm: %v\n%s", err, out)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestWaitForStoreRollAlarm(t *testing.T) {
	t.Run("alarm after delay", func(t *testing.T) {
		var out atomic.Value
		out.Store("loop: starting\n")
		exited := make(chan error, 1)
		go func() {
			time.Sleep(50 * time.Millisecond)
			out.Store("loop: starting\nalarm started: ALARM store-roll-blocked (drain)\n")
		}()
		if err := waitForStoreRollAlarm(func() string { return out.Load().(string) }, exited); err != nil {
			t.Fatalf("waitForStoreRollAlarm() = %v, want nil", err)
		}
	})

	t.Run("loop exits before alarm", func(t *testing.T) {
		exited := make(chan error, 1)
		exited <- errors.New("exit status 2")
		err := waitForStoreRollAlarm(func() string { return "loop: failed early\n" }, exited)
		if err == nil {
			t.Fatal("waitForStoreRollAlarm() = nil, want error on early loop exit")
		}
		if !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "loop: failed early") {
			t.Fatalf("waitForStoreRollAlarm() error = %q, want exit error and loop output", err)
		}
	})

	t.Run("alarm logged before exit is observed", func(t *testing.T) {
		calls := 0
		exited := make(chan error, 1)
		exited <- nil
		err := waitForStoreRollAlarm(func() string {
			calls++
			if calls == 1 {
				return "loop: starting\n"
			}
			return "loop: starting\nalarm started: ALARM store-roll-blocked (drain)\n"
		}, exited)
		if err != nil {
			t.Fatalf("waitForStoreRollAlarm() = %v, want nil when alarm was logged before exit", err)
		}
	})
}
