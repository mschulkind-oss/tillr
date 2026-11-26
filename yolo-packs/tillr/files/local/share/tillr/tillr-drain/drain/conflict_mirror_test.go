package drain_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepoFileForTest(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGitForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, dir, args)
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestConflictWithToolsAndMirrorHandsOnlyToolsPathToWorkerAndRegeneratesMirror verifies
// that when a squash conflicts on both a tools/ file and its mirrored copy under
// yolo-packs/tillr/files/local/share/tillr/, the conflict-fix worker is given only the
// tools/ path (no mirror in paths, reports, or markers), and pack-sync regenerates the
// mirrored copy after the worker repairs the tools/ file.
func TestConflictWithToolsAndMirrorHandsOnlyToolsPathToWorkerAndRegeneratesMirror(t *testing.T) {
	const (
		sourcePath = "tools/tillr-drain/shared.go"
		mirrorPath = "yolo-packs/tillr/files/local/share/tillr/tillr-drain/shared.go"
	)

	// On run 1 the worker edits both tools/ and its mirror copy; on run 2 (conflict-fix)
	// the worker edits only tools/ and leaves the mirror untouched for pack-sync.
	marker := filepath.Join(scratch(t, "mirror-mixed-count"), "count")
	t.Setenv("MIRROR_MIXED_COUNT", marker)
	adapter := stub(t, "adapter", fmt.Sprintf(`n=0
if [ -f "$MIRROR_MIXED_COUNT" ]; then read -r n < "$MIRROR_MIXED_COUNT"; fi
n=$((n+1))
echo "$n" > "$MIRROR_MIXED_COUNT"
if [ "$n" -eq 1 ]; then
  printf 'package drain\n// candidate-v1\n' > %[1]q
  printf 'package drain\n// candidate-v1\n' > %[2]q
  git add %[1]q %[2]q
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m "feat: update shared"
else
  printf 'package drain\n// resolved-v2\n' > %[1]q
  git add %[1]q
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m "feat: resolve shared"
fi
printf 'RESULT: done\n'
`, sourcePath, mirrorPath))

	f := newLoopFixture(t, adapter)
	writeRepoFileForTest(t, f.root, "yolo-packs/tillr/pack.json", "{}\n")
	writeRepoFileForTest(t, f.root, sourcePath, "package drain\n// base\n")
	writeRepoFileForTest(t, f.root, mirrorPath, "package drain\n// base\n")
	runGitForTest(t, f.root, "add", ".")
	runGitForTest(t, f.root, "commit", "-q", "-m", "add tillr-drain source and pack mirror")

	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))

	conflict := landingEnvelope(t, "squash-conflict", []string{
		"CONFLICT (content): Merge conflict in " + sourcePath,
		"CONFLICT (content): Merge conflict in " + mirrorPath,
	})
	landed := landingEnvelope(t, "landed", nil)

	captureDir := t.TempDir()
	capturedSource := filepath.Join(captureDir, "source.go")
	capturedMirror := filepath.Join(captureDir, "mirror.go")
	landPath := filepath.Join(t.TempDir(), "land")
	landScript := fmt.Sprintf(`#!/bin/sh
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
head_sha=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--head" ]; then
    head_sha="$arg"
  fi
  prev="$arg"
done
if [ ! -f %[1]q/land-once ]; then
  touch %[1]q/land-once
  printf 'package drain\n// trunk-move\n' > %[1]q/%[2]s
  printf 'package drain\n// trunk-move\n' > %[1]q/%[3]s
  git -C %[1]q add %[2]q %[3]q
  git -C %[1]q commit -q -m "trunk move on shared and mirror"
  printf '%%s\n' '%[4]s'
  exit 1
fi
git -C %[1]q show "$head_sha:%[2]s" > %[6]q
git -C %[1]q show "$head_sha:%[3]s" > %[7]q
printf '%%s\n' '%[5]s'
exit 0
`, f.root, sourcePath, mirrorPath, conflict, landed, capturedSource, capturedMirror)
	if err := writeForkSafe(landPath, []byte(landScript), 0o755); err != nil {
		t.Fatalf("writing land script: %v", err)
	}

	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"tools/tillr-drain/", "yolo-packs/tillr/"}}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), landPath, always(t, "verdict", 0)), stage, os.Stderr)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop.Run failed: %v", err)
	}
	if result.ConflictFixes != 1 {
		t.Fatalf("ConflictFixes = %d, want 1: %s", result.ConflictFixes, result.Detail)
	}
	if result.Landed != 1 {
		t.Fatalf("Landed = %d, want 1: %s", result.Landed, result.Detail)
	}
	if len(stage.specs) != 2 {
		t.Fatalf("staged %d run(s), want 2 (original + conflict-fix): %+v", len(stage.specs), stage.specs)
	}
	fix := stage.specs[1]
	if fix.Conflict == nil {
		t.Fatal("conflict-fix spec has nil Conflict")
	}
	if len(fix.Conflict.Paths) != 1 || fix.Conflict.Paths[0] != sourcePath {
		t.Fatalf("conflict-fix paths = %v, want [%s]", fix.Conflict.Paths, sourcePath)
	}
	if len(fix.Conflict.Reports) != 1 || !strings.Contains(fix.Conflict.Reports[0], sourcePath) || strings.Contains(fix.Conflict.Reports[0], mirrorPath) {
		t.Fatalf("conflict-fix reports = %v, want only %s", fix.Conflict.Reports, sourcePath)
	}
	if !strings.Contains(fix.Conflict.Markers, "### "+sourcePath) {
		t.Fatalf("conflict-fix markers missing %s:\n%s", sourcePath, fix.Conflict.Markers)
	}
	if strings.Contains(fix.Conflict.Markers, mirrorPath) {
		t.Fatalf("conflict-fix markers unexpectedly include mirror path %s:\n%s", mirrorPath, fix.Conflict.Markers)
	}
	if !fix.Conflict.MirrorOmitted {
		t.Fatal("conflict-fix MirrorOmitted = false, want true")
	}

	gotSource, err := os.ReadFile(capturedSource)
	if err != nil {
		t.Fatalf("reading captured source: %v", err)
	}
	gotMirror, err := os.ReadFile(capturedMirror)
	if err != nil {
		t.Fatalf("reading captured mirror: %v", err)
	}
	const wantResolved = "package drain\n// resolved-v2\n"
	if string(gotSource) != wantResolved {
		t.Fatalf("landed source = %q, want %q", string(gotSource), wantResolved)
	}
	if string(gotMirror) != wantResolved {
		t.Fatalf("landed mirror = %q, want %q (regenerated by pack-sync)", string(gotMirror), wantResolved)
	}
}

