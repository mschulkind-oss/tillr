package main_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredTrunkBranchWithoutMain(t *testing.T) {
	dir := scratch(t, "repo-trunk-setting")
	git(t, dir, "init", "-b", "dev")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	write(t, filepath.Join(dir, ".tillr.toml"), "[trunk]\nbranch = \"dev\"\n")
	git(t, dir, "add", "base.txt", ".tillr.toml")
	git(t, dir, "commit", "-m", "init on dev")
	baseSHA := git(t, dir, "rev-parse", "HEAD")

	// Verify no main branch exists.
	if out := git(t, dir, "branch", "--list", "main"); out != "" {
		t.Fatalf("unexpected main branch: %q", out)
	}

	// Candidate 1 for `pass` while main checkout is on `work`.
	git(t, dir, "checkout", "-b", "lane-1", baseSHA)
	write(t, filepath.Join(dir, "one.txt"), "one\n")
	git(t, dir, "add", "one.txt")
	git(t, dir, "commit", "-m", "lane one")
	head1 := git(t, dir, "rev-parse", "HEAD")

	// Candidate 2 for `one`.
	git(t, dir, "checkout", "-b", "lane-2", baseSHA)
	write(t, filepath.Join(dir, "two.txt"), "two\n")
	git(t, dir, "add", "two.txt")
	git(t, dir, "commit", "-m", "lane two")

	// Switch main checkout to an unrelated branch `work`.
	git(t, dir, "checkout", "-b", "work", baseSHA)

	db := filepath.Join(scratch(t, "store"), "state.db")
	tree1 := git(t, dir, "rev-parse", head1+"^{tree}")
	f := &fixture{dir: dir, baseSHA: baseSHA, headSHA: head1, treeSHA: tree1}
	verdictPath := f.verdictFile(t, "pass")

	code, out := run(t,
		"pass", "--store", db, "--repo", dir,
		"--item", "item_001", "--run", "run-1",
		"--base", baseSHA, "--head", head1, "--verdict", verdictPath,
		"--now", "2026-11-25T00:00:00Z",
	)
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "landed" {
		t.Fatalf("pass landing = %+v, want landed", got.Payload)
	}
	devAfterPass := git(t, dir, "rev-parse", "refs/heads/dev")
	if devAfterPass == baseSHA {
		t.Fatal("dev did not advance after pass")
	}
	if gotBranch := git(t, dir, "symbolic-ref", "--short", "HEAD"); gotBranch != "work" {
		t.Fatalf("main checkout moved to %q, want work", gotBranch)
	}

	// Now test `one` without --trunk (reads [trunk] branch = "dev").
	gateScript2 := filepath.Join(scratch(t, "gate2"), "gate.sh")
	write(t, gateScript2, `#!/bin/sh
set -e
BASE=$(git -C "`+dir+`" merge-base refs/heads/dev refs/heads/lane-2)
HEAD=$(git -C "`+dir+`" rev-parse refs/heads/lane-2)
TREE=$(git -C "`+dir+`" rev-parse "$HEAD^{tree}")
printf '{"record_version":1,"kind":"gate.verdict","id":"verdict_002","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-25T00:01:00Z","payload":{"verdict_id":"verdict_002","plan_id":"plan_001","registry_digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000","base_sha":"%s","tree_sha":"%s","result":"pass","checks":[{"check_id":"go-test","result":"pass","duration_ms":1,"log_ref":"logs/go-test.log"}]}}\n' "$BASE" "$TREE"
`)
	if err := os.Chmod(gateScript2, 0o755); err != nil {
		t.Fatal(err)
	}

	code, out = run(t,
		"one", "--store", db, "--repo", dir,
		"--branch", "lane-2", "--item", "item_002", "--run", "run-2",
		"--gate", gateScript2, "--now", "2026-11-25T00:01:00Z",
	)
	if code != 0 {
		t.Fatalf("one exited %d: %s", code, out)
	}
	got2 := decodeLanding(t, out)
	if got2.Payload.Result != "landed" {
		t.Fatalf("one landing = %+v, want landed", got2.Payload)
	}
	devAfterOne := git(t, dir, "rev-parse", "refs/heads/dev")
	if devAfterOne == devAfterPass {
		t.Fatal("dev did not advance after one")
	}
	if out := git(t, dir, "branch", "--list", "main"); out != "" {
		t.Fatalf("unexpected main branch created: %q", out)
	}
	if gotBranch := git(t, dir, "symbolic-ref", "--short", "HEAD"); gotBranch != "work" {
		t.Fatalf("main checkout moved to %q, want work", gotBranch)
	}
}
