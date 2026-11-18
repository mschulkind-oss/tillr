package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

// Use the production worktree preparation when a held lane rejoins, rather than
// assuming the fake content stager creates a worktree.
type startupHeldStager struct {
	f       refusalLoopFixture
	onStage func()
}

func (s *startupHeldStager) Stage(ctx context.Context, spec StageSpec) error {
	if s.onStage != nil {
		s.onStage()
	}
	stager := &LaneStager{Root: s.f.root, LaneRoot: s.f.laneRoot}
	if err := stager.ensureWorktree(ctx, spec, filepath.Join(s.f.laneRoot, spec.LaneID), laneWorktree(s.f, spec.LaneID)); err != nil {
		return err
	}
	return (&itemContentStager{}).Stage(ctx, spec)
}

func moveLaneHolder(t *testing.T, f refusalLoopFixture, lane string) string {
	t.Helper()
	holder := filepath.Join(scratchUnderDurable(t), "holder-"+lane)
	runGit(t, f.root, "worktree", "move", laneWorktree(f, lane), holder)
	return holder
}

// Stop after the first dispatch: recordingSteps deliberately writes no terminal
// item state, so it cannot make Run's exit-when-drained condition true.
type startupHeldSteps struct {
	laneSteps
	cancel context.CancelFunc
}

func (s *startupHeldSteps) Run(ctx context.Context, request run.Request) (run.Record, string, error) {
	rec, log, err := s.laneSteps.Run(ctx, request)
	s.cancel()
	return rec, log, err
}

func TestLoopStartsWithABranchHeldLaneOutOfRotation(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	holder := moveLaneHolder(t, f, "lane-1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	steps := &startupHeldSteps{cancel: cancel}
	stage := &startupHeldStager{f: f}
	l := f.loop(t, steps, stage)
	var offered []string
	stage.onStage = func() { offered = l.rotationLanes() }
	result, err := l.Run(ctx)
	if err != nil || result.Stop != StopOperatorStop {
		t.Fatalf("loop did not start with a free lane: %+v / %v", result, err)
	}
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("item ran on %v, want lane-2", got)
	}
	c := l.laneConditions["lane-1"]
	if len(offered) != 1 || offered[0] != "lane-2" {
		t.Fatalf("rotation at dispatch: %v, want only lane-2", offered)
	}
	if c == nil || c.reason != ReasonLaneBranchHeld || !strings.Contains(c.detail, holder) {
		t.Fatalf("held condition: %+v", c)
	}
	if n := laneEvents(t, f, laneRefusedKind, "lane-1"); n != 1 {
		t.Fatalf("refusal events: %d", n)
	}
}

func TestStartupBranchHoldRechecksWhenHolderIsRemoved(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	holder := moveLaneHolder(t, f, "lane-1")
	l := f.loop(t, &laneSteps{}, &startupHeldStager{f: f})
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := l.recheckLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := l.rotationLanes(); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("unchanged holder returned to rotation: %v", got)
	}
	runGit(t, f.root, "worktree", "remove", holder)
	if err := l.recheckLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := l.rotationLanes(); len(got) != 2 {
		t.Fatalf("removed holder did not return lane: %v", got)
	}
	onePass(t, l)
	if n := laneEvents(t, f, laneRestoredKind, "lane-1"); n != 1 {
		t.Fatalf("restored events: %d", n)
	}
}

func TestStartupHoldRestartPreservesUnlandedBranchAfterHolderRemoval(t *testing.T) {
	for _, rechecked := range []bool{false, true} {
		t.Run(fmt.Sprintf("rechecked-%t", rechecked), func(t *testing.T) {
			f := newRefusalLoopFixture(t, "item-a")
			holder := moveLaneHolder(t, f, "lane-1")
			l := f.loop(t, &laneSteps{}, &startupHeldStager{f: f})
			if err := l.stageLandingLanes(context.Background()); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(holder, "unlanded.txt"), "unlanded\n")
			runGit(t, holder, "add", "unlanded.txt")
			runGit(t, holder, "commit", "-qm", "unlanded change")
			branch := "refs/heads/" + LaneBranch(f.laneRoot, "lane-1")
			tip := strings.TrimSpace(runGit(t, f.root, "rev-parse", branch))
			if rechecked {
				// Persist the new unlanded-change condition before the restart.
				if err := l.recheckLanes(context.Background()); err != nil {
					t.Fatal(err)
				}
				l.cfg.Lanes = []string{"lane-1"}
				onePass(t, l)
			}
			runGit(t, f.root, "worktree", "remove", holder)
			restarted := f.loop(t, &laneSteps{}, &startupHeldStager{f: f})
			if err := restarted.stageLandingLanes(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(runGit(t, f.root, "rev-parse", branch)); got != tip {
				t.Fatalf("startup reset unlanded branch: got %s, want %s", got, tip)
			}
			if got := restarted.rotationLanes(); len(got) != 1 || got[0] != "lane-2" {
				t.Fatalf("restart rotation: %v, want only lane-2", got)
			}
			if c := restarted.laneConditions["lane-1"]; c == nil || !c.out || c.reason != ReasonLaneUnlandedChange {
				t.Fatalf("restart unlanded condition: %+v", c)
			}
		})
	}
}

