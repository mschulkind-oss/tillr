package drain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRegistryLockCoversDetachedReplayAndLaneStaging(t *testing.T) {
	root, tip := heavyRepo(t)
	registryGuardGit(t, root)
	scratch := scratchUnderDurable(t)
	steps := &ProcessSteps{Root: root}
	work, cleanup, err := steps.detachedWorktree(context.Background(), filepath.Join(scratch, "replay"), tip)
	if err != nil {
		t.Fatal(err)
	}
	if head := heavyGit(t, work, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("replay=%s", head)
	}
	cleanup()
	stager := &LaneStager{Root: root, LaneRoot: filepath.Join(scratch, "lanes")}
	lane := filepath.Join(stager.LaneRoot, "lane-1")
	for i := 0; i < 2; i++ {
		if err := stager.ensureWorktree(context.Background(), StageSpec{LaneID: "lane-1", BaseSHA: tip}, lane, filepath.Join(lane, WorktreeDir)); err != nil {
			t.Fatal(err)
		}
	}
	// Startup reads persisted lane conditions before recording any new hold.
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	loop := &Loop{cfg: LoopConfig{Root: root, Store: s.Path(), Now: time.Now, LaneRoot: filepath.Join(scratch, "loop-lanes"), Lanes: []string{"desk-1"}}}
	if err := loop.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := loop.resetLaneTo(context.Background(), "desk-1", tip); err != nil {
		t.Fatal(err)
	}
}
