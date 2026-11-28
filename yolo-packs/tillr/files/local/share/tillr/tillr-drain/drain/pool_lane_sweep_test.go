package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// TestStartSweepRemovesUnneededPoolLaneDirsAndKeepsHeldOnes shows the start-time sweep
// under item-owned worktrees: a clean pool lane at a trunk commit and a scratch-only
// express directory are removed; a pool lane with uncommitted changes, one whose HEAD
// the trunk does not contain, and one named by preserved work are kept; a desk is not
// touched. The start report names both sets and why each kept lane stays.
func TestStartSweepRemovesUnneededPoolLaneDirsAndKeepsHeldOnes(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-c", "item-d")
	var report strings.Builder
	l.cfg.Report = &report
	l.cfg.ItemWorktrees = true
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4"}
	ctx := context.Background()

	// lane-2: uncommitted work.
	writeItemTestFile(t, filepath.Join(f.laneRoot, "lane-2", WorktreeDir, "wip.txt"), "unsaved\n")
	// lane-3: a committed HEAD the trunk does not contain.
	lane3 := filepath.Join(f.laneRoot, "lane-3", WorktreeDir)
	writeItemTestFile(t, filepath.Join(lane3, "unlanded.txt"), "unlanded\n")
	runGit(t, lane3, "add", "unlanded.txt")
	runGit(t, lane3, "commit", "-q", "-m", "unlanded")
	// lane-4: preserved work recorded against it.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, workPreservedKind, "run-kept", PreservedWork{RunID: "run-kept", LaneID: "lane-4", Ref: preservedPrefix + "run-kept", Repository: f.root}, l.cfg.Now())
	})
	// express-1: an old pool directory holding only scratch.
	writeItemTestFile(t, filepath.Join(f.laneRoot, "express-1", "scratch", "run-old", "note.txt"), "old\n")
	// A desk still stages under item-owned worktrees and is not a pool directory.
	writeItemTestFile(t, filepath.Join(f.laneRoot, "desk-docs", "keep.txt"), "desk\n")

	if err := l.stageLandingLanes(ctx); err != nil {
		t.Fatalf("stageLandingLanes: %v", err)
	}

	for _, lane := range []string{"lane-1", "express-1"} {
		if _, err := os.Stat(filepath.Join(f.laneRoot, lane)); !os.IsNotExist(err) {
			t.Fatalf("%s still on disk (err=%v), want removed\n%s", lane, err, report.String())
		}
	}
	for _, lane := range []string{"lane-2", "lane-3", "lane-4", "desk-docs"} {
		if _, err := os.Stat(filepath.Join(f.laneRoot, lane)); err != nil {
			t.Fatalf("%s removed (err=%v), want kept\n%s", lane, err, report.String())
		}
	}
	if list := runGit(t, f.root, "worktree", "list", "--porcelain"); strings.Contains(list, filepath.Join("lane-1", WorktreeDir)) {
		t.Fatalf("lane-1 worktree still registered:\n%s", list)
	}
	out := report.String()
	for _, want := range []string{
		"loop: pool lane directories: removed express-1, lane-1 (nothing needs them under item-owned worktrees); kept ",
		"lane-2 (worktree holds uncommitted changes)",
		"lane-3 (HEAD ",
		"is not on the trunk)",
		"lane-4 (holds preserved work from run-kept)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("start report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "desk-docs") {
		t.Fatalf("start report names the desk:\n%s", out)
	}
}
