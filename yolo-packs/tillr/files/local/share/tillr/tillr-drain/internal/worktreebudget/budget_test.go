package worktreebudget

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureLaneRootAndRunLogs(t *testing.T) {
	root := t.TempDir()
	laneRoot := filepath.Join(root, ".tillr", "items")

	// Hidden dir is skipped.
	if err := os.MkdirAll(filepath.Join(laneRoot, ".hidden", "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}

	// item-a: in-use with worktree (100B) and scratch (250B).
	wtA := filepath.Join(laneRoot, "item-a", "worktree")
	scA := filepath.Join(laneRoot, "item-a", "scratch", "run-a-1")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(scA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtA, "main.go"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scA, "build.bin"), make([]byte, 250), 0o644); err != nil {
		t.Fatal(err)
	}

	// item-b: idle with worktree (50B) and durable (400B).
	wtB := filepath.Join(laneRoot, "item-b", "worktree")
	duB := filepath.Join(laneRoot, "item-b", "durable", "run-b-1")
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(duB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtB, "file.txt"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(duB, "tmp.out"), make([]byte, 400), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run logs under .tillr/loop.
	logDir := filepath.Join(root, ".tillr", "loop", "run-a-1")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "run.log"), make([]byte, 777), 0o644); err != nil {
		t.Fatal(err)
	}

	snap, err := MeasureLaneRoot(laneRoot, map[string]bool{"item-a": true})
	if err != nil {
		t.Fatalf("MeasureLaneRoot: %v", err)
	}
	snap.RunLogBytes = MeasureRunLogs(root)

	if snap.OnDisk != 2 || snap.InUse != 1 || snap.Idle != 1 {
		t.Fatalf("counts = (onDisk=%d, inUse=%d, idle=%d), want (2, 1, 1)", snap.OnDisk, snap.InUse, snap.Idle)
	}
	if snap.WorktreeBytes != 150 || snap.ScratchBytes != 650 || snap.TotalBytes != 800 {
		t.Fatalf("bytes = (wt=%d, sc=%d, total=%d), want (150, 650, 800)", snap.WorktreeBytes, snap.ScratchBytes, snap.TotalBytes)
	}
	if snap.RunLogBytes != 777 {
		t.Fatalf("RunLogBytes = %d, want 777", snap.RunLogBytes)
	}
	if len(snap.LargestScratch) != 2 || snap.LargestScratch[0].RunID != "run-b-1" || snap.LargestScratch[0].Bytes != 400 {
		t.Fatalf("unexpected LargestScratch: %+v", snap.LargestScratch)
	}
}

func TestFormatBytes(t *testing.T) {
	if got := FormatBytes(512); got != "512 B" {
		t.Fatalf("FormatBytes(512) = %q", got)
	}
	if got := FormatBytes(2 * 1024 * 1024); got != "2.0 MiB" {
		t.Fatalf("FormatBytes(2MiB) = %q", got)
	}
	if got := FormatBytes(4 * 1024 * 1024 * 1024); got != "4.0 GiB" {
		t.Fatalf("FormatBytes(4GiB) = %q", got)
	}
}
