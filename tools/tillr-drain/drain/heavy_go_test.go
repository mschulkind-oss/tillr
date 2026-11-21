package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeavyGoSuiteAttributesEachFailingTestAndNamesBothContributors(t *testing.T) {
	const mod = "example.com/drain"
	const alpha = "go-test:tools/tillr-drain:" + mod + ":TestAlpha"
	const beta = "go-test:tools/tillr-drain:" + mod + "/sub:TestBeta/sub"
	const both = "go-test:tools/tillr-drain:" + mod + ":TestBoth"
	h := newRevertHarness(t, nil)
	for _, dir := range []string{"tools/gate/checks", "tools/tillr-drain/sub"} {
		if err := os.MkdirAll(filepath.Join(h.root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeForkFile(t, filepath.Join(h.root, "tools/gate/checks/.keep"), "")
	writeForkFile(t, filepath.Join(h.root, "tools/tillr-drain/go.mod"), "module "+mod+"\ngo 1.22\n")
	writeForkFile(t, filepath.Join(h.root, "tools/tillr-drain/alpha_test.go"), `package drain
import ("os"; "testing")
func exists(name string) bool { _, err := os.Stat("../../" + name); return err == nil }
func TestAlpha(t *testing.T) { if exists("a") { t.Fatal("a broke alpha") } }
func TestBoth(t *testing.T) { if exists("a") || exists("b") { t.Fatal("a or b broke both") } }
`)
	writeForkFile(t, filepath.Join(h.root, "tools/tillr-drain/sub/beta_test.go"), `package sub
import ("os"; "testing")
func TestBeta(t *testing.T) { t.Run("sub", func(t *testing.T) {
    if _, err := os.Stat("../../../b"); err == nil { t.Fatal("b broke beta") }
}) }
`)
	// Exercise the production adapter's observation seam. The gate has full
	// check output internally, but prints only the tail into the proof log.
	writeForkFile(t, filepath.Join(h.root, "tools/fork-check.py"), fmt.Sprintf(`
import json, os, sys
from pathlib import Path
from types import SimpleNamespace

def declaration_id(kind, label, root): return "drain-suite"
def check_status(check): return "FAIL"
def execute_check(check, *_):
    check.output = "go-test-shards: %[1]s shard 0 failed (exit 1): TestAlpha, TestBoth\n"
    check.output += "opaque diagnostics\n" * 100
    check.output += "go-test-shards: %[1]s/sub shard 2 failed (exit 1): TestBeta/sub\n"

def main(argv):
    check = SimpleNamespace(kind="go-tool", label="tools/tillr-drain's whole suite passes")
    if "--registry-json" in argv:
        print(json.dumps({"declarations": [{"id": "drain-suite", "kind": check.kind, "label": check.label}]}))
        return 0
    execute_check(check, Path.cwd(), None)
    print(check.output.splitlines()[-1])
    Path(os.environ["TILLR_GATE_SPANS_OUT"]).write_text(json.dumps({"check_spans": [{
        "check_id": "drain-suite", "kind": check.kind, "label": check.label,
        "status": "FAIL", "attributes_json": "{}",
    }]}))
    return 1

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
`, mod))
	h.base = heavyCommit(t, h.root, "proof fixtures")
	heavyGit(t, h.root, "update-ref", GreenRef, h.base)
	a := h.land("item-a", "a", "break Alpha and Both")
	b := h.land("item-b", "b", "break Beta and Both")
	tip := h.land("item-c", "c", "unrelated")
	steps := &ProcessSteps{Root: h.root, LaneRoot: scratchUnderDurable(t), RollingProgram: "python3 tools/fork-check.py {since} --tier rolling"}
	r := heavyFixture(t, h.root, steps.JudgeHeavy)
	r.Store = h.path
	offered := map[string][]string{}
	r.Revert = func(_ context.Context, _, sha string, checks []HeavyCheck, _ HeavyReplay) (RevertOutcome, error) {
		offered[sha] = heavyIDs(checks)
		return RevertOutcome{Refused: "test does not publish reverts"}, nil
	}
	fixed := map[string][]string{}
	r.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
		fixed[sha] = heavyIDs(checks)
		return nil
	}
	out, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 1 || len(out.Runs[0].Checks) != 3 {
		t.Fatalf("want three independent red verdicts: %+v", out)
	}
	for _, check := range out.Runs[0].Checks {
		want := map[string]string{alpha: a, beta: b, both: a}[check.CheckID]
		if want == "" || check.FirstRedLanding != want {
			t.Fatalf("wrong first red for %s: %+v", check.CheckID, check)
		}
		if check.CheckID == both && (!strings.Contains(check.Detail, a) || !strings.Contains(check.Detail, b)) {
			t.Fatalf("multi-culprit evidence did not name both landings: %+v", check)
		}
	}
	if len(offered) != 2 || strings.Join(offered[a], ",") != alpha || strings.Join(offered[b], ",") != beta {
		t.Fatalf("revert replay should only be offered single-culprit tests: %v", offered)
	}
	if strings.Join(fixed[tip], ",") != both {
		t.Fatalf("multi-culprit test should get a fixer against the tip: %v", fixed)
	}
}

func TestHeavyGoReplaySelectsOneTestAndRejectsOtherFailures(t *testing.T) {
	root := scratchUnderDurable(t)
	mod := filepath.Join(root, "tools/example")
	if err := os.MkdirAll(mod, 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(mod, "go.mod"), "module example.com/suite\ngo 1.22\n")
	config := filepath.Join(root, "gitconfig")
	writeForkFile(t, config, "[test]\n value = original\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	writeForkFile(t, filepath.Join(mod, "suite_test.go"), `package suite
import ("os/exec"; "testing")
func TestPrivateGit(t *testing.T) {
    if out, err := exec.Command("git", "config", "--global", "test.value").Output(); err != nil || string(out) != "original\n" {
        t.Fatalf("missing inherited config: %q, %v", out, err)
    }
    if err := exec.Command("git", "config", "--global", "test.value", "changed").Run(); err != nil { t.Fatal(err) }
}
func TestSibling(t *testing.T) { t.Fatal("not the selected test") }
func TestSelected(t *testing.T) {
    t.Run("passes", func(t *testing.T) {})
    t.Run("fails", func(t *testing.T) { t.Fatal("selected failure") })
}
`)
	s := &ProcessSteps{}
	for name, want := range map[string]string{"TestSelected/passes": "pass", "TestSelected/fails": "fail", "TestNotAddedYet": "pass", "TestPrivateGit": "pass"} {
		t.Run(name, func(t *testing.T) {
			id := "go-test:tools/example:example.com/suite:" + name
			got, err := s.JudgeRequired(context.Background(), root, id, "")
			if err != nil || got.Result != want {
				t.Fatalf("wanted %s: %+v, %v", want, got, err)
			}
		})
	}
	if raw, err := os.ReadFile(config); err != nil || !strings.Contains(string(raw), "original") {
		t.Fatalf("replay modified the inherited Git config: %q, %v", raw, err)
	}
	if err := os.WriteFile(filepath.Join(mod, "broken.go"), []byte("syntax error"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JudgeRequired(context.Background(), root, "go-test:tools/example:example.com/suite:TestSelected", ""); err == nil {
		t.Fatal("a build failure was treated as a test verdict")
	}
}
