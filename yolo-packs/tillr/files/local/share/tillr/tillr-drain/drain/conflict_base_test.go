package drain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureStager records what conflictFix asked to stage and then refuses, so a test reads
// the staged base and the lane without launching an adapter.
type captureStager struct{ specs []StageSpec }

func (s *captureStager) Stage(_ context.Context, spec StageSpec) error {
	s.specs = append(s.specs, spec)
	return errors.New("capture stager: stop after recording the spec")
}

// conflictBaseFixture holds a worker's committed change to one file, and the trunk moved by
// another commit to the same file (conflicting) or to a different one (not conflicting).
func conflictBaseFixture(t *testing.T, conflicting bool) (*Loop, Candidate, string, *captureStager) {
	t.Helper()
	f, l := designSeatFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1"}
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	lane := p.decision.LaneID
	dir := l.laneDirPath(lane)
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "11111111-1111-4111-8111-111111111111\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "shared.txt\n")
	target := l.laneTarget(lane)
	writeForkFile(t, filepath.Join(target, "shared.txt"), "the worker's line\n")
	runGit(t, target, "add", "shared.txt")
	runGit(t, target, "commit", "-qm", "worker change")
	head := runGit(t, target, "rev-parse", "HEAD")
	// The trunk moves under the worker, in the repository the lander merges in.
	trunkFile := "other.txt"
	if conflicting {
		trunkFile = "shared.txt"
	}
	writeForkFile(t, filepath.Join(f.root, trunkFile), "the trunk's line\n")
	runGit(t, f.root, "add", trunkFile)
	runGit(t, f.root, "commit", "-qm", "trunk moved")
	if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	c := Candidate{ItemID: "item-a", LaneID: lane, RunID: p.decision.RunRequest.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, Request: p.decision.RunRequest}
	var err error
	if c.WorkerSession, err = l.captureWorkerSession(context.Background(), c); err != nil || c.WorkerSession == nil {
		t.Fatalf("snapshot: %+v %v", c.WorkerSession, err)
	}
	stager := &captureStager{}
	l.cfg.Stage = stager
	return l, c, base, stager
}

func conflictFixStaged(t *testing.T, l *Loop, c Candidate, stager *captureStager) StageSpec {
	t.Helper()
	paths := []string{"CONFLICT (content): Merge conflict in shared.txt"}
	_, _, _ = l.conflictFix(context.Background(), c, LandOutcome{ConflictPaths: paths}, &LoopResult{}, &PassTiming{})
	if len(stager.specs) != 1 {
		t.Fatalf("conflict-fix staged %d runs", len(stager.specs))
	}
	return stager.specs[0]
}

// A conflict-fix run is judged, fenced and landed against the trunk it was made on: the
// base it is staged from is the trunk tip, never the base the worker started from. Where
// the worker's change cannot replay on the tip, the lane is restaged on the tip instead of
// resuming a branch whose base would make every later commit conflict again.
func TestConflictFixRunIsBasedOnTheTrunkTheConflictWasReadOn(t *testing.T) {
	l, c, oldBase, stager := conflictBaseFixture(t, true)
	tip := runGit(t, l.cfg.Root, "rev-parse", TrunkRef)
	spec := conflictFixStaged(t, l, c, stager)
	if spec.BaseSHA != tip || spec.BaseSHA == oldBase || spec.Continue {
		t.Fatalf("staged base=%s continue=%v, want a fresh run on %s (old base %s)", spec.BaseSHA, spec.Continue, tip, oldBase)
	}
	target := c.Worktree
	if head := runGit(t, target, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("lane is at %s, want the trunk tip %s", head, tip)
	}
	// The worker (a fresh session on the tip) resolves the line and commits.
	writeForkFile(t, filepath.Join(target, "shared.txt"), "the worker's line, reconciled\n")
	runGit(t, target, "add", "shared.txt")
	runGit(t, target, "commit", "-qm", "resolve")
	head := runGit(t, target, "rev-parse", "HEAD")
	// The fence check diffs the recorded base to HEAD: only the item's own path.
	if names := runGit(t, target, "diff", "--name-only", spec.BaseSHA, head); names != "shared.txt" {
		t.Fatalf("fence view of the fix: %q", names)
	}
	// The lander's merge uses the recorded base: it is clean now.
	if out := mergeTreeOut(l.cfg.Root, spec.BaseSHA, tip, head); strings.Contains(out, "CONFLICT") {
		t.Fatalf("the fix still conflicts: %s", out)
	}
	// Against the old base the same commit conflicts: the failure this guards.
	if out := mergeTreeOut(l.cfg.Root, oldBase, tip, head); !strings.Contains(out, "CONFLICT") {
		t.Fatalf("the old base no longer reproduces the conflict: %s", out)
	}
}

// When the worker's commits replay cleanly the session is kept, on the tip, with the base
// advanced to match.
func TestConflictFixResumesTheSessionOnTheTrunkTip(t *testing.T) {
	l, c, _, stager := conflictBaseFixture(t, false)
	tip := runGit(t, l.cfg.Root, "rev-parse", TrunkRef)
	spec := conflictFixStaged(t, l, c, stager)
	if spec.BaseSHA != tip || !spec.Continue || spec.SessionID == "" {
		t.Fatalf("staged base=%s continue=%v session=%q, want the session resumed on %s", spec.BaseSHA, spec.Continue, spec.SessionID, tip)
	}
	if parent := runGit(t, c.Worktree, "rev-parse", "HEAD^"); parent != tip {
		t.Fatalf("worker commit is on %s, want %s", parent, tip)
	}
	if names := runGit(t, c.Worktree, "diff", "--name-only", spec.BaseSHA, "HEAD"); names != "shared.txt" {
		t.Fatalf("fence view of the fix: %q", names)
	}
	raw, err := os.ReadFile(filepath.Join(l.laneDirPath(c.LaneID), BaseSHAFile))
	if err != nil || strings.TrimSpace(string(raw)) != tip {
		t.Fatalf("recorded base %q (%v), want %s", raw, err, tip)
	}
}

// mergeTreeOut is the lander's merge, whose conflict exit status is part of the answer.
func mergeTreeOut(root, base, trunk, head string) string {
	out, _ := exec.Command("git", "-C", root, "merge-tree", "--write-tree", "--merge-base="+base, trunk, head).CombinedOutput()
	return string(out)
}
