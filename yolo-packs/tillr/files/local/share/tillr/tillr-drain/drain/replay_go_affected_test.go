package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
