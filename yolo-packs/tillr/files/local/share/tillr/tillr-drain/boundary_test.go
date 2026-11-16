package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// boundaryFixture is the shape a tick gives a run: a repository with a trunk, one worktree
// the run owns, and one sibling lane worktree it shares the repository with. The two
// worktrees are real `git worktree add`s of the same repository, so the shared refs and the
// per-worktree index are the ones a real run meets.
type boundaryFixture struct {
	repo   string
	target string
	lane   string
}

func newBoundaryFixture(t *testing.T) boundaryFixture {
	t.Helper()
	repo := filepath.Join(scratch(t, "boundary-repo"), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("cannot create the fixture repository: %v", err)
	}
	git(t, repo, "init", "-b", "main")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	lanes := scratch(t, "boundary-lanes")
	target := filepath.Join(lanes, "lane-1")
	lane := filepath.Join(lanes, "lane-2")
	git(t, repo, "worktree", "add", "-b", "tillr/lane-1", target, "main")
	git(t, repo, "worktree", "add", "-b", "tillr/lane-2", lane, "main")
	return boundaryFixture{repo: repo, target: target, lane: lane}
}

// snapshots takes one half of the bracket through the binary, and answers its path. The
// span is the fixture's: the main checkout, the run's target, and the sibling lane.
func (f boundaryFixture) snapshot(t *testing.T, name string) string {
	t.Helper()
	out := filepath.Join(scratch(t, "boundary-snapshots"), name+".json")
	code, output := invoke(t, "boundary", "snapshot",
		"--repo", f.repo, "--target", f.target, "--lane", f.lane, "--out", out)
	if code != 0 {
		t.Fatalf("boundary snapshot exited %d: %s", code, output)
	}
	return out
}

// check runs the check face over a bracket and answers its exit code and its output.
func check(t *testing.T, before, after string) (int, string) {
	t.Helper()
	code, output := invoke(t, "boundary", "check", "--before", before, "--after", after)
	return code, string(output)
}

// The positive case the loop depends on: a run that writes and commits inside the worktree
// it was given is not a boundary violation. Its own branch ref moves, and that ref is the
// one ref the check excuses — a check that stopped here would stop every successful run.
func TestTheBoundaryIsCleanWhenARunOnlyTouchesItsWorktree(t *testing.T) {
	f := newBoundaryFixture(t)
	before := f.snapshot(t, "before")

	writeFile(t, filepath.Join(f.target, "work.txt"), "the run's work\n")
	writeFile(t, filepath.Join(f.target, "base.txt"), "base, edited\n")
	git(t, f.target, "add", ".")
	git(t, f.target, "commit", "-m", "the run's commit")

	after := f.snapshot(t, "after")
	code, output := check(t, before, after)
	if code != 0 {
		t.Fatalf("a run that stayed in its worktree was reported as a violation: exit %d\n%s", code, output)
	}
	if strings.TrimSpace(output) != "" {
		t.Fatalf("a clean boundary answered with output: %q", output)
	}
}

// Dirt that was already there when the bracket opened is not the run's doing. The check
// compares, it does not demand a clean host: demanding one is the loop's precondition and a
// different stop reason.
func TestTheBoundaryDoesNotBlameDirtThatWasThereBeforeTheRun(t *testing.T) {
	f := newBoundaryFixture(t)
	writeFile(t, filepath.Join(f.repo, "already-dirty.txt"), "not this run's\n")
	before := f.snapshot(t, "before")

	writeFile(t, filepath.Join(f.target, "work.txt"), "the run's work\n")

	after := f.snapshot(t, "after")
	code, output := check(t, before, after)
	if code != 0 {
		t.Fatalf("dirt that predates the bracket was blamed on the run: exit %d\n%s", code, output)
	}
}

// Main-checkout content belongs to the owner, even when it appears mid-run.
func TestTheBoundaryIgnoresOwnerContentInTheMainCheckout(t *testing.T) {
	f := newBoundaryFixture(t)
	before := f.snapshot(t, "before")

	writeFile(t, filepath.Join(f.repo, "trespass.txt"), "outside the target\n")
	writeFile(t, filepath.Join(f.repo, "base.txt"), "the main checkout's trunk, edited\n")

	after := f.snapshot(t, "after")
	code, output := check(t, before, after)
	if code != 0 {
		t.Fatalf("owner content convicted a run: exit %d: %s", code, output)
	}
}

// And the same write in a sibling lane's worktree, which is the other half of "outside the
// target": the main checkout is not the only state a run shares the repository with.
func TestTheBoundaryCatchesAWriteInAnotherWorktree(t *testing.T) {
	f := newBoundaryFixture(t)
	before := f.snapshot(t, "before")

	writeFile(t, filepath.Join(f.lane, "trespass.txt"), "another lane's worktree\n")

	after := f.snapshot(t, "after")
	code, output := check(t, before, after)
	if code != 1 {
		t.Fatalf("a write in a sibling worktree answered exit %d, want 1: %s", code, output)
	}
	for _, want := range []string{"trespass.txt", f.lane} {
		if !strings.Contains(output, want) {
			t.Fatalf("the violation does not name %q:\n%s", want, output)
		}
	}
}

