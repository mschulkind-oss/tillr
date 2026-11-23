package drain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A long culprit search is visible while it runs: the failing check that starts it, each
// replay with where and why, each replay's verdict and time, and the landing it names. On
// 2026-11-18 a search replayed a twelve-minute check for half an hour with no line at all.
func TestHeavyCulpritSearchLogsEachReplayAsItEnds(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	h.loop.gatePolicy.AutoRevert = false
	culprit := h.land("item-broke", "a", "broken\n")
	tip := h.land("item-later", "c", "unrelated\n")
	h.proof()
	log := h.report.String()
	want := []string{
		"loop: heavy unit failed at tip " + shortSHA(tip) + "; searching for the first red commit since green " + shortSHA(h.base),
		"loop: heavy unit replaying at " + shortSHA(h.base) + " (the green base)",
		"loop: heavy unit at " + shortSHA(h.base) + ": green after",
		"loop: heavy unit replaying at " + shortSHA(culprit) + " (the last of 1 candidate commits since green)",
		"loop: heavy unit at " + shortSHA(culprit) + ": red after",
		"loop: heavy unit first red commit " + shortSHA(culprit),
	}
	last := -1
	for _, line := range want {
		at := strings.Index(log, line)
		if at < 0 {
			t.Fatalf("log lacks %q:\n%s", line, log)
		}
		if at < last {
			t.Fatalf("%q is out of order:\n%s", line, log)
		}
		last = at
	}
	// Every line precedes the run's one summary line.
	if summary := strings.Index(log, "loop: heavy run red at"); summary < last {
		t.Fatalf("the progress came after the summary:\n%s", log)
	}
}

// A replay that gives no verdict says so, with its reason, and the search ends there with
// the fixer against the tip: it never retries or holds the proof.
func TestHeavyReplayWithNoVerdictSaysWhy(t *testing.T) {
	h := newRevertHarness(t, nil)
	h.loop.gatePolicy.AutoRevert = false
	h.land("item-broke", "a", "broken\n")
	tip := h.land("item-later", "c", "unrelated\n")
	h.steps.judge = func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		if only != "" {
			return nil, errors.New("the replay checkout has no go toolchain")
		}
		return fileRed("a")(ctx, dir, only)
	}
	h.loop.cfg.Steps = h.steps
	h.proof()
	log := h.report.String()
	for _, line := range []string{
		"loop: heavy unit at " + shortSHA(h.base) + ": no verdict after",
		"because the replay checkout has no go toolchain",
		"loop: heavy unit culprit search gave no verdict, because the replay checkout has no go toolchain; the fixer is filed against the tip",
	} {
		if !strings.Contains(log, line) {
			t.Fatalf("log lacks %q:\n%s", line, log)
		}
	}
	if f := h.fixers(); len(f) != 1 || f[0] != TrunkFixerID(tip) {
		t.Fatalf("fixers=%v", f)
	}
	if strings.Count(log, "replaying at") != 1 {
		t.Fatalf("a replay with no verdict was retried:\n%s", log)
	}
}
