package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeavyFirstRedChecksActualParentOfWatchCandidate(t *testing.T) {
	h := newRevertHarness(t, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		checks, err := fileRed("a")(ctx, dir, only)
		checks[0].Reads = []string{"b"} // The actual failing input was omitted.
		return checks, err
	})
	culprit := h.land("item-cause", "a", "broken\n")
	h.land("item-later", "b", "unrelated\n")
	h.proof()
	if events := h.reverts(); len(events) != 1 || events[0].Landing != culprit || events[0].Result != "reverted" {
		t.Fatalf("wanted only the actual green-to-red landing reverted, got %+v\n%s", events, h.report)
	}
}

func TestHeavyAttributionReuseNeedsSameFailureAndFirstParentRange(t *testing.T) {
	ctx := context.Background()
	h := newRevertHarness(t, fileRed("a"))
	culprit := h.land("item-cause", "a", "broken\n")
	tip := h.land("item-later", "b", "unrelated\n")
	r := heavyFixture(t, h.root, fileRed("a"))
	r.Now = time.Now
	dir, err := heavyDirectory(ctx, h.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	var progress strings.Builder
	r.Progress = func(line string) { progress.WriteString(line) }
	run := HeavyRun{BaseSHA: h.base, TipSHA: tip}
	check := HeavyCheck{CheckID: "unit", Result: "fail", Reads: []string{"a", "b"}, Detail: "first failure"}
	find := func(reuse bool) {
		t.Helper()
		progress.Reset()
		red, fault, _, err := r.findFirstRed(ctx, run, check)
		if err != nil || fault || red != culprit || strings.Contains(progress.String(), "reused first red commit") != reuse {
			t.Fatalf("want first red %s, reuse=%t; got %s fault=%t err=%v\n%s", culprit, reuse, red, fault, err, &progress)
		}
	}
	find(false)
	run.TipSHA = h.land("item-descendant", "c", "unrelated again\n")
	find(true)
	check.Detail = "different failure"
	find(false)
	find(true)
	check.Detail = ""
	check.LogRef = filepath.Join(scratchUnderDurable(t), "failure.log")
	if err := os.WriteFile(check.LogRef, []byte("first diagnostic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	find(false)
	find(true)
	if err := os.WriteFile(check.LogRef, []byte("different diagnostic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	find(false)
	records := readHeavyAttributions(dir)
	prior := records[check.CheckID]
	prior.FirstRed = run.BaseSHA // An ancestor, but not a change in the range.
	records[check.CheckID] = prior
	if err := publishHeavyJSON(dir, heavyAttributionsFile, records); err != nil {
		t.Fatal(err)
	}
	find(false)
}

// The fast gate happens to include the same failing check, as in the incident.
// A fast-gate refusal must not prevent the focused inverse from refuting the blame.
type sameCheckGate struct{ *revertSteps }

func (s sameCheckGate) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if _, err := os.Stat(filepath.Join(c.Worktree, "a")); err == nil {
		return GateOutcome{Outcome: GateFailed, FailingChecks: []string{"unit"}}, nil
	}
	return s.revertSteps.Gate(ctx, c)
}

func TestHeavyStillRedRevertDiscardsAttributionAndSearchesAgain(t *testing.T) {
	for _, early := range []bool{false, true} {
		t.Run(map[bool]string{true: "early", false: "batch"}[early], func(t *testing.T) {
			judge := func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
				checks, err := fileRed("a")(ctx, dir, only)
				if early && only == "" {
					heavyCheckEnded(ctx, checks[0])
				}
				return checks, err
			}
			h := newRevertHarness(t, judge)
			h.loop.cfg.Steps = sameCheckGate{h.steps}
			culprit := h.land("item-cause", "a", "broken\n")
			tip := h.land("item-later", "b", "unrelated\n")
			// Simulate a stale focused verdict that makes the unrelated landing
			// appear to have a green parent. Its inverse disproves that story.
			dir, err := heavyDirectory(context.Background(), h.root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := saveHeavyReplay(dir, culprit, "unit", false); err != nil {
				t.Fatal(err)
			}
			h.proof()
			events := h.reverts()
			if len(events) != 2 || events[0].Landing != tip || !events[0].StillRed || events[1].Landing != culprit || events[1].Result != "reverted" {
				t.Fatalf("wanted disproved attribution then corrected revert; got %+v\n%s", events, h.report)
			}
			if f := h.fixers(); len(f) != 0 {
				t.Fatalf("corrected revert still filed fixers: %v", f)
			}
			if body := heavyGit(t, h.root, "ls-tree", "--name-only", TrunkRef); body != "b\nvalue" {
				t.Fatalf("wanted to keep unrelated file and remove failing input: %s", body)
			}
			if !strings.Contains(events[0].Reason, "the fast gate did not pass") || !strings.Contains(h.report.String(), "discarding attribution and searching again") {
				t.Fatalf("missing evidence that the fast gate did not hide a still-red revert: %+v\n%s", events, h.report)
			}
		})
	}
}
