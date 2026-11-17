package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A lane is reused across items, and a fork-series item leaves `target` and `patch_ref`
// in the lane directory. Recorded 2026-11-17: lane-9 ran a pi fork item, was restaged
// for item-ig-view-nodes (fence tools/tillr-view/, docs/ ...), and kept the fork's
// target. The node write-scope check then diffed the trunk range 0d9eb9ce..caf4f9c3
// inside .forks/lanes/lane-9/pi, git refused the range, and the loop exited on every
// restart. Staging an ordinary item must clear what the fork item left behind.
func TestStagingAnOrdinaryItemClearsAForkTargetLeftInTheLane(t *testing.T) {
	f := newStagingFixture(t, "candidate.txt")
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	if err := os.MkdirAll(laneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(f.root, ".forks", "lanes", "lane-1", "pi")
	writeForkFile(t, filepath.Join(laneDir, TargetFile), fork+"\n")
	writeForkFile(t, filepath.Join(laneDir, PatchRefFile), "refs/heads/patch/pi\n")

	if err := f.stager().Stage(context.Background(), f.spec("run-item_041-1")); err != nil {
		t.Fatalf("staging the ordinary item failed: %v", err)
	}
	for _, name := range []string{TargetFile, PatchRefFile} {
		if _, err := os.Stat(filepath.Join(laneDir, name)); !os.IsNotExist(err) {
			t.Fatalf("staging an ordinary item left the earlier fork item's %s in the lane (stat error %v)", name, err)
		}
	}
	loop := &Loop{cfg: LoopConfig{Root: f.root, LaneRoot: f.laneRoot}}
	if got, want := loop.laneTarget("lane-1"), loop.lanePath("lane-1"); got != want {
		t.Fatalf("the lane's target is %q, want its own worktree %q", got, want)
	}
}

// A lane staged before that fix still carries the stale pair, and the loop must not
// crash on it after a restart: a target is the lane's only when the lane's own fence
// names a path inside it, which staging guarantees for every fork-series item.
func TestALaneTargetTheFenceDoesNotNameIsNotTheLanesTarget(t *testing.T) {
	root := scratchUnderDurable(t)
	laneRoot := filepath.Join(root, ".tillr", "lanes")
	laneDir := filepath.Join(laneRoot, "lane-9")
	if err := os.MkdirAll(laneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(root, ".forks", "lanes", "lane-9", "pi")
	writeForkFile(t, filepath.Join(laneDir, TargetFile), fork+"\n")
	writeForkFile(t, filepath.Join(laneDir, PatchRefFile), "refs/heads/patch/pi\n")
	writeForkFile(t, filepath.Join(laneDir, FenceFile), "tools/tillr-view/\ndocs/\n")
	loop := &Loop{cfg: LoopConfig{Root: root, LaneRoot: laneRoot}}

	if got, want := loop.laneTarget("lane-9"), loop.lanePath("lane-9"); got != want {
		t.Fatalf("a target the fence does not name was read as the lane's: got %q, want %q", got, want)
	}
	if ref := loop.lanePatchRef("lane-9"); ref != "" {
		t.Fatalf("an ordinary item's lane answered patch ref %q; its landing would move a fork branch", ref)
	}

	// The control: the same files with a fence inside the checkout are a fork lane.
	for _, fence := range []string{".forks/lanes/lane-9/pi/\n", ".forks/lanes/lane-9/pi/src/\n", ".forks/lanes/lane-9/pi\n"} {
		writeForkFile(t, filepath.Join(laneDir, FenceFile), fence)
		if got := loop.laneTarget("lane-9"); got != fork {
			t.Fatalf("fence %q: the fork lane's target is %q, want %q", fence, got, fork)
		}
		if ref := loop.lanePatchRef("lane-9"); ref != "refs/heads/patch/pi" {
			t.Fatalf("fence %q: the fork lane's patch ref is %q", fence, ref)
		}
	}
	// The whole-item fence wins over a node's narrower one, as it does for the fence check.
	writeForkFile(t, filepath.Join(laneDir, FenceFile), "docs/\n")
	writeForkFile(t, filepath.Join(laneDir, ItemFenceFile), ".forks/lanes/lane-9/pi/\n")
	if got := loop.laneTarget("lane-9"); got != fork {
		t.Fatalf("the item fence names the checkout, but the target read was %q", got)
	}
}
