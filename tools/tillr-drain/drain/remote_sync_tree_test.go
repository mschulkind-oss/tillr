package drain

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func checkRemoteSyncCandidate(t *testing.T, p *pushFixture, st remoteSyncState, wt, laneFence, want string) {
	t.Helper()
	fenceFile := filepath.Join(t.TempDir(), "fence.txt")
	writeForkFile(t, fenceFile, laneFence)
	candidate := Candidate{ItemID: st.ItemID, Worktree: wt, BaseSHA: st.BaseSHA, HeadSHA: runGit(t, wt, "rev-parse", "HEAD")}
	msg, err := p.l.fenceViolationAt(context.Background(), candidate, fenceFile)
	if err != nil || (want == "" && msg != "") || (want != "" && !strings.Contains(msg, want)) {
		t.Fatalf("pre-gate check = %q, %v; want %q", msg, err, want)
	}
}

func remoteSyncMechanicalTree(t *testing.T, root string, st remoteSyncState) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "merge-tree", "--write-tree", "--merge-base="+st.ConflictParent, st.BaseSHA, st.ConflictCommit)
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("expected mechanical conflict, got %q, %v", out, err)
	}
	tree, _, _ := strings.Cut(string(out), "\n")
	return tree
}

func fileTreeCheckRepair(t *testing.T, p *pushFixture, name string) (remoteSyncState, string) {
	t.Helper()
	root := p.f.root
	remote := p.remoteCommit(t, name, "remote version\n")
	runGit(t, root, "fetch", "-q", "origin")
	st := remoteSyncFilingFixture(t, p, remote, remote, runGit(t, root, "rev-parse", "HEAD"))
	if _, err := p.l.fileOrUpdateRemoteSyncItem(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "repair")
	runGit(t, root, "worktree", "add", "-q", "--detach", wt, st.BaseSHA)
	return st, wt
}

func TestRemoteSyncPreGateTreeCheck(t *testing.T) {
	p := newPushFixture(t)
	st, wt := fileTreeCheckRepair(t, p, "three.txt")
	writeForkFile(t, filepath.Join(wt, "three.txt"), "resolved\n")
	writeForkFile(t, filepath.Join(wt, "one.txt"), "tampered\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-qm", "tampered")
	// A legacy list cannot override the mechanical merge invariant.
	checkRemoteSyncCandidate(t, p, st, wt, "three.txt\none.txt\n", "one.txt")

	runGit(t, wt, "reset", "--hard", "-q", st.BaseSHA)
	writeForkFile(t, filepath.Join(wt, "three.txt"), "resolved\n")
	runGit(t, wt, "add", "three.txt")
	tree := runGit(t, wt, "write-tree")
	merge := runGit(t, wt, "commit-tree", tree, "-p", st.BaseSHA, "-p", st.ConflictCommit, "-m", "two-parent repair")
	runGit(t, wt, "reset", "--hard", "-q", merge)
	checkRemoteSyncCandidate(t, p, st, wt, "three.txt\n", "single linear commit")

	linear := runGit(t, wt, "commit-tree", tree, "-p", st.BaseSHA, "-m", "linear repair")
	runGit(t, wt, "reset", "--hard", "-q", linear)
	checkRemoteSyncCandidate(t, p, st, wt, "three.txt\n", "")

	// An empty legacy list does not bar the actual conflicting path.
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='[]' WHERE item_id=?`, st.ItemID)
		return err
	})
	checkRemoteSyncCandidate(t, p, st, wt, "", "")
}

func TestRemoteSyncPreservesMechanicalNonConflictingChanges(t *testing.T) {
	p := newPushFixture(t)
	root := p.f.root
	writeForkFile(t, filepath.Join(root, "unrelated.txt"), "keep this change\n")
	runGit(t, root, "add", "unrelated.txt")
	runGit(t, root, "commit", "--amend", "--no-edit", "-q")
	st, wt := fileTreeCheckRepair(t, p, "three.txt")
	writeForkFile(t, filepath.Join(wt, "three.txt"), "resolved\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-qm", "omitted the mechanical addition")
	checkRemoteSyncCandidate(t, p, st, wt, "three.txt\n", "unrelated.txt")

	writeForkFile(t, filepath.Join(wt, "unrelated.txt"), "keep this change\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "--amend", "--no-edit", "-q")
	checkRemoteSyncCandidate(t, p, st, wt, "three.txt\n", "")

	runGit(t, wt, "update-index", "--chmod=+x", "unrelated.txt")
	runGit(t, wt, "commit", "--amend", "--no-edit", "-q")
	checkRemoteSyncCandidate(t, p, st, wt, "three.txt\n", "unrelated.txt")
}
