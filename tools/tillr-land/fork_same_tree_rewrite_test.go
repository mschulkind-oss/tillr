package main_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

type sameTreeFixture struct {
	fixture
	upstreamBase string
	oldTip       string
	cache        string
	gateDir      string
	runID        string
	target       string
}

func newSameTreeFixture(t *testing.T) sameTreeFixture {
	t.Helper()
	f := newFixture(t)
	upstreamBase := f.baseSHA

	// Build a 3-commit linear patch stack above upstreamBase on candidate.
	git(t, f.dir, "checkout", "candidate")
	write(t, filepath.Join(f.dir, "part2.txt"), "second\n")
	git(t, f.dir, "add", "part2.txt")
	git(t, f.dir, "commit", "-m", "patch commit 2")
	write(t, filepath.Join(f.dir, "part3.txt"), "third\n")
	git(t, f.dir, "add", "part3.txt")
	git(t, f.dir, "commit", "-m", "patch commit 3")
	oldTip := git(t, f.dir, "rev-parse", "HEAD")
	oldTree := git(t, f.dir, "rev-parse", "HEAD^{tree}")

	// Publish oldTip to patch/pi and seed .forks/pi with upstream/main at upstreamBase.
	git(t, f.dir, "branch", "patch/pi", oldTip)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", oldTip)
	git(t, cache, "branch", "patch/pi", oldTip)
	git(t, cache, "update-ref", "refs/remotes/upstream/main", upstreamBase)

	// Fold the 3 commits into 2 linear commits above upstreamBase with the exact same tree.
	candidateContent := git(t, f.dir, "show", oldTip+":candidate.txt") + "\n"
	git(t, f.dir, "checkout", "--detach", upstreamBase)
	write(t, filepath.Join(f.dir, "candidate.txt"), candidateContent)
	write(t, filepath.Join(f.dir, "part2.txt"), "second\n")
	git(t, f.dir, "add", "candidate.txt", "part2.txt")
	git(t, f.dir, "commit", "-m", "folded commit 1")
	write(t, filepath.Join(f.dir, "part3.txt"), "third\n")
	git(t, f.dir, "add", "part3.txt")
	git(t, f.dir, "commit", "-m", "folded commit 2")
	newTip := git(t, f.dir, "rev-parse", "HEAD")
	newTree := git(t, f.dir, "rev-parse", "HEAD^{tree}")
	if newTree != oldTree {
		t.Fatalf("folded tree %s != old tree %s", newTree, oldTree)
	}
	git(t, f.dir, "branch", "-f", "candidate", newTip)
	git(t, f.dir, "checkout", f.ref)

	f.baseSHA = oldTip
	f.headSHA = newTip
	f.treeSHA = newTree

	const runID = "run-same-tree-rewrite"
	s := sameTreeFixture{
		fixture:      f,
		upstreamBase: upstreamBase,
		oldTip:       oldTip,
		cache:        cache,
		gateDir:      filepath.Join(f.dir, ".tillr/loop", runID),
		runID:        runID,
		target:       filepath.Join(f.dir, "bin/pi-forks"),
	}
	s.gate(t)
	return s
}

func (s sameTreeFixture) gate(t *testing.T) {
	t.Helper()
	v := s.verdict("pass")
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(forkBuildDeclaration+"[]")))
	v["registry_digest"] = digest
	write(t, filepath.Join(s.gateDir, "gate.verdict.json"), envelope(t, "gate.verdict", "verdict_001", v))
	write(t, filepath.Join(s.gateDir, "gate.change.json"), envelope(t, "gate.change", "change-"+s.runID, map[string]string{
		"base_sha": s.baseSHA, "head_sha": s.headSHA, "tree_sha": s.treeSHA,
	}))
	write(t, filepath.Join(s.gateDir, "fork.registry.json"), fmt.Sprintf(`{"digest":%q,"declarations":%s,"in_process":[]}`, digest, forkBuildDeclaration))
}

func (s sameTreeFixture) installSameReleaseTarget(t *testing.T) {
	t.Helper()
	write(t, s.target, fmt.Sprintf("#!/bin/sh\n[ \"$*\" = 'patch-branch target --fetch --fork pi' ] || exit 2\nprintf 'upstream/pi\\trelease\\trefs/tags/v1\\t%s\\n'\n", s.upstreamBase))
	if err := os.Chmod(s.target, 0755); err != nil {
		t.Fatal(err)
	}
}

