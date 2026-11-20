package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func historyCandidate(t *testing.T, f refusalLoopFixture, l *Loop, base string) Candidate {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue',allowed_paths='["candidate.txt"]' WHERE item_id='item-a'`)
		return err
	})
	plan := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch: %v / %v", ok, err)
	}
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "candidate\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate")
	c := Candidate{ItemID: "item-a", RunID: plan.decision.RunRequest.RunID, LaneID: "lane-1", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD"), Request: plan.decision.RunRequest}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}})
	})
	if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	return c
}

func historyPass(t *testing.T, l *Loop, result *LoopResult) {
	t.Helper()
	if stop, err := l.pass(context.Background(), result); err != nil || (stop != "" && stop != StopQueueDrained) {
		t.Fatalf("pass: %s / %v", stop, err)
	}
	drainOwned(t, l, result)
}

func TestObservedRewriteSurvivesReplacementWithoutWaitingCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state='blocked'`); return err })
	base := runGit(t, f.root, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(f.root, "owner.txt"), "owner\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "owner")
	lost := runGit(t, f.root, "rev-parse", "HEAD")
	var result LoopResult
	historyPass(t, l, &result)
	runGit(t, f.root, "reset", "--hard", base)
	historyPass(t, l, &result) // no candidate exists when the rewrite is seen
	// SQLite identifiers alone do not keep commits alive. The durable history
	// must still be usable after normal Git retention has been removed.
	runGit(t, f.root, "reflog", "expire", "--expire=now", "--all")
	runGit(t, f.root, "gc", "--prune=now")
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	candidate := historyCandidate(t, f, replacement, base)
	// The next loop is offered a verdict already bound to this candidate, not
	// another request to gate it. This is also the crash-after-gate recovery path.
	if _, err := steps.Gate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if err := appendJudgment(context.Background(), f.storePath, candidateGatedKind, "run", candidate.RunID, candidate, replacement.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	historyPass(t, replacement, &result)
	if steps.lands != 0 || steps.gates != 1 || result.Rejected != 0 {
		t.Fatalf("rewrite landed or convicted: %+v / %+v", steps, result)
	}
	d := mustPlan(t, replacement)[0].decision
	if d.Action != ActionHold || !strings.HasPrefix(d.Reason, "trunk-rewritten: "+lost+" -> "+base) || !strings.Contains(d.Reason, "no match on the trunk for "+lost) {
		t.Fatalf("lost rewrite: %+v", d)
	}
	runGit(t, f.root, "reset", "--hard", lost)
	historyPass(t, replacement, &result)
	if steps.lands != 1 || steps.gates != 1 || result.Landed != 1 {
		t.Fatalf("restoration did not land without gate: %+v / %+v", steps, result)
	}
}

type pullingSteps struct {
	dirtyPathSteps
	remote string
}

func (s *pullingSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	out, err := s.dirtyPathSteps.Gate(ctx, c)
	runGit(s.t, s.root, "pull", "--ff-only", s.remote, "main")
	return out, err
}
func TestTwoCommitsPulledAfterGateLandWithoutSecondGate(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	base := runGit(t, f.root, "rev-parse", "HEAD")
	remote := filepath.Join(filepath.Dir(f.root), "history-remote")
	runGit(t, f.root, "clone", f.root, remote)
	for _, name := range []string{"one.txt", "two.txt"} {
		writeForkFile(t, filepath.Join(remote, name), name)
		runGit(t, remote, "add", ".")
		runGit(t, remote, "commit", "-qm", name)
	}
	steps := &pullingSteps{dirtyPathSteps: dirtyPathSteps{t: t, root: f.root, store: f.storePath}, remote: remote}
	l.cfg.Steps = steps
	historyCandidate(t, f, l, base)
	var result LoopResult
	historyPass(t, l, &result)
	if result.Landed != 1 || steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("pull prevented landing: %+v / %+v", result, steps)
	}
	for _, name := range []string{"one.txt", "two.txt", "candidate.txt"} {
		if !strings.Contains(runGit(t, f.root, "ls-tree", "--name-only", "HEAD"), name) {
			t.Fatalf("landing lost %s", name)
		}
	}
}
