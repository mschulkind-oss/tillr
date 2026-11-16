package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// dualRunReport is the harness's report as a case reads it. A case asserts the fields it
// is about; the rest are what the harness always writes.
type dualRunReport struct {
	Root           string `json:"root"`
	RegistryDigest string `json:"registry_digest"`
	Candidates     []struct {
		Candidate struct {
			Name         string   `json:"name"`
			Base         string   `json:"base"`
			Head         string   `json:"head"`
			ChangedPaths []string `json:"changed_paths"`
		} `json:"candidate"`
		InProcess []string `json:"in_process"`
		Go        struct {
			PlanOrder   []string `json:"plan_order"`
			PlanKinds   []string `json:"plan_kinds"`
			PlanExit    int      `json:"plan_exit"`
			Verdict     string   `json:"verdict"`
			VerdictExit int      `json:"verdict_exit"`
		} `json:"go"`
		Python struct {
			PlanKinds   []string `json:"plan_kinds"`
			PlanExit    int      `json:"plan_exit"`
			Verdict     string   `json:"verdict"`
			VerdictExit int      `json:"verdict_exit"`
		} `json:"python"`
		Agreement     string `json:"agreement"`
		Disagreements []struct {
			Kind   string `json:"kind"`
			Check  string `json:"check"`
			Go     string `json:"go"`
			Python string `json:"python"`
		} `json:"disagreements"`
	} `json:"candidates"`
}

// dualRun drives the harness over one candidate and returns its exit code and report.
func dualRun(t *testing.T, root, registry, python string, candidates ...string) (int, dualRunReport) {
	t.Helper()
	work := scratch(t, "dualrun-work")
	reportPath := filepath.Join(scratch(t, "dualrun-report"), "report.json")
	args := []string{"dualrun", "--registry", registry, "--report", reportPath,
		"--root", root, "--self", gateBin, "--python", python, "--work", work}
	for _, candidate := range candidates {
		args = append(args, "--candidate", candidate)
	}
	code, out := run(t, args...)
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("the harness wrote no report (exit %d): %v\n%s", code, err, out)
	}
	var report dualRunReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, raw)
	}
	return code, report
}

// dualRunRepo is a real checkout with two commits, so the harness's `git diff` names a
// real change rather than a fixture's path list.
func dualRunRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = scratch(t, "dualrun-repo")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Gate Harness", "GIT_AUTHOR_EMAIL=harness@example.invalid",
			"GIT_COMMITTER_NAME=Gate Harness", "GIT_COMMITTER_EMAIL=harness@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	writeFile(t, root, "README.md", "first\n")
	git("add", "-A")
	git("commit", "-q", "-m", "first")
	base = git("rev-parse", "HEAD")
	writeFile(t, root, "docs/a.md", "a document\n")
	git("add", "-A")
	git("commit", "-q", "-m", "second")
	return root, base
}

func writeFile(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating %s failed: %v", path, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

// dualRunRegistry writes a registry whose digest is the one its own bytes hash to, which
// is what the loader checks. A case names the declarations and the in-process checks it is
// about; the digest is computed, never typed.
func dualRunRegistry(t *testing.T, declarations, inProcess []map[string]any) string {
	t.Helper()
	decl, err := json.Marshal(declarations)
	if err != nil {
		t.Fatalf("encoding the declarations failed: %v", err)
	}
	proc, err := json.Marshal(inProcess)
	if err != nil {
		t.Fatalf("encoding the in-process checks failed: %v", err)
	}
	sum := sha256.Sum256(append(append([]byte{}, decl...), proc...))
	body := `{"digest":"` + hex.EncodeToString(sum[:]) + `","declarations":` + string(decl) +
		`,"in_process":` + string(proc) + `}`
	path := filepath.Join(scratch(t, "dualrun-registry"), "registry.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing the registry failed: %v", err)
	}
	return path
}

