package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestParseGoAffectedSummary(t *testing.T) {
	t.Run("concrete tests from per-check log", func(t *testing.T) {
		raw := `go-affected: failing packages and tests:
  github.com/mschulkind-oss/tillr-drain/drain: TestA, TestB/sub
  github.com/mschulkind-oss/tillr-land/land: TestC
`
		pkgs, found, concrete := parseGoAffectedSummary(raw)
		if !found || !concrete {
			t.Fatalf("found=%v concrete=%v, want true/true", found, concrete)
		}
		if got := strings.Join(pkgs["github.com/mschulkind-oss/tillr-drain/drain"], ","); got != "TestA,TestB/sub" {
			t.Fatalf("drain tests = %q, want TestA,TestB/sub", got)
		}
		if got := strings.Join(pkgs["github.com/mschulkind-oss/tillr-land/land"], ","); got != "TestC" {
			t.Fatalf("land tests = %q, want TestC", got)
		}
	})

	t.Run("pipe-prefixed gate log", func(t *testing.T) {
		raw := `       | go-affected: failing packages and tests:
       |   github.com/mschulkind-oss/tillr-drain/drain: TestOnly
       | --- end output ---
`
		pkgs, found, concrete := parseGoAffectedSummary(raw)
		if !found || !concrete {
			t.Fatalf("found=%v concrete=%v, want true/true", found, concrete)
		}
		if got := strings.Join(pkgs["github.com/mschulkind-oss/tillr-drain/drain"], ","); got != "TestOnly" {
			t.Fatalf("drain tests = %q, want TestOnly", got)
		}
	})

	t.Run("unnamed package failure falls back", func(t *testing.T) {
		raw := `go-affected: failing packages and tests:
  github.com/mschulkind-oss/tillr-drain/drain: (no failing test named)
`
		_, found, concrete := parseGoAffectedSummary(raw)
		if !found || concrete {
			t.Fatalf("found=%v concrete=%v, want true/false", found, concrete)
		}
	})
}

func writeReplayTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestProcessStepsReplayScopesGoAffectedAndSetsReplayEnv(t *testing.T) {
	root := t.TempDir()
	laneRoot := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "user.email", "test@example.com")

	// Create a tiny Go module with two packages:
	// - pkgA has TestPass (passes) and TestFailOnBase (fails)
	// - pkgB has TestUnrelatedFail (always fails) — MUST NOT run when pkgA: TestPass is named!
	writeReplayTestFile(t, filepath.Join(root, "tools", "sample", "go.mod"), "module example.com/sample\n\ngo 1.22\n")
	writeReplayTestFile(t, filepath.Join(root, "tools", "sample", "pkga", "a_test.go"), `package pkga
import "testing"
func TestPass(t *testing.T) {}
func TestFailOnBase(t *testing.T) { t.Fatal("already red on base") }
`)
	writeReplayTestFile(t, filepath.Join(root, "tools", "sample", "pkgb", "b_test.go"), `package pkgb
import "testing"
func TestUnrelatedFail(t *testing.T) { t.Fatal("unrelated package failure") }
`)
	// Also place a fake tools/gate/go_affected.py that records its args & env.
	argsFile := filepath.Join(t.TempDir(), "go_affected_args.txt")
	envFile := filepath.Join(t.TempDir(), "go_affected_env.txt")
	writeReplayTestFile(t, filepath.Join(root, "tools", "gate", "go_affected.py"), `import os, sys
with open(`+`"`+argsFile+`"`+`, "w") as f:
    f.write("\n".join(sys.argv[1:]))
with open(`+`"`+envFile+`"`+`, "w") as f:
    for k in sorted(os.environ):
        if k.startswith(("TILLR_", "GIT_CONFIG_")):
            f.write(f"{k}={os.environ[k]}\n")
sys.exit(0)
`)
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	baseSHA := runGit(t, root, "rev-parse", "HEAD")

	// Set inherited TILLR_LANE_ID and TILLR_LOOP_STORE to verify replayChildEnv strips them.
	t.Setenv("TILLR_LANE_ID", "lane-stale")
	t.Setenv("TILLR_LOOP_STORE", "/tmp/stale-store.db")

	steps := &ProcessSteps{
		Root:          root,
		LaneRoot:      laneRoot,
		ReplayProgram: DefaultReplayProgram,
	}

	t.Run("named passing test ignores unrelated failing package", func(t *testing.T) {
		runID := "run-named-pass"
		logPath := filepath.Join(RunDir(root, runID), "logs", "fork-check-1-go-affected-affected-tillr-go-test-packages-pass.log")
		writeReplayTestFile(t, logPath, "go-affected: failing packages and tests:\n  example.com/sample/pkga: TestPass\n")

		out, err := steps.Replay(context.Background(), Candidate{
			RunID:  runID,
			ItemID: "item-1",
		}, baseSHA, []string{goAffectedCheckID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 0 {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=nil", out)
		}
	})

	t.Run("named failing test reports red", func(t *testing.T) {
		runID := "run-named-fail"
		logPath := filepath.Join(RunDir(root, runID), "logs", "fork-check-1-go-affected-affected-tillr-go-test-packages-pass.log")
		writeReplayTestFile(t, logPath, "go-affected: failing packages and tests:\n  example.com/sample/pkga: TestFailOnBase\n")

		out, err := steps.Replay(context.Background(), Candidate{
			RunID:  runID,
			ItemID: "item-2",
		}, baseSHA, []string{goAffectedCheckID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 1 || out.Red[0] != goAffectedCheckID {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=[%s]", out, goAffectedCheckID)
		}
	})

	t.Run("unnamed failure falls back to go_affected.py --changed with replay env", func(t *testing.T) {
		runID := "run-unnamed-fallback"
		logPath := filepath.Join(RunDir(root, runID), "logs", "fork-check-1-go-affected-affected-tillr-go-test-packages-pass.log")
		writeReplayTestFile(t, logPath, "go-affected: failing packages and tests:\n  example.com/sample/pkga: (no failing test named)\n")

		out, err := steps.Replay(context.Background(), Candidate{
			RunID:                 runID,
			ItemID:                "item-3",
			CandidateChangedPaths: []string{"tools/sample/pkga/a_test.go"},
		}, baseSHA, []string{goAffectedCheckID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 0 {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=nil", out)
		}
		gotArgs, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("read args: %v", err)
		}
		if strings.TrimSpace(string(gotArgs)) != "--changed\ntools/sample/pkga/a_test.go" {
			t.Fatalf("go_affected.py args = %q, want --changed tools/sample/pkga/a_test.go", string(gotArgs))
		}
		gotEnv, err := os.ReadFile(envFile)
		if err != nil {
			t.Fatalf("read env: %v", err)
		}
		envStr := string(gotEnv)
		for _, want := range []string{
			"TILLR_RUN_ID=" + runID,
			"TILLR_ITEM_ID=item-3",
			"gc.auto",
			"maintenance.auto",
		} {
			if !strings.Contains(envStr, want) {
				t.Fatalf("replay env missing %q:\n%s", want, envStr)
			}
		}
		for _, forbid := range []string{"TILLR_LANE_ID=", "TILLR_LOOP_STORE="} {
			if strings.Contains(envStr, forbid) {
				t.Fatalf("replay env unexpectedly contains %q:\n%s", forbid, envStr)
			}
		}
	})

	t.Run("custom ReplayProgram stays unchanged and receives replay env", func(t *testing.T) {
		customDir := t.TempDir()
		customOut := filepath.Join(customDir, "custom.txt")
		customScript := filepath.Join(customDir, "replay.py")
		writeReplayTestFile(t, customScript, `import os, sys
with open(`+`"`+customOut+`"`+`, "w") as f:
    f.write("|".join([
        os.environ.get("TILLR_RUN_ID", ""),
        os.environ.get("TILLR_ITEM_ID", ""),
        os.environ.get("TILLR_LANE_ID", ""),
        sys.argv[1],
    ]))
`)
		customSteps := &ProcessSteps{
			Root:          root,
			LaneRoot:      laneRoot,
			ReplayProgram: "python3 " + customScript + " {only}",
		}
		out, err := customSteps.Replay(context.Background(), Candidate{
			RunID:  "run-custom",
			ItemID: "item-4",
		}, baseSHA, []string{goAffectedCheckID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 0 {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=nil", out)
		}
		raw, err := os.ReadFile(customOut)
		if err != nil {
			t.Fatalf("read customOut: %v", err)
		}
		if got := string(raw); got != "run-custom|item-4||"+goAffectedCheckID {
			t.Fatalf("custom replay output = %q, want run-custom|item-4||%s", got, goAffectedCheckID)
		}
	})
}

func TestParseTriageGoTests(t *testing.T) {
	t.Run("go-tool concrete cases from triage block", func(t *testing.T) {
		raw := `fork-check: FAILED -- 1 of 4 check(s)
fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       command: sh -c 'cd tools/sample && go test ./...'
       case: TestPass -- example.com/sample/pkga (a_test.go:10)
       assertion: wanted green
       case: TestSub/one -- example.com/sample/pkga
       reproduce (one file): sh -c 'cd tools/sample && go test ./...'
`
		pkgs, found, concrete := parseTriageGoTests(raw, "go-tool/tools-sample-s-whole-suite-passes")
		if !found || !concrete {
			t.Fatalf("found=%v concrete=%v, want true/true", found, concrete)
		}
		if got := strings.Join(pkgs["example.com/sample/pkga"], ","); got != "TestPass,TestSub/one" {
			t.Fatalf("pkga tests = %q, want TestPass,TestSub/one", got)
		}
	})

	t.Run("go-substrate concrete case", func(t *testing.T) {
		raw := `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-substrate: tools/tillr's whole suite passes
       case: TestStoreLock -- github.com/mschulkind-oss/tillr/store (lock_test.go:42)
`
		pkgs, found, concrete := parseTriageGoTests(raw, "go-substrate/tools-tillr-s-whole-suite-passes")
		if !found || !concrete {
			t.Fatalf("found=%v concrete=%v, want true/true", found, concrete)
		}
		if got := strings.Join(pkgs["github.com/mschulkind-oss/tillr/store"], ","); got != "TestStoreLock" {
			t.Fatalf("store tests = %q, want TestStoreLock", got)
		}
	})

	t.Run("unnamed failure falls back to whole check", func(t *testing.T) {
		raw := `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       no case named in the output; reproduce with: sh -c 'cd tools/sample && go test ./...'
`
		_, found, concrete := parseTriageGoTests(raw, "go-tool/tools-sample-s-whole-suite-passes")
		if !found || concrete {
			t.Fatalf("found=%v concrete=%v, want true/false", found, concrete)
		}
	})

	t.Run("truncated triage falls back to whole check", func(t *testing.T) {
		raw := `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       case: TestPass -- example.com/sample/pkga (a_test.go:10)
       (2 more failing case(s) not listed; the packet keeps 5)
`
		_, found, concrete := parseTriageGoTests(raw, "go-tool/tools-sample-s-whole-suite-passes")
		if !found || concrete {
			t.Fatalf("found=%v concrete=%v, want true/false", found, concrete)
		}
	})

	t.Run("non-Go failure in gate-runner falls back to whole check", func(t *testing.T) {
		raw := `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage gate-runner: tools/tillr-gate's whole suite passes
       case: test_change_gate -- tools/gate/change-gate.test.py:88
`
		_, found, concrete := parseTriageGoTests(raw, "gate-runner/tools-tillr-gate-s-whole-suite-passes")
		if !found || concrete {
			t.Fatalf("found=%v concrete=%v, want true/false", found, concrete)
		}
	})
}

func TestProcessStepsReplayScopesGoChecksFromTriage(t *testing.T) {
	root := t.TempDir()
	laneRoot := t.TempDir()
	runGit(t, root, "init", "-q", "-b", "main")
	runGit(t, root, "config", "user.name", "Test")
	runGit(t, root, "config", "user.email", "test@example.com")

	writeReplayTestFile(t, filepath.Join(root, "tools", "sample", "go.mod"), "module example.com/sample\n\ngo 1.22\n")
	writeReplayTestFile(t, filepath.Join(root, "tools", "sample", "pkga", "a_test.go"), `package pkga
import "testing"
func TestPass(t *testing.T) {}
func TestFailOnBase(t *testing.T) { t.Fatal("already red on base") }
func TestSub(t *testing.T) {
	t.Run("pass", func(t *testing.T) {})
	t.Run("fail_sibling", func(t *testing.T) { t.Fatal("unrelated sibling subtest failure") })
}
`)
	writeReplayTestFile(t, filepath.Join(root, "tools", "sample", "pkgb", "b_test.go"), `package pkgb
import "testing"
func TestUnrelatedFail(t *testing.T) { t.Fatal("unrelated package failure") }
`)
	// The default replay program is the tree's loop adapter; the stand-in tillr-gate
	// records the arguments it was handed.
	wholeCheckLog := filepath.Join(t.TempDir(), "whole-check-called.txt")
	bin := t.TempDir()
	fakeGate := filepath.Join(bin, "tillr-gate")
	writeReplayTestFile(t, fakeGate, `#!/usr/bin/env python3
import sys
with open(`+`"`+wholeCheckLog+`"`+`, "a") as f:
    f.write(" ".join(sys.argv[1:]) + "\n")
sys.exit(0)
`)
	if err := os.Chmod(fakeGate, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeReplayTestFile(t, filepath.Join(root, "tools", "gate", "checks", "go-tool-sample.json"), `{"id": "go-tool/tools-sample-s-whole-suite-passes", "scope": "tree", "tier": "heavy"}`+"\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	baseSHA := runGit(t, root, "rev-parse", "HEAD")

	steps := &ProcessSteps{
		Root:          root,
		LaneRoot:      laneRoot,
		ReplayProgram: DefaultReplayProgram,
	}
	const checkID = "go-tool/tools-sample-s-whole-suite-passes"

	t.Run("named passing case from triage runs only that case and skips the whole check", func(t *testing.T) {
		_ = os.Remove(wholeCheckLog)
		logPath := filepath.Join(t.TempDir(), "00-gate.log")
		writeReplayTestFile(t, logPath, `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       case: TestPass -- example.com/sample/pkga (a_test.go:3)
`)
		out, err := steps.Replay(context.Background(), Candidate{
			RunID:           "run-triage-pass",
			ItemID:          "item-1",
			replayCheckLogs: map[string]string{checkID: logPath},
		}, baseSHA, []string{checkID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 0 {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=nil", out)
		}
		if _, err := os.Stat(wholeCheckLog); !os.IsNotExist(err) {
			raw, _ := os.ReadFile(wholeCheckLog)
			t.Fatalf("the whole check unexpectedly ran: %q", string(raw))
		}
	})

	t.Run("named failing case from triage reports red", func(t *testing.T) {
		_ = os.Remove(wholeCheckLog)
		logPath := filepath.Join(t.TempDir(), "00-gate.log")
		writeReplayTestFile(t, logPath, `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       case: TestFailOnBase -- example.com/sample/pkga (a_test.go:4)
`)
		out, err := steps.Replay(context.Background(), Candidate{
			RunID:           "run-triage-fail",
			ItemID:          "item-2",
			replayCheckLogs: map[string]string{checkID: logPath},
		}, baseSHA, []string{checkID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 1 || out.Red[0] != checkID {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=[%s]", out, checkID)
		}
		if _, err := os.Stat(wholeCheckLog); !os.IsNotExist(err) {
			raw, _ := os.ReadFile(wholeCheckLog)
			t.Fatalf("the whole check unexpectedly ran: %q", string(raw))
		}
	})

	t.Run("unnamed failure in triage falls back to whole check", func(t *testing.T) {
		_ = os.Remove(wholeCheckLog)
		logPath := filepath.Join(t.TempDir(), "00-gate.log")
		writeReplayTestFile(t, logPath, `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       no case named in the output; reproduce with: sh -c 'cd tools/sample && go test ./...'
`)
		out, err := steps.Replay(context.Background(), Candidate{
			RunID:           "run-triage-unnamed",
			ItemID:          "item-3",
			replayCheckLogs: map[string]string{checkID: logPath},
		}, baseSHA, []string{checkID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 0 {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=nil", out)
		}
		raw, err := os.ReadFile(wholeCheckLog)
		if err != nil {
			t.Fatalf("expected the whole check fallback: %v", err)
		}
		if got := strings.TrimSpace(string(raw)); !strings.Contains(got, "--only "+checkID) {
			t.Fatalf("whole-check args = %q, want --only %s", got, checkID)
		}
	})

	t.Run("wholeCheckReplay forces whole check even when triage names a case", func(t *testing.T) {
		_ = os.Remove(wholeCheckLog)
		logPath := filepath.Join(t.TempDir(), "00-gate.log")
		writeReplayTestFile(t, logPath, `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       case: TestFailOnBase -- example.com/sample/pkga (a_test.go:4)
`)
		out, err := steps.Replay(context.Background(), Candidate{
			RunID:            "run-whole-forced",
			ItemID:           "item-4",
			replayCheckLogs:  map[string]string{checkID: logPath},
			wholeCheckReplay: true,
		}, baseSHA, []string{checkID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 0 {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=nil", out)
		}
		raw, err := os.ReadFile(wholeCheckLog)
		if err != nil {
			t.Fatalf("expected the whole check when wholeCheckReplay=true: %v", err)
		}
		if got := strings.TrimSpace(string(raw)); !strings.Contains(got, "--only "+checkID) {
			t.Fatalf("whole-check args = %q, want --only %s", got, checkID)
		}
	})

	t.Run("subtest and parent in triage replays only the subtest", func(t *testing.T) {
		_ = os.Remove(wholeCheckLog)
		logPath := filepath.Join(t.TempDir(), "00-gate.log")
		writeReplayTestFile(t, logPath, `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       case: TestSub/pass -- example.com/sample/pkga (a_test.go:6)
       case: TestSub -- example.com/sample/pkga
`)
		out, err := steps.Replay(context.Background(), Candidate{
			RunID:           "run-triage-subtest-only",
			ItemID:          "item-5",
			replayCheckLogs: map[string]string{checkID: logPath},
		}, baseSHA, []string{checkID})
		if err != nil {
			t.Fatalf("Replay: %v", err)
		}
		if !out.Known || len(out.Red) != 0 {
			t.Fatalf("Replay outcome = %+v, want Known=true Red=nil (sibling subtest should not run)", out)
		}
		if _, err := os.Stat(wholeCheckLog); !os.IsNotExist(err) {
			raw, _ := os.ReadFile(wholeCheckLog)
			t.Fatalf("the whole check unexpectedly ran: %q", string(raw))
		}
	})
}

type shortReplayGateSteps struct {
	Steps
	gateOutcome GateOutcome
	ps          *ProcessSteps
}

func (s *shortReplayGateSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	return s.gateOutcome, nil
}

func (s *shortReplayGateSteps) Replay(ctx context.Context, candidate Candidate, rev string, checks []string) (ReplayOutcome, error) {
	return s.ps.Replay(ctx, candidate, rev, checks)
}

func TestGateFailureNamedGoCaseShortReplayRecordsRefusal(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	writeReplayTestFile(t, filepath.Join(f.root, "tools", "sample", "go.mod"), "module example.com/sample\n\ngo 1.22\n")
	writeReplayTestFile(t, filepath.Join(f.root, "tools", "sample", "pkga", "a_test.go"), `package pkga
import "testing"
func TestCandidateBrokeThis(t *testing.T) {}
`)
	writeReplayTestFile(t, filepath.Join(f.root, "tools", "sample", "pkgb", "b_test.go"), `package pkgb
import "testing"
func TestUnrelatedBaseRed(t *testing.T) { t.Fatal("unrelated package failure on base") }
`)
	wholeCheckMarker := filepath.Join(t.TempDir(), "whole-check-ran")
	writeReplayTestFile(t, filepath.Join(f.root, "tools", "fork-check.py"), `import sys
with open(`+`"`+wholeCheckMarker+`"`+`, "w") as f:
    f.write(" ".join(sys.argv[1:]))
sys.exit(1)
`)
	runGit(t, f.root, "add", "tools/sample", "tools/fork-check.py")
	runGit(t, f.root, "commit", "-qm", "add sample go module")
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]'`)
		return err
	})
	p := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch: %v/%v", ok, err)
	}
	laneWorktree := l.lanePath(p.decision.LaneID)
	writeForkFile(t, filepath.Join(laneWorktree, "candidate.txt"), "candidate\n")
	runGit(t, laneWorktree, "add", "candidate.txt")
	runGit(t, laneWorktree, "commit", "-qm", "candidate")
	writeForkFile(t, filepath.Join(l.laneDirPath(p.decision.LaneID), FenceFile), "candidate.txt\n")
	c := Candidate{
		ItemID:   "item-a",
		LaneID:   p.decision.LaneID,
		RunID:    p.decision.RunRequest.RunID,
		Worktree: laneWorktree,
		BaseSHA:  base,
		HeadSHA:  runGit(t, laneWorktree, "rev-parse", "HEAD"),
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID:        c.RunID,
			ItemID:       c.ItemID,
			LaneID:       c.LaneID,
			Backend:      "fixture",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    l.cfg.Now().Format(time.RFC3339),
			FinishedAt:   l.cfg.Now().Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})

	const checkID = "go-tool/tools-sample-s-whole-suite-passes"
	gateLog := filepath.Join(t.TempDir(), "00-gate.log")
	writeReplayTestFile(t, gateLog, `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools/sample's whole suite passes
       case: TestCandidateBrokeThis -- example.com/sample/pkga (a_test.go:3)
`)

	ps := &ProcessSteps{
		Root:          f.root,
		LaneRoot:      f.laneRoot,
		ReplayProgram: DefaultReplayProgram,
	}
	l.cfg.Steps = &shortReplayGateSteps{
		Steps: l.cfg.Steps,
		gateOutcome: GateOutcome{
			Outcome:       GateFailed,
			FailingChecks: []string{checkID},
			CheckLogs:     map[string]string{checkID: gateLog},
		},
		ps: ps,
	}

	examineHeldCandidate(t, l)

	if _, err := os.Stat(wholeCheckMarker); !os.IsNotExist(err) {
		raw, _ := os.ReadFile(wholeCheckMarker)
		t.Fatalf("whole-check fork-check.py ran instead of case-only replay: %q", string(raw))
	}

	var refusals []string
	fixtureRead(t, f, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? AND json_extract(payload, '$.item_id') = ?`, RefusalKind, "item-a")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var p struct {
				Step   string `json:"step"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				return err
			}
			refusals = append(refusals, p.Step+":"+p.Detail)
		}
		return rows.Err()
	})
	if len(refusals) != 1 || !strings.HasPrefix(refusals[0], "gate:") {
		t.Fatalf("refusals = %v (base %s), want one gate refusal recorded when short replay returned", refusals, shortSHA(base))
	}
}
