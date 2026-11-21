package main_test

import (
	"path/filepath"
	"testing"
)

func TestLandingRestampsWorkerWithoutJudgingTrunkIdentity(t *testing.T) {
	f := newFixture(t)
	foreign := gitAs(t, f.dir, "Other", "other@example.invalid", "commit-tree", f.baseSHA+"^{tree}", "-p", f.baseSHA, "-m", "foreign")
	git(t, f.dir, "update-ref", "refs/heads/master", foreign, f.baseSHA)
	// Keep the gated tree, but give the worker commit its own author and committer.
	f.headSHA = gitAs(t, f.dir, "Worker", "worker@example.invalid", "commit-tree", f.treeSHA, "-p", f.baseSHA, "-m", "worker change")
	db := filepath.Join(scratch(t, "identity-store"), "state.db")
	code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-identity", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--observed-trunk", f.baseSHA)
	if code != 0 || decodeLanding(t, out).Payload.Result != "landed" {
		t.Fatalf("landing onto foreign trunk commit: %d %s", code, out)
	}
	tip := f.trunk(t)
	if parent := git(t, f.dir, "rev-parse", tip+"^"); parent != foreign {
		t.Fatalf("landing did not preserve foreign parent: %s != %s", parent, foreign)
	}
	want := fixtureName + " <" + fixtureEmail + ">"
	if got := git(t, f.dir, "show", "-s", "--format=%an <%ae>|%cn <%ce>", tip); got != want+"|"+want {
		t.Fatalf("landing identity = %q, want %q for author and committer", got, want)
	}
}
