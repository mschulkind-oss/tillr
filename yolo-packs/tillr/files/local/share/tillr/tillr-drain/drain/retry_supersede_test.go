package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func authorizeFixtureRetry(t *testing.T, f refusalLoopFixture, l *Loop, c Candidate) string {
	t.Helper()
	var id string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		retry, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: c.ItemID, RunID: c.RunID, By: "operator", Why: "the candidate cannot proceed", At: l.cfg.Now()})
		id = retry.RetryID
		return err
	})
	return id
}

func refSHA(t *testing.T, root, ref string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, root, "for-each-ref", "--format=%(objectname)", ref))
}

// A retained retirement candidate whose gate answered error re-gates in place when its fault
// is cleared, so it answers error again: on 2026-11-18 item-gate-pinned-fork-inputs run a3's
// tree pinned a stale fork commit, and its operator retry stayed unspent. The retry naming
// that run supersedes the candidate: the worker head is pinned under the run ref, the lane's
// retirement commit under the superseded-head ref, the lane is reset to the trunk, the fault
// is cleared, and the item is retried fresh.
func TestARetrySupersedesARetainedRetirementCandidateWhoseGateErred(t *testing.T) {
	ctx := context.Background()
	f, l, c := heldCandidateFixture(t)
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "candidate\nretired block\n")
	runGit(t, c.Worktree, "commit", "-qam", "chore: retire landed document block")
	retired := runGit(t, c.Worktree, "rev-parse", "HEAD")
	c.WorkerHeadSHA = c.HeadSHA
	c.RetirementTreeSHA = runGit(t, c.Worktree, "rev-parse", "HEAD^{tree}")
	fixtureWrite(t, f, func(tx *store.Tx) error { return appendEffect(tx, candidateRetirementKind, c.RunID, c, l.cfg.Now()) })
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, `the gate answered "error"`); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if got := laneStateOf(t, f.storePath, c.ItemID); got.Status != "kept" || got.Reason != retiredHeadKeep {
		t.Fatalf("precondition: %+v", got)
	}
	if d := planFor(t, l, c.ItemID); d.Action != ActionHold || d.Reason != StopGateHarnessError {
		t.Fatalf("precondition: %+v", d)
	}
	retryID := authorizeFixtureRetry(t, f, l, c)
	if d := planFor(t, l, c.ItemID); d.Action != ActionHold || d.Reason != StopGateHarnessError {
		t.Fatalf("the retry alone moved nothing before the supersession, want the fault hold: %+v", d)
	}
	if err := l.supersedeRetriedCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if got := refSHA(t, f.root, "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("run pin = %q, want the worker head %s", got, c.HeadSHA)
	}
	if got := refSHA(t, f.root, "refs/tillr/runs/"+c.RunID+supersededHeadSuffix); got != retired {
		t.Fatalf("retirement commit pin = %q, want %s", got, retired)
	}
	trunk := runGit(t, f.root, "rev-parse", TrunkRef)
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != trunk {
		t.Fatalf("lane HEAD %s, want the trunk %s", got, trunk)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if _, held := faults[c.ItemID]; held {
			t.Fatalf("fault still held: %+v", faults[c.ItemID])
		}
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND json_extract(payload,'$.retry_id')=?`, candidateSupersededKind, c.RunID, retryID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("%d supersession records, want 1", n)
		}
		return nil
	})
	d := planFor(t, l, c.ItemID)
	if d.Action != ActionRetry || d.RetryID != retryID || d.SupersedesRunID != c.RunID || d.LaneID != "lane-1" || d.RunRequest == nil || d.RunRequest.RunID == c.RunID {
		t.Fatalf("superseded candidate's item not retried fresh: %+v", d)
	}
	before := eventCount(t, f.storePath)
	if err := l.supersedeRetriedCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("a second supersession pass wrote %d event(s)", got-before)
	}
}

// A candidate record that names a checkout no lane has (item-ig-view-nodes run a3 named the
// fork checkout .forks/lanes/lane-9/pi after lane-9 was reset) re-raises its mismatch the
// moment its fault is cleared. The retry naming the run supersedes the record: the existing
// pin is kept as it is, the lane is reset, and the item is retried fresh.
func TestARetrySupersedesACandidateRecordThatMatchesNoLane(t *testing.T) {
	ctx := context.Background()
	f, l, c := heldCandidateFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "fork")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := c
	stale.Worktree, stale.BaseRef = elsewhere, "refs/heads/patch/pi"
	fixtureWrite(t, f, func(tx *store.Tx) error { return appendEffect(tx, candidateReadyKind, c.RunID, stale, l.cfg.Now()) })
	runGit(t, f.root, "update-ref", "refs/tillr/runs/"+c.RunID, c.HeadSHA)
	if err := l.candidateFault(ctx, stale, "recorded candidate worktree "+elsewhere+" is not lane lane-1's target"); err == nil {
		t.Fatal("candidate fault did not hold")
	}
	retryID := authorizeFixtureRetry(t, f, l, c)
	if err := l.supersedeRetriedCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if got := refSHA(t, f.root, "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("run pin = %q, want %s kept", got, c.HeadSHA)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != runGit(t, f.root, "rev-parse", TrunkRef) {
		t.Fatalf("lane not reset to the trunk: %s", got)
	}
	d := planFor(t, l, c.ItemID)
	if d.Action != ActionRetry || d.RetryID != retryID || d.SupersedesRunID != c.RunID {
		t.Fatalf("item not retried fresh: %+v", d)
	}
	// The superseded pin outlives the item: it is never pruned.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, c.ItemID, "test closes the item", "operator", l.cfg.Now())
		return err
	})
	if err := l.pruneRunRefs(ctx); err != nil {
		t.Fatal(err)
	}
	if got := refSHA(t, f.root, "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("superseded pin pruned: %q", got)
	}
}

// Without a retry naming the held run, or with a fault the retry does not supersede,
// nothing is written: the hold keeps its own release.
func TestNoRetryOrAnotherHoldSupersedesNothing(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, reason string
		retry        bool
	}{
		{"no retry", StopGateHarnessError, false},
		{"boundary conviction", StopHostBoundaryViolation, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, c := heldCandidateFixture(t)
			if err := l.holdItemFault(ctx, c, tc.reason, "held"); err != nil {
				t.Fatal(err)
			}
			if tc.retry {
				authorizeFixtureRetry(t, f, l, c)
			}
			before := eventCount(t, f.storePath)
			head := runGit(t, c.Worktree, "rev-parse", "HEAD")
			if err := l.supersedeRetriedCandidates(ctx); err != nil {
				t.Fatal(err)
			}
			if got := eventCount(t, f.storePath); got != before {
				t.Fatalf("wrote %d event(s)", got-before)
			}
			if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != head {
				t.Fatalf("lane moved to %s", got)
			}
		})
	}
}
