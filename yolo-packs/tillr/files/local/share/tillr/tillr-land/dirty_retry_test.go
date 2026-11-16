package main_test

import (
	"os"
	"path/filepath"
	"testing"
)

// A retained run may call pass again with its original verdict after the owner
// resolves the path. Use the same store, run, head and verdict on both calls.
func TestDirtyLandingRetriesOriginalVerdictAfterOwnerResolution(t *testing.T) {
	for _, resolution := range []string{"restore", "commit"} {
		t.Run(resolution, func(t *testing.T) {
			f := newFixture(t)
			write(t, filepath.Join(f.dir, "candidate.txt"), "owner\n")
			db, code, out := f.land(t, "run-1", "pass")
			if code != 1 || decodeLanding(t, out).Payload.Reason != "trunk-worktree-uncommitted" {
				t.Fatalf("dirty path not refused: %d %s", code, out)
			}
			if resolution == "restore" {
				if err := os.Remove(filepath.Join(f.dir, "candidate.txt")); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, filepath.Join(f.dir, "candidate.txt"), "candidate\n")
				git(t, f.dir, "add", "candidate.txt")
				git(t, f.dir, "commit", "-m", "owner")
			}
			code, out = run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-1", "--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"), "--now", "2026-11-10T00:01:00Z")
			if code != 0 {
				t.Fatalf("resolved path did not land: %d %s", code, out)
			}
			got := decodeLanding(t, out)
			if got.Payload.Result != "landed" || got.Payload.HeadSHA != f.headSHA || got.Payload.GatedTree != f.treeSHA {
				t.Fatalf("retry changed candidate or verdict binding: %s", out)
			}
			if got := git(t, f.dir, "status", "--porcelain"); got != "" {
				t.Fatalf("dirty after retry: %s", got)
			}
		})
	}
}
