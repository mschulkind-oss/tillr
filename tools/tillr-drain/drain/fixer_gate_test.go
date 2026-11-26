package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	repo, _ := repositoryFile(t, "tools/tillr-loop/go.mod")
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
	const namedID = "fixture-suite"
	const retiredID = "go-test:tools/tillr-drain:example.invalid/repair:TestVariants/failover=true"
	const skippedID = "go-test:tools/tillr-drain:example.invalid/repair:TestVariants/skipped"
	for _, tc := range []struct {
		name, repair, want string
		names              []string
		lands              bool
	}{
		{"no name", "repaired", "pass", nil, true},
		{"named failure", "still red", "fail", []string{namedID}, false},
		{"named unavailable", "unavailable", "error", []string{namedID}, false},
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
			write("tools/gate/repo-gate", "#!/bin/sh\nexec "+filepath.Join(repo, "tools", "gate", "repo-gate")+" \"$@\"\n")
			if err := os.Chmod(filepath.Join(f.root, "tools", "gate", "repo-gate"), 0o755); err != nil {
				t.Fatal(err)
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
			// The named check is a heavy whole-tree declaration running one Go test. The
			// unavailable case declares a tool no machine has, so the check cannot be judged.
			suiteTools := `["sh","go"]`
			if tc.name == "named unavailable" {
				suiteTools = `["sh","go","fixture-tool-nobody-installs"]`
			}
			write("tools/gate/checks/fixture-suite.json", `{"id":"fixture-suite","label":"fixture suite","scope":"tree","tier":"heavy","timeout_ms":600000,"reads":["tools/tillr-drain/"],"tools":`+suiteTools+`,"command":["sh","-c","cd tools/tillr-drain && exec go test -run '^TestBroken$'"]}`)
			write("tools/gate/checks/fixture-change.json", `{"id":"fixture-change","label":"fixture change","scope":"change","tier":"fast","budget_ms":2000,"reads":["tools/tillr-drain/"],"command":["sh","-c","! grep -q 'ordinary red' tools/tillr-drain/repair_test.go"]}`)
			write("tools/gate/checks/fixture-fast.json", `{"id":"fixture-fast","label":"fixture fast","scope":"tree","tier":"fast","budget_ms":2000,"reads":["tools/tillr-drain/"],"command":["true"]}`)
			// go_build_needed and tillr-gate both read go-build.json, so use a real declaration.
			write("tools/gate/checks/go-build.json", `{"id":"go-build","label":"fixture build","scope":"tree","tier":"fast","budget_ms":2000,"reads":["unrelated/"],"command":["true"]}`)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "green fixture")
			green := runGit(t, f.root, "rev-parse", "HEAD")
			runGit(t, f.root, "update-ref", "refs/tillr/green", green)
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
			red := exec.Command("go", "test", "-run", "^TestBroken$")
			red.Dir = filepath.Join(f.root, "tools", "tillr-drain")
			if out, err := red.CombinedOutput(); err == nil || !strings.Contains(string(out), "TestBroken") {
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
			repoRoot, _ := repositoryFile(t, "tools/tillr-loop/go.mod")
			t.Setenv("TILLR_INSTALL_ROOT", filepath.Join(repoRoot, "tools"))
			steps := &ProcessSteps{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: time.Now,
				GateProgram: "tillr-loop gate", LandProgram: "tillr-loop land", FixerGateProgram: DefaultFixerGateProgram}
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
			if tc.name == "named unavailable" && !strings.Contains(text, "fixer named check "+namedID+": could not judge") {
				t.Fatalf("missing unknown evidence:\n%s", text)
			}
			if tc.name == "skipped subtest" && !strings.Contains(text, skippedID+": could not judge\ntest or enclosing test skipped at this commit") {
				t.Fatalf("skipped subtest must remain unjudged:\n%s", text)
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
		})
	}
}

func TestTrunkRepairGateUsesRepairPriorityClass(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	work := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "niceness.txt")
	gateProg := executable(t, fmt.Sprintf(`nice > %q
exit 0
`, outFile))
	prio := newNicePriority("test")
	defer prio.Close()
	repairNice, _ := prio.NiceTarget(ProcessClassRepair)
	gateNice, _ := prio.NiceTarget(ProcessClassGate)
	steps := &ProcessSteps{
		Root:             root,
		Store:            gateTimingStore(t, root),
		GateProgram:      gateProg,
		FixerGateProgram: gateProg,
		Now:              time.Now,
		priority:         prio,
	}

	for _, tc := range []struct {
		name   string
		itemID string
		runID  string
		want   int
	}{
		{name: "ordinary candidate", itemID: "item-ordinary-1", runID: "run-item-ordinary-1", want: gateNice},
		{name: "revert gate", itemID: RevertItemID("deadbeef1234"), runID: "run-revert-deadbeef1234-cafebabe5678", want: repairNice},
		{name: "trunk fixer gate", itemID: TrunkFixerID("deadbeef1234"), runID: "run-fixer-main-deadbeef1234-a1-n1", want: repairNice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(outFile)
			c := Candidate{
				ItemID:   tc.itemID,
				LaneID:   "lane-1",
				RunID:    tc.runID,
				Worktree: work,
				BaseSHA:  "1111111111111111111111111111111111111111",
				HeadSHA:  "2222222222222222222222222222222222222222",
			}
			if _, err := steps.Gate(ctx, c); err != nil {
				t.Fatalf("Gate: %v", err)
			}
			got, err := os.ReadFile(outFile)
			if err != nil {
				t.Fatalf("read %s: %v", outFile, err)
			}
			if strings.TrimSpace(string(got)) != strconv.Itoa(tc.want) {
				t.Fatalf("gate nice = %q, want %d", strings.TrimSpace(string(got)), tc.want)
			}
		})
	}

	t.Run("fixer-gate named check", func(t *testing.T) {
		_ = os.Remove(outFile)
		c := Candidate{
			ItemID:   TrunkFixerID("deadbeef1234"),
			LaneID:   "lane-1",
			RunID:    "run-fixer-main-deadbeef1234-a1-n1",
			Worktree: work,
			BaseSHA:  "1111111111111111111111111111111111111111",
			HeadSHA:  "2222222222222222222222222222222222222222",
		}
		res, out := steps.fixerCheck(ctx, c, "legacy-suite")
		if res != "pass" {
			t.Fatalf("fixerCheck = %q (%s), want pass", res, out)
		}
		got, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatalf("read %s: %v", outFile, err)
		}
		if strings.TrimSpace(string(got)) != strconv.Itoa(repairNice) {
			t.Fatalf("fixerCheck nice = %q, want %d", strings.TrimSpace(string(got)), repairNice)
		}
	})
}
