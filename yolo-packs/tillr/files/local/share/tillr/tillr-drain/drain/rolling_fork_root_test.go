package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// forkRootRecorder is a stub program that records what the loop handed it: the fork root in
// PI_FORKS_ROOT (or "<unset>") and its working directory, one per line, to a file the test
// reads back. It exits with code.
func forkRootRecorder(t *testing.T, code string) (program, record string) {
	t.Helper()
	record = filepath.Join(scratchUnderDurable(t), "seen")
	return executable(t, `printf '%s\n%s\n' "${PI_FORKS_ROOT-<unset>}" "$(pwd)" > `+record+"\nexit "+code), record
}

func readRecord(t *testing.T, path string) (forksRoot, cwd string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the stub program recorded nothing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("the stub's record is %q, want two lines", raw)
	}
	return lines[0], lines[1]
}

func committedRoot(t *testing.T) (root, tip string) {
	t.Helper()
	root = scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "base")
	return root, runGit(t, root, "rev-parse", "HEAD")
}

// The rolling program runs in a detached worktree, a linked worktree of the repository, and
// `bin/pi-forks` refuses to run from one unless it is told the repository that owns `.forks/`.
// The loop's own repository is that root, so the program is handed it, and an inherited value
// that names some other tree does not win.
func TestTheRollingProgramIsHandedTheSharedForkRoot(t *testing.T) {
	root, tip := committedRoot(t)
	t.Setenv("PI_FORKS_ROOT", "/inherited/elsewhere")
	program, record := forkRootRecorder(t, "0")
	laneRoot := filepath.Join(root, ".tillr", "lanes")
	steps := &ProcessSteps{Root: root, LaneRoot: laneRoot, RollingProgram: program}
	if _, err := steps.Rolling(context.Background(), "", tip); err != nil {
		t.Fatalf("the rolling run failed: %v", err)
	}
	forksRoot, cwd := readRecord(t, record)
	if forksRoot != root {
		t.Fatalf("the rolling program saw PI_FORKS_ROOT=%q, want the repository root %q", forksRoot, root)
	}
	if want := filepath.Join(laneRoot, ".rolling", "worktree"); cwd != want {
		t.Fatalf("the rolling program ran in %q, want the detached worktree %q", cwd, want)
	}
}

// The replay program is the same fork-check in a scratch worktree, so it needs the same root.
func TestTheReplayProgramIsHandedTheSharedForkRoot(t *testing.T) {
	root, tip := committedRoot(t)
	program, record := forkRootRecorder(t, "0")
	steps := &ProcessSteps{Root: root, LaneRoot: filepath.Join(root, ".tillr", "lanes"), ReplayProgram: program + " {only}"}
	out, err := steps.Replay(context.Background(), Candidate{}, tip, []string{"unit"})
	if err != nil || !out.Known {
		t.Fatalf("the replay answered %+v, %v", out, err)
	}
	if forksRoot, _ := readRecord(t, record); forksRoot != root {
		t.Fatalf("the replay program saw PI_FORKS_ROOT=%q, want %q", forksRoot, root)
	}
}

// Named checks run in the candidate lane but can need the shared fork root.
func TestTheFixerGateProgramIsHandedTheSharedForkRoot(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	program, record := forkRootRecorder(t, "0")
	worktree := t.TempDir()
	steps := &ProcessSteps{Root: f.root, Store: f.storePath, FixerGateProgram: program, Now: time.Now}
	answer, output := steps.fixerCheck(context.Background(), Candidate{ItemID: TrunkFixerID("0123456789abcdef"), RunID: "run-1", Worktree: worktree}, "check")
	if answer != "pass" {
		t.Fatalf("the fixer check %s: %s", answer, output)
	}
	forksRoot, cwd := readRecord(t, record)
	if forksRoot != f.root {
		t.Fatalf("the fixer gate saw PI_FORKS_ROOT=%q, want %q", forksRoot, f.root)
	}
	if cwd != worktree {
		t.Fatalf("the fixer gate ran in %q, want the candidate worktree %q", cwd, worktree)
	}
}

