package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func endCause(t *testing.T, payload []byte) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	cause, _ := body["end_cause"].(string)
	return cause
}

func TestTerminalEndCauses(t *testing.T) {
	for _, tc := range []struct {
		name         string
		command      []string
		timeout      int
		cause        string
		startFailure bool
	}{
		{"success", []string{"/bin/true"}, 0, "normal-exit", false},
		{"nonzero", []string{"/bin/sh", "-c", "exit 7"}, 0, "normal-exit", false},
		{"signal", []string{"/bin/sh", "-c", "kill -TERM $$"}, 0, "signal", false},
		{"wall", []string{"/bin/sleep", "10"}, 10, "time-limit", false},
		{"missing", []string{"/no/such/tillr-command"}, 0, "failed-to-start", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, root := leaseFixture(t)
			env, err := run.Exec(context.Background(), s, run.Request{RunID: "run-cause", LaneID: "lane-cause", Backend: "fixture", Command: tc.command, WorkingDir: ".", TimeoutMS: tc.timeout}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), Now: time.Now().UTC()})
			if (err != nil) != tc.startFailure {
				t.Fatalf("Exec: %v", err)
			}
			if got := endCause(t, env.Payload); got != tc.cause {
				t.Errorf("record end cause %q, want %q", got, tc.cause)
			}
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				status, found, err := run.StatusRecord(r, "run-cause")
				if err != nil {
					return err
				}
				if !found {
					t.Fatal("run missing")
				}
				var body struct {
					Run json.RawMessage `json:"run"`
				}
				if err := json.Unmarshal(status.Payload, &body); err != nil {
					return err
				}
				if got := endCause(t, body.Run); got != tc.cause {
					t.Errorf("status end cause %q, want %q", got, tc.cause)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			events, err := eventsOf(t, s)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Kind == run.FinishKind && endCause(t, event.Payload) != tc.cause {
					t.Errorf("finish event dropped cause: %s", event.Payload)
				}
			}
		})
	}
}

func TestReclaimedLeaseEndCause(t *testing.T) {
	s, _ := leaseFixture(t)
	now := time.Now().UTC()
	until := claim(t, s, "lane-cause", "run-cause", now, 1)
	releases := reap(t, s, time.UnixMilli(until))
	if len(releases) != 1 || !releases[0].TerminalRecord {
		t.Fatalf("releases: %+v", releases)
	}
	events, err := eventsOf(t, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == run.FinishKind && endCause(t, event.Payload) != "lease-reclaimed" {
			t.Errorf("reclaimed run cause: %s", event.Payload)
		}
	}
}

func TestLegacyEndCauseIsNotInferred(t *testing.T) {
	s, root := leaseFixture(t)
	ctx := context.Background()
	env, err := run.Exec(ctx, s, run.Request{RunID: "run-old", Backend: "fixture", Command: []string{"/bin/true"}, WorkingDir: "."}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes")})
	if err != nil {
		t.Fatal(err)
	}
	// Replay an old record: its state and code cannot distinguish all end causes.
	var old map[string]any
	if err := json.Unmarshal(env.Payload, &old); err != nil {
		t.Fatal(err)
	}
	bad := env
	old["end_cause"] = "invented-cause"
	bad.Payload, _ = json.Marshal(old)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := run.Apply(ctx, tx, bad)
		return err
	}); err == nil || run.ExitCode(err) != record.ExitUsage {
		t.Fatalf("invalid end cause was not refused as malformed input: %v", err)
	}
	delete(old, "end_cause")
	env.Payload, _ = json.Marshal(old)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := run.Apply(ctx, tx, env); return err }); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			status, _, err := run.StatusRecord(r, "run-old")
			if err != nil {
				return err
			}
			var body struct {
				Run json.RawMessage `json:"run"`
			}
			if err := json.Unmarshal(status.Payload, &body); err != nil {
				return err
			}
			if got := endCause(t, body.Run); got != "end cause not recorded" {
				t.Errorf("legacy cause %q", got)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check()
	// A read of an unmigrated store must also be honest without writing a migration.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { _, err := tx.Exec(`ALTER TABLE runs DROP COLUMN end_cause`); return err }); err != nil {
		t.Fatal(err)
	}
	check()
	// Write-open adds the nullable column back and leaves the old cause unknown.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, filepath.Join(root, "board.sqlite3"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check()
}

