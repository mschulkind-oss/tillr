package drain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestFetchAndOrdinaryRefsConvictNoRun(t *testing.T) {
	f := newBoundaryFixture(t)
	remote := filepath.Join(scratch(t, "remote"), "repo")
	git(t, f.repo, "clone", f.repo, remote)
	git(t, f.repo, "remote", "add", "fixture", remote)
	git(t, f.repo, "fetch", "fixture")
	before := f.snapshot(t, "before-fetch")
	writeFile(t, filepath.Join(remote, "remote.txt"), "new remote commit\n")
	git(t, remote, "add", ".")
	git(t, remote, "commit", "-m", "remote advanced")
	git(t, remote, "tag", "new-tag")
	git(t, f.repo, "fetch", "fixture", "--tags")
	git(t, f.repo, "branch", "someone-elses-branch", "fixture/main")
	after := f.snapshot(t, "after-fetch")
	if code, output := check(t, before, after); code != 0 {
		t.Fatalf("fetch convicted a run: exit %d: %s", code, output)
	}
	var snapshots [2]drain.Boundary
	for i, path := range []string{before, after} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &snapshots[i]); err != nil {
			t.Fatal(err)
		}
	}
	if violations, err := drain.CompareRun(&snapshots[0], &snapshots[1]); err != nil || len(violations) != 0 {
		t.Fatalf("fetch convicted the loop's run bracket: %v / %v", violations, err)
	}
}