// A program that could not judge says why on stderr, and the outcome carries that tail for
// every exit code instead of leaving the loop's line to end in an empty sentence.
func TestARollingRunThatCouldNotJudgeCarriesItsStderrTail(t *testing.T) {
	for _, code := range []string{"1", "2", "3"} {
		t.Run("exit-"+code, func(t *testing.T) {
			root, tip := committedRoot(t)
			steps := &ProcessSteps{
				Root: root, LaneRoot: filepath.Join(root, ".tillr", "lanes"),
				RollingProgram: executable(t, "echo 'pi-forks: refusing to run from a linked worktree' >&2\nexit "+code),
			}
			out, err := steps.Rolling(context.Background(), "", tip)
			if err != nil {
				t.Fatalf("the rolling run failed: %v", err)
			}
			if out.Known || !strings.Contains(out.Detail, "refusing to run from a linked worktree") {
				t.Fatalf("the rolling run answered %+v, want an unknown run whose detail carries the stderr", out)
			}
		})
	}
}

// The first-ever run has no watermark, and the line says so rather than printing an empty value.
// Runs that never judge are counted, so a tier that can never judge is visible as a streak
// instead of one forgettable line each time.
func TestTheCouldNotJudgeLineSaysWhenThereIsNoWatermarkAndCountsTheStreak(t *testing.T) {
	f, l := passFixture(t, "item-a")
	tip := runGit(t, f.root, "rev-parse", "HEAD")
	var report bytes.Buffer
	l.cfg.Report = &report
	l.cfg.Steps = &ProcessSteps{
		Root: f.root, LaneRoot: filepath.Join(f.root, ".tillr", "lanes"), Store: f.storePath,
		RollingProgram: executable(t, "echo 'no reason to judge' >&2\nexit 2"),
		Now:            time.Now,
	}
	var result LoopResult
	if err := l.maybeRolling(context.Background(), tip, true, &result); err != nil {
		t.Fatalf("the rolling run failed: %v", err)
	}
	line := report.String()
	if !strings.Contains(line, "no watermark yet") || strings.Contains(line, "stays at ;") || strings.Contains(line, "stays at .") {
		t.Fatalf("the first run's line does not say there is no watermark yet:\n%s", line)
	}
	if !strings.Contains(line, "could not judge (exit 2)") || !strings.Contains(line, "no reason to judge") {
		t.Fatalf("the line lost the exit code or the reason:\n%s", line)
	}
	if !strings.Contains(line, "1 consecutive") {
		t.Fatalf("the first unjudged run is not counted:\n%s", line)
	}

	// An ordinary (not forced) run past the cadence: the streak is read from the log, not held
	// in memory, so the loop's second attempt counts the first.
	l.cfg.Now = func() time.Time { return time.Now().Add(2 * RollingEvery) }
	report.Reset()
	if err := l.maybeRolling(context.Background(), tip, false, &result); err != nil {
		t.Fatalf("the second rolling run failed: %v", err)
	}
	if !strings.Contains(report.String(), "2 consecutive") {
		t.Fatalf("the second unjudged run is not counted as the second in a row:\n%s", report.String())
	}
}

// A land or verdict program that fails says why; the error the loop stops with quotes the
// stderr tail the span recorded, because "answered exit 4" alone sent an operator to the logs.
func TestLandAndVerdictFailuresCarryTheStderrTail(t *testing.T) {
	root := t.TempDir()
	program := executable(t, "echo 'the reason it failed' >&2\nexit 4")
	steps := &ProcessSteps{Root: root, LandProgram: program, VerdictProgram: program, Store: filepath.Join(root, "s.db")}
	if _, err := steps.Land(context.Background(), Candidate{ItemID: "item_041", RunID: "run-1"}); err == nil || !strings.Contains(err.Error(), "the reason it failed") {
		t.Fatalf("the land error is %v, want the stderr tail", err)
	}
	if err := steps.Verdict(context.Background(), Candidate{ItemID: "item_041", RunID: "run-1"}, "pass"); err == nil || !strings.Contains(err.Error(), "the reason it failed") {
		t.Fatalf("the verdict error is %v, want the stderr tail", err)
	}
}
