package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
import json, os, shlex, sys
from pathlib import Path
from types import SimpleNamespace

GO_TOOL_DIRS = GO_SHARD_DIRS = ("tools/tillr-drain",)
def go_tool_command(module): return "python3 tools/go-test-shards.py " + module

def declaration_id(kind, label, root): return "drain-suite"
def check_status(check): return "FAIL"
def execute_check(check, *_):
    # Deliberately disagree with the case names in the actual event stream.
    check.output = "go-test-shards: %[1]s shard 0 failed (exit 1): TestWrong\n"
    check.output += "opaque diagnostics\n" * 100
    check.output += "go-test-shards: %[1]s/sub shard 2 failed (exit 1): TestBeta/sub\n"
    args = shlex.split(check.argv[2])
    if "--events" in args:
        events = []
        for pkg, name, assertion in [
            ("%[1]s", "TestAlpha", "alpha_test.go:4: a broke alpha"),
            ("%[1]s", "TestBoth", "alpha_test.go:5: a or b broke both"),
            ("%[1]s/sub", "TestBeta/sub", "beta_test.go:4: b broke beta"),
        ]:
            events += [dict(Action="output", Package=pkg, Test=name, Output="    " + assertion + "\n"),
                       dict(Action="output", Package=pkg, Test=name, Output="more diagnostics\n" * 1000),
                       dict(Action="fail", Package=pkg, Test=name)]
        events.append(dict(Action="fail", Package="%[1]s/sub", Test="TestBeta"))
        Path(args[args.index("--events") + 1]).write_text("\n".join(map(json.dumps, events)))

