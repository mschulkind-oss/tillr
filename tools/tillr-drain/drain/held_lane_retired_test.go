package drain

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// laneStateOf reads one item's durable lane state.
func laneStateOf(t *testing.T, path, itemID string) CandidateLaneState {
	t.Helper()
	s := openFixture(t, path)
	defer s.Close()
	var state CandidateLaneState
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		states, err := PendingLaneStates(r)
		state = states[itemID]
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

// A held candidate whose lane HEAD is the loop's own document-retirement commit — the one
// commit whose parent and tree its recorded retirement binds — keeps its lane, under a reason
// that says so, and the keep is recorded once. The pin carries the worker head only, so a
// release would discard the retirement and leave the retry a candidate-record mismatch
// (TestRestartGatesAndLandsTheRetiredHead). On 2026-11-17 item-gate-pinned-fork-inputs run a3
// was held for a gate harness error with lane-8 at its retirement commit 15f5c29f7, kept as
// "unrecorded lane HEAD needs inspection", and claimed and released every pass.
func TestAHeldCandidateAtItsRetirementCommitKeepsItsLaneForTheRetry(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "candidate\nretired block\n")
	runGit(t, c.Worktree, "commit", "-qam", "chore: retire landed document block")
	retired := runGit(t, c.Worktree, "rev-parse", "HEAD")
	c.WorkerHeadSHA = c.HeadSHA
	c.RetirementTreeSHA = runGit(t, c.Worktree, "rev-parse", "HEAD^{tree}")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateRetirementKind, c.RunID, c, l.cfg.Now())
	})
	if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, `the gate answered "error"`); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := laneStateOf(t, f.storePath, c.ItemID); got.Status != "kept" || got.Reason != retiredHeadKeep {
		t.Fatalf("a lane at its own retirement commit: %+v, want kept as %q", got, retiredHeadKeep)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != retired {
		t.Fatalf("lane HEAD %s, want the retirement commit %s", got, retired)
	}
	before := eventCount(t, f.storePath)
	for i := 0; i < 3; i++ {
		if err := l.releaseIdleHeldCandidates(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("three passes over the unchanged retirement keep appended %d event(s)", got-before)
	}
}

// A lane kept for a reason that has not changed is not examined again with a write: the
// keep is recorded once, with its reason, and a later pass that finds the same keep
// appends nothing. Run a3 above appended a claim and a release of its examination every
// pass — 3,305 drain.examination.owner events by 2026-11-18 15:04Z.
func TestAnUnchangedKeptLaneIsRecordedOnceAndNotReclaimedEveryPass(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "an unbound later commit\n")
	runGit(t, c.Worktree, "commit", "-qam", "unbound")
	if err := l.holdItemFault(context.Background(), c, StopGateHarnessError, `the gate answered "error"`); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := laneStateOf(t, f.storePath, c.ItemID); got.Status != "kept" || got.Reason != "unrecorded lane HEAD needs inspection" {
		t.Fatalf("the unbound HEAD was not kept with its reason: %+v", got)
	}
	before := eventCount(t, f.storePath)
	for i := 0; i < 3; i++ {
		if err := l.releaseIdleHeldCandidates(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("three passes over an unchanged keep appended %d event(s)", got-before)
	}
}
