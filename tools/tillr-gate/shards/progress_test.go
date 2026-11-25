package shards

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestSharedCacheDirPrefersGoCacheThenXDGThenHome(t *testing.T) {
	root := requireTempDir(t)
	custom := filepath.Join(root, "custom-gocache")
	xdg := filepath.Join(root, "xdg-cache")
	home := filepath.Join(root, "home")

	got1 := SharedCacheDir(map[string]string{
		"GOCACHE":        custom,
		"XDG_CACHE_HOME": xdg,
		"HOME":           home,
	})
	want1, _ := filepath.Abs(custom)
	if got1 != want1 {
		t.Fatalf("got %q, want %q", got1, want1)
	}

	got2 := SharedCacheDir(map[string]string{
		"GOCACHE":        "off",
		"XDG_CACHE_HOME": xdg,
		"HOME":           home,
	})
	want2, _ := filepath.Abs(filepath.Join(xdg, "go-build"))
	if got2 != want2 {
		t.Fatalf("got %q, want %q", got2, want2)
	}

	t.Setenv("HOME", home)
	got3 := SharedCacheDir(map[string]string{"HOME": home})
	want3, _ := filepath.Abs(filepath.Join(home, ".cache", "go-build"))
	if got3 != want3 {
		t.Fatalf("got %q, want %q", got3, want3)
	}
}

func TestProgressIntervalValidationRejectsNonPositiveAndNonFinite(t *testing.T) {
	root := requireTempDir(t)
	record := filepath.Join(root, "durations.json")
	if err := os.WriteFile(record, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"0", "-1", "-0.5", "nan", "inf", "-inf"} {
		t.Run("interval="+bad, func(t *testing.T) {
			code, _, stderr := runCLIInSubprocess(t, []string{
				root, "--durations", record, "--progress-interval=" + bad,
			}, nil, "")
			if code != 2 {
				t.Fatalf("expected exit 2 for %q, got %d (stderr=%s)", bad, code, stderr)
			}
			if !strings.Contains(stderr, "progress interval") {
				t.Fatalf("expected 'progress interval' in stderr, got %q", stderr)
			}
		})
	}
}

