package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestFixerInheritedGateFailure(t *testing.T) {
	for _, tc := range []struct {
		name                string
		changed             string
		mixed, other, cycle bool
		outcome             string
		held, attached      bool
	}{
		{name: "disjoint", changed: "docs/guide.md", outcome: GatePassedInherited},
		{name: "mixed", changed: "docs/guide.md", mixed: true},
		{name: "self", changed: "src/input", attached: true},
		{name: "other", changed: "src/input", other: true, held: true},
		{name: "transitive cycle", changed: "src/input", other: true, cycle: true, attached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, l := oqgp7Fixture(t)
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			fixer, err := l.fileTrunkFixer(ctx, base, []string{"named"}, GateOutcome{SuspectPaths: []string{"base.txt"}})
			if err != nil {
				t.Fatal(err)
			}
			failing := []string{"unnamed"}
			if tc.mixed {
				failing = append(failing, "named/module")
			}
			steps := &oqgp7ReplaySteps{recordingSteps: &recordingSteps{}, storePath: f.storePath,
				replays: map[string]ReplayOutcome{base: {Known: true, Red: failing}}}
			l.cfg.Steps = steps
			if tc.other {
				other, err := l.fileTrunkFixer(ctx, "000-other", []string{"unnamed"}, GateOutcome{SuspectPaths: []string{"src/input"}})
				if err != nil {
					t.Fatal(err)
				}
				if tc.cycle {
					middle, err := l.fileTrunkFixer(ctx, "middle-commit", []string{"middle"}, GateOutcome{SuspectPaths: []string{"base.txt"}})
					if err != nil {
						t.Fatal(err)
					}
					for id, target := range map[string]string{other: middle, middle: fixer} {
						if err := steps.ItemState(ctx, id, "", StateBlocked, item.WaitingOn{Waiter: "item:" + target, Reason: "test dependency"}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			c := Candidate{ItemID: fixer, BaseSHA: base, RunID: "fixer-run"}
			gate := GateOutcome{Outcome: GateFailed, FailingChecks: failing, CandidateChangedPaths: []string{tc.changed}, CheckWatches: map[string][]string{"unnamed": {"src/input"}}}
			out, held, err := l.settleTrunkAlreadyRed(ctx, &c, gate, &LoopResult{})
			if err != nil || out != tc.outcome || held != tc.held {
				t.Fatalf("outcome=%q held=%v err=%v", out, held, err)
			}
			named, err := l.fixerNamedChecks(ctx, fixer)
			if err != nil || allIn([]string{"unnamed"}, named) != tc.attached {
				t.Fatalf("named=%v err=%v", named, err)
			}
			if tc.attached && (!strings.Contains(c.RefusalDetail, base[:12]) || !strings.Contains(c.RefusalDetail, "unnamed")) {
				t.Fatalf("refusal did not name the check and red commit: %q", c.RefusalDetail)
			}
		})
	}
}

type fixerGateFixtureSteps struct {
	*ProcessSteps
}

func (s *fixerGateFixtureSteps) ItemState(ctx context.Context, id, run, state string, waiting ...item.WaitingOn) error {
	// The fixture runs the real lander without staging a worker graph. Use the
	// existing fixture writer for item completion, not a synthetic worker receipt.
	return (&fixerLandSteps{storePath: s.Store}).ItemState(ctx, id, run, state, waiting...)
}

func (s *fixerGateFixtureSteps) Verdict(ctx context.Context, c Candidate, verdict string) error {
	return (&fixerLandSteps{storePath: s.Store}).Verdict(ctx, c, verdict)
}

func TestFixerGateJudgesOwnDiffThenNamedChecksAndLands(t *testing.T) {
	ctx := context.Background()
	repo, adapter := repositoryFile(t, "tools/tillr-loop/tillr-loop")
	tmp := scratchUnderDurable(t)
	declareSiblingInputs(t)
	for _, tool := range []string{"tillr-land", "tillr-gate"} {
		bin := filepath.Join(tmp, tool)
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Dir = filepath.Join(repo, "tools", tool)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", tool, err, out)
		}
	}
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_LOOP_REGISTRY", "")
	const testPath = "tools/tillr-drain/repair_test.go"
	const namedID = "go-tool/tools-tillr-drain-s-whole-suite-passes"
	const upstreamID = "patch-branch-inv2"
	const retiredID = "go-test:tools/tillr-drain:example.invalid/repair:TestVariants/failover=true"
	const skippedID = "go-test:tools/tillr-drain:example.invalid/repair:TestVariants/skipped"
	for _, tc := range []struct {
		name, repair, want string
		names              []string
		lands              bool
	}{
		{"conflicting release", "repaired", "pass", []string{namedID}, true},
		{"no name", "repaired", "pass", nil, true},
		{"named failure", "still red", "fail", []string{namedID}, false},
		{"named unavailable", "unavailable", "error", []string{namedID}, false},
		{"old upstream name", "repaired", "pass", []string{upstreamID, namedID}, true},
		{"removed subtest", "repaired", "pass", []string{retiredID, namedID}, true},
		{"removed subtest with failure", "still red", "fail", []string{retiredID, namedID}, false},
		{"skipped subtest", "repaired", "error", []string{skippedID}, false},
		{"ordinary refuses first", "ordinary red", "fail", []string{namedID}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t)
			write := func(path, text string) {
				path = filepath.Join(f.root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, path, text)
			}
			for _, name := range []string{"tools/fork-check.py", "tools/gate/change-gate.py"} {
				raw, err := os.ReadFile(filepath.Join(repo, name))
				if err != nil {
					t.Fatal(err)
				}
				write(name, string(raw))
			}
			write("tools/tillr-drain/go.mod", "module example.invalid/repair\n\ngo 1.22\n")
			write("tools/tillr-drain/repair.go", "package repair\n")
			test := func(value string) string {
				return `package repair
import "testing"
func TestBroken(t *testing.T) { if "` + value + `" != "repaired" { t.Fatal("repair test still red") } }
func TestVariants(t *testing.T) {
    t.Run("skipped", func(t *testing.T) { t.Skip("fixture skipped") })
}
`
			}
			write(testPath, test("repaired"))
			write("tools/go-test-shards.py", `import os, sys
from pathlib import Path
if "unavailable" in Path("tools/tillr-drain/repair_test.go").read_text():
    print("fixture: tool unavailable", file=sys.stderr)
    sys.exit(127)
os.chdir(sys.argv[1])
os.execvp("go", ["go", "test", "-run", "^TestBroken$"])
`)
			write("tools/gate/checks/fixture-change.json", `{"id":"fixture-change","label":"fixture change","scope":"change","tier":"fast","budget_ms":2000,"reads":["tools/tillr-drain/"],"command":["sh","-c","! grep -q 'ordinary red' tools/tillr-drain/repair_test.go"]}`)
			write("tools/gate/checks/fixture-fast.json", `{"id":"fixture-fast","label":"fixture fast","scope":"tree","tier":"fast","budget_ms":2000,"reads":["tools/tillr-drain/"],"command":["true"]}`)
			// go_build_needed and tillr-gate both read go-build.json, so use a real declaration.
			write("tools/gate/checks/go-build.json", `{"id":"go-build","label":"fixture build","scope":"tree","tier":"fast","budget_ms":2000,"reads":["unrelated/"],"command":["true"]}`)
			if tc.name == "conflicting release" || tc.name == "old upstream name" {
				makeFixerConflict(t, f.root)
			}
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "green fixture")
			green := runGit(t, f.root, "rev-parse", "HEAD")
			runGit(t, f.root, "update-ref", "refs/tillr/green", green)
			if tc.name == "conflicting release" || tc.name == "old upstream name" {
				write("yolo-packs/fixture/pack.json", `{"contributes":[{"source":"file:///fixture.git","patch_branch":"file:///downstream.git?ref=patch/fixture"}]}`)
			}
			removed := allIn([]string{retiredID}, tc.names)
			redTest := test("broken on trunk")
			if removed {
				redTest = strings.Replace(redTest, "func TestVariants(t *testing.T) {", `func TestVariants(t *testing.T) {
    t.Run("failover=true", func(t *testing.T) { t.Fatal("old dispatch test") })`, 1)
			}
			write(testPath, redTest)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "break the rolling test after green")
			base := runGit(t, f.root, "rev-parse", "HEAD")
			if tc.name == "conflicting release" {
				fork := exec.Command("python3", "tools/fork-check.py", "--replay", "--only="+upstreamID)
				fork.Dir = f.root
				if out, err := fork.CombinedOutput(); err == nil || !strings.Contains(string(out), "FAIL patch-branch-inv2") || !strings.Contains(string(out), "CONFLICT (content)") {
					t.Fatalf("the real replay must see the conflicting release: %v\n%s", err, out)
				}
			}
			red := exec.Command("python3", "tools/fork-check.py", "--replay", "--only="+namedID)
			red.Dir = f.root
			if out, err := red.CombinedOutput(); err == nil || !strings.Contains(string(out), "FAIL go-tool") || !strings.Contains(string(out), "TestBroken") {
				t.Fatalf("fixture must start with a red named test: %v\n%s", err, out)
			}
			id, err := l.fileTrunkFixer(ctx, base, tc.names, GateOutcome{SuspectPaths: []string{testPath}})
			if err != nil {
				t.Fatal(err)
			}
			if removed {
				write(testPath, test("broken on trunk"))
				runGit(t, f.root, "commit", "-qam", "replace the old dispatch subtest")
				base = runGit(t, f.root, "rev-parse", "HEAD")
			}
			lane := l.lanePath("lane-1")
			if err := os.MkdirAll(filepath.Dir(lane), 0755); err != nil {
				t.Fatal(err)
			}
			runGit(t, f.root, "worktree", "add", "-q", "-b", LaneBranch(f.laneRoot, "lane-1"), lane, base)
			writeForkFile(t, filepath.Join(lane, testPath), test(tc.repair))
			runGit(t, lane, "add", testPath)
			runGit(t, lane, "commit", "-qm", "fix: repair the rolling test")
			head := runGit(t, lane, "rev-parse", "HEAD")
			for _, file := range []string{FenceFile, ItemFenceFile} {
				writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), file), testPath+"\n")
			}
			steps := &ProcessSteps{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: time.Now,
				GateProgram: "python3 " + adapter + " gate", LandProgram: "python3 " + adapter + " land", FixerGateProgram: DefaultFixerGateProgram}
			l.cfg.Steps = &fixerGateFixtureSteps{ProcessSteps: steps}
			c := Candidate{ItemID: id, RunID: "run-fixer", LaneID: "lane-1", BaseSHA: base, HeadSHA: head, Worktree: lane}
			var result LoopResult
			_, landed, err := l.Verify(ctx, c, &result, nil)
			if err != nil && !(tc.want == "error" && errors.Is(err, errItemFaultHeld)) {
				steps, _ := os.ReadFile(filepath.Join(RunDir(f.root, c.RunID), StepsFile))
				logs, _ := filepath.Glob(filepath.Join(RunDir(f.root, c.RunID), "logs", "*.log"))
				for _, path := range logs {
					if raw, e := os.ReadFile(path); e == nil {
						t.Logf("%s: %s", filepath.Base(path), raw)
					}
				}
				t.Fatalf("verify: %v; steps: %s", err, steps)
			}
			if tc.want == "error" && (!errors.Is(err, errItemFaultHeld) || result.Rejected != 0) {
				t.Fatalf("unanswered named check did not hold: %v %+v", err, result)
			}
			if landed != tc.lands || (!tc.lands && runGit(t, f.root, "rev-parse", "HEAD") != base) {
				t.Fatalf("landed=%v want %v: %+v", landed, tc.lands, result)
			}
			if tc.want == "fail" && result.RefusedSteps[RefusalGate] != 1 {
				t.Fatalf("no gate refusal: %+v", result)
			}
			runDir := RunDir(f.root, c.RunID)
			var verdict struct {
				Result string `json:"result"`
				Checks []struct {
					ID     string `json:"check_id"`
					LogRef string `json:"log_ref"`
				} `json:"checks"`
			}
			payload, _ := readRecordPayload(filepath.Join(runDir, GateVerdictRecord))
			if json.Unmarshal(payload, &verdict) != nil || verdict.Result != tc.want {
				t.Fatalf("gate verdict: %s, want %s", payload, tc.want)
			}
			if err := l.readStore(ctx, func(r *store.Reader) error {
				var count int
				var saved string
				if err := r.QueryRow(`SELECT COUNT(*), COALESCE(MAX(json_extract(payload,'$.result')), '') FROM events WHERE kind=? AND object_id=?`, GateVerdictKind, c.RunID).Scan(&count, &saved); err != nil {
					return err
				}
				if count != 1 || saved != tc.want {
					t.Fatalf("stored gate judgment: %d records at %q, want one at %q", count, saved, tc.want)
				}
				if removed {
					if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, itemFaultKind, id).Scan(&count); err != nil {
						return err
					}
					if count != 0 {
						t.Fatalf("a removed subtest recorded %d fault holds", count)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(verdict.Checks) == 0 {
				t.Fatalf("gate did not log its checks: %s", payload)
			}
			log, err := os.ReadFile(strings.TrimSuffix(verdict.Checks[0].LogRef, ".triage.log") + ".log")
			if err != nil {
				t.Fatal(err)
			}
			text := string(log)
			ordinary := strings.Index(text, "fixture-change")
			named := strings.Index(text, "fixer named check "+namedID+":")
			if ordinary < 0 || !strings.Contains(text, "fixture-fast") || (named >= 0) != (allIn([]string{namedID}, tc.names) && tc.name != "ordinary refuses first") || (named >= 0 && named < ordinary) {
				t.Fatalf("ordinary checks must run before named checks:\n%s", text)
			}
			if tc.name == "conflicting release" && (strings.Contains(text, upstreamID) || !strings.Contains(text, "PASS go-tool")) {
				t.Fatalf("gate bought the wrong checks:\n%s", text)
			}
			if tc.name == "named unavailable" && (!strings.Contains(text, "could not judge") || !strings.Contains(text, "tool unavailable")) {
				t.Fatalf("missing unknown evidence:\n%s", text)
			}
			if tc.name == "skipped subtest" && !strings.Contains(text, skippedID+": could not judge\ntest or enclosing test skipped at this commit") {
				t.Fatalf("skipped subtest must remain unjudged:\n%s", text)
			}
			if tc.name == "old upstream name" && !strings.Contains(text, upstreamID+": dropped (reads upstream)") {
				t.Fatalf("missing dropped-name line:\n%s", text)
			}
			if removed {
				for _, check := range verdict.Checks {
					if check.ID == retiredID {
						t.Fatalf("removed subtest was not omitted: %s", payload)
					}
				}
				if !strings.Contains(text, retiredID+": dropped (test not present at this commit)") {
					t.Fatalf("missing dropped-subtest evidence:\n%s", text)
				}
			}
			if tc.name == "named failure" && !strings.Contains(text, "TestBroken") {
				t.Fatalf("missing named failure evidence:\n%s", text)
			}
			if tc.name == "conflicting release" && runGit(t, f.root, "rev-parse", "HEAD^{tree}") != runGit(t, lane, "rev-parse", "HEAD^{tree}") {
				t.Fatal("landed a different tree")
			}
		})
	}
}

