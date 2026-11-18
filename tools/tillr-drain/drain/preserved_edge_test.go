package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestOrphanedWorkflowPreservesBeforeRestart(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	work := pendingForTest(t, f, "item-a")
	if _, err := l.stageDispatches(context.Background(), []Decision{work.Decision}, work.BaseSHA, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	target := l.laneTarget("lane-1")
	writeForkFile(t, filepath.Join(target, "base.txt"), "orphaned tracked work\n")
	writeForkFile(t, filepath.Join(target, "unfinished-new.txt"), "orphaned untracked work\n")
	writeForkFile(t, filepath.Join(target, ".gitignore"), "ignored.txt\n")
	writeForkFile(t, filepath.Join(target, "ignored.txt"), "ignored work must not be cleaned away\n")
	next := replaceGraphLoop(t, f, l, steps)
	graphPass(t, next, &LoopResult{})
	fixtureRead(t, f, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		if len(saved) != 1 {
			t.Fatalf("orphan restart discarded its work: %+v", saved)
		}
		for path, want := range map[string]string{"base.txt": "orphaned tracked work", "unfinished-new.txt": "orphaned untracked work"} {
			if got := runGit(t, f.root, "show", saved[0].Ref+":"+path); got != want {
				t.Fatalf("%s lost: %q", path, got)
			}
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(target, "ignored.txt")); err != nil {
		t.Fatalf("ignored files outside the snapshot were deleted: %v", err)
	}
}

func TestTerminalReplayCannotRecreatePrunedRef(t *testing.T) {
	f, l, d, _ := savedFixture(t)
	ctx := context.Background()
	writeForkFile(t, filepath.Join(l.laneTarget(d.LaneID), "untracked.txt"), "saved untracked work\n")
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, l.headOf(ctx, d.LaneID)); stop != "" {
		t.Fatal(stop)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`)
		return err
	})
	if err := l.prunePreservedWork(ctx); err != nil {
		t.Fatal(err)
	}
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, l.headOf(ctx, d.LaneID)); stop != "" {
		t.Fatal(stop)
	}
	if err := l.prunePreservedWork(ctx); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, f.root, "for-each-ref", preservedPrefix+d.RunRequest.RunID); got != "" {
		t.Fatal("terminal replay recreated an already-pruned ref")
	}
}

func TestStagedOnlyWorkSurvivesSettlement(t *testing.T) {
	f, l, d, _ := savedFixture(t)
	ctx := context.Background()
	target := l.laneTarget(d.LaneID)
	runGit(t, target, "add", "base.txt")
	writeForkFile(t, filepath.Join(target, "base.txt"), "base\n")
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, l.headOf(ctx, d.LaneID)); stop != "" {
		t.Fatal(stop)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		if len(saved) != 1 || saved[0].IndexRef != preservedPrefix+d.RunRequest.RunID+"^2" || len(saved[0].IndexFiles) != 1 {
			t.Fatalf("staged edits disappeared: %+v", saved)
		}
		facts, err := preservedFacts(ctx, f.storePath, StageSpec{ItemID: d.ItemID})
		if err != nil || !strings.Contains(facts, saved[0].IndexRef) {
			t.Fatalf("brief omitted staged revision: %s / %v", facts, err)
		}
		// The working copy and the index had different bytes. Both must survive.
		if got := runGit(t, f.root, "show", saved[0].Ref+":base.txt"); got != "base" {
			t.Fatal(got)
		}
		if got := runGit(t, f.root, "show", saved[0].Ref+"^2:base.txt"); got != "earlier work" {
			t.Fatal(got)
		}
		return nil
	})
}

func TestNewContentsAfterSnapshotRetainLane(t *testing.T) {
	f, l, d, _ := savedFixture(t)
	ctx := context.Background()
	target := l.laneTarget(d.LaneID)
	if err := l.preserveWork(ctx, d.LaneID, d.RunRequest.RunID, "blocked"); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(target, "base.txt"), "newer work\n")
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, l.headOf(ctx, d.LaneID)); stop != stopLaneRetained {
		t.Fatalf("stale snapshot permitted reset: %q", stop)
	}
	body, err := os.ReadFile(filepath.Join(target, "base.txt"))
	if err != nil || string(body) != "newer work\n" {
		t.Fatalf("newer work lost: %q / %v", body, err)
	}
	if got := runGit(t, f.root, "show", preservedPrefix+d.RunRequest.RunID+":base.txt"); got != "earlier work" {
		t.Fatalf("original evidence overwritten: %q", got)
	}
}

func TestConflictRepairPreservesDirtyOriginal(t *testing.T) {
	f, l, d, _ := savedFixture(t)
	ctx := context.Background()
	target := l.laneTarget(d.LaneID)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, target, "add", "base.txt")
	runGit(t, target, "commit", "-qm", "candidate")
	head := runGit(t, target, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(target, "base.txt"), "uncommitted after candidate\n")
	writeForkFile(t, filepath.Join(f.root, "moved-trunk.txt"), "trunk moved\n")
	runGit(t, f.root, "add", "moved-trunk.txt")
	runGit(t, f.root, "commit", "-qm", "trunk moved")
	l.cfg.Stage = &itemContentStager{refusing: d.ItemID}
	c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, Request: d.RunRequest}
	_, _, err := l.conflictFix(ctx, c, LandOutcome{ConflictPaths: []string{"CONFLICT (content): Merge conflict in base.txt"}}, &LoopResult{}, &PassTiming{})
	if err == nil {
		t.Fatal("expected injected staging refusal")
	}
	if got := runGit(t, f.root, "show", preservedPrefix+d.RunRequest.RunID+":base.txt"); got != "uncommitted after candidate" {
		t.Fatal(got)
	}
}
