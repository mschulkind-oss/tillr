package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func heavyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, root, args)
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func heavyRepo(t *testing.T) (string, string) {
	t.Helper()
	root := scratchUnderDurable(t)
	heavyGit(t, root, "init", "-q", "-b", "main")
	return root, heavyCommit(t, root, "good")
}
func heavyCommit(t *testing.T, root, value string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "value"), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "add", ".")
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", value)
	return heavyGit(t, root, "rev-parse", "HEAD")
}
func heavyFixture(t *testing.T, root string, judge func(context.Context, string, string) ([]HeavyCheck, error)) HeavyRunner {
	t.Helper()
	return HeavyRunner{
		Root:                     root,
		Lock:                     loopHeld(t, root),
		Work:                     filepath.Join(scratchUnderDurable(t), "worktree"),
		Trunk:                    "refs/heads/main",
		Judge:                    judge,
		WithWorktreeRegistryLock: withWorktreeRegistryLock,
		UnlockRegisteredWorktree: unlockRegisteredWorktree,
	}
}
func TestHeavyFirstRedFindsUnrecordedCommitWithoutAutoRevert(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	if err := os.WriteFile(filepath.Join(h.root, "a"), []byte("broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, h.root, "add", "a")
	heavyGit(t, h.root, "commit", "-qm", "manual: break a")
	manual := heavyGit(t, h.root, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(h.root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	h.land("item-docs", "docs/queue-order.txt", "unrelated\n")
	h.proof()
	if r := h.reverts(); len(r) != 0 {
		t.Fatalf("unrecorded commit triggered revert: %+v", r)
	}
	fixers := h.fixers()
	if len(fixers) != 1 || fixers[0] != TrunkFixerID(manual) {
		t.Fatalf("fixers=%v, want one against unrecorded commit %s (%s)", fixers, TrunkFixerID(manual), shortSHA(manual))
	}
	if text := h.fixerText(fixers[0]); !strings.Contains(text, shortSHA(manual)) {
		t.Fatalf("fixer brief does not name unrecorded commit %s: %s", shortSHA(manual), text)
	}
	if !strings.Contains(h.report.String(), "has no landing record; not reverting automatically") {
		t.Fatalf("log does not say unrecorded commit is not reverted automatically:\n%s", h.report.String())
	}
}

func TestHeavyProofRefMovementDoesNotConvictALane(t *testing.T) {
	if !landerRef(GreenRef) {
		t.Fatal("heavy proof ref is not recognized as runner-owned")
	}
	if landerRef("refs/tillr/unassigned") {
		t.Fatal("excused an unassigned ref")
	}
}