func makeFixerConflict(t *testing.T, root string) {
	t.Helper()
	upstream := t.TempDir()
	runGit(t, upstream, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(upstream, "conflict"), "original\n")
	runGit(t, upstream, "add", ".")
	runGit(t, upstream, "commit", "-qm", "original")
	runGit(t, upstream, "checkout", "-qb", "patch/fixture")
	writeForkFile(t, filepath.Join(upstream, "conflict"), "downstream patch\n")
	runGit(t, upstream, "commit", "-qam", "downstream patch")
	runGit(t, upstream, "checkout", "-q", "main")
	writeForkFile(t, filepath.Join(upstream, "conflict"), "new upstream release\n")
	runGit(t, upstream, "commit", "-qam", "upstream release")
	runGit(t, upstream, "tag", "v1.6.0")
	// Keep the network out of the fixture but ask the same Git merge question.
	path := filepath.Join(root, "bin", "pi-forks")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, "#!/bin/sh\n[ \"$1 $2\" = 'patch-branch merge-check' ] || exit 2\n"+
		"git -C '"+upstream+"' merge-tree --write-tree patch/fixture refs/tags/v1.6.0 && exit 0\n"+
		"echo 'FAILED: main conflicts with refs/tags/v1.6.0 (INV-2)'\nexit 1\n")
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(path, "patch-branch", "merge-check").CombinedOutput(); err == nil || !strings.Contains(string(out), "CONFLICT (content)") {
		t.Fatalf("upstream fixture did not conflict: %v\n%s", err, out)
	}
}