func TestProgressEnvOneHopAndFlagPrecedence(t *testing.T) {
	root := requireTempDir(t)
	record := filepath.Join(root, "durations.json")
	if err := os.WriteFile(record, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(root, "envmod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module envmod\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package envmod
import ("os"; "testing"; "time")
func TestEnvPopped(t *testing.T) {
	if v, ok := os.LookupEnv("TILLR_GO_SHARDS_PROGRESS_OUT"); ok {
		t.Fatalf("child inherited TILLR_GO_SHARDS_PROGRESS_OUT=%q", v)
	}
	time.Sleep(30 * time.Millisecond)
}
`
	if err := os.WriteFile(filepath.Join(mod, "env_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	envTarget := filepath.Join(root, "env-progress.json")
	flagTarget := filepath.Join(root, "flag-progress.json")

	// 1. TILLR_GO_SHARDS_PROGRESS_OUT is popped before Run() and used when --progress-file is absent.
	t.Setenv(ProgressOutEnv, envTarget)
	if code := RunCLI([]string{mod, "--durations", record, "--progress-interval", "0.01"}); code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if _, ok := os.LookupEnv(ProgressOutEnv); ok {
		t.Fatalf("expected %s to be unset after RunCLI", ProgressOutEnv)
	}
	if _, err := os.Stat(envTarget); err != nil {
		t.Fatalf("expected %s to be written: %v", envTarget, err)
	}

	// 2. --progress-file takes precedence over TILLR_GO_SHARDS_PROGRESS_OUT, which is still popped.
	_ = os.Remove(envTarget)
	t.Setenv(ProgressOutEnv, envTarget)
	if code := RunCLI([]string{mod, "--durations", record, "--progress-file", flagTarget, "--progress-interval", "0.01"}); code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if _, ok := os.LookupEnv(ProgressOutEnv); ok {
		t.Fatalf("expected %s to be unset after RunCLI", ProgressOutEnv)
	}
	if _, err := os.Stat(flagTarget); err != nil {
		t.Fatalf("expected %s to be written: %v", flagTarget, err)
	}
	if _, err := os.Stat(envTarget); !os.IsNotExist(err) {
		t.Fatalf("expected %s not to be written when --progress-file is set", envTarget)
	}

	// 3. Subprocess child never inherits TILLR_GO_SHARDS_PROGRESS_OUT.
	code, stdout, stderr := runCLIInSubprocess(t, []string{mod, "--durations", record}, map[string]string{
		ProgressOutEnv: envTarget,
	}, "")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
}

type progressSnap struct {
	Seq               int    `json:"seq"`
	CompletedUnits    int    `json:"completed_units"`
	CompletedPackages int    `json:"completed_packages"`
	CompletedTests    int    `json:"completed_tests"`
	CompletedShards   int    `json:"completed_shards"`
	LastDetail        string `json:"last_detail"`
}

func TestProgressSnapshotsAdvanceOnPackageTestAndShardCompletions(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "progmod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module progmod\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package progmod
import ("testing"; "time")
func TestFirst(t *testing.T) {
	t.Run("sub1", func(t *testing.T) { time.Sleep(40 * time.Millisecond) })
	t.Run("sub2", func(t *testing.T) { time.Sleep(40 * time.Millisecond) })
}
func TestSecond(t *testing.T) {
	time.Sleep(80 * time.Millisecond)
}
`
	if err := os.WriteFile(filepath.Join(mod, "prog_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	progressFile := filepath.Join(root, "nested", "progress.json")
	var snapMu sync.Mutex
	var snapshots []progressSnap
	lastSeq := 0

	events, err := Run(mod, 1, map[string]map[string]float64{}, filepath.Join(root, "scratch"), "0", RunOptions{
		ProgressFile:     progressFile,
		ProgressInterval: 0.05,
		PollInterval:     10 * time.Millisecond,
		onMaybePublish: func(p *ProgressTracker) {
			p.maybePublish()
			raw, rerr := os.ReadFile(progressFile)
			if rerr != nil {
				return
			}
			var snap progressSnap
			if json.Unmarshal(raw, &snap) == nil {
				snapMu.Lock()
				if snap.Seq > lastSeq {
					lastSeq = snap.Seq
					snapshots = append(snapshots, snap)
				}
				snapMu.Unlock()
			}
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	passed := 0
	for _, ev := range events {
		if ev.Action == "pass" && (ev.Test == "TestFirst" || ev.Test == "TestSecond") {
			passed++
		}
	}
	if passed != 2 {
		t.Fatalf("expected 2 passed tests, got %d", passed)
	}
	if len(snapshots) < 2 {
		t.Fatalf("expected >= 2 snapshots, got %+v", snapshots)
	}
	for i, snap := range snapshots {
		if snap.Seq != i+1 {
			t.Fatalf("snapshot %d seq=%d, want %d", i, snap.Seq, i+1)
		}
		if snap.CompletedUnits != snap.CompletedPackages+snap.CompletedTests+snap.CompletedShards {
			t.Fatalf("snapshot %+v units mismatch", snap)
		}
		if snap.LastDetail == "" {
			t.Fatalf("snapshot %+v empty last_detail", snap)
		}
	}
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i].CompletedUnits <= snapshots[i-1].CompletedUnits {
			t.Fatalf("completed_units did not advance: %+v -> %+v", snapshots[i-1], snapshots[i])
		}
	}
	last := snapshots[len(snapshots)-1]
	if last.CompletedTests > 2 {
		t.Fatalf("subtests counted toward completed_tests: %d", last.CompletedTests)
	}
	if last.CompletedPackages != 1 {
		t.Fatalf("expected completed_packages=1, got %d", last.CompletedPackages)
	}
}

func setupFakeSingleTestGo(t *testing.T, root, pkgName, testName, binary string) {
	t.Helper()
	fakeBin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	realGo := ""
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		cand := filepath.Join(entry, "go")
		if isExecutableFile(cand) {
			realGo = cand
			break
		}
	}
	if realGo == "" {
		t.Fatal("could not find real go")
	}
	writeFakeScript(t, fakeBin, "go", "#!/bin/sh", fmt.Sprintf(`
if [ "$1" = "list" ]; then
  printf '{"ImportPath":%q,"Dir":%q,"TestGoFiles":["x_test.go"]}\n'
  exit 0
fi
if [ "$1" = "test" ] && [ "$2" = "-c" ]; then
  mkdir -p "$(dirname "$4")"
  cp %q "$4"
  chmod +x "$4"
  exit 0
fi
if [ "$1" = "tool" ] && [ "$2" = "test2json" ]; then
  exec %q "$@"
fi
exit 1
`, pkgName, root, binary, realGo))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-gitconfig"))
}

func TestProgressWritesZeroSnapshotsWhenFasterThanInterval(t *testing.T) {
	root := requireTempDir(t)
	binary := writeFakeScript(t, root, "fake-fast", "#!/usr/bin/env python3", `
import sys
if any(a.startswith('-test.list=') for a in sys.argv[1:]):
    print('TestFast')
    sys.exit(0)
print('=== RUN   TestFast', flush=True)
print('--- PASS: TestFast (0.00s)', flush=True)
print('PASS', flush=True)
`)
	setupFakeSingleTestGo(t, root, "fastpkg", "TestFast", binary)

	progressFile := filepath.Join(root, "fast-progress.json")
	events, err := Run(root, 1, map[string]map[string]float64{}, filepath.Join(root, "scratch"), "0", RunOptions{
		ProgressFile:     progressFile,
		ProgressInterval: 60.0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Test == "TestFast" && ev.Action == "pass" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TestFast pass event")
	}
	if _, serr := os.Stat(progressFile); !os.IsNotExist(serr) {
		t.Fatalf("expected zero snapshots when faster than interval, stat err=%v", serr)
	}
}

func TestProgressWritesZeroSnapshotsWhenStalledInRunningTest(t *testing.T) {
	root := requireTempDir(t)
	var clockMu sync.Mutex
	cur := time.Unix(1000, 0)
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return cur
	}
	advanceClock := func(d time.Duration) {
		clockMu.Lock()
		cur = cur.Add(d)
		clockMu.Unlock()
	}

	makeFIFO := func(name string) string {
		fifo := filepath.Join(root, name)
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		fd, err := syscall.Open(fifo, syscall.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Close(fd) })
		return fifo
	}

	driveStalled := func(scratchDir, stalledTest string, expectedUnits int, intervalSec, timeoutSec float64) (func(*ProgressTracker), *int) {
		stage := 0
		deadline := time.Now().Add(hangGuard)
		intervalDur := time.Duration(intervalSec * float64(time.Second))
		timeoutDur := time.Duration(timeoutSec * float64(time.Second))
		hook := func(p *ProgressTracker) {
			if time.Now().After(deadline) {
				t.Fatalf("liveness guard expired waiting for %s in %s", stalledTest, scratchDir)
			}
			switch stage {
			case 0:
				p.maybePublish()
				p.mu.Lock()
				units := p.completedUnits
				path := p.path
				p.mu.Unlock()
				if units >= expectedUnits && scratchHasEvent(scratchDir, stalledTest, "run") {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("expected no progress file yet at stage 0")
					}
					advanceClock(2 * intervalDur)
					p.maybePublish()
					if expectedUnits > 0 {
						raw, err := os.ReadFile(path)
						if err != nil {
							t.Fatalf("expected progress file after interval: %v", err)
						}
						var snap progressSnap
						if err := json.Unmarshal(raw, &snap); err != nil || snap.Seq != 1 {
							t.Fatalf("expected seq=1, got %s (err=%v)", string(raw), err)
						}
					} else if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("expected no progress file when expectedUnits=0")
					}
					stage = 1
				}
			case 1:
				advanceClock(3 * intervalDur)
				p.maybePublish()
				p.mu.Lock()
				path := p.path
				p.mu.Unlock()
				if expectedUnits > 0 {
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var snap progressSnap
					if err := json.Unmarshal(raw, &snap); err != nil || snap.Seq != 1 {
						t.Fatalf("expected seq still 1, got %s", string(raw))
					}
				} else if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("expected no progress file at stage 1 when expectedUnits=0")
				}
				advanceClock(2 * timeoutDur)
				stage = 2
			default:
				p.maybePublish()
			}
		}
		return hook, &stage
	}

	pollInterval := 0.005
	watchdogTimeout := 0.25

	// 1. Mid-run hang stops advancing seq after TestPassFirst.
	hangFIFO := makeFIFO("hang.fifo")
	binaryHang := writeFakeScript(t, root, "hang.sh", "#!/bin/sh", fmt.Sprintf(
		"printf \"=== RUN   TestPassFirst\\n--- PASS: TestPassFirst (0.00s)\\n=== RUN   TestHangForever\\n\"\n"+
			"read _ < %q\n", hangFIFO))
	hangProgress := filepath.Join(root, "hang-progress.json")
	hangScratch := filepath.Join(root, "hang-scratch")
	hangTracker := NewProgressTracker(hangProgress, pollInterval, now)
	hangHook, hangStage := driveStalled(hangScratch, "TestHangForever", 1, pollInterval, watchdogTimeout)
	hangTracker.onMaybePublish = hangHook

	_, err := RunShard("hangpkg", root, binaryHang, []string{"TestPassFirst", "TestHangForever"}, 0, hangScratch, "0", nil, RunOptions{
		ShardTimeout: watchdogTimeout,
		Now:          now,
		PollInterval: 5 * time.Millisecond,
	}, hangTracker)
	if *hangStage != 2 {
		t.Fatalf("expected stage 2, got %d (err=%v)", *hangStage, err)
	}
	var se1 *ShardError
	if !errors.As(err, &se1) || !se1.TimedOut || !strings.Contains(se1.Message, "running: TestHangForever") {
		t.Fatalf("unexpected error: %v", err)
	}
	rawHang, rerr := os.ReadFile(hangProgress)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var snap1 progressSnap
	if err := json.Unmarshal(rawHang, &snap1); err != nil || snap1.Seq != 1 || snap1.CompletedTests != 1 {
		t.Fatalf("unexpected hang snap: %+v", snap1)
	}

	// 2. Pure Action == 'run' hang from start writes zero snapshots.
	pureFIFO := makeFIFO("pure.fifo")
	binaryPureHang := writeFakeScript(t, root, "pure-hang.sh", "#!/bin/sh", fmt.Sprintf(
		"for a in \"$@\"; do case \"$a\" in -test.list=*) echo TestHangOnly; exit 0;; esac; done\n"+
			"printf \"=== RUN   TestHangOnly\\n\"\n"+
			"read _ < %q\n", pureFIFO))
	pureShardProgress := filepath.Join(root, "pure-shard-progress.json")
	pureShardScratch := filepath.Join(root, "pure-shard-scratch")
	pureShardTracker := NewProgressTracker(pureShardProgress, pollInterval, now)
	pureHook, pureStage := driveStalled(pureShardScratch, "TestHangOnly", 0, pollInterval, watchdogTimeout)
	pureShardTracker.onMaybePublish = pureHook

	_, err = RunShard("purehang", root, binaryPureHang, []string{"TestHangOnly"}, 0, pureShardScratch, "0", nil, RunOptions{
		ShardTimeout: watchdogTimeout,
		Now:          now,
		PollInterval: 5 * time.Millisecond,
	}, pureShardTracker)
	if *pureStage != 2 {
		t.Fatalf("expected stage 2, got %d (err=%v)", *pureStage, err)
	}
	var se2 *ShardError
	if !errors.As(err, &se2) || !se2.TimedOut || !strings.Contains(se2.Message, "running: TestHangOnly") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, serr := os.Stat(pureShardProgress); !os.IsNotExist(serr) {
		t.Fatalf("expected zero snapshots for pure hang shard, stat err=%v", serr)
	}

	// 3. Run() with pure hang writes only 1 snapshot (completed_packages=1, tests=0, shards=0).
	setupFakeSingleTestGo(t, root, "purehang", "TestHangOnly", binaryPureHang)
	pureHangProgress := filepath.Join(root, "pure-hang-progress.json")
	pureScratch := filepath.Join(root, "pure-scratch")
	runHook, runStage := driveStalled(pureScratch, "TestHangOnly", 1, pollInterval, watchdogTimeout)
	_, err = Run(root, 1, map[string]map[string]float64{}, pureScratch, "0", RunOptions{
		ShardTimeout:     watchdogTimeout,
		ProgressFile:     pureHangProgress,
		ProgressInterval: pollInterval,
		Now:              now,
		PollInterval:     5 * time.Millisecond,
		onMaybePublish:   runHook,
	})
	if *runStage != 2 {
		t.Fatalf("expected stage 2, got %d (err=%v)", *runStage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "running: TestHangOnly") {
		t.Fatalf("unexpected error: %v", err)
	}
	rawPure, rerr := os.ReadFile(pureHangProgress)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var snap3 progressSnap
	if err := json.Unmarshal(rawPure, &snap3); err != nil {
		t.Fatal(err)
	}
	if snap3.Seq != 1 || snap3.CompletedPackages != 1 || snap3.CompletedTests != 0 || snap3.CompletedShards != 0 {
		t.Fatalf("unexpected pure Run snap: %+v", snap3)
	}
}

func TestProgressWriteErrorWarnsWithoutFailingRun(t *testing.T) {
	root := requireTempDir(t)
	binary := writeFakeScript(t, root, "fake-slow", "#!/usr/bin/env python3", `
import sys, time
if any(a.startswith('-test.list=') for a in sys.argv[1:]):
    print('TestSlow')
    sys.exit(0)
print('=== RUN   TestSlow', flush=True)
time.sleep(0.08)
print('--- PASS: TestSlow (0.08s)', flush=True)
print('PASS', flush=True)
`)
	setupFakeSingleTestGo(t, root, "warnpkg", "TestSlow", binary)

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	// Passing root (an existing directory) as ProgressFile triggers an error on os.Rename.
	events, runErr := Run(root, 1, map[string]map[string]float64{}, filepath.Join(root, "warn-scratch"), "0", RunOptions{
		ProgressFile:     root,
		ProgressInterval: 0.02,
		PollInterval:     10 * time.Millisecond,
	})
	_ = w.Close()
	stderrBytes, _ := io.ReadAll(r)
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("expected nil error when progress write fails, got: %v", runErr)
	}
	found := false
	for _, ev := range events {
		if ev.Test == "TestSlow" && ev.Action == "pass" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected TestSlow pass event")
	}
	if !strings.Contains(string(stderrBytes), "cannot write progress file") {
		t.Fatalf("expected warning in stderr, got %q", string(stderrBytes))
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(root), ".go-progress-*"))
	if len(leftovers) > 0 {
		t.Fatalf("leftover temp progress files: %v", leftovers)
	}
}
