package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestTrunkProofNeverReusesCommitIdentityAcrossCommitsAndFailsDisallowedAuthor(t *testing.T) {
	rawDecl, err := os.ReadFile(filepath.Join("..", "..", "gate", "checks", "commit-identity-test.json"))
	if err != nil {
		t.Fatalf("read commit-identity-test.json: %v", err)
	}
	var repoDecl struct {
		Remember *bool `json:"remember"`
	}
	if err := json.Unmarshal(rawDecl, &repoDecl); err != nil {
		t.Fatalf("parse commit-identity-test.json: %v", err)
	}
	if repoDecl.Remember == nil || *repoDecl.Remember {
		t.Fatalf("tools/gate/checks/commit-identity-test.json must set remember=false, got %v", repoDecl.Remember)
	}

	root, base := heavyRepo(t)
	work := scratchUnderDurable(t)

	gateBin := filepath.Join(work, "tillr-gate")
	buildGate := exec.Command("go", "build", "-o", gateBin, ".")
	buildGate.Dir = filepath.Join("..", "..", "tillr-gate")
	if raw, err := buildGate.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-gate: %v\n%s", err, raw)
	}

	idBin := filepath.Join(work, "git-identity")
	buildID := exec.Command("go", "build", "-o", idBin, "./cmd/git-identity")
	buildID.Dir = filepath.Join("..", "..", "tillr")
	if raw, err := buildID.CombinedOutput(); err != nil {
		t.Fatalf("build git-identity: %v\n%s", err, raw)
	}

	origin := filepath.Join(work, "origin.git")
	if raw, err := exec.Command("git", "init", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare origin: %v\n%s", err, raw)
	}
	heavyGit(t, root, "remote", "add", "origin", origin)
	heavyGit(t, root, "push", "origin", base+":refs/heads/main")
	heavyGit(t, root, "fetch", "origin")

	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	allowedJSON, err := json.Marshal([][]string{{"Fixture", "fixture@example.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "gate", "allowed-commit-identities.json"), string(allowedJSON)+"\n")
	idDecl, err := json.Marshal(map[string]any{
		"id":         "commit-identity-test",
		"label":      "commit-identity-test",
		"scope":      "tree",
		"tier":       "heavy",
		"timeout_ms": 30000,
		"command":    []string{idBin},
		"reads":      []string{"tools/gate/"},
		"remember":   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checksDir, "commit-identity-test.json"), string(idDecl))
	writeForkFile(t, filepath.Join(checksDir, "other.json"), `{"id":"other","label":"other","scope":"tree","tier":"heavy","timeout_ms":30000,"command":["sh","-c","exit 0"],"reads":["tools/gate/"]}`)

	tip1 := heavyCommit(t, root, "allowed-change")

	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: gateBin}
	runner := HeavyRunner{
		Root:  root,
		Lock:  loopHeld(t, root),
		Work:  filepath.Join(work, ".rolling", "worktree"),
		Trunk: TrunkRef,
		Judge: steps.JudgeHeavy,
	}
	out1, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("first proof: %v", err)
	}
	if len(out1.Runs) != 1 || out1.Runs[0].Result != "green" {
		t.Fatalf("first proof = %+v, want green", out1)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip1 {
		t.Fatalf("green = %s, want %s", green, tip1)
	}
	// Simulate pushing the proved commit to origin/main.
	heavyGit(t, root, "push", "origin", tip1+":refs/heads/main")
	heavyGit(t, root, "fetch", "origin")

	// Add a commit outside tools/gate/ authored and committed as a disallowed identity.
	writeForkFile(t, filepath.Join(root, "unrelated.txt"), "first\n")
	heavyGit(t, root, "add", "unrelated.txt")
	commitCmd := exec.Command("git", "-C", root, "commit", "-m", "first")
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=tillr",
		"GIT_AUTHOR_EMAIL=tillr@example.com",
		"GIT_COMMITTER_NAME=tillr",
		"GIT_COMMITTER_EMAIL=tillr@example.com",
	)
	if raw, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("commit as disallowed identity: %v\n%s", err, raw)
	}

	out2, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("second proof: %v", err)
	}
	if len(out2.Runs) != 1 || out2.Runs[0].Result != "red" {
		t.Fatalf("second proof = %+v, want red", out2)
	}
	if !slices.Contains(out2.Runs[0].ReusedChecks, "other") {
		t.Fatalf("expected unrelated check 'other' to be reused, reused=%v", out2.Runs[0].ReusedChecks)
	}
	if slices.Contains(out2.Runs[0].ReusedChecks, "commit-identity-test") {
		t.Fatalf("commit-identity-test was reused across commits: %v", out2.Runs[0].ReusedChecks)
	}
	var foundIDCheck bool
	for _, chk := range out2.Runs[0].Checks {
		if chk.CheckID == "commit-identity-test" {
			foundIDCheck = true
			if chk.Result != "fail" {
				t.Fatalf("commit-identity-test result = %q, want fail", chk.Result)
			}
		}
	}
	if !foundIDCheck {
		t.Fatalf("commit-identity-test missing from checks: %+v", out2.Runs[0].Checks)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip1 {
		t.Fatalf("green moved to %s on red proof, want %s", green, tip1)
	}
}
