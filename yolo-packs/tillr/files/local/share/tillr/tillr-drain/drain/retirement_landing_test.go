package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
)

func TestLoopRetiredBlockLandsUnderTheWorkerMessage(t *testing.T) {
	f, loop := passFixture(t, "item-a")
	root := scratchUnderDurable(t)
	config := filepath.Join(root, "gitconfig")
	writeForkFile(t, config, "[user]\n name = tillr\n email = tillr@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	doc := filepath.Join(repo, "docs", "work.md")
	writeForkFile(t, doc, "# Work\n\n<!-- tillr: item id=\"item-a\" paths=\"candidate.txt\" -->\n\n- **c1:** Work lands.\n\n## Next\n\nKeep this.\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "base")
	base := runGit(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "checkout", "-b", "candidate")
	writeForkFile(t, filepath.Join(repo, "candidate.txt"), "worker\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "feat: worker's own message")
	worker := runGit(t, repo, "rev-parse", "HEAD")
	candidate, err := loop.retireDocumentBlock(context.Background(), Candidate{ItemID: "item-a", RunID: "run-retired", Worktree: repo, BaseSHA: base, HeadSHA: worker})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.WorkerHeadSHA != worker || candidate.HeadSHA == worker {
		t.Fatalf("retirement binding: %+v", candidate)
	}
	if subject := runGit(t, repo, "show", "-s", "--format=%s", candidate.HeadSHA); subject != "chore: retire landed document block" {
		t.Fatalf("maintenance subject: %s", subject)
	}
	tree := runGit(t, repo, "rev-parse", candidate.HeadSHA+"^{tree}")
	runGit(t, repo, "checkout", "main")
	bin := filepath.Join(root, "tillr-land")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = filepath.Join("..", "..", "tillr-land")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-loop", "tillr-loop"))
	if err != nil {
		t.Fatal(err)
	}
	dir := RunDir(repo, candidate.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"verdict_id": "verdict-retired", "plan_id": "plan-retired", "registry_digest": "sha256:fixture", "base_sha": base, "tree_sha": tree, "result": "pass", "checks": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := record.Encode(record.Envelope{RecordVersion: record.SupportedVersion, Kind: "gate.verdict", ID: "verdict-retired", Producer: record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"}, CreatedAt: loop.cfg.Now(), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gate.verdict.json"), verdict, 0o644); err != nil {
		t.Fatal(err)
	}
	steps := &ProcessSteps{Root: repo, LandProgram: "python3 " + adapter + " land --store " + f.storePath + " --tillr-land " + bin}
	outcome, err := steps.Land(context.Background(), candidate)
	if err != nil || outcome.Result != LandLanded {
		t.Fatalf("land: %+v / %v", outcome, err)
	}
	if subject := runGit(t, repo, "show", "-s", "--format=%s", "main"); subject != "feat: worker's own message" {
		t.Fatalf("landed subject: %s", subject)
	}
	if got := runGit(t, repo, "rev-parse", "main^{tree}"); got != tree {
		t.Fatalf("landed tree %s != retirement tree %s", got, tree)
	}
	text, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(text), "tillr: item") || !strings.Contains(string(text), "Keep this.") {
		t.Fatalf("landed document: %s", text)
	}
}