// A ref is not a path: a run that plants a branch writes a file nothing in a worktree
// shows, so the ref half of the snapshot is what catches it.
func TestTheBoundaryCatchesAMovedRef(t *testing.T) {
	f := newBoundaryFixture(t)
	before := f.snapshot(t, "before")

	git(t, f.repo, "update-ref", "refs/heads/planted", "main")

	after := f.snapshot(t, "after")
	code, output := check(t, before, after)
	if code != 1 {
		t.Fatalf("a planted ref answered exit %d, want 1: %s", code, output)
	}
	if !strings.Contains(output, "refs/heads/planted appeared") {
		t.Fatalf("the violation does not name the ref and what happened to it:\n%s", output)
	}
}

// A commit in the main checkout moves the trunk under the loop, which is the row the design
// answers "stop and surface": it has to be named, not inferred from a dirty status.
func TestTheBoundaryCatchesACommitInTheMainCheckout(t *testing.T) {
	f := newBoundaryFixture(t)
	before := f.snapshot(t, "before")

	writeFile(t, filepath.Join(f.repo, "smuggled.txt"), "committed in the main checkout\n")
	git(t, f.repo, "add", "smuggled.txt")
	git(t, f.repo, "commit", "-m", "not the run's commit")

	after := f.snapshot(t, "after")
	code, output := check(t, before, after)
	if code != 1 {
		t.Fatalf("a commit in the main checkout answered exit %d, want 1: %s", code, output)
	}
	for _, want := range []string{"refs/heads/main moved", "HEAD moved"} {
		if !strings.Contains(output, want) {
			t.Fatalf("the violation does not name %q:\n%s", want, output)
		}
	}
}

// A stray worktree is lane lifecycle, which the stager owns: a run that creates one has
// taken a decision that is not its, and it shows up as a path that was not registered before.
func TestTheBoundaryCatchesAStrayWorktree(t *testing.T) {
	f := newBoundaryFixture(t)
	before := f.snapshot(t, "before")

	stray := filepath.Join(scratch(t, "boundary-stray"), "lane-3")
	git(t, f.repo, "worktree", "add", "-b", "tillr/lane-3", stray, "main")

	after := f.snapshot(t, "after")
	code, output := check(t, before, after)
	if code != 1 {
		t.Fatalf("a stray worktree answered exit %d, want 1: %s", code, output)
	}
	for _, want := range []string{"worktree", stray, "appeared"} {
		if !strings.Contains(output, want) {
			t.Fatalf("the violation does not name %q:\n%s", want, output)
		}
	}
}

// Fail closed, in both halves. A snapshot the caller cannot be given is nonzero and writes
// no snapshot file, and a bracket whose halves cover different spans is refused rather than
// compared — "could not look" must never read as "nothing changed".
func TestTheBoundaryRefusesAProbeItCannotTake(t *testing.T) {
	f := newBoundaryFixture(t)
	before := f.snapshot(t, "before")

	notAWorktree := scratch(t, "boundary-not-a-worktree")
	out := filepath.Join(scratch(t, "boundary-snapshots"), "never.json")
	code, output := invoke(t, "boundary", "snapshot",
		"--repo", f.repo, "--target", notAWorktree, "--out", out)
	if code == 0 {
		t.Fatalf("a snapshot of a directory that is not a worktree exited 0")
	}
	if !strings.Contains(string(output), notAWorktree) {
		t.Fatalf("the refusal does not name the path it could not take: %s", output)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatalf("a refused snapshot wrote %s: a partial snapshot would compare as clean", out)
	}

	code, output = invoke(t, "boundary", "snapshot",
		"--repo", notAWorktree, "--target", f.target, "--out", out)
	if code == 0 {
		t.Fatalf("a snapshot of a directory that is not a repository exited 0")
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		t.Fatalf("a snapshot that could not be taken said nothing")
	}

	other := filepath.Join(scratch(t, "boundary-lanes"), "other")
	git(t, f.repo, "worktree", "add", "-b", "tillr/other", other, "main")
	otherBefore := filepath.Join(scratch(t, "boundary-snapshots"), "other.json")
	if code, output := invoke(t, "boundary", "snapshot",
		"--repo", f.repo, "--target", other, "--out", otherBefore); code != 0 {
		t.Fatalf("boundary snapshot exited %d: %s", code, output)
	}
	code, output = invoke(t, "boundary", "check", "--before", before, "--after", otherBefore)
	if code == 0 {
		t.Fatalf("a bracket over two different targets compared as clean: %s", output)
	}
}