func TestSignalVsCrashEndCauses(t *testing.T) {
	endedJSON := `{"type":"tillr_harness_ended","after":"agent_settled","actions":["sent SIGTERM to pi 1234"],"harness_exit":143}`
	for _, tc := range []struct {
		name      string
		command   []string
		timeout   int
		wantState string
		wantCause string
		wantCode  *int
		wantEx    bool
	}{
		{
			name:      "direct-sigterm",
			command:   []string{"/bin/sh", "-c", "kill -TERM $$"},
			wantState: run.StateExited,
			wantCause: run.EndSignal,
			wantCode:  intPtr(-1),
		},
		{
			name: "direct-sigint",
			// A process inherits an ignored SIGINT across exec, and a test started from a
			// background job has it ignored, so the fixture restores the default first.
			command:   []string{"python3", "-c", "import os, signal; signal.signal(signal.SIGINT, signal.SIG_DFL); os.kill(os.getpid(), signal.SIGINT)"},
			wantState: run.StateExited,
			wantCause: run.EndSignal,
			wantCode:  intPtr(-1),
		},
		{
			name:      "adapter-exit-143",
			command:   []string{"/bin/sh", "-c", "exit 143"},
			wantState: run.StateExited,
			wantCause: run.EndSignal,
			wantCode:  intPtr(143),
		},
		{
			name:      "adapter-exit-130",
			command:   []string{"/bin/sh", "-c", "exit 130"},
			wantState: run.StateExited,
			wantCause: run.EndSignal,
			wantCode:  intPtr(130),
		},
		{
			name:      "crash-sigabrt",
			command:   []string{"/bin/sh", "-c", "kill -ABRT $$"},
			wantState: run.StateExited,
			wantCause: run.EndNormalExit,
			wantCode:  intPtr(-1),
		},
		{
			name:      "unrequested-sigkill",
			command:   []string{"/bin/sh", "-c", "kill -KILL $$"},
			wantState: run.StateExited,
			wantCause: run.EndNormalExit,
			wantCode:  intPtr(-1),
		},
		{
			name:      "ending-exception-exit-0",
			command:   []string{"/bin/sh", "-c", "printf '%s\\n' '" + endedJSON + "'"},
			wantState: run.StateExited,
			wantCause: run.EndNormalExit,
			wantCode:  intPtr(0),
			wantEx:    true,
		},
		{
			name:      "ending-exception-exit-143",
			command:   []string{"/bin/sh", "-c", "printf '%s\\n' '" + endedJSON + "'; exit 143"},
			wantState: run.StateExited,
			wantCause: run.EndNormalExit,
			wantCode:  intPtr(143),
			wantEx:    true,
		},
		{
			name:      "wall-timeout",
			command:   []string{"/bin/sleep", "10"},
			timeout:   10,
			wantState: run.StateTimedOut,
			wantCause: run.EndTimeLimit,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, root := leaseFixture(t)
			env, err := run.Exec(context.Background(), s, run.Request{
				RunID:      "run-" + tc.name,
				LaneID:     "lane-cause",
				Backend:    "fixture",
				Command:    tc.command,
				WorkingDir: ".",
				TimeoutMS:  tc.timeout,
			}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), Now: time.Now().UTC()})
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			var rec run.Record
			if err := json.Unmarshal(env.Payload, &rec); err != nil {
				t.Fatal(err)
			}
			if rec.State != tc.wantState {
				t.Errorf("state = %q, want %q", rec.State, tc.wantState)
			}
			if rec.EndCause != tc.wantCause {
				t.Errorf("end_cause = %q, want %q", rec.EndCause, tc.wantCause)
			}
			if tc.wantCode == nil {
				if rec.ExitCode != nil {
					t.Errorf("exit_code = %d, want nil", *rec.ExitCode)
				}
			} else if rec.ExitCode == nil || *rec.ExitCode != *tc.wantCode {
				t.Errorf("exit_code = %v, want %d", rec.ExitCode, *tc.wantCode)
			}
			if tc.wantEx && rec.EndingException == "" {
				t.Errorf("ending_exception = empty, want non-empty")
			}
			if !tc.wantEx && rec.EndingException != "" {
				t.Errorf("ending_exception = %q, want empty", rec.EndingException)
			}
		})
	}
}

func TestCanceledContextEndCauseIsSignal(t *testing.T) {
	s, root := leaseFixture(t)
	fifo := filepath.Join(t.TempDir(), "ready.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		env record.Envelope
		err error
	}
	done := make(chan result, 1)
	go func() {
		env, err := run.Exec(ctx, s, run.Request{
			RunID:      "run-canceled",
			LaneID:     "lane-cause",
			Backend:    "fixture",
			Command:    []string{"/bin/sh", "-c", `printf ready > "$1"; exec sleep 60`, "_", fifo},
			WorkingDir: ".",
		}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), Now: time.Now().UTC()})
		done <- result{env: env, err: err}
	}()

	f, err := os.OpenFile(fifo, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := f.Read(buf); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	cancel()

	res := <-done
	if res.err != nil {
		t.Fatalf("Exec: %v", res.err)
	}
	var rec run.Record
	if err := json.Unmarshal(res.env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.State != run.StateExited || rec.EndCause != run.EndSignal {
		t.Fatalf("record = %s/%s, want exited/signal", rec.State, rec.EndCause)
	}
}

func intPtr(v int) *int { return &v }
