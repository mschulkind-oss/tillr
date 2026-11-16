package run_test

import (
	"context"
	"encoding/json"
	"path/filepath"
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
		{"wall", []string{"/bin/sleep", "10"}, 1000, "time-limit", false},
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