// documentsDeclaration is the one check the harness cases plan: a Markdown change selects
// it, and its command is cheap so the Go run face finishes in a test.
func documentsDeclaration() map[string]any {
	return map[string]any{
		"id": "documents", "kind": "documents", "label": "{count} changed Markdown file(s)",
		"selects": []map[string]any{{"regex": `.*\.md$`, "must_exist": true}},
		"command": []string{"sh", "-c", "exit 0"},
		"watches": []map[string]any{{"paths": true}},
		"tier":    "landing", "timeout_ms": 30000,
	}
}

// fakePython writes a stand-in for tools/fork-check.py. The harness's contract with that
// program is two lines -- the selection line from `--explain` and the plan line from a run
// -- so a case can state exactly what the Python gate would have said.
//
// The plan face always answers 0; the run face answers `runExit`, which is how a case
// makes the two gates disagree about a verdict.
func fakePython(t *testing.T, kinds []string, runExit int) string {
	t.Helper()
	checks := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		checks = append(checks, `{"kind":"`+kind+`"}`)
	}
	selection := `fork-check: selection {"checks":[` + strings.Join(checks, ",") + `],"reused":[],"skips":[]}`
	plan := `fork-check: plan base=- head=- ran=` + strconv.Itoa(len(kinds)) +
		` notrun=0 seconds=0.0 full=0.0 saved=0.0 priced=0 integrity=0 checks=` +
		strings.Join(kinds, ",") + ` required=` + strings.Join(kinds, ",") +
		` reused=- tier=full owed=- landing=0.0`
	script := "import sys\n" +
		"if \"--explain\" in sys.argv[1:]:\n" +
		"    print(" + strconv.Quote(selection) + ")\n" +
		"    sys.exit(0)\n" +
		"print(" + strconv.Quote(plan) + ")\n" +
		"sys.exit(" + strconv.Itoa(runExit) + ")\n"
	path := filepath.Join(scratch(t, "dualrun-python"), "fork-check.py")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the fake Python gate failed: %v", err)
	}
	return path
}

// The harness's product is the classification, so a candidate both gates plan and judge
// the same way must read `agree` with no disagreement named.
func TestDualRunAgreesWhenBothGatesPlanAndJudgeTheSame(t *testing.T) {
	root, base := dualRunRepo(t)
	registry := dualRunRegistry(t, []map[string]any{documentsDeclaration()}, []map[string]any{})
	python := fakePython(t, []string{"documents"}, 0)
	code, report := dualRun(t, root, registry, python, "markdown="+base)
	if code != 0 {
		t.Fatalf("the harness exited %d", code)
	}
	if len(report.Candidates) != 1 {
		t.Fatalf("the report holds %d candidates, want 1", len(report.Candidates))
	}
	got := report.Candidates[0]
	if got.Agreement != "agree" {
		t.Fatalf("the candidate reads %q with disagreements %+v", got.Agreement, got.Disagreements)
	}
	if len(got.Disagreements) != 0 {
		t.Fatalf("an agreeing candidate named disagreements: %+v", got.Disagreements)
	}
	if strings.Join(got.Go.PlanKinds, ",") != "documents" {
		t.Fatalf("the Go plan names kinds %v, want [documents]", got.Go.PlanKinds)
	}
	if got.Go.Verdict != "pass" || got.Go.VerdictExit != 0 {
		t.Fatalf("the Go verdict is %q at exit %d, want pass at 0", got.Go.Verdict, got.Go.VerdictExit)
	}
	if got.Python.Verdict != "pass" || got.Python.VerdictExit != 0 {
		t.Fatalf("the Python verdict is %q at exit %d, want pass at 0", got.Python.Verdict, got.Python.VerdictExit)
	}
	if len(got.Candidate.ChangedPaths) != 1 || got.Candidate.ChangedPaths[0] != "docs/a.md" {
		t.Fatalf("the candidate's changed paths are %v, want [docs/a.md]", got.Candidate.ChangedPaths)
	}
}

