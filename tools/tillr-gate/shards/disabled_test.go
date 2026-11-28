package shards

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipPatternAnchorsSegmentsAndPrunesUnderListedParent(t *testing.T) {
	got := SkipPattern([]string{"TestA/sub1", "TestA/sub1/deep", "TestB/x.y/z", "TestTop"})
	want := `^TestA$/^sub1$|^TestB$/^x\.y$/^z$`
	if got != want {
		t.Fatalf("SkipPattern = %q, want %q", got, want)
	}
	if strings.Contains(got, "(") {
		t.Fatalf("a slash-separated pattern must never be parenthesized: %q", got)
	}
	if SkipPattern([]string{"TestOnlyTop"}) != "" || SkipPattern(nil) != "" {
		t.Fatal("top-level selectors are dropped from the run list, not skipped")
	}
}

func TestPlanDropsListedTopLevelTestsAndCountsOnlyLiveSelectors(t *testing.T) {
	set := NewDisabledSet(map[string][]string{"p": {"TestB", "TestA/sub", "TestGone"}, "q": {"TestZ"}})
	kept := set.Plan("p", []string{"TestA", "TestB", "TestC"})
	if strings.Join(kept, ",") != "TestA,TestC" {
		t.Fatalf("kept = %v", kept)
	}
	if got := strings.Join(set.Applied(), ","); got != "p:TestA/sub,p:TestB" {
		t.Fatalf("Applied = %q: a stale selector must not count", got)
	}
	if set.SkipFor("p") != `^TestA$/^sub$` || set.SkipFor("other") != "" {
		t.Fatalf("SkipFor = %q", set.SkipFor("p"))
	}
	var none *DisabledSet
	if k := none.Plan("p", []string{"TestA"}); len(k) != 1 || none.SkipFor("p") != "" || len(none.Applied()) != 0 {
		t.Fatal("a nil set disables nothing")
	}
}

func goEvent(action, pkg, test, output string) TestEvent {
	return TestEvent{Action: action, Package: pkg, Test: test, Output: output}
}

func TestFailedTestsNamesOnlyTheDeepestFailureWithItsOwnOutput(t *testing.T) {
	events := []TestEvent{
		goEvent("run", "p", "TestWhy", ""),
		goEvent("output", "p", "TestWhy", "preamble line\n"),
		goEvent("run", "p", "TestWhy/first_segment", ""),
		goEvent("output", "p", "TestWhy/first_segment", "first ok\n"),
		goEvent("pass", "p", "TestWhy/first_segment", ""),
		goEvent("run", "p", "TestWhy/resumed_segment", ""),
		goEvent("output", "p", "TestWhy/resumed_segment", "idle_test.go:88: replanned\n"),
		goEvent("fail", "p", "TestWhy/resumed_segment", ""),
		goEvent("output", "p", "TestWhy", "parent trailer\n"),
		goEvent("fail", "p", "TestWhy", ""),
		goEvent("run", "p", "TestParentOnly", ""),
		goEvent("output", "p", "TestParentOnly", "fatal outside subtests\n"),
		goEvent("fail", "p", "TestParentOnly", ""),
		goEvent("output", "p", "TestOther", "unrelated\n"),
		goEvent("pass", "p", "TestOther", ""),
	}
	got := FailedTests(events)
	if len(got) != 2 {
		t.Fatalf("FailedTests = %+v", got)
	}
	if got[0].Test != "TestWhy/resumed_segment" || got[0].Output != "preamble line\nidle_test.go:88: replanned\n" {
		t.Fatalf("leaf = %+v: want the subtest named without its parent, with the parent's preamble only", got[0])
	}
	if got[1].Test != "TestParentOnly" || got[1].Output != "fatal outside subtests\n" {
		t.Fatalf("parent-only failure = %+v", got[1])
	}
}

func TestFailedTestsDoesNotMergePackages(t *testing.T) {
	got := FailedTests([]TestEvent{goEvent("fail", "a", "TestX", ""), goEvent("fail", "b", "TestX/sub", ""), goEvent("fail", "b", "TestX", "")})
	if len(got) != 2 || got[0].Package != "a" || got[0].Test != "TestX" || got[1].Package != "b" || got[1].Test != "TestX/sub" {
		t.Fatalf("FailedTests = %+v", got)
	}
}

const disabledFixtureTests = `package m

import "testing"

func TestOK(t *testing.T) {}

func TestBroken(t *testing.T) { t.Fatal("broken on purpose") }

func TestParent(t *testing.T) {
	t.Run("good", func(t *testing.T) {})
	t.Run("bad one", func(t *testing.T) { t.Fatal("bad subtest") })
}
`

