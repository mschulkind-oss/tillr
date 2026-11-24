package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// TestWatchWaitWakesWhenWALPrecedesIndexPublish reproduces the SQLite WAL commit
// split deterministically on both direct and symlinked store paths: SQLite writes
// committed frames to -wal (firing fsnotify) before publishing the new WAL-index
// header in -shm under WAL_WRITE_LOCK without touching -wal again.
func TestWatchWaitWakesWhenWALPrecedesIndexPublish(t *testing.T) {
	for _, name := range []string{"direct", "symlink"} {
		t.Run(name, func(t *testing.T) {
			walPrecedesIndexPublish(t, name == "symlink")
		})
	}
}

func walPrecedesIndexPublish(t *testing.T, useSymlink bool) {
	t.Helper()
	s, rules := fixture(t, false)
	storeArg := "./state.db"
	if useSymlink {
		t.Chdir(t.TempDir())
		storeArg = "./linked.db"
		if err := os.Symlink(s.Path(), storeArg); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Chdir(filepath.Dir(s.Path()))
	}
	var initOut, initErr bytes.Buffer
	if code := run(t.Context(), []string{"eval", "--once", "--store", storeArg, "--rules", rules}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
	}
	fact(t, s, `{"item_id":"item-a"}`)

	entered := installWaitForWriterHook(t)

	ctx, cancel := context.WithCancel(t.Context())
	type waitResult struct {
		code        int
		out, stderr string
	}
	var result waitResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		var out, stderr bytes.Buffer
		code := runWithTicks(ctx, []string{"wait", "--store", storeArg, "--rules", rules}, &out, &stderr, make(chan time.Time))
		result = waitResult{code: code, out: out.String(), stderr: stderr.String()}
	}()
	defer func() {
		cancel()
		<-done
	}()
	awaitAttached(t, s)

	helper := startSplitWALHelper(t, ctx, s.Path())
	select {
	case <-entered:
	case <-done:
		t.Fatalf("wait exited before synchronizing with unpublished WAL writer: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	case <-ctx.Done():
		t.Fatalf("wait did not synchronize with the unpublished WAL writer: %v", ctx.Err())
	}
	helper.publishAndUnlock(t)

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("wait did not exit: %v", ctx.Err())
	}
	if result.code != 0 || result.stderr != "" || !strings.Contains(result.out, "item-a has 2 refusals") {
		t.Fatalf("wait missed split WAL commit: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	}
}

func TestWatchDaemonWakesWhenWALPrecedesIndexPublish(t *testing.T) {
	s, rules := fixture(t, false)
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err := os.Symlink(s.Path(), alias); err != nil {
		t.Fatal(err)
	}
	var initOut, initErr bytes.Buffer
	if code := run(t.Context(), []string{"eval", "--once", "--store", alias, "--rules", rules}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
	}
	fact(t, s, `{"item_id":"item-a"}`)

	entered := installWaitForWriterHook(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int, 1)
	var out, stderr bytes.Buffer
	ticks := make(chan time.Time)
	go func() {
		done <- runWithTicks(ctx, []string{"daemon", "--store", alias, "--rules", rules}, &out, &stderr, ticks)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Ensure the daemon completed its initial quiet evaluation pass.
	ticks <- time.Now()

	helper := startSplitWALHelper(t, ctx, s.Path())
	select {
	case <-entered:
	case <-time.After(500 * time.Millisecond):
		helper.close()
		t.Fatal("daemon did not synchronize with the unpublished WAL writer")
	}
	helper.publishAndUnlock(t)

	eventually(t, func() bool {
		return daemonCount(t, s, `SELECT COUNT(*) FROM alert_episodes WHERE state='open'`) == 1
	})
}

func TestWakeDetectsCommitPublishedAfterBeginPass(t *testing.T) {
	s, _ := fixture(t, false)
	other, err := store.Open(t.Context(), s.Path(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })

	wake, err := watchStore(s, make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	defer wake.close()

	if err := wake.beginPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	fact(t, other, `{"item_id":"item-a"}`)
	// Simulate the -wal write notification having been coalesced before the
	// writer published its WAL-index header in -shm.
	if err := wake.drainEvents(); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(200 * time.Millisecond)
	reason, err := wake.next(t.Context(), deadline)
	if err != nil || reason != walWake {
		t.Fatalf("wake.next = (%v, %v), want (walWake, nil)", reason, err)
	}
}

func TestWakeHonorsDeadlineWhileWaitingForWriter(t *testing.T) {
	s, _ := fixture(t, false)
	fact(t, s, `{"item_id":"item-a"}`)

	wake, err := watchStore(s, make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	defer wake.close()

	if err := wake.beginPass(t.Context()); err != nil {
		t.Fatal(err)
	}

	helper := startSplitWALHelper(t, t.Context(), s.Path())
	defer helper.close()
	if err := wake.drainEvents(); err != nil {
		t.Fatal(err)
	}

	timer := time.NewTimer(30 * time.Millisecond)
	defer timer.Stop()
	reason, err := wake.next(t.Context(), timer.C)
	if err != nil || reason != deadlineWake {
		t.Fatalf("wake.next = (%v, %v), want (deadlineWake, nil)", reason, err)
	}
}

func installWaitForWriterHook(t *testing.T) <-chan struct{} {
	t.Helper()
	entered := make(chan struct{})
	var once sync.Once
	fn := func() { once.Do(func() { close(entered) }) }
	onWaitForWriter.Store(&fn)
	t.Cleanup(func() { onWaitForWriter.Store(nil) })
	return entered
}

type splitWALHelper struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	helperErr *bytes.Buffer
	closed    bool
}

func startSplitWALHelper(t *testing.T, ctx context.Context, storePath string) *splitWALHelper {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stageDir := t.TempDir()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHelperSplitWALCommit$")
	cmd.Env = append(os.Environ(),
		"TILLR_TEST_SPLIT_WAL_STORE="+storePath,
		"TILLR_TEST_SPLIT_WAL_STAGE="+stageDir,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var helperErr bytes.Buffer
	cmd.Stderr = &helperErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &splitWALHelper{cmd: cmd, stdin: stdin, helperErr: &helperErr}
	t.Cleanup(h.close)

	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(ready) != "wal-written" {
		h.close()
		t.Fatalf("helper setup: line=%q err=%v stderr=%s", ready, err, helperErr.String())
	}
	return h
}

func (h *splitWALHelper) publishAndUnlock(t *testing.T) {
	t.Helper()
	if h.closed {
		return
	}
	h.closed = true
	if err := h.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.cmd.Wait(); err != nil {
		t.Fatalf("helper publish: %v\n%s", err, h.helperErr.String())
	}
}

func (h *splitWALHelper) close() {
	if h.closed {
		return
	}
	h.closed = true
	_ = h.stdin.Close()
	_ = h.cmd.Wait()
}

func TestHelperSplitWALCommit(t *testing.T) {
	storePath := os.Getenv("TILLR_TEST_SPLIT_WAL_STORE")
	stageDir := os.Getenv("TILLR_TEST_SPLIT_WAL_STAGE")
	if storePath == "" || stageDir == "" {
		return
	}
	ctx := context.Background()

	stagedPath := filepath.Join(stageDir, "state.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(storePath + suffix)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s%s: %v\n", storePath, suffix, err)
			os.Exit(2)
		}
		if err := os.WriteFile(stagedPath+suffix, data, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "write staged%s: %v\n", suffix, err)
			os.Exit(2)
		}
	}

	beforeWAL, err := os.Stat(stagedPath + "-wal")
	if err != nil {
		fmt.Fprintf(os.Stderr, "stat staged wal: %v\n", err)
		os.Exit(2)
	}
	stagedStore, err := store.Open(ctx, stagedPath, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open staged: %v\n", err)
		os.Exit(2)
	}
	err = stagedStore.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{At: time.Now(), Kind: "drain.refusal", Payload: []byte(`{"item_id":"item-a"}`)})
		return err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "append staged: %v\n", err)
		os.Exit(2)
	}
	stagedWALBytes, err := os.ReadFile(stagedPath + "-wal")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read committed staged wal: %v\n", err)
		os.Exit(2)
	}
	stagedSHMBytes, err := os.ReadFile(stagedPath + "-shm")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read committed staged shm: %v\n", err)
		os.Exit(2)
	}
	_ = stagedStore.Close()

	// Hold SQLite's WAL_WRITE_LOCK on the real store without writing any frames
	// of our own, write the committed WAL frames to -wal (triggering fsnotify),
	// wait for the parent, then publish the matching -shm bytes and unlock.
	rawDB, err := sql.Open("sqlite", storePath+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open raw sqlite: %v\n", err)
		os.Exit(2)
	}
	defer rawDB.Close()
	tx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "begin immediate: %v\n", err)
		os.Exit(2)
	}

	walFile, err := os.OpenFile(storePath+"-wal", os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open real wal: %v\n", err)
		os.Exit(2)
	}
	off := beforeWAL.Size()
	if _, err := walFile.WriteAt(stagedWALBytes[off:], off); err != nil {
		fmt.Fprintf(os.Stderr, "write wal tail: %v\n", err)
		os.Exit(2)
	}
	_ = walFile.Close()

	fmt.Println("wal-written")
	_, _ = io.Copy(io.Discard, os.Stdin)

	shmFile, err := os.OpenFile(storePath+"-shm", os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open real shm: %v\n", err)
		os.Exit(2)
	}
	if _, err := shmFile.WriteAt(stagedSHMBytes, 0); err != nil {
		fmt.Fprintf(os.Stderr, "write shm: %v\n", err)
		os.Exit(2)
	}
	// Do not close shmFile before tx.Rollback: closing a -shm descriptor in this
	// helper process would drop rawDB's POSIX lock prematurely.
	if err := tx.Rollback(); err != nil {
		fmt.Fprintf(os.Stderr, "rollback: %v\n", err)
		os.Exit(2)
	}
	_ = shmFile.Close()
}

