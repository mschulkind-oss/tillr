package drain

import (
	"context"
	"testing"
)

func TestGatedCandidateRetryStillChecksRecordedHead(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	c := historyCandidate(t, f, l, runGit(t, f.root, "rev-parse", "HEAD"))
	if err := appendJudgment(context.Background(), f.storePath, candidateGatedKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	runGit(t, c.Worktree, "commit", "--allow-empty", "-qm", "unexpected head movement")
	var result LoopResult
	historyPass(t, l, &result)
	if steps.lands != 0 || steps.gates != 0 {
		t.Fatalf("changed gated candidate was used: %+v", steps)
	}
	if d := mustPlan(t, l)[0].decision; d.Reason != ReasonCandidateMismatch {
		t.Fatalf("changed candidate has no fault: %+v", d)
	}
}
