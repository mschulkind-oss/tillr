package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChecksScopeToChangedReads(t *testing.T) {
	root := scratch(t, "changed-reads")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name string, body []byte) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("tools/tïllr/main.go", []byte("package main\n"))
	write("docs/README.md", []byte("# Base\n"))
	git("add", ".")
	git("commit", "-qm", "base")
	write("docs/README.md", []byte("# Candidate\n"))
	git("commit", "-qam", "docs only")

	for _, tc := range []struct {
		id, scope string
		reads     []string
	}{
		{"go-build", "tree", []string{"tools/tïllr/"}},
		{"source-change", "change", []string{"tools/tïllr/main.go"}},
		{"repo-wide", "tree", []string{"."}},
		{"undeclared", "tree", nil},
	} {
		decl := map[string]any{"id": tc.id, "label": tc.id, "scope": tc.scope, "tier": "fast",
			"budget_ms": 5000, "command": []string{"touch", tc.id + ".ran"}}
		if tc.reads != nil {
			decl["reads"] = tc.reads
			decl["remember"] = false
		}
		raw, err := json.Marshal(decl)
		if err != nil {
			t.Fatal(err)
		}
		write("checks/"+tc.id+".json", raw)
	}

	run := func(base string, scoped, readsGo, measure bool) {
		t.Helper()
		args := []string{"checks", "--fast-tree", "--base", base, "--head", "HEAD", "--worktree", root,
			"--checks", "checks", "--logs", scratch(t, "logs")}
		if scoped {
			args = append(args, "--scope-to-change")
		}
		var stderr bytes.Buffer
		cmd := exec.Command(gateBin, args...)
		cmd.Env = append(os.Environ(), "TILLR_FAST_TREE_TIMING=")
		if measure {
			cmd.Env = append(cmd.Env, "TILLR_FAST_TREE_TIMING=1")
		}
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("checks: %v\n%s", err, &stderr)
		}
		got := map[string]string{}
		want := map[string]string{"repo-wide": "pass", "undeclared": "pass"}
		for _, id := range []string{"go-build", "source-change"} {
			want[id] = "pass"
			if !readsGo {
				want[id] = "skipped: no changed path is read"
			}
		}
		for _, check := range decodeVerdict(t, out).Payload.Checks {
			got[check.CheckID] = check.Result
		}
		var verdict struct {
			Payload struct {
				WallMS *int64 `json:"wall_ms"`
				Checks []struct {
					CheckID   string `json:"check_id"`
					Execution string `json:"execution"`
					WallMS    *int64 `json:"wall_ms"`
				} `json:"checks"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(out, &verdict); err != nil {
			t.Fatal(err)
		}
		if (verdict.Payload.WallMS != nil) != measure {
			t.Fatalf("unexpected phase wall time: %s", out)
		}
		for _, check := range verdict.Payload.Checks {
			execution, timed := "", false
			if measure {
				execution = "not-run"
				if want[check.CheckID] == "pass" {
					execution, timed = "ran", true
				}
			}
			if check.Execution != execution || (check.WallMS != nil) != timed {
				t.Fatalf("check %s: want execution %q and timed=%t: %s", check.CheckID, execution, timed, out)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("results = %v, want %v", got, want)
		}
		for id, result := range want {
			marker := filepath.Join(root, id+".ran")
			if err := os.Remove(marker); result == "pass" && err != nil || result != "pass" && !os.IsNotExist(err) {
				t.Fatalf("execution marker for %s (%s): %v", id, result, err)
			}
		}
		if scoped && !strings.Contains(stderr.String(), "checks with no declared reads (always run): undeclared\n") {
			t.Fatalf("missing undeclared reads notice:\n%s", &stderr)
		}
		if !readsGo && !strings.Contains(stderr.String(), "skipped: no changed path is read: go-build, source-change\n") {
			t.Fatalf("missing skip notice:\n%s", &stderr)
		}
	}

	run("HEAD^", false, true, false) // Trunk proof does not opt in.
	run("HEAD^", true, false, true)
	run("HEAD^", true, false, true) // No reads means run, even with a remembered pass.
	git("mv", "tools/tïllr/main.go", "docs/moved.go")
	git("commit", "-qm", "rename out of declared reads")
	run("HEAD^", true, true, true) // Both the old path and the new path are considered.
}

func TestChecksScopeSkipIsNotCountedAsFailureOnStall(t *testing.T) {
	t.Setenv("TILLR_FAST_VERDICT_OUT", filepath.Join(scratch(t, "stalled-fast"), "fast.json"))
	gate := fakeGate(t, `#!/bin/sh
printf '%s\n' '{"payload":{"checks":[{"check_id":"go-build","result":"skipped: no changed path is read"},{"check_id":"blocked","result":"stalled","silent_samples":60}]}}' > "$TILLR_FAST_VERDICT_OUT"
exit 6
`)
	plan := planRecord(t, planIn{PlanID: "p-skip", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "d"})
	cmd := exec.Command(gateBin, "run", "--input", plan, "--worktree", tree(t), "--gate", gate,
		"--output", filepath.Join(scratch(t, "verdict"), "verdict.json"))
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 6 {
		t.Fatalf("exit = %v, want stalled\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("1 skipped, 1 failed")) {
		t.Fatalf("the skipped check must not be counted as failed:\n%s", out)
	}
}

func TestChecksScopeToChangedReadsWithExclusion(t *testing.T) {
	root := scratch(t, "changed-reads-exclusion")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name string, body []byte) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("tools/tïllr/main.go", []byte("package main\n"))
	write("tools/tïllr/README.md", []byte("# Tool\n"))
	write("tools/tïllr/docs/guide.md", []byte("# Guide\n"))
	git("add", ".")
	git("commit", "-qm", "base")

	for _, tc := range []struct {
		id, scope, tier string
		reads           []string
		remember        bool
	}{
		{"fast-recursive", "tree", "fast", []string{"tools/tïllr/", "!tools/tïllr/**/*.md"}, false},
		{"fast-top", "tree", "fast", []string{"tools/tïllr/", "!tools/tïllr/*.md"}, false},
		{"change-recursive", "change", "fast", []string{"tools/tïllr/", "!tools/tïllr/**/*.md"}, false},
		{"heavy-proof", "tree", "heavy", []string{"tools/tïllr/", "!tools/tïllr/**/*.md"}, true},
	} {
		decl := map[string]any{
			"id": tc.id, "label": tc.id, "scope": tc.scope, "tier": tc.tier,
			"reads":   tc.reads,
			"command": []string{"sh", "-c", "echo ran >> '" + filepath.Join(root, tc.id+".ran") + "'"},
		}
		if tc.tier == "fast" {
			decl["budget_ms"] = 5000
		} else {
			decl["timeout_ms"] = 5000
		}
		if !tc.remember {
			decl["remember"] = false
		}
		raw, err := json.Marshal(decl)
		if err != nil {
			t.Fatal(err)
		}
		write("checks/"+tc.id+".json", raw)
	}

	runFast := func(want map[string]string) {
		t.Helper()
		var stderr bytes.Buffer
		cmd := exec.Command(gateBin, "checks", "--fast-tree", "--scope-to-change",
			"--base", "HEAD^", "--head", "HEAD", "--worktree", root,
			"--checks", "checks", "--logs", scratch(t, "fast-logs"))
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("fast checks: %v\n%s", err, &stderr)
		}
		got := map[string]string{}
		for _, check := range decodeVerdict(t, out).Payload.Checks {
			got[check.CheckID] = check.Result
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("fast results = %v, want %v", got, want)
		}
		for id, result := range want {
			marker := filepath.Join(root, id+".ran")
			_, statErr := os.Stat(marker)
			if result == "pass" && statErr != nil {
				t.Fatalf("expected %s to run: %v", id, statErr)
			}
			if result != "pass" && !os.IsNotExist(statErr) {
				t.Fatalf("expected %s not to run: %v", id, statErr)
			}
			_ = os.Remove(marker)
		}
	}

	runProof := func(wantMemory string, wantRuns int) {
		t.Helper()
		var stderr bytes.Buffer
		cmd := exec.Command(gateBin, "checks", "--heavy-tree",
			"--base", "HEAD", "--head", "HEAD", "--worktree", root,
			"--checks", "checks", "--logs", scratch(t, "proof-logs"))
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("heavy checks: %v\n%s", err, &stderr)
		}
		var verdict struct {
			Payload struct {
				Checks []struct {
					CheckID string `json:"check_id"`
					Result  string `json:"result"`
					Memory  string `json:"memory"`
				} `json:"checks"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(out, &verdict); err != nil {
			t.Fatal(err)
		}
		if len(verdict.Payload.Checks) != 1 || verdict.Payload.Checks[0].CheckID != "heavy-proof" ||
			verdict.Payload.Checks[0].Result != "pass" || verdict.Payload.Checks[0].Memory != wantMemory {
			t.Fatalf("proof verdict = %+v, want memory=%q", verdict.Payload.Checks, wantMemory)
		}
		raw, err := os.ReadFile(filepath.Join(root, "heavy-proof.ran"))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(raw), "ran\n"); got != wantRuns {
			t.Fatalf("heavy-proof run count = %d, want %d", got, wantRuns)
		}
	}

	// Prime proof on base commit.
	runProof("passed", 1)

	// Commit 1: top-level excluded Markdown only.
	write("tools/tïllr/README.md", []byte("# Updated\n"))
	git("commit", "-qam", "top-level docs only")
	runFast(map[string]string{
		"fast-recursive":   "skipped: no changed path is read",
		"fast-top":         "skipped: no changed path is read",
		"change-recursive": "skipped: no changed path is read",
	})
	runProof("remembered", 1)

	// Commit 2: nested excluded Markdown only.
	write("tools/tïllr/docs/guide.md", []byte("# Updated guide\n"))
	git("commit", "-qam", "nested docs only")
	runFast(map[string]string{
		"fast-recursive":   "skipped: no changed path is read",
		"fast-top":         "pass",
		"change-recursive": "skipped: no changed path is read",
	})
	runProof("remembered", 1)

	// Commit 3: included Go source change.
	write("tools/tïllr/main.go", []byte("package main\nfunc main() {}\n"))
	git("commit", "-qam", "code change")
	runFast(map[string]string{
		"fast-recursive":   "pass",
		"fast-top":         "pass",
		"change-recursive": "pass",
	})
	runProof("passed", 2)
}