// TestMirrorOnlyConflictRepairsWithoutWorkerRun verifies that when a conflict's only
// conflicting paths are mirrored copies under yolo-packs/tillr/files/local/share/tillr/,
// the drain repairs it mechanically onto the moved trunk and regenerates the mirror via
// pack-sync with no worker run.
func TestMirrorOnlyConflictRepairsWithoutWorkerRun(t *testing.T) {
	const (
		sourcePath = "tools/tillr-drain/shared.go"
		mirrorPath = "yolo-packs/tillr/files/local/share/tillr/tillr-drain/shared.go"
	)

	// Base file has enough context lines so top (candidate) and bottom (trunk) edits
	// three-way merge cleanly in tools/, while the mirror copy conflicts.
	baseSource := "package drain\n\n// line 1\n// line 2\n// line 3\n// line 4\n// line 5\n// line 6\n// line 7\n// line 8\n"
	candSource := "package drain\n// candidate-top\n\n// line 1\n// line 2\n// line 3\n// line 4\n// line 5\n// line 6\n// line 7\n// line 8\n"
	trunkSource := "package drain\n\n// line 1\n// line 2\n// line 3\n// line 4\n// line 5\n// line 6\n// line 7\n// line 8\n// trunk-bottom\n"
	wantMerged := "package drain\n// candidate-top\n\n// line 1\n// line 2\n// line 3\n// line 4\n// line 5\n// line 6\n// line 7\n// line 8\n// trunk-bottom\n"

	adapter := stub(t, "adapter", fmt.Sprintf(`cat <<'EOF' > %[1]q
%[3]sEOF
printf 'package drain\n// candidate-mirror-conflict\n' > %[2]q
git add %[1]q %[2]q
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m "feat: add top comment"
printf 'RESULT: done\n'
`, sourcePath, mirrorPath, candSource))

	f := newLoopFixture(t, adapter)
	writeRepoFileForTest(t, f.root, "yolo-packs/tillr/pack.json", "{}\n")
	writeRepoFileForTest(t, f.root, sourcePath, baseSource)
	writeRepoFileForTest(t, f.root, mirrorPath, "package drain\n// mirror-base\n")
	runGitForTest(t, f.root, "add", ".")
	runGitForTest(t, f.root, "commit", "-q", "-m", "add tillr-drain source and pack mirror")

	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))

	conflict := landingEnvelope(t, "squash-conflict", []string{
		"CONFLICT (content): Merge conflict in " + mirrorPath,
	})
	landed := landingEnvelope(t, "landed", nil)

	captureDir := t.TempDir()
	capturedSource := filepath.Join(captureDir, "source.go")
	capturedMirror := filepath.Join(captureDir, "mirror.go")
	landPath := filepath.Join(t.TempDir(), "land")
	landScript := fmt.Sprintf(`#!/bin/sh
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
head_sha=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--head" ]; then
    head_sha="$arg"
  fi
  prev="$arg"
done
if [ ! -f %[1]q/land-once ]; then
  touch %[1]q/land-once
  cat <<'EOF' > %[1]q/%[2]s
%[8]sEOF
  printf 'package drain\n// trunk-mirror-conflict\n' > %[1]q/%[3]s
  git -C %[1]q add %[2]q %[3]q
  git -C %[1]q commit -q -m "trunk move on bottom and mirror"
  printf '%%s\n' '%[4]s'
  exit 1
fi
git -C %[1]q show "$head_sha:%[2]s" > %[6]q
git -C %[1]q show "$head_sha:%[3]s" > %[7]q
printf '%%s\n' '%[5]s'
exit 0
`, f.root, sourcePath, mirrorPath, conflict, landed, capturedSource, capturedMirror, trunkSource)
	if err := writeForkSafe(landPath, []byte(landScript), 0o755); err != nil {
		t.Fatalf("writing land script: %v", err)
	}

	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"tools/tillr-drain/", "yolo-packs/tillr/"}}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), landPath, always(t, "verdict", 0)), stage, os.Stderr)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("loop.Run failed: %v", err)
	}
	if result.ConflictFixes != 0 {
		t.Fatalf("ConflictFixes = %d, want 0 (mirror-only conflict must not run a worker): %s", result.ConflictFixes, result.Detail)
	}
	if result.Dispatched != 1 || len(stage.specs) != 1 {
		t.Fatalf("Dispatched = %d, staged = %d, want 1 (no repair worker run): %s", result.Dispatched, len(stage.specs), result.Detail)
	}
	if result.Landed != 1 {
		t.Fatalf("Landed = %d, want 1: %s", result.Landed, result.Detail)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "landed" {
		t.Fatalf("item state = %q, want landed", got)
	}

	gotSource, err := os.ReadFile(capturedSource)
	if err != nil {
		t.Fatalf("reading captured source: %v", err)
	}
	gotMirror, err := os.ReadFile(capturedMirror)
	if err != nil {
		t.Fatalf("reading captured mirror: %v", err)
	}
	if string(gotSource) != wantMerged {
		t.Fatalf("repaired source = %q, want %q", string(gotSource), wantMerged)
	}
	if string(gotMirror) != wantMerged {
		t.Fatalf("repaired mirror = %q, want %q (regenerated by pack-sync)", string(gotMirror), wantMerged)
	}
}
