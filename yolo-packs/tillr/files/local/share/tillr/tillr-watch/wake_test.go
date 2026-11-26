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
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func startExternalRelay(t *testing.T, storePath string) (cancel context.CancelFunc, waitStop func()) {
	t.Helper()
	relayCtx, cancel := context.WithCancel(t.Context())
	ready := make(chan string, 1)
	var serveErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveErr = notify.Serve(relayCtx, notify.ServeConfig{
			StorePath: storePath,
			IdleExit:  notify.DefaultIdleExit,
			Ready:     ready,
		})
	}()
	select {
	case sockPath := <-ready:
		t.Cleanup(func() {
			cancel()
			<-done
			_ = os.Remove(notify.LockPath(sockPath))
		})
	case <-done:
		cancel()
		t.Fatalf("notify.Serve(%s): %v", storePath, serveErr)
	}
	return cancel, func() {
		cancel()
		<-done
		if serveErr != nil {
			t.Fatalf("notify.Serve stop: %v", serveErr)
		}
	}
}

func appendRawRefusalWithoutNotify(t *testing.T, storePath, itemID string) {
	t.Helper()
	rawDB, err := sql.Open("sqlite", storePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	payload := fmt.Sprintf(`{"item_id":%q}`, itemID)
	if _, err := rawDB.ExecContext(t.Context(), `INSERT INTO events (ts, kind, payload) VALUES (?, 'drain.refusal', ?)`, time.Now().UTC().UnixMilli(), payload); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyWakeDeliveredWithoutDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		wake := &notifyWakeSource{wakes: make(chan struct{}, 1)}
		result := make(chan wakeReason, 1)
		go func() {
			reason, _ := wake.next(ctx, nil)
			result <- reason
		}()
		synctest.Wait()
		select {
		case reason := <-result:
			t.Fatalf("wake without signal: %v", reason)
		default:
		}
		wake.signalWake()
		synctest.Wait()
		select {
		case reason := <-result:
			if reason != notifyWake {
				t.Fatalf("wake reason = %v, want notifyWake", reason)
			}
		default:
			t.Fatal("queued notification did not wake the waiter")
		}
	})
}

func TestNotifyWakeDeadlineWithoutSignal(t *testing.T) {
	wake := &notifyWakeSource{wakes: make(chan struct{}, 1)}
	deadline := make(chan time.Time)
	close(deadline)
	reason, err := wake.next(t.Context(), deadline)
	if err != nil || reason != deadlineWake {
		t.Fatalf("missing wake: reason=%v err=%v, want deadlineWake", reason, err)
	}
}

