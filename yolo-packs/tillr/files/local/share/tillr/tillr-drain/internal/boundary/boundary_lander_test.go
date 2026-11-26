package boundary

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// TestARealLandingDuringASiblingRunConvictsNothing brackets one lane's run the way the loop
// does, and lands another lane's candidate inside the bracket with the REAL tillr-land, not
// a stub. With two lanes, landings and runs overlap, so every ref the lander writes —
// run pins, its landed markers, the trunk-history pins and the trunk itself — appears in a
// sibling run's after-snapshot. A lander ref the bracket does not excuse would hold that
// sibling's item as a host-boundary violation, which is how refs/tillr/stranded/* stopped
// the loop on 2026-11-14. The stub lander the rest of the suite uses writes none of them.
func TestARealLandingDuringASiblingRunConvictsNothing(t *testing.T) {
	root := scratchUnderDurable(t)
	config := filepath.Join(root, "gitconfig")
	writeForkFile(t, config, "[user]\n name = tillr\n email = tillr@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(repo, "base.txt"), "base\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "base")
	base := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "checkout", "-q", "-b", "candidate")
	writeForkFile(t, filepath.Join(repo, "candidate.txt"), "candidate\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "feat: the other lane's change")
	head := runGit(t, repo, "rev-parse", "HEAD")
	tree := runGit(t, repo, "rev-parse", "HEAD^{tree}")
	runGit(t, repo, "checkout", "-q", "main")
	running := filepath.Join(root, "lanes", "lane-1", run.WorktreeDir)
	runGit(t, repo, "worktree", "add", "-q", "-b", "tillr/lane-1", running, base)

	bin := filepath.Join(root, "tillr-land")
	declareSiblingInputs(t)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join("..", "..", "..", "tillr-land")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	// The drain's trunk_history row makes the lander write its trunk-history pins too.
	db := filepath.Join(root, "state.db")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS trunk_history(id INTEGER PRIMARY KEY,observed_tip TEXT NOT NULL,lost_tip TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT OR REPLACE INTO trunk_history VALUES(1,?,'')`, base)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	payload, err := json.Marshal(map[string]any{"verdict_id": "verdict-b", "plan_id": "plan-b", "registry_digest": "sha256:fixture", "base_sha": base, "tree_sha": tree, "result": "pass", "checks": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := record.Encode(record.Envelope{RecordVersion: record.SupportedVersion, Kind: "gate.verdict", ID: "verdict-b", Producer: record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"}, CreatedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	verdictPath := filepath.Join(root, "gate.verdict.json")
	if err := os.WriteFile(verdictPath, verdict, 0o644); err != nil {
		t.Fatal(err)
	}

	spec := BoundarySpec{Repo: repo, Targets: []string{running}}
	before, err := TakeBoundary(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	land := exec.Command(bin, "pass", "--store", db, "--repo", repo, "--item", "item-b", "--run", "run-item-b-a1",
		"--base", base, "--head", head, "--verdict", verdictPath, "--observed-trunk", base, "--now", "2026-11-16T12:00:00Z")
	if out, err := land.CombinedOutput(); err != nil {
		t.Fatalf("the real lander did not land: %v: %s", err, out)
	}
	after, err := TakeBoundary(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}

	var written []string
	for ref, sha := range after.Refs {
		if strings.HasPrefix(ref, "refs/tillr/") && before.Refs[ref] != sha {
			written = append(written, ref)
		}
	}
	if len(written) == 0 || after.Refs["refs/heads/main"] == before.Refs["refs/heads/main"] {
		t.Fatalf("fixture: the lander wrote no tillr ref or did not move the trunk: %v", written)
	}
	violations, err := compareAll(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("a sibling's real landing (refs %v) convicted the run: %v", written, violations)
	}
}