func writeDisabledFixture(t *testing.T, list string) (repoRoot, mod, durations string) {
	t.Helper()
	repoRoot = requireTempDir(t)
	mod = filepath.Join(repoRoot, "tools", "m")
	for path, body := range map[string]string{
		filepath.Join(repoRoot, ".tillr.toml"):           "[gate]\ndisabled_tests = \"gate/disabled.json\"\n",
		filepath.Join(repoRoot, "gate", "disabled.json"): list,
		filepath.Join(mod, "go.mod"):                     "module example.com/m\n\ngo 1.23\n",
		filepath.Join(mod, "m_test.go"):                  disabledFixtureTests,
		filepath.Join(repoRoot, "durations.json"):        "{}",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repoRoot, mod, filepath.Join(repoRoot, "durations.json")
}

const disabledFixtureList = `{"version":1,"disabled":[
 {"selector":"go-test:tools/m:example.com/m:TestBroken","source":"hand"},
 {"selector":"go-test:tools/m:example.com/m:TestParent/bad_one","source":"hand"},
 {"selector":"go-test:tools/m:example.com/m:TestRemoved","source":"hand"},
 {"selector":"go-test:tools/other:example.com/m:TestOK","source":"hand"}]}`

func eventTests(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var ev TestEvent
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Test != "" && (ev.Action == "pass" || ev.Action == "fail" || ev.Action == "run") {
			seen[ev.Test] = ev.Action
		}
	}
	return seen
}

func TestShardsExcludeListedTestsAndSubtestsAndCountThem(t *testing.T) {
	for _, mode := range []string{"uncached", "cached"} {
		t.Run(mode, func(t *testing.T) {
			root, mod, dur := writeDisabledFixture(t, disabledFixtureList)
			events := filepath.Join(root, "events.jsonl")
			args := []string{mod, "--durations", dur, "--events", events, "--scratch", filepath.Join(root, "scratch")}
			if mode == "cached" {
				args = append(args, "--cache")
			}
			code, stdout, stderr := runCLIInSubprocess(t, args, map[string]string{"TILLR_RUN_ID": ""}, root)
			if code != 0 {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, ", 2 disabled") {
				t.Fatalf("summary %q should count the two live listed selectors and no stale or foreign one", stdout)
			}
			seen := eventTests(t, events)
			if seen["TestOK"] != "pass" || seen["TestParent/good"] != "pass" {
				t.Fatalf("the rest of the suite must still run: %v", seen)
			}
			for _, name := range []string{"TestBroken", "TestParent/bad_one"} {
				if _, ran := seen[name]; ran {
					t.Fatalf("%s is listed and must not start: %v", name, seen)
				}
			}
		})
	}
}

func TestProveDisabledRunsOnlyTheNamedListedTest(t *testing.T) {
	root, mod, dur := writeDisabledFixture(t, disabledFixtureList)
	events := filepath.Join(root, "events.jsonl")
	code, _, stderr := runCLIInSubprocess(t, []string{mod, "--durations", dur, "--events", events, "--scratch", filepath.Join(root, "scratch"),
		"--prove-disabled", "go-test:tools/m:example.com/m:TestBroken"}, nil, root)
	if code != 1 {
		t.Fatalf("exit %d, want 1 because the proved test fails", code)
	}
	if !strings.Contains(stderr, "TestBroken") || strings.Contains(stderr, "bad_one") {
		t.Fatalf("stderr should name only the proved failure:\n%s", stderr)
	}
	seen := eventTests(t, events)
	if seen["TestBroken"] != "fail" {
		t.Fatalf("proved test did not run: %v", seen)
	}
	if _, ran := seen["TestParent/bad_one"]; ran {
		t.Fatalf("a listed test that was not proved must stay excluded: %v", seen)
	}
}

func TestMissingDisabledListDisablesNothing(t *testing.T) {
	root, mod, dur := writeDisabledFixture(t, disabledFixtureList)
	if err := os.Remove(filepath.Join(root, "gate", "disabled.json")); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLIInSubprocess(t, []string{mod, "--durations", dur, "--scratch", filepath.Join(root, "scratch")}, nil, root)
	if code != 1 || !strings.Contains(stderr, "TestBroken") {
		t.Fatalf("exit %d, stderr %s: with no list nothing is excluded", code, stderr)
	}
}

func TestMalformedDisabledListRefusesTheRun(t *testing.T) {
	root, mod, dur := writeDisabledFixture(t, `{"version":1,"disabled":[{"selector":"nonsense!","source":"hand"}]}`)
	code, _, stderr := runCLIInSubprocess(t, []string{mod, "--durations", dur, "--scratch", filepath.Join(root, "scratch")}, nil, root)
	if code != 1 || !strings.Contains(stderr, "disabled.json") {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
}

func TestFullSkipPatternJoinsTopLevelAndSubtestSelectors(t *testing.T) {
	got := FullSkipPattern([]string{"TestB", "TestA/sub1", "TestA/sub1/deep", "TestC", "TestC/x", "TestD/x.y/z"})
	want := `^TestA$/^sub1$|^TestB$|^TestC$|^TestD$/^x\.y$/^z$`
	if got != want {
		t.Fatalf("FullSkipPattern = %q, want %q", got, want)
	}
	if strings.Contains(got, "(") || FullSkipPattern(nil) != "" {
		t.Fatalf("a pattern is never parenthesized and nothing listed is empty: %q", got)
	}
}
