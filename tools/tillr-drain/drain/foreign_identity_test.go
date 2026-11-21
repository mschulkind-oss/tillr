package drain

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func foreignCommit(t *testing.T, root, parent string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "commit-tree", runGit(t, root, "rev-parse", parent+"^{tree}"), "-p", parent, "-m", "foreign")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Other", "GIT_AUTHOR_EMAIL=other@example.invalid", "GIT_COMMITTER_NAME=Other", "GIT_COMMITTER_EMAIL=other@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("foreign commit: %v %s", err, out)
	}
	sha := strings.TrimSpace(string(out))
	runGit(t, root, "update-ref", "refs/heads/main", sha, parent)
	return sha
}

func TestForeignTrunkCommitDoesNotHoldGatedCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a")
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	base := runGit(t, f.root, "rev-parse", "HEAD")
	historyCandidate(t, f, l, base)
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	foreign := foreignCommit(t, f.root, base)
	var result LoopResult
	historyPass(t, l, &result)
	if steps.lands != 1 || result.Landed != 1 {
		t.Fatalf("foreign trunk commit %s held landing: %+v", foreign, result)
	}
}
