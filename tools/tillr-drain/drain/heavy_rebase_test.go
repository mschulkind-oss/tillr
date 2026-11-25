package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A rebase of main gives every landed commit above its base a new sha, and the landing
// record keeps the old one. The loop must name the rebase as why no record matches, say
// what the operator does, and still file a fixer instead of reverting.
func TestHeavyNamesRebaseOfMainWhenLandedShaChanged(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	landed := h.land("item-break", "a", "broken\n")
	h.land("item-docs", "docs-note", "unrelated\n")

	// Rebase main onto a new commit: the landed commits get new shas, the old objects stay.
	heavyGit(t, h.root, "checkout", "-q", "-b", "side", h.base)
	if err := os.WriteFile(filepath.Join(h.root, "upstream"), []byte("moved\n"), 0644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, h.root, "add", "upstream")
	heavyGit(t, h.root, "commit", "-qm", "chore: upstream moved")
	heavyGit(t, h.root, "checkout", "-q", "main")
	heavyGit(t, h.root, "rebase", "-q", "side")
	rebased := heavyGit(t, h.root, "log", "--format=%H", "--grep=item-break", "-1")
	if rebased == landed {
		t.Fatal("fixture: rebase kept the sha")
	}

	h.proof()
	if r := h.reverts(); len(r) != 0 {
		t.Fatalf("rebased commit triggered an automatic revert: %+v", r)
	}
	fixers := h.fixers()
	if len(fixers) != 1 || fixers[0] != TrunkFixerID(rebased) {
		t.Fatalf("fixers=%v, want one against %s", fixers, TrunkFixerID(rebased))
	}
	report := h.report.String()
	for _, want := range []string{"a rebase of the trunk changed the landed commit's sha", shortSHA(landed) + " (item item-break) before the rebase", "operator: run git revert " + shortSHA(rebased)} {
		if !strings.Contains(report, want) {
			t.Fatalf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "has no landing record; not reverting automatically") {
		t.Fatalf("a rebased commit was reported as unrecorded:\n%s", report)
	}
}
