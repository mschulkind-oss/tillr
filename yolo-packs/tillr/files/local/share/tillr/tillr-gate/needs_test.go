package main

import (
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
	// The pins are new in the working tree and not yet in the cloned HEAD.
	pins, err := os.ReadDir(filepath.Join(root, "yolo-packs/fork-pins"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range pins {
		data, err := os.ReadFile(filepath.Join(root, "yolo-packs/fork-pins", pin.Name()))
		if err != nil {
			t.Fatal(err)
		}
		memoryWrite(t, filepath.Join(clone, "yolo-packs/fork-pins", pin.Name()), string(data))
	}
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
	for _, check := range checks {
		for _, need := range check.Needs {
			if strings.HasPrefix(need, "ref:refs/heads/patch/") {
				t.Fatalf("local branch need: %s: %s", check.CheckID, need)
			}
		}
		pin := "yolo-packs/fork-pins/" + strings.TrimPrefix(check.CheckID, "patch-ref-")
		found := false
		for _, read := range check.Reads {
			found = found || read == pin
		}
		if !found {
			t.Fatalf("%s does not read %s: %v", check.CheckID, pin, check.Reads)
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
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "clone", "--bare", root, remote).CombinedOutput(); err != nil {
		t.Fatalf("remote: %v: %s", err, out)
	}
	pin := filepath.Join(root, "yolo-packs/fork-pins/pi")
	run := func(want int, contains ...string) {
		t.Helper()
		cmd := exec.Command("python3", program, "patch/pi")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if code != want {
			t.Fatalf("exit %d, want %d: %s", code, want, out)
		}
		for _, text := range contains {
			if !strings.Contains(string(out), text) {
				t.Fatalf("missing %q: %s", text, out)
			}
		}
	}
	for _, script := range []string{`["build:live"]`, `["build:offline"]`} {
		memoryWrite(t, filepath.Join(root, "scripts/fork-build.mjs"), script)
		memoryGit(t, root, "add", "scripts")
		memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "script fixture")
		oid := memoryGit(t, root, "rev-parse", "HEAD")
		memoryGit(t, root, "push", "-q", remote, "HEAD:refs/heads/fixture")
		memoryWrite(t, pin, oid+"\n"+remote+"\n"+oid+"\n")
		want := 1
		if strings.Contains(script, "build:offline") {
			want = 0
		}
		run(want)
	}
	memoryGit(t, root, "update-ref", "refs/heads/patch/pi", "HEAD^")
	run(0)
	memoryGit(t, root, "update-ref", "-d", "refs/heads/patch/pi")
	run(0)
	missing := strings.Repeat("a", 40)
	memoryWrite(t, pin, missing+"\n"+remote+"\n"+missing+"\n")
	run(4, "could not judge", missing, remote)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	check.Command = []string{"python3", program, "patch/pi"}
	_, tree := root, memoryGit(t, root, "rev-parse", "HEAD^{tree}")
	got, err := memoryRun(t, root, tree, check)
	if err == nil || got.Result != ResultError {
		t.Fatalf("missing input must be an error, not a failed check: %+v %v", got, err)
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
