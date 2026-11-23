package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestForkLandingWritesPinInTrunkCommit verifies that when the lander moves
// refs/heads/patch/<fork> for a pin-tracked fork, it records the new tip and its
// upstream merge base in yolo-packs/fork-pins/<fork> in a landed trunk commit.
func TestForkLandingWritesPinInTrunkCommit(t *testing.T) {
	f := newFixture(t)
	upstreamBase := f.baseSHA
	oldTip := f.headSHA

	// Seed a tracked pin and pack manifest on trunk pointing at oldTip over upstreamBase.
	const remote = "git@github.com:example/tillr"
	pinRel := "yolo-packs/fork-pins/pi"
	write(t, filepath.Join(f.dir, pinRel), fmt.Sprintf("%s\n%s\n%s\n", oldTip, remote, upstreamBase))
	write(t, filepath.Join(f.dir, "yolo-packs/pi-fork/pack.json"),
		`{"contributes":[{"source":"git+https://github.com/example/pi?ref=main","patch_branch":"git+ssh://git@github.com/example/tillr?ref=patch/pi"}]}`)
	git(t, f.dir, "add", pinRel, "yolo-packs/pi-fork/pack.json")
	git(t, f.dir, "commit", "-m", "track pi fork pin")
	trunkBefore := f.trunk(t)

	// Set up patch/pi at oldTip and .forks/pi with upstream/main at upstreamBase.
	git(t, f.dir, "branch", "patch/pi", oldTip)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", oldTip)
	git(t, cache, "update-ref", "refs/remotes/upstream/main", upstreamBase)

	// Add a new fork commit on candidate (descending from oldTip).
	git(t, f.dir, "checkout", "candidate")
	write(t, filepath.Join(f.dir, "fork-change.txt"), "new fork work\n")
	git(t, f.dir, "add", "fork-change.txt")
	git(t, f.dir, "commit", "-m", "feat: fork change")
	newTip := git(t, f.dir, "rev-parse", "HEAD")
	f.baseSHA, f.headSHA = oldTip, newTip
	f.treeSHA = git(t, f.dir, "rev-parse", "HEAD^{tree}")
	git(t, f.dir, "checkout", f.ref)

	_, code, out := f.land(t, "run-fork-pin", "pass", "--base-ref", "patch/pi", "--observed-trunk", trunkBefore)
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "landed" {
		t.Fatalf("landing = %s/%s, want landed", got.Payload.Result, got.Payload.Reason)
	}
	if moved := git(t, f.dir, "rev-parse", "refs/heads/patch/pi"); moved != newTip {
		t.Fatalf("patch/pi = %s, want %s", moved, newTip)
	}

	trunkAfter := f.trunk(t)
	if trunkAfter == trunkBefore {
		t.Fatal("trunk did not advance with the pin update commit")
	}
	if parent := git(t, f.dir, "rev-parse", trunkAfter+"^"); parent != trunkBefore {
		t.Fatalf("trunk commit parent = %s, want %s", parent, trunkBefore)
	}
	changed := git(t, f.dir, "diff-tree", "--no-commit-id", "--name-only", "-r", trunkAfter)
	if changed != pinRel {
		t.Fatalf("trunk commit changed %q, want only %q", changed, pinRel)
	}
	wantPin := fmt.Sprintf("%s\n%s\n%s\n", newTip, remote, upstreamBase)
	if committed := git(t, f.dir, "show", trunkAfter+":"+pinRel) + "\n"; committed != wantPin {
		t.Fatalf("committed %s:\n%q\nwant:\n%q", pinRel, committed, wantPin)
	}
	diskBytes, err := os.ReadFile(filepath.Join(f.dir, pinRel))
	if err != nil {
		t.Fatal(err)
	}
	if string(diskBytes) != wantPin {
		t.Fatalf("working tree %s:\n%q\nwant:\n%q", pinRel, string(diskBytes), wantPin)
	}
	if status := git(t, f.dir, "status", "--porcelain", "-uno"); status != "" {
		t.Fatalf("trunk worktree dirty after fork pin landing:\n%s", status)
	}
	if msg := git(t, f.dir, "log", "-1", "--format=%B", trunkAfter); strings.Contains(msg, "item_042") {
		t.Fatalf("pin commit message leaked item id: %q", msg)
	}
}