// A disagreement is a finding, not a failure: the harness must name which gate said what
// and on which check, and still answer 0 because it produced its report.
//
// Negative control: the agreeing case above runs the same harness over the same registry,
// so a harness that named a disagreement for every candidate fails there.
func TestDualRunNamesTheCheckAndTheVerdictEachGateDisagreedOn(t *testing.T) {
	root, base := dualRunRepo(t)
	registry := dualRunRegistry(t, []map[string]any{documentsDeclaration()}, []map[string]any{})
	python := fakePython(t, []string{"documents", "structural"}, 1)
	code, report := dualRun(t, root, registry, python, "markdown="+base)
	if code != 0 {
		t.Fatalf("the harness exited %d; a disagreement is a finding, not a harness failure", code)
	}
	got := report.Candidates[0]
	if got.Agreement != "disagree" {
		t.Fatalf("the candidate reads %q, want disagree", got.Agreement)
	}
	byKind := map[string]struct{ check, goSide, pythonSide string }{}
	for _, disagreement := range got.Disagreements {
		byKind[disagreement.Kind] = struct{ check, goSide, pythonSide string }{
			disagreement.Check, disagreement.Go, disagreement.Python}
	}
	only, ok := byKind["plan/only-in-python"]
	if !ok {
		t.Fatalf("the plan disagreement is not named: %+v", got.Disagreements)
	}
	if only.check != "structural" {
		t.Fatalf("the plan disagreement names check %q, want structural", only.check)
	}
	if only.goSide != "not planned" || only.pythonSide != "planned" {
		t.Fatalf("the plan disagreement reads go=%q python=%q", only.goSide, only.pythonSide)
	}
	verdict, ok := byKind["verdict"]
	if !ok {
		t.Fatalf("the verdict disagreement is not named: %+v", got.Disagreements)
	}
	if verdict.goSide != "pass (exit 0)" || verdict.pythonSide != "fail (exit 1)" {
		t.Fatalf("the verdict disagreement reads go=%q python=%q", verdict.goSide, verdict.pythonSide)
	}
}

// A check the Python gate runs as its own code is the disagreement that makes every Go
// verdict on a real candidate `error`: the Go plan carries it as in-process and `run`
// refuses the plan rather than omitting the check (A-INPROCESS).
func TestDualRunNamesAnInProcessCheckAsTheReasonTheGoVerdictIsError(t *testing.T) {
	root, base := dualRunRepo(t)
	registry := dualRunRegistry(t, []map[string]any{documentsDeclaration()},
		[]map[string]any{{"id": "file-size", "kind": "file-size", "label": "no tracked file over the size ceiling"}})
	python := fakePython(t, []string{"documents", "file-size"}, 0)
	code, report := dualRun(t, root, registry, python, "markdown="+base)
	if code != 0 {
		t.Fatalf("the harness exited %d", code)
	}
	got := report.Candidates[0]
	if strings.Join(got.InProcess, ",") != "file-size" {
		t.Fatalf("the report names in-process kinds %v, want [file-size]", got.InProcess)
	}
	if got.Go.Verdict != "error" || got.Go.VerdictExit != 4 {
		t.Fatalf("the Go verdict is %q at exit %d, want error at 4", got.Go.Verdict, got.Go.VerdictExit)
	}
	if got.Python.Verdict != "pass" {
		t.Fatalf("the Python verdict is %q, want pass", got.Python.Verdict)
	}
	var sawInProcess, sawVerdict bool
	for _, disagreement := range got.Disagreements {
		switch disagreement.Kind {
		case "in-process":
			sawInProcess = disagreement.Check == "file-size"
		case "verdict":
			sawVerdict = true
		}
	}
	if !sawInProcess {
		t.Fatalf("the in-process disagreement is not named: %+v", got.Disagreements)
	}
	if !sawVerdict {
		t.Fatalf("the verdict disagreement is not named: %+v", got.Disagreements)
	}
	if got.Agreement != "disagree" {
		t.Fatalf("the candidate reads %q, want disagree", got.Agreement)
	}
}
