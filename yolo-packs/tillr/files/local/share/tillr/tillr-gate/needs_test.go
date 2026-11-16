package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The structural suite judges committed pack metadata, not optional local refs.
// Copy the working script so this regression also runs before its fix is committed.
func TestFreshCloneStructuralWithoutPatchBranches(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	cmd := exec.Command("git", "clone", "--quiet", "--no-local", "--single-branch", root, clone)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	if refs := memoryGit(t, clone, "for-each-ref", "--format=%(refname)", "refs/heads/patch/"); refs != "" {
		t.Fatalf("unexpected patch branches: %s", refs)
	}
	raw, err := os.ReadFile(filepath.Join(root, "tests/pi-patch-series.test.sh"))
	if err != nil {
		t.Fatal(err)
	}
	memoryWrite(t, filepath.Join(clone, "tests/pi-patch-series.test.sh"), string(raw))
	cmd = exec.Command("sh", "tests/pi-patch-series.test.sh")
	cmd.Dir = clone
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("structural without refs: %v:\n%s", err, out)
	}
	reg, err := loadCheckDirectory(filepath.Join(root, "tools/gate/checks"))
	if err != nil {
		t.Fatal(err)
	}
	var checks []PlanCheck
	for _, check := range reg.Plan([]string{"yolo-packs/pi-fork/pack.json", "yolo-packs/matt/pack.json"}, clone) {
		if strings.HasPrefix(check.ID, "patch-ref-") {
			checks = append(checks, PlanCheck{CheckID: check.ID, Scope: check.Scope, Reads: check.Reads, Tools: check.Tools, Command: check.Command, Needs: check.Needs})
		}
	}
	if len(checks) != 8 {
		t.Fatalf("fork ref declarations: %d", len(checks))
	}
	tree := memoryGit(t, clone, "rev-parse", "HEAD^{tree}")
	envelope, err := Run(memoryPlan(t, tree, checks...), clone, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var verdict VerdictPayload
	if err := json.Unmarshal(envelope.Payload, &verdict); err != nil {
		t.Fatal(err)
	}
	if verdict.Result != ResultPass {
		t.Fatalf("verdict: %+v", verdict)
	}
	for _, check := range verdict.Checks {
		if check.Result != "unavailable" || len(check.MissingNeeds) != 1 || !strings.HasPrefix(check.MissingNeeds[0].Command, "git fetch origin ") {
			t.Fatalf("fork outcome: %+v", check)
		}
	}
}

func TestMissingRefUnavailableBeforeMemoryOrCommand(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Needs = []string{"ref:refs/heads/patch/pi"}
	// A tool that does not exist must not be probed before the missing need.
	check.Tools = []string{"missing-tool-for-unavailable-check"}
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Result != "unavailable" {
		t.Fatalf("missing ref: %+v %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command ran: %v", err)
	}
	if len(memoryFiles(t, memoryStore(t, root))) != 0 {
		t.Fatal("unavailable was remembered")
	}
}

func TestPatchRefProgramJudgesPiBranchContents(t *testing.T) {
	program, err := filepath.Abs("../gate/checks/patch-ref.py")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := memoryRepo(t)
	memoryGit(t, root, "update-ref", "refs/heads/patch/pi", "HEAD")
	run := func(wantPass bool) {
		cmd := exec.Command("python3", program, "patch/pi")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantPass {
			t.Fatalf("pi branch content: %v: %s", err, out)
		}
	}
	run(false) // Branch exists but does not add the script.
	for _, script := range []string{`["build:live"]`, `["build:offline"]`} {
		memoryWrite(t, filepath.Join(root, "scripts/fork-build.mjs"), script)
		memoryGit(t, root, "add", ".")
		memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "script fixture")
		memoryGit(t, root, "update-ref", "refs/heads/patch/pi", "HEAD")
		run(strings.Contains(script, "build:offline"))
	}
}

func TestRefPatternFingerprintTracksMembership(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	check.Needs = []string{"ref:refs/heads/patch/*"}
	memoryGit(t, root, "update-ref", "refs/heads/patch/one", "HEAD")
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Memory != "passed" {
		t.Fatalf("first: %+v %v", first, err)
	}
	memoryGit(t, root, "update-ref", "refs/heads/patch/two", "HEAD")
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "passed" || second.Fingerprint == first.Fingerprint {
		t.Fatalf("added match: %+v %v", second, err)
	}
	memoryGit(t, root, "update-ref", "-d", "refs/heads/patch/one")
	third, err := memoryRun(t, root, tree, check)
	if err != nil || third.Memory != "passed" || third.Fingerprint == second.Fingerprint {
		t.Fatalf("removed match: %+v %v", third, err)
	}
}

func TestRefNeedRejectsMalformedPatternAndExactPrefixMatch(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	for _, need := range []string{"ref:patch/pi", "ref:refs/heads/patch/pi-?", "ref:refs/heads/patch/pi-[ab]", "ref:refs/heads/patch/**", "ref:refs/heads/patch/"} {
		check.Needs = []string{need}
		got, err := memoryRun(t, root, tree, check)
		if err == nil || got.Result != ResultError {
			t.Fatalf("malformed ref %q: %+v %v", need, got, err)
		}
	}
	memoryGit(t, root, "update-ref", "refs/heads/patch/pi/other", "HEAD")
	check.Needs = []string{"ref:refs/heads/patch/pi"}
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Result != ResultUnavailable {
		t.Fatalf("prefix matched exact ref: %+v %v", got, err)
	}
}

func TestNeededRefChangedDuringCommandIsNotRemembered(t *testing.T) {
	root, tree := memoryRepo(t)
	memoryGit(t, root, "update-ref", "refs/heads/patch/pi", "HEAD")
	commit := memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit-tree", tree, "-p", "HEAD", "-m", "moved ref")
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	check.Needs = []string{"ref:refs/heads/patch/pi"}
	check.Command = []string{"git", "update-ref", "refs/heads/patch/pi", commit}
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Result != ResultPass || got.Memory != "not remembered" {
		t.Fatalf("mid-command move: %+v %v", got, err)
	}
	if len(memoryFiles(t, memoryStore(t, root))) != 0 {
		t.Fatal("result remembered under stale ref")
	}
}

func TestNeededRefMovesInvalidateMemory(t *testing.T) {
	root, tree := memoryRepo(t)
	ref := "refs/heads/patch/pi"
	memoryGit(t, root, "update-ref", ref, "HEAD")
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Needs = []string{"ref:" + ref}
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Memory != "passed" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "remembered" {
		t.Fatalf("second: %+v %v", second, err)
	}
	parent := memoryGit(t, root, "rev-parse", "HEAD")
	commit := memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit-tree", tree, "-p", parent, "-m", "moved ref")
	memoryGit(t, root, "update-ref", ref, commit)
	third, err := memoryRun(t, root, tree, check)
	if err != nil || third.Memory != "passed" || third.Fingerprint == first.Fingerprint {
		t.Fatalf("moved: %+v %v", third, err)
	}
	raw, _ := os.ReadFile(marker)
	if strings.Count(string(raw), "ran\n") != 2 {
		t.Fatalf("runs: %q", raw)
	}
	memoryGit(t, root, "update-ref", "-d", ref)
	missing, err := memoryRun(t, root, tree, check)
	if err != nil || missing.Result != "unavailable" {
		t.Fatalf("removed ref reused memory: %+v %v", missing, err)
	}
}