// TestForkRebaseLandingUpdatesPinTipAndMergeBase verifies that when a fork release
// rebase lands, yolo-packs/fork-pins/<fork> in the landed trunk commit holds both
// the rebased tip and the new upstream release as its merge base.
func TestForkRebaseLandingUpdatesPinTipAndMergeBase(t *testing.T) {
	r := newRebaseFixture(t)
	oldBase := git(t, r.dir, "rev-parse", r.release+"^")
	const remote = "git@github.com:example/tillr"
	pinRel := "yolo-packs/fork-pins/pi"

	write(t, filepath.Join(r.dir, pinRel), fmt.Sprintf("%s\n%s\n%s\n", r.baseSHA, remote, oldBase))
	git(t, r.dir, "add", pinRel)
	git(t, r.dir, "commit", "-m", "track pi fork pin before rebase")
	trunkBefore := r.trunk(t)

	// Point upstream/main in .forks/pi at the new release commit.
	git(t, r.cache, "fetch", "--quiet", "--no-tags", r.dir, r.release)
	git(t, r.cache, "update-ref", "refs/remotes/upstream/main", r.release)

	code, out := r.land(t, "--base-ref", "patch/pi")
	if code != 0 {
		t.Fatalf("rebase land exited %d: %s", code, out)
	}
	if moved := git(t, r.dir, "rev-parse", patchBranch); moved != r.headSHA {
		t.Fatalf("patch/pi = %s, want %s", moved, r.headSHA)
	}
	trunkAfter := r.trunk(t)
	if trunkAfter == trunkBefore {
		t.Fatal("trunk did not advance with the rebased pin commit")
	}
	wantPin := fmt.Sprintf("%s\n%s\n%s\n", r.headSHA, remote, r.release)
	if committed := git(t, r.dir, "show", trunkAfter+":"+pinRel) + "\n"; committed != wantPin {
		t.Fatalf("committed %s:\n%q\nwant:\n%q", pinRel, committed, wantPin)
	}
	diskBytes, err := os.ReadFile(filepath.Join(r.dir, pinRel))
	if err != nil {
		t.Fatal(err)
	}
	if string(diskBytes) != wantPin {
		t.Fatalf("working tree %s:\n%q\nwant:\n%q", pinRel, string(diskBytes), wantPin)
	}
	if status := git(t, r.dir, "status", "--porcelain", "-uno"); status != "" {
		t.Fatalf("trunk worktree dirty after rebase pin landing:\n%s", status)
	}
}

// TestForkLandingRefusesDirtyPinWithoutMovingPatchRef verifies that uncommitted
// edits to yolo-packs/fork-pins/<fork> on the trunk refuse the landing before
// refs/heads/patch/<fork> moves.
func TestForkLandingRefusesDirtyPinWithoutMovingPatchRef(t *testing.T) {
	f := newFixture(t)
	upstreamBase := f.baseSHA
	oldTip := f.headSHA

	const remote = "git@github.com:example/tillr"
	pinRel := "yolo-packs/fork-pins/pi"
	write(t, filepath.Join(f.dir, pinRel), fmt.Sprintf("%s\n%s\n%s\n", oldTip, remote, upstreamBase))
	git(t, f.dir, "add", pinRel)
	git(t, f.dir, "commit", "-m", "track pi fork pin")
	trunkBefore := f.trunk(t)

	git(t, f.dir, "branch", "patch/pi", oldTip)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", oldTip)
	git(t, cache, "update-ref", "refs/remotes/upstream/main", upstreamBase)

	git(t, f.dir, "checkout", "candidate")
	write(t, filepath.Join(f.dir, "fork-change.txt"), "new fork work\n")
	git(t, f.dir, "add", "fork-change.txt")
	git(t, f.dir, "commit", "-m", "feat: fork change")
	f.baseSHA, f.headSHA = oldTip, git(t, f.dir, "rev-parse", "HEAD")
	f.treeSHA = git(t, f.dir, "rev-parse", "HEAD^{tree}")
	git(t, f.dir, "checkout", f.ref)

	// Dirty the pin file in the trunk worktree.
	write(t, filepath.Join(f.dir, pinRel), "dirty-local-edit\n")

	_, code, out := f.land(t, "run-dirty-pin", "pass", "--base-ref", "patch/pi")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "refused" || got.Payload.Reason != "trunk-worktree-uncommitted" {
		t.Fatalf("landing = %s/%s, want refused/trunk-worktree-uncommitted", got.Payload.Result, got.Payload.Reason)
	}
	if moved := git(t, f.dir, "rev-parse", "refs/heads/patch/pi"); moved != oldTip {
		t.Fatalf("patch/pi moved to %s on refused landing, want %s", moved, oldTip)
	}
	if f.trunk(t) != trunkBefore {
		t.Fatalf("trunk moved on refused landing")
	}
}