func TestWatchWaitWakesOnRelayedFrameAndFallback(t *testing.T) {
	for _, name := range []string{"direct", "symlink"} {
		t.Run("relayed-frame/"+name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", "")
			s, rules := fixture(t, false)
			startExternalRelay(t, s.Path())

			storeArg := "./state.db"
			if name == "symlink" {
				t.Chdir(t.TempDir())
				storeArg = "./linked.db"
				if err := os.Symlink(s.Path(), storeArg); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Chdir(filepath.Dir(s.Path()))
			}

			var initOut, initErr bytes.Buffer
			if code := run(t.Context(), []string{"eval", "--once", "--store", storeArg, "--rules", rules, "--no-shipped-rules"}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
				t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
			}
			fact(t, s, `{"item_id":"item-a"}`)

			ctx, cancel := context.WithCancel(t.Context())
			deadline := make(chan time.Time)
			idle := make(chan struct{}, 1)
			frameRead := make(chan struct{}, 1)
			hooks := &waitTestHooks{
				deadline: deadline,
				onIdle: func(bool) {
					select {
					case idle <- struct{}{}:
					default:
					}
				},
				onRelayProgress: func(reconnected bool, frame notify.Frame) {
					if !reconnected && frame.ItemID == "wake-test-marker" {
						select {
						case frameRead <- struct{}{}:
						default:
						}
					}
				},
			}
			type waitResult struct {
				code        int
				out, stderr string
			}
			var result waitResult
			done := make(chan struct{})
			go func() {
				defer close(done)
				var out, stderr bytes.Buffer
				code := runWithWaitHooks(ctx, []string{"wait", "--store", storeArg, "--rules", rules, "--no-shipped-rules"}, &out, &stderr, make(chan time.Time), hooks)
				result = waitResult{code: code, out: out.String(), stderr: stderr.String()}
			}()
			defer func() {
				cancel()
				<-done
			}()
			select {
			case <-idle:
			case <-done:
				t.Fatalf("wait exited before becoming idle: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
			}

			// Commit the second refusal directly to SQLite without publishing a
			// notification: wait must stay asleep rather than scanning -wal.
			appendRawRefusalWithoutNotify(t, s.Path(), "item-a")
			select {
			case <-done:
				t.Fatalf("wait woke on raw WAL write without relayed frame: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
			case <-time.After(60 * time.Millisecond):
			}

			// Relaying a wake hint (including resync) wakes wait to evaluate rules.
			topic := notify.TopicQueueChanged
			if name == "symlink" {
				topic = notify.TopicResync
			}
			if err := notify.PublishContext(t.Context(), s.Path(), notify.Frame{Topic: topic, ItemID: "wake-test-marker"}); err != nil {
				t.Fatal(err)
			}

			// The relay acknowledges enqueue, not receipt. Only expire the
			// injected deadline after the reader processed this particular frame.
			// A transport that never progresses is bounded by the test runner's
			// hang guard, not a per-wake scheduling budget.
			select {
			case <-frameRead:
				close(deadline)
			case <-done:
				select {
				case <-frameRead:
				default:
					t.Fatalf("wait exited before reading relayed frame: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
				}
			}
			<-done
			if result.code != 0 || result.stderr != "" || !strings.Contains(result.out, "item-a has 2 refusals") {
				t.Fatalf("wait output after relayed frame: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
			}
		})
	}

	t.Run("15s-fallback-default-and-tick", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		s, rules := fixture(t, false)
		var initOut, initErr bytes.Buffer
		if code := run(t.Context(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
			t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
		}

		// Verify default wait wires a 15-second fallback ticker.
		recorded := make(chan time.Duration, 1)
		hook := func(d time.Duration) {
			select {
			case recorded <- d:
			default:
			}
		}
		onFallbackTicker.Store(&hook)
		t.Cleanup(func() { onFallbackTicker.Store(nil) })

		var timeoutOut, timeoutErr bytes.Buffer
		if code := run(t.Context(), []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--no-notify", "--timeout", "25ms"}, &timeoutOut, &timeoutErr); code != 2 || timeoutErr.Len() != 0 {
			t.Fatalf("default fallback wait: code=%d out=%q err=%q", code, &timeoutOut, &timeoutErr)
		}
		select {
		case got := <-recorded:
			if got != 15*time.Second {
				t.Fatalf("default fallback interval = %v, want 15s", got)
			}
		default:
			t.Fatal("wait did not initialize fallback ticker")
		}
		onFallbackTicker.Store(nil)

		// Verify fallback tick evaluates rules when no relayed frame arrives.
		fact(t, s, `{"item_id":"item-fallback"}`)
		ctx, cancel := context.WithCancel(t.Context())
		ticks := make(chan time.Time)
		deadline := make(chan time.Time)
		idle := make(chan struct{}, 1)
		hooks := &waitTestHooks{
			deadline: deadline,
			onIdle: func(bool) {
				select {
				case idle <- struct{}{}:
				default:
				}
			},
		}
		type waitResult struct {
			code        int
			out, stderr string
		}
		var result waitResult
		done := make(chan struct{})
		go func() {
			defer close(done)
			var out, stderr bytes.Buffer
			code := runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--no-notify"}, &out, &stderr, ticks, hooks)
			result = waitResult{code: code, out: out.String(), stderr: stderr.String()}
		}()
		defer func() {
			cancel()
			<-done
		}()
		select {
		case <-idle:
		case <-done:
			t.Fatalf("wait exited before becoming idle: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
		}

		appendRawRefusalWithoutNotify(t, s.Path(), "item-fallback")
		select {
		case <-done:
			t.Fatalf("wait woke before fallback tick: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
		case <-time.After(40 * time.Millisecond):
		}

		select {
		case ticks <- time.Now():
			close(deadline)
		case <-done:
		}
		<-done
		if result.code != 0 || result.stderr != "" || !strings.Contains(result.out, "item-fallback has 2 refusals") {
			t.Fatalf("fallback evaluation: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
		}
	})
}

func TestWatchWaitReconnectsAndEvaluatesImmediatelyAfterRelayKill(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	disableAutoRelay.Store(true)
	t.Cleanup(func() { disableAutoRelay.Store(false) })

	s, rules := fixture(t, false)
	var initOut, initErr bytes.Buffer
	if code := run(t.Context(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
	}
	_, stopFirstRelay := startExternalRelay(t, s.Path())

	ctx, cancel := context.WithCancel(t.Context())
	deadline := make(chan time.Time)
	idle := make(chan struct{}, 1)
	armed := make(chan struct{})
	reconnected := make(chan struct{}, 1)
	hooks := &waitTestHooks{
		deadline: deadline,
		onIdle: func(bool) {
			select {
			case idle <- struct{}{}:
			default:
			}
		},
		onRelayProgress: func(isReconnect bool, _ notify.Frame) {
			if isReconnect {
				select {
				case <-armed:
					select {
					case reconnected <- struct{}{}:
					default:
					}
				default:
				}
			}
		},
	}
	type waitResult struct {
		code        int
		out, stderr string
	}
	var result waitResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		var out, stderr bytes.Buffer
		code := runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, &out, &stderr, make(chan time.Time), hooks)
		result = waitResult{code: code, out: out.String(), stderr: stderr.String()}
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-idle:
	case <-done:
		t.Fatalf("wait exited before becoming idle: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	}

	// Kill the relay and commit firing events while the socket is absent so
	// post-commit publish drops every wake hint.
	stopFirstRelay()
	if _, ok := notify.ActiveSocketPath(s.Path()); ok {
		t.Fatal("relay socket remained active after relay shutdown")
	}
	fact(t, s, `{"item_id":"item-reconnect"}`)
	fact(t, s, `{"item_id":"item-reconnect"}`)

	// Restart the relay; wait must reconnect with backoff and immediately run
	// its evaluation pass without waiting on the 15-second fallback clock.
	close(armed)
	startExternalRelay(t, s.Path())

	select {
	case <-reconnected:
		close(deadline)
	case <-done:
		select {
		case <-reconnected:
		default:
			t.Fatalf("wait exited before reconnecting: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
		}
	}
	<-done
	if result.code != 0 || result.stderr != "" || !strings.Contains(result.out, "item-reconnect has 2 refusals") {
		t.Fatalf("reconnect evaluation: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	}
}

func TestWatchWaitAlertParityWithRelayAbsent(t *testing.T) {
	type parityRun struct {
		alertLines []string
		episodes   []string
	}
	extractAlertLines := func(chunks []string) []string {
		var alerts []string
		seen := make(map[string]bool)
		for _, chunk := range chunks {
			for _, line := range strings.Split(chunk, "\n") {
				if strings.HasPrefix(line, "[alert ") {
					// Normalize random alert IDs (`[alert alrt-... rule subj, sev] summary`)
					// to `[alert rule subj, sev] summary` for deterministic parity comparison.
					rest := strings.TrimPrefix(line, "[alert ")
					if sp := strings.IndexByte(rest, ' '); sp >= 0 {
						normalized := "[alert " + rest[sp+1:]
						if !seen[normalized] {
							seen[normalized] = true
							alerts = append(alerts, normalized)
						}
					}
				}
			}
		}
		return alerts
	}
	readEpisodes := func(t *testing.T, s *store.Store) []string {
		t.Helper()
		var rowsOut []string
		err := s.ReadTx(t.Context(), func(rd *store.Reader) error {
			rows, err := rd.Query(`SELECT rule_id, subject_key, episode_num, state, severity, observation_count FROM alert_episodes ORDER BY rule_id, subject_key, episode_num`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var ruleID, subject, state, sev string
				var epNum, obs int
				if err := rows.Scan(&ruleID, &subject, &epNum, &state, &sev, &obs); err != nil {
					return err
				}
				rowsOut = append(rowsOut, fmt.Sprintf("%s|%s|%d|%s|%s|%d", ruleID, subject, epNum, state, sev, obs))
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		return rowsOut
	}

	runScenario := func(t *testing.T, mode string) parityRun {
		t.Helper()
		t.Setenv("XDG_RUNTIME_DIR", "")
		s, rules := fixture(t, false)
		var initOut, initErr bytes.Buffer
		if code := run(t.Context(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
			t.Fatalf("init eval (%s): code=%d out=%q err=%q", mode, code, &initOut, &initErr)
		}

		args := []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--follow"}
		var ticks chan time.Time
		switch mode {
		case "with-relay":
			startExternalRelay(t, s.Path())
			ticks = make(chan time.Time)
		case "relay-absent-no-notify":
			args = append(args, "--no-notify", "--fallback-poll", "25ms")
		case "relay-absent-dial-enoent":
			disableAutoRelay.Store(true)
			t.Cleanup(func() { disableAutoRelay.Store(false) })
			args = append(args, "--fallback-poll", "25ms")
		default:
			t.Fatalf("unknown mode %s", mode)
		}

		ctx, cancel := context.WithCancel(t.Context())
		output := make(outputChunks, 16)
		var stderr bytes.Buffer
		done := make(chan int, 1)
		go func() {
			if ticks != nil {
				done <- runWithTicks(ctx, args, output, &stderr, ticks)
			} else {
				done <- run(ctx, args, output, &stderr)
			}
		}()
		defer func() {
			cancel()
			<-done
		}()
		awaitAttached(t, s)

		if mode != "with-relay" {
			if _, ok := notify.ActiveSocketPath(s.Path()); ok {
				t.Fatalf("%s: expected relay socket to be absent", mode)
			}
		} else {
			if _, ok := notify.ActiveSocketPath(s.Path()); !ok {
				t.Fatalf("%s: expected relay socket to be active", mode)
			}
		}

		var collected []string
		// Wave 1: item-alpha fires (2 refusals); item-noise stays below threshold (1 refusal).
		fact(t, s, `{"item_id":"item-noise"}`)
		fact(t, s, `{"item_id":"item-alpha"}`)
		fact(t, s, `{"item_id":"item-alpha"}`)
		collected = append(collected, awaitChunk(t, output))

		// Wave 2: item-beta fires (2 refusals).
		fact(t, s, `{"item_id":"item-beta"}`)
		fact(t, s, `{"item_id":"item-beta"}`)
		for {
			chunk := awaitChunk(t, output)
			collected = append(collected, chunk)
			if strings.Contains(chunk, "item-beta has 2 refusals") {
				break
			}
		}

		if mode != "with-relay" {
			if _, ok := notify.ActiveSocketPath(s.Path()); ok {
				t.Fatalf("%s: relay socket appeared unexpectedly", mode)
			}
		}
		if stderr.Len() != 0 {
			t.Fatalf("%s: unexpected stderr %q", mode, stderr.String())
		}

		return parityRun{
			alertLines: extractAlertLines(collected),
			episodes:   readEpisodes(t, s),
		}
	}

	withRelay := runScenario(t, "with-relay")
	absentNoNotify := runScenario(t, "relay-absent-no-notify")
	absentENOENT := runScenario(t, "relay-absent-dial-enoent")

	if len(withRelay.alertLines) != 2 {
		t.Fatalf("expected 2 alerts with relay, got %v", withRelay.alertLines)
	}
	for label, got := range map[string]parityRun{
		"relay-absent-no-notify":   absentNoNotify,
		"relay-absent-dial-enoent": absentENOENT,
	} {
		if strings.Join(got.alertLines, "\n") != strings.Join(withRelay.alertLines, "\n") {
			t.Fatalf("%s alerts = %v, want %v", label, got.alertLines, withRelay.alertLines)
		}
		if strings.Join(got.episodes, "\n") != strings.Join(withRelay.episodes, "\n") {
			t.Fatalf("%s episodes = %v, want %v", label, got.episodes, withRelay.episodes)
		}
	}
}

func TestWatchWaitFlagValidationAndRearm(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	s, rules := fixture(t, false)
	for _, bad := range []string{"0s", "-5s"} {
		var out, stderr bytes.Buffer
		code := run(t.Context(), []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--fallback-poll", bad}, &out, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "fallback-poll must be positive") {
			t.Fatalf("--fallback-poll %s: code=%d out=%q stderr=%q", bad, code, &out, &stderr)
		}
	}

	var out, stderr bytes.Buffer
	code := run(t.Context(), []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--no-notify", "--fallback-poll", "5s", "--timeout", "20ms"}, &out, &stderr)
	if code != 2 || stderr.Len() != 0 || !strings.Contains(out.String(), "--fallback-poll 5s") || !strings.Contains(out.String(), "--no-notify") {
		t.Fatalf("re-arm flags: code=%d out=%q stderr=%q", code, &out, &stderr)
	}
}

func TestWatchDaemonWakesWhenWALPrecedesIndexPublish(t *testing.T) {
	s, rules := fixture(t, false)
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err := os.Symlink(s.Path(), alias); err != nil {
		t.Fatal(err)
	}
	var initOut, initErr bytes.Buffer
	if code := run(t.Context(), []string{"eval", "--once", "--store", alias, "--rules", rules, "--no-shipped-rules"}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
	}
	fact(t, s, `{"item_id":"item-a"}`)

	entered := installWaitForWriterHook(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int, 1)
	var out, stderr bytes.Buffer
	ticks := make(chan time.Time)
	go func() {
		done <- runWithTicks(ctx, []string{"daemon", "--store", alias, "--rules", rules, "--no-shipped-rules"}, &out, &stderr, ticks)
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
	case <-time.After(hangGuard):
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

	deadline := time.After(hangGuard)
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
	const rulesDir = "rules"
	if err := event.Register("watch-bench.unrelated", "watch-test"); err != nil {
		t.Fatal(err)
	}
	f := newTrunkProofFixture(t)
	ctx := t.Context()

	var initOut, initErr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", f.s.Path(), "--rules", rulesDir, "--no-shipped-rules", "--root", f.root}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
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

	child := startCLIChild(t, "wait", "--store", f.s.Path(), "--rules", rulesDir, "--no-shipped-rules", "--root", f.root, "--owner", "operator-session", "--seen-through", fmt.Sprint(seenHead))
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

	sessAlert := "sess-alert"
	if err := f.s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       "drain.loop-ended",
			ObjectType: "session",
			ObjectID:   &sessAlert,
			At:         time.Now().UTC(),
			Payload:    []byte(`{"session_id":"sess-alert","stop":"fault","detail":"loop fault","exit_code":1}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := child.wait(t); err != nil {
		t.Fatalf("wait did not exit 0 on firing alert: err=%v out=%q stderr=%q", err, &child.out, &child.stderr)
	}
	if !strings.Contains(child.out.String(), "drain-loop-fault") || !strings.Contains(child.out.String(), "TILLR_WATCH_NOTICE 1 pending") {
		t.Fatalf("wait did not deliver firing alert: out=%q stderr=%q", &child.out, &child.stderr)
	}
}

func TestWatchWaitDeliversAlertCommittedBeforeTimeoutDuringWALBurst(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	s, rules := fixture(t, false)
	var initOut, initErr bytes.Buffer
	if code := run(t.Context(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules, "--no-shipped-rules"}, &initOut, &initErr); code != 0 || initErr.Len() != 0 {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, &initOut, &initErr)
	}
	if err := event.Register("watch-bench.burst-unrelated", "watch-test"); err != nil && !strings.Contains(err.Error(), "already") {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time)
	deadline := make(chan time.Time)

	type waitResult struct {
		code        int
		out, stderr string
	}
	var (
		mu          sync.Mutex
		cond        = sync.NewCond(&mu)
		passCount   int
		passHeadSeq int64
		passHeadErr error
		idleCount   int
		idleHeadSeq int64
		idleArmed   bool
		inCoalesce  bool
		waitDone    bool
		result      waitResult
	)
	hooks := &waitTestHooks{
		deadline: deadline,
		onPass: func() {
			head, err := s.EventHeadSeq()
			mu.Lock()
			passCount++
			passHeadSeq = head
			if err != nil && passHeadErr == nil {
				passHeadErr = err
			}
			mu.Unlock()
		},
		onIdle: func(armed bool) {
			mu.Lock()
			idleCount = passCount
			idleHeadSeq = passHeadSeq
			idleArmed = armed
			cond.Broadcast()
			mu.Unlock()
		},
		onCoalesce: func() func() {
			mu.Lock()
			inCoalesce = true
			cond.Broadcast()
			mu.Unlock()
			return func() {
				mu.Lock()
				inCoalesce = false
				cond.Broadcast()
				mu.Unlock()
			}
		},
	}

	go func() {
		var out, stderr bytes.Buffer
		code := runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--timeout", "120ms"}, &out, &stderr, ticks, hooks)
		mu.Lock()
		result = waitResult{code: code, out: out.String(), stderr: stderr.String()}
		waitDone = true
		cond.Broadcast()
		mu.Unlock()
	}()
	defer func() {
		cancel()
		mu.Lock()
		for !waitDone {
			cond.Wait()
		}
		mu.Unlock()
	}()

	mu.Lock()
	for idleCount < 1 && !waitDone {
		cond.Wait()
	}
	if err := passHeadErr; err != nil {
		mu.Unlock()
		t.Fatal(err)
	}
	if waitDone {
		r := result
		mu.Unlock()
		t.Fatalf("wait exited before burst: code=%d out=%q stderr=%q", r.code, r.out, r.stderr)
	}
	mu.Unlock()

	// Trigger a first wake so the coalesce window is active, wait until that
	// quiet pass has completed, then commit a firing alert before letting the
	// waiter's deadline expire inside the coalesce window.
	var unrelatedSeq int64
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		unrelatedSeq, err = tx.Append(event.Event{Kind: "watch-bench.burst-unrelated", ObjectType: "item", At: time.Now().UTC(), Payload: []byte(`{}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := notify.PublishContext(ctx, s.Path(), notify.Frame{Topic: notify.TopicQueueChanged}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	for (idleHeadSeq < unrelatedSeq || !idleArmed) && !waitDone {
		cond.Wait()
	}
	if err := passHeadErr; err != nil {
		mu.Unlock()
		t.Fatal(err)
	}
	if waitDone {
		r := result
		mu.Unlock()
		t.Fatalf("wait exited on unrelated event: code=%d out=%q stderr=%q", r.code, r.out, r.stderr)
	}
	mu.Unlock()

	fact(t, s, `{"item_id":"item-burst"}`)
	fact(t, s, `{"item_id":"item-burst"}`)
	if err := notify.PublishContext(ctx, s.Path(), notify.Frame{Topic: notify.TopicQueueChanged}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	for !inCoalesce && !waitDone {
		cond.Wait()
	}
	if !waitDone {
		close(deadline)
	}
	for !waitDone {
		cond.Wait()
	}
	r := result
	mu.Unlock()

	if r.code != 0 || !strings.Contains(r.out, "item-burst has 2 refusals") {
		t.Fatalf("wait missed pre-deadline alert during WAL coalesce window: code=%d out=%q stderr=%q", r.code, r.out, r.stderr)
	}
}

type lockingProtocolErr struct{}

func (lockingProtocolErr) Error() string { return "locking protocol (15)" }
func (lockingProtocolErr) Code() int     { return 15 }

func TestWatchWaitRidesOutLockingProtocolError(t *testing.T) {
	s, rules := fixture(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	deadline := make(chan time.Time)

	var mu sync.Mutex
	failNext := false
	failed := make(chan struct{}, 1)
	idle := make(chan struct{}, 4)

	hooks := &waitTestHooks{
		deadline: deadline,
		onEvaluate: func() error {
			mu.Lock()
			shouldFail := failNext
			if shouldFail {
				failNext = false
			}
			mu.Unlock()
			if !shouldFail {
				return nil
			}
			select {
			case failed <- struct{}{}:
			default:
			}
			return fmt.Errorf("store: reading the event head: %w", lockingProtocolErr{})
		},
		onIdle: func(bool) {
			select {
			case idle <- struct{}{}:
			default:
			}
		},
	}

	type waitResult struct {
		code        int
		out, stderr string
	}
	var result waitResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		var out, stderr bytes.Buffer
		code := runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--no-notify"}, &out, &stderr, ticks, hooks)
		result = waitResult{code: code, out: out.String(), stderr: stderr.String()}
	}()
	defer func() {
		cancel()
		<-done
	}()

	select {
	case <-idle:
	case <-done:
		t.Fatalf("wait exited before becoming idle: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	}

	mu.Lock()
	failNext = true
	mu.Unlock()

	appendRawRefusalWithoutNotify(t, s.Path(), "item-unrelated")
	select {
	case ticks <- time.Now():
	case <-done:
		t.Fatalf("wait exited before tick: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	}

	select {
	case <-failed:
	case <-done:
		t.Fatalf("wait exited on locking-protocol error: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	case <-time.After(hangGuard):
		t.Fatal("wait did not reach failing evaluation")
	}

	// Wait for the retry pass to succeed and return to idle.
	select {
	case <-idle:
	case <-done:
		t.Fatalf("wait exited instead of riding out locking-protocol error: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	case <-time.After(hangGuard):
		t.Fatal("wait did not recover to idle after locking-protocol error")
	}

	select {
	case <-done:
		t.Fatalf("wait exited while it should still be waiting: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	default:
	}

	// Verify the waiter is still alive and delivers a subsequent alert.
	appendRawRefusalWithoutNotify(t, s.Path(), "item-waiter")
	appendRawRefusalWithoutNotify(t, s.Path(), "item-waiter")
	select {
	case ticks <- time.Now():
	case <-done:
	}
	select {
	case <-done:
	case <-time.After(hangGuard):
		t.Fatal("wait did not deliver alert after riding out locking-protocol error")
	}
	if result.code != 0 || result.stderr != "" || !strings.Contains(result.out, "item-waiter has 2 refusals") {
		t.Fatalf("wait after locking-protocol error: code=%d out=%q stderr=%q", result.code, result.out, result.stderr)
	}
}

func TestWatchWaitSkipsEvalAndStoreOpenWhenUnchanged(t *testing.T) {
	s, rules := fixture(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	deadline := make(chan time.Time)

	var passes, evals atomic.Int64
	idle := make(chan struct{}, 8)
	hooks := &waitTestHooks{
		deadline: deadline,
		onPass: func() {
			passes.Add(1)
		},
		onEvaluate: func() error {
			evals.Add(1)
			return nil
		},
		onIdle: func(bool) {
			select {
			case idle <- struct{}{}:
			default:
			}
		},
	}

	done := make(chan struct{})
	var code int
	var out, stderr bytes.Buffer
	go func() {
		defer close(done)
		code = runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--no-notify"}, &out, &stderr, ticks, hooks)
	}()
	defer func() {
		cancel()
		<-done
	}()

	select {
	case <-idle:
	case <-done:
		t.Fatalf("wait exited before becoming idle: code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	case <-time.After(hangGuard):
		t.Fatal("wait did not reach initial idle")
	}

	evalsAfterInit := evals.Load()
	passesAfterInit := passes.Load()
	if evalsAfterInit != 1 {
		t.Fatalf("expected 1 initial evaluation before BeginWait, got %d", evalsAfterInit)
	}

	// Count open file descriptors for the store before unchanged ticks.
	countStoreFDs := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Skipf("cannot inspect /proc/self/fd: %v", err)
		}
		target := filepath.Clean(s.Path())
		n := 0
		for _, e := range entries {
			link, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
			if err == nil && filepath.Clean(link) == target {
				n++
			}
		}
		return n
	}
	fdsBefore := countStoreFDs()

	for i := 0; i < 4; i++ {
		select {
		case ticks <- time.Now():
		case <-done:
			t.Fatalf("wait exited on tick %d: code=%d out=%q stderr=%q", i, code, out.String(), stderr.String())
		}
		select {
		case <-idle:
		case <-done:
			t.Fatalf("wait exited after tick %d: code=%d out=%q stderr=%q", i, code, out.String(), stderr.String())
		case <-time.After(hangGuard):
			t.Fatalf("wait did not return to idle after tick %d", i)
		}
	}

	if got := passes.Load(); got != passesAfterInit+4 {
		t.Fatalf("expected %d passes after 4 ticks, got %d", passesAfterInit+4, got)
	}
	if got := evals.Load(); got != evalsAfterInit {
		t.Fatalf("wait evaluated rules %d extra times when nothing in the store changed", got-evalsAfterInit)
	}
	if fdsAfter := countStoreFDs(); fdsAfter != fdsBefore {
		t.Fatalf("store open fd count changed from %d to %d when nothing in the store changed", fdsBefore, fdsAfter)
	}

	// Now commit a change and verify wait evaluates rules and delivers the alert.
	appendRawRefusalWithoutNotify(t, s.Path(), "item-changed")
	appendRawRefusalWithoutNotify(t, s.Path(), "item-changed")
	select {
	case ticks <- time.Now():
	case <-done:
	}
	select {
	case <-done:
		if code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "item-changed has 2 refusals") {
			t.Fatalf("wait after store change: code=%d out=%q stderr=%q", code, out.String(), stderr.String())
		}
	case <-time.After(hangGuard):
		t.Fatal("wait did not deliver alert after store change")
	}
	if got := evals.Load(); got <= evalsAfterInit {
		t.Fatalf("expected evaluation count to increase after store change, still %d", got)
	}
}

func TestWatchWaitPreservesEvalCacheUnderStoreWrites(t *testing.T) {
	const rulesDir = "rules"
	if err := event.Register("watch-bench.cache-unrelated", "watch-test"); err != nil {
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
		for i := 0; i < 4000; i++ {
			sess := "sess-cache"
			seq, err := tx.Append(event.Event{
				Kind:       "drain.loop-pass",
				ObjectType: "session",
				ObjectID:   &sess,
				At:         now.Add(-time.Duration(4000-i) * time.Second),
				Payload:    []byte(`{"session_id":"sess-cache","dispatched":0}`),
			})
			if err != nil {
				return err
			}
			seenHead = seq
		}
		_, err := tx.Exec(`INSERT INTO foreground_delivery(owner_handle, seen_seq, shown_seq) VALUES ('operator-session', ?, ?)
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
	time.Sleep(100 * time.Millisecond)

	startTicks := readProcCPUTicks(t, child.cmd.Process.Pid)
	startWall := time.Now()
	const commits = 60
	const window = 2 * time.Second
	for i := 0; i < commits; i++ {
		runID := "run-cache"
		if err := f.s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "watch-bench.cache-unrelated",
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
	const clkTck = 100.0
	coreFraction := (float64(endTicks-startTicks) / clkTck) / wall.Seconds()
	t.Logf("cached wait CPU: %.2f%% of one core (%d ticks over %.2fs across %d store commits)",
		coreFraction*100, endTicks-startTicks, wall.Seconds(), commits)
	if coreFraction >= 0.10 {
		t.Fatalf("wait used %.2f%% of one core (%d ticks over %.2fs), want < 10%% (evalCache lost?)",
			coreFraction*100, endTicks-startTicks, wall.Seconds())
	}
}

func TestWatchWaitStoreErrorExitsDistinctlyWithStoppedNotice(t *testing.T) {
	t.Run("mid-wait store error", func(t *testing.T) {
		s, rules := fixture(t, false)
		ctx, cancel := context.WithCancel(t.Context())
		ticks := make(chan time.Time)
		deadline := make(chan time.Time)
		idle := make(chan struct{}, 4)

		hooks := &waitTestHooks{
			deadline: deadline,
			onIdle: func(bool) {
				select {
				case idle <- struct{}{}:
				default:
				}
			},
		}

		var code int
		var out, stderr bytes.Buffer
		done := make(chan struct{})
		go func() {
			defer close(done)
			code = runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--no-notify"}, &out, &stderr, ticks, hooks)
		}()
		defer func() {
			cancel()
			<-done
		}()

		select {
		case <-idle:
		case <-done:
			t.Fatalf("wait exited before becoming idle: code=%d out=%q stderr=%q", code, out.String(), stderr.String())
		case <-time.After(hangGuard):
			t.Fatal("wait did not reach initial idle")
		}

		// Break the store schema from another connection so the next evaluation hits a real store error.
		rawDB, err := sql.Open("sqlite", s.Path()+"?_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rawDB.ExecContext(ctx, `DROP TABLE alert_episodes`); err != nil {
			rawDB.Close()
			t.Fatal(err)
		}
		rawDB.Close()

		select {
		case ticks <- time.Now():
		case <-done:
		}
		select {
		case <-done:
		case <-time.After(hangGuard):
			t.Fatal("wait did not exit after store error")
		}
		if code == 0 || code == 1 || code == 2 || code != record.ExitIncompatible {
			t.Fatalf("wait on store error: code=%d, want %d (distinct from alert=0, error/no-alert=1, quiet=2); out=%q stderr=%q",
				code, record.ExitIncompatible, out.String(), stderr.String())
		}
		if out.Len() != 0 {
			t.Fatalf("wait on store error wrote stdout %q, want empty", out.String())
		}
		msg := stderr.String()
		if !strings.Contains(msg, "waiter stopped") || !strings.Contains(msg, "alerts are not being delivered") {
			t.Fatalf("wait on store error stderr=%q, want line saying waiter stopped and alerts are not being delivered", msg)
		}
	})

	t.Run("startup store error", func(t *testing.T) {
		_, rules := fixture(t, false)
		corrupt := filepath.Join(t.TempDir(), "corrupt.db")
		if err := os.WriteFile(corrupt, []byte("not a sqlite database"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, stderr bytes.Buffer
		code := run(t.Context(), []string{"wait", "--store", corrupt, "--rules", rules, "--no-shipped-rules", "--no-notify"}, &out, &stderr)
		if code == 0 || code == 1 || code == 2 || code != record.ExitIncompatible {
			t.Fatalf("wait on corrupt store: code=%d, want %d; out=%q stderr=%q", code, record.ExitIncompatible, out.String(), stderr.String())
		}
		if out.Len() != 0 {
			t.Fatalf("wait on corrupt store wrote stdout %q, want empty", out.String())
		}
		msg := stderr.String()
		if !strings.Contains(msg, "waiter stopped") || !strings.Contains(msg, "alerts are not being delivered") {
			t.Fatalf("wait on corrupt store stderr=%q, want line saying waiter stopped and alerts are not being delivered", msg)
		}
	})
}

func TestWatchWaitWaitsForWriteLockAndDeliversNextAlert(t *testing.T) {
	s, rules := fixture(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	deadline := make(chan time.Time)
	idle := make(chan struct{}, 4)
	busyCh := make(chan error, 4)

	holderDB, err := sql.Open("sqlite", s.Path()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer holderDB.Close()
	holderConn, err := holderDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holderConn.Close()

	var mu sync.Mutex
	lockNextEval := false
	lockHeld := false
	defer func() {
		mu.Lock()
		held := lockHeld
		lockHeld = false
		mu.Unlock()
		if held {
			_, _ = holderConn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	hooks := &waitTestHooks{
		deadline: deadline,
		openOpts: []store.OpenOption{store.WithBusyTimeout(20 * time.Millisecond)},
		onEvaluate: func() error {
			mu.Lock()
			shouldLock := lockNextEval
			if shouldLock {
				lockNextEval = false
			}
			mu.Unlock()
			if !shouldLock {
				return nil
			}
			if _, err := holderConn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
				return err
			}
			mu.Lock()
			lockHeld = true
			mu.Unlock()
			return nil
		},
		onBusy: func(err error) {
			select {
			case busyCh <- err:
			default:
			}
		},
		onIdle: func(bool) {
			select {
			case idle <- struct{}{}:
			default:
			}
		},
	}

	var code int
	var out, stderr bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		code = runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--no-notify"}, &out, &stderr, ticks, hooks)
	}()
	defer func() {
		cancel()
		<-done
	}()

	select {
	case <-idle:
	case <-done:
		t.Fatalf("wait exited before becoming idle: code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	case <-time.After(hangGuard):
		t.Fatal("wait did not reach initial idle")
	}

	// Arm the write lock to be acquired from another connection right before the next evaluation.
	mu.Lock()
	lockNextEval = true
	mu.Unlock()

	// Append two refusals so EvaluateRules must open a write transaction to record the alert.
	appendRawRefusalWithoutNotify(t, s.Path(), "item-locked")
	appendRawRefusalWithoutNotify(t, s.Path(), "item-locked")
	select {
	case ticks <- time.Now():
	case <-done:
	default:
	}

	// Confirm the waiter actually hit SQLITE_BUSY when beginning its write transaction.
	var busyErr error
	select {
	case busyErr = <-busyCh:
	case <-done:
		t.Fatalf("wait exited on busy store instead of waiting: code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	case <-time.After(hangGuard):
		t.Fatal("wait did not observe busy write lock")
	}
	if !store.IsBusy(busyErr) || !strings.Contains(busyErr.Error(), "beginning the write transaction") {
		t.Fatalf("expected SQLITE_BUSY beginning write transaction, got %v", busyErr)
	}

	// Waiter must still be running while the lock is held.
	select {
	case <-done:
		t.Fatalf("wait exited while write lock was held: code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	default:
	}

	// Release the write lock and confirm the still-running waiter continues and delivers the alert.
	mu.Lock()
	lockHeld = false
	mu.Unlock()
	if _, err := holderConn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(hangGuard):
		t.Fatal("wait did not deliver alert after riding out write lock")
	}
	if code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "item-locked has 2 refusals") {
		t.Fatalf("wait after write lock: code=%d out=%q stderr=%q", code, out.String(), stderr.String())
	}
}
