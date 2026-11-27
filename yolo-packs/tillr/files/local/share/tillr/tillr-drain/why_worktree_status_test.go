package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/internal/worktreebudget"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyWorktreeStatusReportsCountsAndBytes(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	storePath := filepath.Join(root, ".tillr", "state.db")
	laneRoot := filepath.Join(root, ".tillr", "items")

	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[worktrees]\nmax_count = 6\ndisk_budget_bytes = 1048576\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create 1 in-use item worktree + scratch, 1 idle item worktree + scratch, and 1 run log.
	if err := os.MkdirAll(filepath.Join(laneRoot, "item-live", "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(laneRoot, "item-live", "scratch", "run-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneRoot, "item-live", "worktree", "a.go"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneRoot, "item-live", "scratch", "run-1", "out.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(laneRoot, "item-idle", "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneRoot, "item-idle", "worktree", "b.go"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".tillr", "loop", "run-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr", "loop", "run-1", "run-1.log"), make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, id := range []string{"lane-1", "lane-2"} {
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES(?, ?)`, id, run.LaneStateIdle); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('item-live', 'leased', 'run-1', ?)`, now+60000); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Test human output.
	code, outBytes := invoke(t, "why", "--root", root, "--store", storePath)
	out := string(outBytes)
	if code != 0 {
		t.Fatalf("why human exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "worktrees: 2 on disk (1 in use, 1 idle; limit 6)") {
		t.Fatalf("human output missing worktrees summary:\n%s", out)
	}
	if !strings.Contains(out, "512 B run logs") {
		t.Fatalf("human output missing run logs bytes:\n%s", out)
	}

	// Test JSON output.
	code, jsonOut := invoke(t, "why", "--root", root, "--store", storePath, "--json")
	if code != 0 {
		t.Fatalf("why json exit %d:\n%s", code, jsonOut)
	}
	var parsed struct {
		drain.WhyAnswer
	}
	if err := json.Unmarshal(jsonOut, &parsed); err != nil {
		t.Fatalf("unmarshal why json: %v\n%s", err, jsonOut)
	}
	if parsed.Worktrees == nil {
		t.Fatalf("expected Worktrees in JSON output, got nil:\n%s", jsonOut)
	}
	want := worktreebudget.Snapshot{
		OnDisk:          2,
		InUse:           1,
		Idle:            1,
		CountLimit:      6,
		WorktreeBytes:   3072,
		ScratchBytes:    4096,
		TotalBytes:      7168,
		DiskBudgetBytes: 1048576,
		RunLogBytes:     512,
	}
	if parsed.Worktrees.OnDisk != want.OnDisk ||
		parsed.Worktrees.InUse != want.InUse ||
		parsed.Worktrees.Idle != want.Idle ||
		parsed.Worktrees.CountLimit != want.CountLimit ||
		parsed.Worktrees.WorktreeBytes != want.WorktreeBytes ||
		parsed.Worktrees.ScratchBytes != want.ScratchBytes ||
		parsed.Worktrees.TotalBytes != want.TotalBytes ||
		parsed.Worktrees.DiskBudgetBytes != want.DiskBudgetBytes ||
		parsed.Worktrees.RunLogBytes != want.RunLogBytes {
		t.Fatalf("JSON Worktrees = %+v, want %+v", *parsed.Worktrees, want)
	}
}