def main(argv):
    check = SimpleNamespace(kind="go-tool", label="tools/tillr-drain's whole suite passes",
                            argv=["sh", "-c", go_tool_command("tools/tillr-drain")])
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
	assertions := map[string]string{alpha: "alpha_test.go:4: a broke alpha", beta: "beta_test.go:4: b broke beta", both: "alpha_test.go:5: a or b broke both"}
	for _, check := range out.Runs[0].Checks {
		body, err := os.ReadFile(check.LogRef)
		if err != nil || !strings.Contains(string(body), check.CheckID) || !strings.Contains(string(body), assertions[check.CheckID]) || strings.Contains(string(body), "TestWrong") {
			t.Fatalf("missing exact failure for %s in %s: %.300q (%v)", check.CheckID, check.LogRef, body, err)
		}
		for other, assertion := range assertions {
			if other != check.CheckID && (strings.Contains(string(body), other) || strings.Contains(string(body), assertion)) {
				t.Fatalf("%s log includes a different case, %s", check.CheckID, other)
			}
		}
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

func TestHeavyGoNativeSuiteKeepsNamedAndUnscopedFailures(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	if err := os.MkdirAll(filepath.Join(root, "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools/fork-check.py"), `
import json, os, sys
from pathlib import Path
from types import SimpleNamespace

GO_TOOL_DIRS = ("tools/example",)
GO_SHARD_DIRS = ()
def go_tool_command(module): return "cd " + module + " && CGO_ENABLED=0 go test ./..."
def declaration_id(kind, label, root): return "native-suite"
def check_status(check): return "FAIL"
def execute_check(check, *_):
    assert "go test -json " in check.argv[2]
    check.output = "\n".join(map(json.dumps, [
        dict(Action="output", Package="example.com/native", Test="TestOne", Output="    one_test.go:23: expected one\n"),
        dict(Action="fail", Package="example.com/native", Test="TestOne"),
        dict(Action="fail", Package="example.com/native"),
        dict(Action="build-output", ImportPath="example.com/native/broken", Output="broken.go:9: undefined: x\n"),
        dict(Action="build-fail", ImportPath="example.com/native/broken"),
    ]))

def main(argv):
    check = SimpleNamespace(kind="go-tool", label="tools/example's whole suite passes",
                            argv=["sh", "-c", go_tool_command("tools/example")])
    if "--registry-json" in argv:
        print(json.dumps({"declarations": [{"id": "native-suite", "kind": check.kind, "label": check.label}]}))
        return 0
    execute_check(check, Path.cwd(), None)
    Path(os.environ["TILLR_GATE_SPANS_OUT"]).write_text(json.dumps({"check_spans": [{
        "check_id": "native-suite", "kind": check.kind, "label": check.label,
        "status": "FAIL", "attributes_json": "{}",
    }]}))
    return 1

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
`)
	heavyCommit(t, root, "native suite fixture")
	s := &ProcessSteps{LaneRoot: work, RollingProgram: "python3 tools/fork-check.py"}
	var checks []HeavyCheck
	r := heavyFixture(t, root, func(ctx context.Context, dir, _ string) ([]HeavyCheck, error) {
		var err error
		checks, err = s.runHeavyLegacy(ctx, dir, "")
		// This fixture checks the proof's evidence only, not focused replays.
		return []HeavyCheck{{CheckID: "fixture", Result: "pass"}}, err
	})
	_, err := r.Run(context.Background())
	if err != nil || len(checks) != 2 {
		t.Fatalf("want named case and unscoped build failure: %+v, %v", checks, err)
	}
	want := map[string]string{
		"go-test:tools/example:example.com/native:TestOne": "one_test.go:23: expected one",
		"native-suite": "broken.go:9: undefined: x",
	}
	for _, check := range checks {
		body, err := os.ReadFile(check.LogRef)
		if err != nil || want[check.CheckID] == "" || !strings.Contains(string(body), want[check.CheckID]) {
			t.Fatalf("wrong output for %s: %q (%v)", check.CheckID, body, err)
		}
		if check.CheckID != "native-suite" && strings.Contains(string(body), "broken.go") {
			t.Fatalf("named case got unscoped output: %q", body)
		}
	}
}

func TestHeavyGoTimeoutUsesFirstProofWithoutReplay(t *testing.T) {
	for _, mode := range []string{"sharded", "native", "sharded-with-crash"} {
		t.Run(mode, func(t *testing.T) { testHeavyGoTimeout(t, mode) })
	}
}

func testHeavyGoTimeout(t *testing.T, mode string) {
	t.Helper()
	const timedOut = "go-test:tools/example:example.com/suite:TestHung"
	const ordinary = "go-test:tools/example:example.com/suite:TestFailed"
	const panicLine = "panic: test timed out after 10m0s"
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	if err := os.MkdirAll(filepath.Join(root, "tools/gate/checks"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools/gate/checks/.keep"), "")
	writeForkFile(t, filepath.Join(root, "tools/fork-check.py"), fmt.Sprintf(`
import json, os, shlex, sys
from pathlib import Path
from types import SimpleNamespace

GO_TOOL_DIRS = ("tools/example",)
mode = %q
GO_SHARD_DIRS = GO_TOOL_DIRS if mode.startswith("sharded") else ()
def go_tool_command(module):
    if GO_SHARD_DIRS:
        return "python3 tools/go-test-shards.py " + module
    return "cd " + module + " && CGO_ENABLED=0 go test ./..."
def declaration_id(kind, label, root): return "suite"
def check_status(check): return "FAIL"
def execute_check(check, *_):
    args = shlex.split(check.argv[2])
    pkg = "example.com/suite"
    panic = ["panic: test timed out after 10m0s\n", "running tests:\n", "\tTestHung (10m0s)\n",
             "\n", "goroutine 1 [running]:\n"]
    events = [dict(Action="output", Package=pkg, Test="TestFailed", Output="ordinary assertion\n"),
              dict(Action="fail", Package=pkg, Test="TestFailed"),
              dict(Action="run", Package=pkg, Test="TestHung")]
    events += [dict(Action="output", Package=pkg, Test="TestHung", Output=line) for line in panic]
    events.append(dict(Action="fail", Package=pkg))
    report = "\n".join(map(json.dumps, events))
    if GO_SHARD_DIRS:
        Path(args[args.index("--events") + 1]).write_text(report)
        check.output = ("go-test-shards: example.com/suite shard 0 failed (exit 2): TestFailed\n"
                        "ordinary assertion\n" + "".join(panic))
        if mode == "sharded-with-crash":
            check.output += "go-test-shards: example.com/suite shard 1 failed (exit 2)\nunscoped crash\n"
    else:
        assert "go test -json " in check.argv[2]
        check.output = report

def main(argv):
    check = SimpleNamespace(kind="go-tool", label="tools/example's whole suite passes",
                            argv=["sh", "-c", go_tool_command("tools/example")])
    if "--registry-json" in argv:
        print(json.dumps({"declarations": [{"id": "suite", "kind": check.kind, "label": check.label}]}))
        return 0
    execute_check(check, Path.cwd(), None)
    # The panic header is outside the displayed tail.
    print("\n".join(check.output.splitlines()[-2:]))
    Path(os.environ["TILLR_GATE_SPANS_OUT"]).write_text(json.dumps({"check_spans": [{
        "check_id": "suite", "kind": check.kind, "label": check.label,
        "status": "FAIL", "attributes_json": "{}",
    }]}))
    return 1

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
`, mode))
	tip := heavyCommit(t, root, "failed suite")
	steps := &ProcessSteps{Root: root, LaneRoot: scratchUnderDurable(t), RollingProgram: "python3 tools/fork-check.py {since} --tier rolling"}
	var replayed []string
	r := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		if only == "" {
			return steps.JudgeHeavy(ctx, dir, only)
		}
		replayed = append(replayed, only)
		result := "pass"
		if heavyGit(t, dir, "rev-parse", "HEAD") == tip {
			result = "fail"
		}
		return []HeavyCheck{{CheckID: only, Result: result}}, nil
	})
	var fixed []string
	r.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
		if sha != tip {
			t.Errorf("fixer filed against %s, want %s", sha, tip)
		}
		fixed = append(fixed, heavyIDs(checks)...)
		return nil
	}
	out, err := r.Run(context.Background())
	wantChecks := 2
	if mode == "sharded-with-crash" {
		wantChecks++
	}
	if err != nil || len(out.Runs) != 1 || out.Runs[0].Result != "red" || len(out.Runs[0].Checks) != wantChecks {
		t.Fatalf("want %d red verdicts, got %+v / %v (replayed %v)", wantChecks, out, err, replayed)
	}
	var timeoutCheck HeavyCheck
	for _, check := range out.Runs[0].Checks {
		switch check.CheckID {
		case timedOut:
			timeoutCheck = check
			if check.Result != "fail" || check.FirstRedLanding != "" || !strings.Contains(check.Detail, panicLine) {
				t.Errorf("timeout lost its first proof evidence or blamed a landing: %+v", check)
			}
			if raw, err := os.ReadFile(check.LogRef); err != nil || !strings.Contains(string(raw), "goroutine 1 [running]:") {
				t.Errorf("first proof log not retained: %q / %v", raw, err)
			}
		case ordinary:
			if check.Result != "fail" || check.FirstRedLanding != tip {
				t.Errorf("ordinary failure lost attribution: %+v", check)
			}
			if raw, err := os.ReadFile(check.LogRef); err != nil || !strings.Contains(string(raw), "ordinary assertion") || strings.Contains(string(raw), panicLine) {
				t.Errorf("ordinary failure lost its separate evidence: %q / %v", raw, err)
			}
		case "suite":
			raw, err := os.ReadFile(check.LogRef)
			if mode != "sharded-with-crash" || check.Result != "fail" || !check.GoTimedOut || err != nil || !strings.Contains(string(raw), "unscoped crash") {
				t.Errorf("unscoped crash should stay visible without replaying the suite: %+v / %v", check, err)
			}
		default:
			t.Errorf("unexpected check: %+v", check)
		}
	}
	if !slices.Contains(fixed, timedOut) {
		t.Errorf("timeout did not get a fixer: %v", fixed)
	}
	if len(replayed) == 0 || slices.ContainsFunc(replayed, func(id string) bool { return id != ordinary }) {
		t.Errorf("only the ordinary failure should be replayed: %v", replayed)
	}
	if timeoutCheck.CheckID == "" {
		t.Fatal("missing named timeout")
	}
	// An advanced trunk must not trigger a focused supersession replay either.
	heavyCommit(t, root, "landed during proof")
	groups := map[string][]HeavyCheck{tip: {timeoutCheck}}
	replayed = nil
	order, err := r.retainRedRepairs(context.Background(), &out.Runs[0], []string{tip}, groups)
	if err != nil || !slices.Equal(order, []string{tip}) || len(replayed) != 0 {
		t.Errorf("advanced trunk should keep the timeout repair without replay: %v / %v (replayed %v)", order, err, replayed)
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