func readProcCPUTicks(t *testing.T, pid int) int64 {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("read /proc/%d/stat: %v", pid, err)
	}
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 || end+2 >= len(raw) {
		t.Fatalf("malformed /proc/%d/stat: %q", pid, raw)
	}
	fields := strings.Fields(string(raw[end+2:]))
	if len(fields) <= 12 {
		t.Fatalf("short /proc/%d/stat fields: %v", pid, fields)
	}
	var utime, stime int64
	if _, err := fmt.Sscanf(fields[11], "%d", &utime); err != nil {
		t.Fatalf("parse utime %q: %v", fields[11], err)
	}
	if _, err := fmt.Sscanf(fields[12], "%d", &stime); err != nil {
		t.Fatalf("parse stime %q: %v", fields[12], err)
	}
	return utime + stime
}

func TestWatchWaitIdleUnderStoreWritesAndDeliversAlert(t *testing.T) {
	const rulesDir = "../../config/tillr/watch"
	if err := event.Register("watch-bench.unrelated", "watch-test"); err != nil {
		t.Fatal(err)
	}
	f := newTrunkProofFixture(t)
	ctx := t.Context()

	var initOut, initErr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", f.s.Path(), "--rules", rulesDir, "--root", f.root}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
	}

	now := time.Now().UTC()
	var seenHead int64
	if err := f.s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE _pad (id INTEGER PRIMARY KEY, b BLOB)`); err != nil {
			return err
		}
		for i := 0; i < 30; i++ {
			if _, err := tx.Exec(`INSERT INTO _pad(b) VALUES (zeroblob(4000000))`); err != nil {
				return err
			}
		}
		for i := 0; i < 400; i++ {
			aid := fmt.Sprintf("alrt-hist-%04d", i)
			seq, err := tx.Append(event.Event{
				Kind:       "watch.alert.opened",
				ObjectType: "alert",
				ObjectID:   &aid,
				At:         now.Add(-time.Hour),
				Payload:    []byte(`{"receiver":"foreground"}`),
			})
			if err != nil {
				return err
			}
			if i >= 370 {
				if _, err := tx.Exec(`INSERT INTO alert_episodes (
					alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint,
					opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json
				) VALUES (?, 'retired-rule', ?, 1, 'open', 'warning', 'fp', ?, ?, ?, ?, '{}')`,
					aid, fmt.Sprintf("subj-%04d", i), seq, now.Add(-time.Hour).UnixMilli(), seq, now.Add(-time.Hour).UnixMilli()); err != nil {
					return err
				}
			}
		}
		sess := "sess-idle"
		var err error
		seenHead, err = tx.Append(event.Event{
			Kind:       "drain.loop-pass",
			ObjectType: "session",
			ObjectID:   &sess,
			At:         now,
			Payload:    []byte(`{"session_id":"sess-idle","dispatched":0}`),
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO foreground_delivery(owner_handle, seen_seq, shown_seq) VALUES ('operator-session', ?, ?)
			ON CONFLICT(owner_handle) DO UPDATE SET seen_seq=excluded.seen_seq, shown_seq=excluded.shown_seq`, seenHead, seenHead)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	storePath := f.s.Path()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	f.s = reopened
	t.Cleanup(func() { f.s.Close() })

	child := startCLIChild(t, "wait", "--store", f.s.Path(), "--rules", rulesDir, "--root", f.root, "--owner", "operator-session", "--seen-through", fmt.Sprint(seenHead))
	eventually(t, func() bool {
		var attached bool
		if err := f.s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT EXISTS(SELECT 1 FROM foreground_delivery WHERE owner_handle='operator-session' AND waiting_since IS NOT NULL)`).Scan(&attached)
		}); err != nil {
			t.Fatal(err)
		}
		return attached
	})
	time.Sleep(50 * time.Millisecond)

	startTicks := readProcCPUTicks(t, child.cmd.Process.Pid)
	startWall := time.Now()
	const commits = 90
	const window = 3 * time.Second
	for i := 0; i < commits; i++ {
		runID := "run-idle"
		if err := f.s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "watch-bench.unrelated",
				ObjectType: "run",
				ObjectID:   &runID,
				At:         time.Now().UTC(),
				Payload:    []byte(`{"step":"heartbeat"}`),
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if sleep := time.Until(startWall.Add(time.Duration(i+1) * window / commits)); sleep > 0 {
			time.Sleep(sleep)
		}
	}
	wall := time.Since(startWall)
	endTicks := readProcCPUTicks(t, child.cmd.Process.Pid)
	select {
	case err := <-child.done:
		t.Fatalf("idle wait exited early: err=%v out=%q stderr=%q", err, &child.out, &child.stderr)
	default:
	}

	const clkTck = 100.0
	cpuSeconds := float64(endTicks-startTicks) / clkTck
	coreFraction := cpuSeconds / wall.Seconds()
	t.Logf("idle wait CPU: %.2f%% of one core (%.3fs CPU over %.2fs across %d store commits)",
		coreFraction*100, cpuSeconds, wall.Seconds(), commits)
	if coreFraction >= 0.05 {
		t.Fatalf("idle wait used %.2f%% of one core (%.3fs CPU over %.2fs across %d store commits), want < 5%%",
			coreFraction*100, cpuSeconds, wall.Seconds(), commits)
	}

	backend := "pi"
	if err := f.s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       "drain.backend-outcome",
			ObjectType: "backend",
			ObjectID:   &backend,
			At:         time.Now().UTC(),
			Payload:    []byte(`{"backend":"pi","outcome_class":"quota","closes":true}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := child.wait(t); err != nil {
		t.Fatalf("wait did not exit 0 on firing alert: err=%v out=%q stderr=%q", err, &child.out, &child.stderr)
	}
	if !strings.Contains(child.out.String(), "drain-backend-closed") || !strings.Contains(child.out.String(), "TILLR_WATCH_NOTICE 1 pending") {
		t.Fatalf("wait did not deliver firing alert: out=%q stderr=%q", &child.out, &child.stderr)
	}
}

func TestWatchWaitDeliversAlertCommittedBeforeTimeoutDuringWALBurst(t *testing.T) {
	s, rules := fixture(t, false)
	var initOut, initErr bytes.Buffer
	if code := run(t.Context(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
	}
	if err := event.Register("watch-bench.burst-unrelated", "watch-test"); err != nil && !strings.Contains(err.Error(), "already") {
		t.Fatal(err)
	}

	ctx := t.Context()
	type waitResult struct {
		code        int
		out, stderr string
	}
	done := make(chan waitResult, 1)
	go func() {
		var out, stderr bytes.Buffer
		code := run(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "120ms"}, &out, &stderr)
		done <- waitResult{code: code, out: out.String(), stderr: stderr.String()}
	}()
	awaitAttached(t, s)

	// Trigger a first WAL wake so the 150ms coalesce window is active, then
	// commit a firing alert inside that window before the 120ms timeout expires.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: "watch-bench.burst-unrelated", ObjectType: "item", At: time.Now().UTC(), Payload: []byte(`{}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	fact(t, s, `{"item_id":"item-burst"}`)
	fact(t, s, `{"item_id":"item-burst"}`)

	select {
	case r := <-done:
		if r.code != 0 || !strings.Contains(r.out, "item-burst has 2 refusals") {
			t.Fatalf("wait missed pre-deadline alert during WAL coalesce window: code=%d out=%q stderr=%q", r.code, r.out, r.stderr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not exit")
	}
}