func TestStartupBranchHoldSlowRecheckDoesNotWatchTheMissingLane(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	moveLaneHolder(t, f, "lane-1")
	steps := &laneSteps{}
	l := f.loop(t, steps, &startupHeldStager{f: f})
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := l.cfg.Now().Add(laneRecheckInterval)
	l.cfg.Now = func() time.Time { return now }
	onePass(t, l)
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("item ran on %v, want lane-2 after held-lane re-check", got)
	}
	if n := laneEvents(t, f, laneRefusedKind, "lane-1"); n != 1 {
		t.Fatalf("unchanged hold recorded %d times", n)
	}
}

func TestStartupHoldWithAnUnlandedCommitKeepsMissingLaneOutOfSnapshots(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	holder := moveLaneHolder(t, f, "lane-1")
	writeForkFile(t, filepath.Join(holder, "unlanded.txt"), "unlanded\n")
	runGit(t, holder, "add", "unlanded.txt")
	runGit(t, holder, "commit", "-qm", "unlanded change")
	steps := &laneSteps{}
	l := f.loop(t, steps, &startupHeldStager{f: f})
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := l.cfg.Now().Add(laneRecheckInterval)
	l.cfg.Now = func() time.Time { return now }
	// Offer only the held lane for its slow re-check, leaving the unrelated
	// item ready for the following pass when the free lane is offered again.
	lanes := l.cfg.Lanes
	l.cfg.Lanes = []string{"lane-1"}
	onePass(t, l)
	l.cfg.Lanes = lanes
	if c := l.laneConditions["lane-1"]; c == nil || !c.out || c.reason != ReasonLaneUnlandedChange {
		t.Fatalf("unlanded holder condition: %+v", c)
	}
	onePass(t, l)
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("item-a ran on %v, want lane-2", got)
	}
}

func TestStartupChecksBranchHoldersEvenWhenLanePathsExist(t *testing.T) {
	for _, heldCount := range []int{1, 2} {
		t.Run(fmt.Sprintf("held-%d", heldCount), func(t *testing.T) {
			f := newRefusalLoopFixture(t, "item-a", "item-b")
			var holders []string
			for i := 1; i <= heldCount; i++ {
				lane := fmt.Sprintf("lane-%d", i)
				runGit(t, f.root, "-C", laneWorktree(f, lane), "checkout", "--detach")
				holder := filepath.Join(scratchUnderDurable(t), "holder-"+lane)
				runGit(t, f.root, "worktree", "add", holder, LaneBranch(f.laneRoot, lane))
				holders = append(holders, holder)
			}
			l := f.loop(t, &laneSteps{}, &startupHeldStager{f: f})
			err := l.stageLandingLanes(context.Background())
			if heldCount == 2 {
				if err == nil {
					t.Fatal("all-held existing paths allowed startup")
				}
				for _, holder := range holders {
					if !strings.Contains(err.Error(), holder) {
						t.Errorf("error does not name %s: %v", holder, err)
					}
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got := l.rotationLanes(); len(got) != 1 || got[0] != "lane-2" {
					t.Fatalf("rotation: %v, want only lane-2", got)
				}
			}
		})
	}
}

func TestLoopRefusesStartupWhenEveryLaneBranchIsHeld(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	holders := []string{moveLaneHolder(t, f, "lane-1"), moveLaneHolder(t, f, "lane-2")}
	steps := &laneSteps{}
	l := f.loop(t, steps, &itemContentStager{})
	_, err := l.Run(context.Background())
	if err == nil {
		t.Fatal("all-held startup ran")
	}
	for _, holder := range holders {
		if !strings.Contains(err.Error(), holder) {
			t.Errorf("startup error does not name %s: %v", holder, err)
		}
	}
	if len(steps.ranOn("item-a")) != 0 || len(steps.ranOn("item-b")) != 0 {
		t.Fatal("all-held startup offered an item")
	}
}
