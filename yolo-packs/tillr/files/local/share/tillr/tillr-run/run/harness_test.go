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

func TestExecPassesPiBuildsDirAndRecordsHarness(t *testing.T) {
	ctx := context.Background()
	dir := scratch(t)
	s, err := store.InitFixture(ctx, filepath.Join(dir, "fixture.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	callerDurable := filepath.Join(dir, "jail-durable")
	t.Setenv("YOLO_DURABLE_DIR", callerDurable)
	t.Setenv("TILLR_PI_BUILDS_DIR", "")
	if err := os.Unsetenv("TILLR_PI_BUILDS_DIR"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_HARNESS_RECORD_FILE", "stale-parent-file")

	stub := filepath.Join(dir, "stub-adapter")
	body := `#!/bin/sh
set -eu
printf 'builds=%s\ndurable=%s\nrecord=%s\n' "${TILLR_PI_BUILDS_DIR:-}" "${YOLO_DURABLE_DIR:-}" "${TILLR_HARNESS_RECORD_FILE:-}"
if [ -n "${STUB_HARNESS:-}" ]; then
    printf '%s\n' "$STUB_HARNESS" > "$TILLR_HARNESS_RECORD_FILE"
fi
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	laneRoot := filepath.Join(dir, "lanes")
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	t.Run("records rebuilt pi path and series key", func(t *testing.T) {
		wantHarness := "/workspace/.yolo/durable/pi-builds/9f4cb487c31b/home/.local/bin/pi (series 9f4cb487c31b)"
		t.Setenv("STUB_HARNESS", wantHarness)
		req := run.Request{
			RunID:      "run-rebuilt",
			ItemID:     "item-1",
			LaneID:     "lane-1",
			Backend:    "pi",
			Command:    []string{stub},
			WorkingDir: "worktree",
		}
		env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		rec := recordOf(t, env)
		if rec.Harness != wantHarness {
			t.Fatalf("record harness = %q, want %q", rec.Harness, wantHarness)
		}
		logRaw, err := os.ReadFile(filepath.Join(laneRoot, "lane-1", "run-rebuilt.log"))
		if err != nil {
			t.Fatal(err)
		}
		wantBuilds := filepath.Join(callerDurable, "pi-builds")
		runDurable := run.DurableDir(filepath.Join(laneRoot, "lane-1"), "run-rebuilt")
		wantLog := "builds=" + wantBuilds + "\ndurable=" + runDurable + "\nrecord=" + filepath.Join(runDurable, "harness") + "\n"
		if string(logRaw) != wantLog {
			t.Fatalf("child env log:\n%s\nwant:\n%s", logRaw, wantLog)
		}

		// Read back from StatusRecord and directly from the runs table.
		stored, found := storedRun(t, s, "run-rebuilt")
		if !found {
			t.Fatal("run-rebuilt missing from store")
		}
		if stored.Harness != wantHarness {
			t.Fatalf("status harness = %q, want %q", stored.Harness, wantHarness)
		}
		var colHarness string
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT COALESCE(harness, '') FROM runs WHERE run_id = ?`, "run-rebuilt").Scan(&colHarness)
		}); err != nil {
			t.Fatal(err)
		}
		if colHarness != wantHarness {
			t.Fatalf("runs.harness column = %q, want %q", colHarness, wantHarness)
		}
	})

	t.Run("omits harness when adapter writes nothing", func(t *testing.T) {
		t.Setenv("STUB_HARNESS", "")
		req := run.Request{
			RunID:      "run-other",
			ItemID:     "item-2",
			LaneID:     "lane-1",
			Backend:    "claude",
			Command:    []string{stub},
			WorkingDir: "worktree",
		}
		env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(env.Payload, &raw); err != nil {
			t.Fatal(err)
		}
		if v, ok := raw["harness"]; ok {
			t.Fatalf("expected no harness key for adapter that writes none, got %s", v)
		}
		var isNull bool
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT harness IS NULL FROM runs WHERE run_id = ?`, "run-other").Scan(&isNull)
		}); err != nil {
			t.Fatal(err)
		}
		if !isNull {
			t.Fatal("expected NULL runs.harness when adapter writes nothing")
		}
	})

	t.Run("unmigrated read-only store without harness column stays readable", func(t *testing.T) {
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`ALTER TABLE runs DROP COLUMN harness`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		stored, found := storedRun(t, s, "run-rebuilt")
		if !found {
			t.Fatal("run-rebuilt missing after dropping harness column")
		}
		if stored.Harness != "/workspace/.yolo/durable/pi-builds/9f4cb487c31b/home/.local/bin/pi (series 9f4cb487c31b)" {
			t.Fatalf("status harness from finish event = %q", stored.Harness)
		}
	})
}
