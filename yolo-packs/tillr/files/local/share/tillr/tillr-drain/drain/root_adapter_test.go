package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestLoopRunsRelativeAdapterFromRootNotLane(t *testing.T) {
	const relAdapter = "tools/tillr-run/backends/pi/run-pi"
	f := newLoopFixture(t, relAdapter)

	marker := filepath.Join(scratch(t, "adapter-marker"), "which")
	t.Setenv("ADAPTER_MARKER", marker)

	rootAdapter := filepath.Join(f.root, relAdapter)
	if err := os.MkdirAll(filepath.Dir(rootAdapter), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(filepath.Dir(rootAdapter), "pause-kind"), "stops-where-it-stands\n")
	writeFile(t, filepath.Join(filepath.Dir(rootAdapter), "extract-report"),
		"#!/bin/sh\nfor a in \"$@\"; do if [ \"$prev\" = --out ]; then out=$a; fi; prev=$a; done\nprintf '# run report\\ntokens: 10\\ncost usd: 0.01\\n---\\n' > \"$out\"\n")
	if err := os.Chmod(filepath.Join(filepath.Dir(rootAdapter), "extract-report"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Commit v1 (the stale lane copy) on main and materialize lane-1's worktree at v1.
	writeFile(t, rootAdapter, "#!/bin/sh\nprintf 'lane\\n' >> \"$ADAPTER_MARKER\"\nexit 1\n")
	if err := os.Chmod(rootAdapter, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, f.root, "add", "tools/tillr-run/backends/pi")
	git(t, f.root, "commit", "-m", "v1 adapter")

	laneWorktree := filepath.Join(f.laneRoot, laneName(1), "worktree")
	if err := os.MkdirAll(filepath.Dir(laneWorktree), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, f.root, "worktree", "add", "--detach", laneWorktree, "main")

	// Advance main in f.root to v2 (the trunk copy); lane-1's worktree still holds v1.
	writeFile(t, rootAdapter, `#!/bin/sh
printf 'root\n' >> "$ADAPTER_MARKER"
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
`)
	if err := os.Chmod(rootAdapter, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, f.root, "add", relAdapter)
	git(t, f.root, "commit", "-m", "v2 adapter")

	fixtureAt(t, f.store, fencedItemRecord("item_001", "in-queue", "candidate.txt"))
	fixtureTerminalEvidence(t, f.store, "item_001")

	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	loop := f.newLoop(t, 1, steps)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 1 {
		t.Fatalf("got stop=%q landed=%d (%s), want queue-drained and 1", result.Stop, result.Landed, result.Detail)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading adapter marker: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("adapter marker is empty")
	}
	for _, line := range lines {
		if line != "root" {
			t.Fatalf("adapter marker = %q, want only %q (lane copy ran instead of root)", string(got), "root")
		}
	}
	if result.Tokens != int64(10*len(lines)) {
		t.Fatalf("tokens = %d, want %d (backends dir not resolved against root)", result.Tokens, 10*len(lines))
	}
}

func TestLoopRunsRelativeAdapterFromInstallRootInConsumerRepo(t *testing.T) {
	const relAdapter = "tools/tillr-run/backends/pi/run-pi"
	f := newLoopFixture(t, relAdapter)

	installRoot := scratch(t, "install-root")
	installedDir := filepath.Join(installRoot, "tillr-run", "backends", "pi")
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(scratch(t, "install-marker"), "which")
	t.Setenv("ADAPTER_MARKER", marker)
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)

	writeFile(t, filepath.Join(installedDir, "pause-kind"), "stops-where-it-stands\n")
	writeFile(t, filepath.Join(installedDir, "extract-report"),
		"#!/bin/sh\nfor a in \"$@\"; do if [ \"$prev\" = --out ]; then out=$a; fi; prev=$a; done\nprintf '# run report\\ntokens: 10\\ncost usd: 0.01\\n---\\n' > \"$out\"\n")
	if err := os.Chmod(filepath.Join(installedDir, "extract-report"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedAdapter := filepath.Join(installedDir, "run-pi")
	writeFile(t, installedAdapter, `#!/bin/sh
printf 'install\n' >> "$ADAPTER_MARKER"
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
`)
	if err := os.Chmod(installedAdapter, 0o755); err != nil {
		t.Fatal(err)
	}

	// Confirm the consumer repository has no tools/ directory.
	if _, err := os.Stat(filepath.Join(f.root, "tools")); !os.IsNotExist(err) {
		t.Fatalf("expected consumer root without tools/, stat err = %v", err)
	}

	fixtureAt(t, f.store, fencedItemRecord("item_001", "in-queue", "candidate.txt"))
	fixtureTerminalEvidence(t, f.store, "item_001")

	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	loop := f.newLoop(t, 1, steps)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 1 {
		t.Fatalf("got stop=%q landed=%d (%s), want queue-drained and 1", result.Stop, result.Landed, result.Detail)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading adapter marker: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) == 0 || lines[0] != "install" {
		t.Fatalf("adapter marker = %q, want install", string(got))
	}
	if result.Tokens != int64(10*len(lines)) {
		t.Fatalf("tokens = %d, want %d (extract-report not resolved against install root)", result.Tokens, 10*len(lines))
	}
}
