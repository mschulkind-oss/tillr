package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

func runGitCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func TestPushFaceJSONAndRecordsEvent(t *testing.T) {
	dir := scratch(t, "push-face")
	remote := filepath.Join(dir, "remote.git")
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, dir, "init", "--bare", "-q", remote)
	runGitCLI(t, repo, "init", "-q", "-b", drain.TrunkRef)
	runGitCLI(t, repo, "config", "user.name", "Test")
	runGitCLI(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[push]\nwhat = \"tip\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, repo, "add", ".tillr.toml")
	runGitCLI(t, repo, "commit", "-qm", "init")
	runGitCLI(t, repo, "remote", "add", "origin", remote)
	runGitCLI(t, repo, "push", "-q", "-u", "origin", drain.TrunkRef)

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, repo, "add", "file.txt")
	runGitCLI(t, repo, "commit", "-qm", "second")

	storePath := filepath.Join(repo, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureAt(t, storePath, itemRecord("item_041", "in-queue"))

	code, out := invoke(t, "push", "--root", repo, "--store", storePath, "--json")
	if code != 0 {
		t.Fatalf("push exited %d: %s", code, out)
	}
	rawOut := string(out)
	start, end := strings.Index(rawOut, "{"), strings.LastIndex(rawOut, "}")
	if start < 0 || end < start {
		t.Fatalf("no JSON in output: %q", out)
	}
	var got drain.PushOutcome
	if err := json.Unmarshal([]byte(rawOut[start:end+1]), &got); err != nil {
		t.Fatalf("json %q: %v", out, err)
	}
	if !got.OK || got.What != "tip" || got.Kind != "push-ok" || got.Tip == "" {
		t.Fatalf("PushOutcome = %+v", got)
	}
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='drain.post-landing-action' AND json_extract(payload,'$.action')='push' AND json_extract(payload,'$.kind')='push-ok'`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("recorded push events = %d, want 1", count)
	}
}

func TestPushFaceGreenModePublishesParkedGreenAndAdvancesPublishedRef(t *testing.T) {
	dir := scratch(t, "push-face-green")
	remote := filepath.Join(dir, "remote.git")
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, dir, "init", "--bare", "-q", remote)
	runGitCLI(t, repo, "init", "-q", "-b", drain.TrunkRef)
	runGitCLI(t, repo, "config", "user.name", "Test")
	runGitCLI(t, repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, ".tillr.toml"), []byte("[push]\nwhat = \"green\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, repo, "add", ".tillr.toml")
	runGitCLI(t, repo, "commit", "-qm", "init")
	runGitCLI(t, repo, "remote", "add", "origin", remote)
	runGitCLI(t, repo, "push", "-q", "-u", "origin", drain.TrunkRef)

	// Commit C1 is proven (refs/tillr/green); commit C2 is unproven on main.
	if err := os.WriteFile(filepath.Join(repo, "green.txt"), []byte("green\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, repo, "add", "green.txt")
	runGitCLI(t, repo, "commit", "-qm", "c1")
	greenSHA := strings.TrimSpace(runGitCLI(t, repo, "rev-parse", "HEAD"))
	runGitCLI(t, repo, "update-ref", drain.GreenRef, greenSHA)

	if err := os.WriteFile(filepath.Join(repo, "tip.txt"), []byte("tip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, repo, "add", "tip.txt")
	runGitCLI(t, repo, "commit", "-qm", "c2")

	storePath := filepath.Join(repo, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureAt(t, storePath, itemRecord("item_042", "landed"))

	code, out := invoke(t, "push", "--root", repo, "--store", storePath, "--json")
	if code != 0 {
		t.Fatalf("push exited %d: %s", code, out)
	}
	rawOut := string(out)
	start, end := strings.Index(rawOut, "{"), strings.LastIndex(rawOut, "}")
	if start < 0 || end < start {
		t.Fatalf("no JSON in output: %q", out)
	}
	var got drain.PushOutcome
	if err := json.Unmarshal([]byte(rawOut[start:end+1]), &got); err != nil {
		t.Fatalf("json %q: %v", out, err)
	}
	if !got.OK || got.What != "green" || got.Kind != "push-ok" || got.Tip != greenSHA {
		t.Fatalf("PushOutcome = %+v, want Tip=%s", got, greenSHA)
	}
	if gotRemote := strings.TrimSpace(runGitCLI(t, remote, "rev-parse", "refs/heads/"+drain.TrunkRef)); gotRemote != greenSHA {
		t.Fatalf("remote tip = %s, want green %s", gotRemote, greenSHA)
	}
	if gotPub := strings.TrimSpace(runGitCLI(t, repo, "rev-parse", "refs/tillr/published/origin/"+drain.TrunkRef)); gotPub != greenSHA {
		t.Fatalf("published ref = %s, want green %s", gotPub, greenSHA)
	}
}
