package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func extensionSetOf(t *testing.T, payload []byte) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatal(err)
	}
	v, _ := body["extension_set"].(string)
	return v
}

func TestExecRecordsExtensionSetAndSnapshotsAgentDirs(t *testing.T) {
	ctx := context.Background()

	t.Run("derives agent dirs from caller YOLO_DURABLE_DIR and reads extension-set", func(t *testing.T) {
		s, root := leaseFixture(t)
		callerDurable := filepath.Join(root, "caller-durable")
		t.Setenv("TILLR_PI_AGENT_DIRS_DIR", "")
		// Ensure TILLR_PI_AGENT_DIRS_DIR is unset for this subtest so YOLO_DURABLE_DIR is used.
		os.Unsetenv("TILLR_PI_AGENT_DIRS_DIR")
		t.Setenv("YOLO_DURABLE_DIR", callerDurable)
		t.Setenv("TILLR_EXTENSION_SET_RECORD_FILE", filepath.Join(root, "stale-parent-record"))

		lanes := filepath.Join(root, "lanes")
		staleFile := filepath.Join(run.DurableDir(filepath.Join(lanes, "lane-ext"), "run-ext"), "extension-set")
		if err := os.MkdirAll(filepath.Dir(staleFile), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(staleFile, []byte("stale-value\n"), 0644); err != nil {
			t.Fatal(err)
		}

		seenEnv := filepath.Join(root, "seen-env")
		script := filepath.Join(root, "adapter.sh")
		body := "#!/bin/sh\nset -eu\n" +
			"printf 'DIRS=%s\\nREC=%s\\nDURABLE=%s\\n' \"${TILLR_PI_AGENT_DIRS_DIR:-}\" \"${TILLR_EXTENSION_SET_RECORD_FILE:-}\" \"${YOLO_DURABLE_DIR:-}\" > \"" + seenEnv + "\"\n" +
			"printf '/tmp/built/agent (set abc123def456)\\n' > \"$TILLR_EXTENSION_SET_RECORD_FILE\"\n"
		if err := os.WriteFile(script, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}

		env, err := run.Exec(ctx, s, run.Request{
			RunID:      "run-ext",
			LaneID:     "lane-ext",
			Backend:    "command",
			Command:    []string{script},
			WorkingDir: ".",
		}, run.ExecOptions{LaneRoot: lanes, Now: time.Now().UTC()})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}

		wantSet := "/tmp/built/agent (set abc123def456)"
		if got := extensionSetOf(t, env.Payload); got != wantSet {
			t.Fatalf("record extension_set = %q, want %q", got, wantSet)
		}

		rawEnv, err := os.ReadFile(seenEnv)
		if err != nil {
			t.Fatal(err)
		}
		wantDurable := run.DurableDir(filepath.Join(lanes, "lane-ext"), "run-ext")
		wantRec := filepath.Join(wantDurable, "extension-set")
		wantDirs := filepath.Join(callerDurable, "pi-agent-dirs")
		wantSeen := "DIRS=" + wantDirs + "\nREC=" + wantRec + "\nDURABLE=" + wantDurable + "\n"
		if string(rawEnv) != wantSeen {
			t.Fatalf("child env mismatch:\ngot:\n%s\nwant:\n%s", rawEnv, wantSeen)
		}

		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			status, found, err := run.StatusRecord(r, "run-ext")
			if err != nil || !found {
				t.Fatalf("StatusRecord: found=%v err=%v", found, err)
			}
			var sp struct {
				Run json.RawMessage `json:"run"`
			}
			if err := json.Unmarshal(status.Payload, &sp); err != nil {
				return err
			}
			if got := extensionSetOf(t, sp.Run); got != wantSet {
				t.Fatalf("status extension_set = %q, want %q", got, wantSet)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("preserves explicit empty TILLR_PI_AGENT_DIRS_DIR", func(t *testing.T) {
		s, root := leaseFixture(t)
		t.Setenv("TILLR_PI_AGENT_DIRS_DIR", "")
		t.Setenv("YOLO_DURABLE_DIR", filepath.Join(root, "caller-durable"))

		seenEnv := filepath.Join(root, "seen-empty")
		script := filepath.Join(root, "adapter-empty.sh")
		body := "#!/bin/sh\nset -eu\n" +
			"if [ \"${TILLR_PI_AGENT_DIRS_DIR+set}\" = set ]; then echo \"SET:${TILLR_PI_AGENT_DIRS_DIR}\"; else echo \"UNSET\"; fi > \"" + seenEnv + "\"\n"
		if err := os.WriteFile(script, []byte(body), 0755); err != nil {
			t.Fatal(err)
		}

		if _, err := run.Exec(ctx, s, run.Request{
			RunID:      "run-empty",
			LaneID:     "lane-empty",
			Backend:    "command",
			Command:    []string{script},
			WorkingDir: ".",
		}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), Now: time.Now().UTC()}); err != nil {
			t.Fatalf("Exec: %v", err)
		}
		raw, err := os.ReadFile(seenEnv)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != "SET:\n" {
			t.Fatalf("expected explicit empty TILLR_PI_AGENT_DIRS_DIR, got %q", string(raw))
		}
	})

	t.Run("clears stale extension-set file when new run writes none", func(t *testing.T) {
		s, root := leaseFixture(t)
		lanes := filepath.Join(root, "lanes")
		staleFile := filepath.Join(run.DurableDir(filepath.Join(lanes, "lane-stale"), "run-stale"), "extension-set")
		if err := os.MkdirAll(filepath.Dir(staleFile), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(staleFile, []byte("old-stale-set\n"), 0644); err != nil {
			t.Fatal(err)
		}
		env, err := run.Exec(ctx, s, run.Request{
			RunID:      "run-stale",
			LaneID:     "lane-stale",
			Backend:    "command",
			Command:    []string{"/bin/true"},
			WorkingDir: ".",
		}, run.ExecOptions{LaneRoot: lanes, Now: time.Now().UTC()})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		if got := extensionSetOf(t, env.Payload); got != "" {
			t.Fatalf("expected empty extension_set after clearing stale file, got %q", got)
		}
	})
}

func TestStatusRecordExtensionSetUnmigratedStoreFallback(t *testing.T) {
	ctx := context.Background()
	s, root := leaseFixture(t)
	lanes := filepath.Join(root, "lanes")

	script := filepath.Join(root, "write-set.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '/tmp/a/agent (set 001122334455)\\n' > \"$TILLR_EXTENSION_SET_RECORD_FILE\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Exec(ctx, s, run.Request{
		RunID:      "run-mig",
		LaneID:     "lane-mig",
		Backend:    "command",
		Command:    []string{script},
		WorkingDir: ".",
	}, run.ExecOptions{LaneRoot: lanes, Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`ALTER TABLE runs DROP COLUMN extension_set`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := store.Open(ctx, filepath.Join(root, "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		status, found, err := run.StatusRecord(r, "run-mig")
		if err != nil || !found {
			t.Fatalf("StatusRecord on unmigrated store: found=%v err=%v", found, err)
		}
		var sp struct {
			Run json.RawMessage `json:"run"`
		}
		if err := json.Unmarshal(status.Payload, &sp); err != nil {
			return err
		}
		if got := extensionSetOf(t, sp.Run); got != "/tmp/a/agent (set 001122334455)" {
			t.Fatalf("fallback extension_set = %q, want %q", got, "/tmp/a/agent (set 001122334455)")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