func (s sameTreeFixture) land(t *testing.T) (string, int, []byte, string) {
	t.Helper()
	db := filepath.Join(scratch(t, "same-tree-store"), "state.db")
	args := s.fixture.passArgs(t, db, "item_042", s.runID,
		"--base-ref", patchBranch,
		"--verdict", filepath.Join(s.gateDir, "gate.verdict.json"),
	)
	code, out, stderr := passBoth(t, args)
	return db, code, out, stderr
}

func (s sameTreeFixture) historyPin() string {
	return "refs/tillr/patch-history/pi/" + s.oldTip
}

func TestForkSameTreeRewriteDeliversAndRecordsOldHead(t *testing.T) {
	s := newSameTreeFixture(t)

	// Track a fork pin on trunk to verify it updates alongside the same-tree rewrite.
	const remote = "git@github.com:example/tillr"
	const pinRel = "yolo-packs/fork-pins/pi"
	write(t, filepath.Join(s.dir, pinRel), fmt.Sprintf("%s\n%s\n%s\n", s.oldTip, remote, s.upstreamBase))
	git(t, s.dir, "add", pinRel)
	git(t, s.dir, "commit", "-m", "track pi fork pin")

	// Deliberately omit bin/pi-forks: same-tree rewrites do not need release resolution.
	db, code, out, stderr := s.land(t)
	if code != 0 {
		t.Fatalf("same-tree rewrite exited %d: out=%s stderr=%s", code, out, stderr)
	}

	got := decodeLanding(t, out).Payload
	if got.Result != land.ResultLanded || got.TrunkBefore != s.oldTip || got.TrunkAfter != s.headSHA || got.GatedTree != s.treeSHA || got.LandedTree != s.treeSHA {
		t.Fatalf("unexpected landing payload: %+v", got)
	}

	// Patch branch and shared fork cache moved to the rewritten head.
	for _, check := range []struct{ dir, ref, want string }{
		{s.dir, patchBranch, s.headSHA},
		{s.dir, "refs/tillr/landed/" + s.runID, s.headSHA},
		{s.cache, patchBranch, s.headSHA},
		{s.cache, "refs/heads/main", s.headSHA},
	} {
		if actual := git(t, check.dir, "rev-parse", check.ref); actual != check.want {
			t.Errorf("%s %s = %s, want %s", check.dir, check.ref, actual, check.want)
		}
	}

	// Old head is recorded in patch-history and in the durable landing receipt.
	if pinned := git(t, s.dir, "rev-parse", s.historyPin()); pinned != s.oldTip {
		t.Fatalf("history pin %s = %s, want %s", s.historyPin(), pinned, s.oldTip)
	}
	st, err := store.Open(context.Background(), db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	events, err := st.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var receipt land.Landing
	found := false
	for _, e := range events {
		if e.Kind != land.LandingKind {
			continue
		}
		if err := json.Unmarshal(e.Payload, &receipt); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found || receipt.TrunkBefore != s.oldTip || receipt.TrunkAfter != s.headSHA {
		t.Fatalf("stored receipt found=%v receipt=%+v, want trunk_before=%s trunk_after=%s", found, receipt, s.oldTip, s.headSHA)
	}

	// Tracked fork pin on trunk recorded the new tip with the same upstream base.
	wantPin := fmt.Sprintf("%s\n%s\n%s\n", s.headSHA, remote, s.upstreamBase)
	if committed := git(t, s.dir, "show", s.trunk(t)+":"+pinRel) + "\n"; committed != wantPin {
		t.Fatalf("committed %s:\n%q\nwant:\n%q", pinRel, committed, wantPin)
	}
}

func TestForkSameTreeRewriteRefusesDifferentTree(t *testing.T) {
	s := newSameTreeFixture(t)
	s.installSameReleaseTarget(t)

	// Rewrite candidate with a different file content (different tree).
	git(t, s.dir, "checkout", "candidate")
	write(t, filepath.Join(s.dir, "part3.txt"), "changed content\n")
	git(t, s.dir, "add", "part3.txt")
	git(t, s.dir, "commit", "--amend", "-m", "folded commit 2 with diff")
	s.headSHA = git(t, s.dir, "rev-parse", "HEAD")
	s.treeSHA = git(t, s.dir, "rev-parse", "HEAD^{tree}")
	git(t, s.dir, "checkout", s.ref)
	s.gate(t)

	_, code, out, stderr := s.land(t)
	if code != 1 || decodeLanding(t, out).Payload.Reason != "patch-rewind" {
		t.Fatalf("different-tree rewrite: code=%d out=%s stderr=%s, want exit 1 patch-rewind", code, out, stderr)
	}
	if got := git(t, s.dir, "rev-parse", patchBranch); got != s.oldTip {
		t.Fatalf("patch/pi moved to %s, want %s", got, s.oldTip)
	}
	if got := git(t, s.dir, "for-each-ref", "--format=%(objectname)", s.historyPin()); got != "" {
		t.Fatalf("refused rewrite left history pin %s", got)
	}
}

func TestForkSameTreeRewriteRefusesNonLinearHistory(t *testing.T) {
	s := newSameTreeFixture(t)
	s.installSameReleaseTarget(t)

	// Create a merge commit above upstreamBase whose resulting tree still equals oldTip^{tree}.
	side := git(t, s.dir, "commit-tree", s.upstreamBase+"^{tree}", "-p", s.upstreamBase, "-m", "side commit")
	merge := git(t, s.dir, "commit-tree", s.treeSHA, "-p", s.headSHA, "-p", side, "-m", "merge above upstream")
	s.headSHA = merge
	git(t, s.dir, "branch", "-f", "candidate", merge)
	s.gate(t)

	_, code, out, stderr := s.land(t)
	if code != 1 || decodeLanding(t, out).Payload.Reason != "patch-rewind" {
		t.Fatalf("non-linear same-tree rewrite: code=%d out=%s stderr=%s, want exit 1 patch-rewind", code, out, stderr)
	}
	if got := git(t, s.dir, "rev-parse", patchBranch); got != s.oldTip {
		t.Fatalf("patch/pi moved to %s, want %s", got, s.oldTip)
	}
}

func TestForkSameTreeRewriteRefusesRewrittenUpstreamBase(t *testing.T) {
	t.Run("orphan", func(t *testing.T) {
		s := newSameTreeFixture(t)
		s.installSameReleaseTarget(t)

		// Create an orphan commit with the exact same tree (does not descend from upstreamBase).
		orphan := git(t, s.dir, "commit-tree", s.treeSHA, "-m", "orphan same-tree commit")
		s.headSHA = orphan
		git(t, s.dir, "branch", "-f", "candidate", orphan)
		s.gate(t)

		_, code, out, stderr := s.land(t)
		if code != 1 || decodeLanding(t, out).Payload.Reason != "patch-rewind" {
			t.Fatalf("orphan same-tree rewrite: code=%d out=%s stderr=%s, want exit 1 patch-rewind", code, out, stderr)
		}
		if got := git(t, s.dir, "rev-parse", patchBranch); got != s.oldTip {
			t.Fatalf("patch/pi moved to %s, want %s", got, s.oldTip)
		}
	})
	t.Run("rewritten-below-upstream-base", func(t *testing.T) {
		s := newSameTreeFixture(t)
		// Advance upstreamBase by one commit so upstreamBase has a parent, then
		// place oldTip above the newer upstreamBase and rewrite from the older parent.
		olderBase := s.upstreamBase
		newerBase := git(t, s.dir, "commit-tree", olderBase+"^{tree}", "-p", olderBase, "-m", "upstream base")
		oldTip := git(t, s.dir, "commit-tree", s.treeSHA, "-p", newerBase, "-m", "patch above newer base")
		git(t, s.dir, "update-ref", patchBranch, oldTip)
		git(t, s.cache, "fetch", "--quiet", "--no-tags", s.dir, oldTip, newerBase)
		git(t, s.cache, "checkout", "--quiet", "-B", "main", oldTip)
		git(t, s.cache, "update-ref", patchBranch, oldTip)
		git(t, s.cache, "update-ref", "refs/remotes/upstream/main", newerBase)

		s.upstreamBase = newerBase
		s.oldTip = oldTip
		s.baseSHA = oldTip
		s.installSameReleaseTarget(t)

		// Rewritten head descends from olderBase, skipping newerBase, with the same tree.
		rewritten := git(t, s.dir, "commit-tree", s.treeSHA, "-p", olderBase, "-m", "rewrote upstream base")
		s.headSHA = rewritten
		git(t, s.dir, "branch", "-f", "candidate", rewritten)
		s.gate(t)

		_, code, out, stderr := s.land(t)
		if code != 1 || decodeLanding(t, out).Payload.Reason != "patch-rewind" {
			t.Fatalf("rewritten-base same-tree rewrite: code=%d out=%s stderr=%s, want exit 1 patch-rewind", code, out, stderr)
		}
		if got := git(t, s.dir, "rev-parse", patchBranch); got != oldTip {
			t.Fatalf("patch/pi moved to %s, want %s", got, oldTip)
		}
	})
}
