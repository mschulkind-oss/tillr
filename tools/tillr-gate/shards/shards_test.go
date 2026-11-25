package shards

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const hangGuard = 5 * time.Minute

func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && os.Args[1] == "shards" {
		os.Exit(RunCLI(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == GuardFlag {
		os.Exit(RunCLI(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func requireTempDir(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("TMPDIR")
	if parent == "" {
		parent = os.Getenv("YOLO_DURABLE_DIR")
	}
	if parent == "" {
		t.Fatal("tests require TMPDIR or YOLO_DURABLE_DIR, never /tmp")
	}
	dir, err := os.MkdirTemp(parent, "shards-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func writeFakeScript(t *testing.T, dir, name, shebang, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(shebang+"\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func runCLIInSubprocess(t *testing.T, args []string, envOverride map[string]string, cwd string) (int, string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmdArgs := append([]string{"shards"}, args...)
	cmd := exec.Command(exe, cmdArgs...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	envMap := EnvSliceToMap(os.Environ())
	for k, v := range envOverride {
		if v == "" {
			delete(envMap, k)
		} else {
			envMap[k] = v
		}
	}
	cmd.Env = EnvMapToSlice(envMap)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			t.Fatalf("exec failed: %v", err)
		}
	}
	return code, stdout.String(), stderr.String()
}

func assertProcessGone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for _, pid := range pids {
		for {
			data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			if err != nil {
				break
			}
			s := string(data)
			idx := strings.LastIndex(s, ")")
			if idx >= 0 && idx+2 < len(s) {
				fields := strings.Fields(s[idx+1:])
				if len(fields) > 0 && fields[0] == "Z" {
					break
				}
			}
			if time.Now().After(deadline) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("shard process %d outlived the runner", pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestAssignBalancingAndFallback(t *testing.T) {
	names := []string{"TestSlow", "TestMed", "TestFast", "TestUnknown1", "TestUnknown2"}
	durations := map[string]float64{
		"TestSlow": 10.0,
		"TestMed":  5.0,
		"TestFast": 2.0,
	}
	groups := Assign(names, durations, 2)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	// Known sorted desc: TestSlow (10)->slot0, TestMed (5)->slot1, TestFast (2)->slot1 (load 7)
	// Mean of known = 17/3 = 5.666...
	// TestUnknown1 -> slot1 (load 7 < 10) -> load 12.666
	// TestUnknown2 -> slot0 (load 10 < 12.666) -> load 15.666
	want0 := []string{"TestSlow", "TestUnknown2"}
	want1 := []string{"TestMed", "TestFast", "TestUnknown1"}
	if !reflect.DeepEqual(groups[0], want0) || !reflect.DeepEqual(groups[1], want1) {
		t.Fatalf("unexpected assignment: got %v, want [%v, %v]", groups, want0, want1)
	}
}

func TestAssignOneWorker(t *testing.T) {
	names := []string{"TestB", "TestA", "TestUnknown"}
	durations := map[string]float64{"TestA": 1.0, "TestB": 2.0}
	groups := Assign(names, durations, 1)
	want := [][]string{{"TestB", "TestA", "TestUnknown"}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("got %v, want %v", groups, want)
	}
}

func TestParseArgsFlagsBeforeAndAfterModule(t *testing.T) {
	cfg1, err := parseArgs([]string{"-k", "4", "--cache", "tools/tillr", "--timeout", "30s"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg1.module != "tools/tillr" || cfg1.shards != 4 || !cfg1.cache || cfg1.timeout != "30s" {
		t.Fatalf("unexpected cfg1: %+v", cfg1)
	}

	cfg2, err := parseArgs([]string{"tools/tillr", "--shards=8", "--shard-timeout", "120.5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg2.module != "tools/tillr" || cfg2.shards != 8 || cfg2.shardTimeout != 120.5 || !cfg2.hasShardTimeout {
		t.Fatalf("unexpected cfg2: %+v", cfg2)
	}
}

func TestRunCLIRefusesMissingScratch(t *testing.T) {
	t.Setenv("TMPDIR", "")
	t.Setenv("YOLO_DURABLE_DIR", "")
	durPath := filepath.Join(t.TempDir(), "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := RunCLI([]string{"--durations", durPath, "tools/tillr"})
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
}

func TestRunCLIHelpExitsZero(t *testing.T) {
	code := RunCLI([]string{"--help"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestLoadDurationsValidation(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"pkg":{"TestA":1.5,"TestB":0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := LoadDurations(valid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d["pkg"]["TestA"] != 1.5 || d["pkg"]["TestB"] != 0 {
		t.Fatalf("unexpected durations: %v", d)
	}

	neg := filepath.Join(dir, "neg.json")
	if err := os.WriteFile(neg, []byte(`{"pkg":{"TestA":-1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDurations(neg); err == nil {
		t.Fatal("expected error for negative duration")
	}
}

func TestClassifyGroupCache(t *testing.T) {
	hit := []TestEvent{{Action: "output", Output: "ok  \tpkg\t(cached)\n"}, {Action: "pass"}}
	if got := ClassifyGroupCache(hit); got != "hit" {
		t.Fatalf("got %q, want hit", got)
	}
	miss := []TestEvent{{Action: "output", Output: "ok  \tpkg\t0.123s\n"}, {Action: "pass"}}
	if got := ClassifyGroupCache(miss); got != "miss" {
		t.Fatalf("got %q, want miss", got)
	}
	unk := []TestEvent{{Action: "output", Output: "something else\n"}}
	if got := ClassifyGroupCache(unk); got != "unknown" {
		t.Fatalf("got %q, want unknown", got)
	}
}

func TestScanPackageTests(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package pkg

import "testing"

func TestMain(m *testing.M) {}
func TestAlpha(t *testing.T) {}
func TestHelper(x int) {}
func FuzzBeta(f *testing.F) {}
func ExampleGamma() {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "a_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ScanPackageTests(dir, "pkg", "example.com/pkg", "pkg", nil)
	want := []string{"TestAlpha", "FuzzBeta", "ExampleGamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSharedCacheDirPrecedence(t *testing.T) {
	got := SharedCacheDir(map[string]string{
		"GOCACHE":        "/tmp/custom-gocache",
		"XDG_CACHE_HOME": "/tmp/custom-xdg",
	})
	if got != "/tmp/custom-gocache" {
		t.Fatalf("got %q, want /tmp/custom-gocache", got)
	}
	got2 := SharedCacheDir(map[string]string{
		"GOCACHE":        "off",
		"XDG_CACHE_HOME": "/tmp/custom-xdg",
	})
	if got2 != "/tmp/custom-xdg/go-build" {
		t.Fatalf("got %q, want /tmp/custom-xdg/go-build", got2)
	}
}

func TestProgressTrackerThrottling(t *testing.T) {
	dir := t.TempDir()
	progFile := filepath.Join(dir, "progress.json")
	cur := time.Unix(1000, 0)
	now := func() time.Time { return cur }

	tr := NewProgressTracker(progFile, 10.0, now)
	tr.RecordPackage("pkg1")
	if _, err := os.Stat(progFile); !os.IsNotExist(err) {
		t.Fatalf("expected no snapshot before interval, got err=%v", err)
	}

	cur = cur.Add(11 * time.Second)
	tr.RecordTest("pkg1 TestA pass")
	data, err := os.ReadFile(progFile)
	if err != nil {
		t.Fatalf("expected snapshot after interval: %v", err)
	}
	var snap struct {
		Seq            int    `json:"seq"`
		CompletedUnits int    `json:"completed_units"`
		LastDetail     string `json:"last_detail"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Seq != 1 || snap.CompletedUnits != 2 || snap.LastDetail != "pkg1 TestA pass" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

func TestCheckCachedWorkerRefusal(t *testing.T) {
	t.Setenv("TILLR_RUN_ID", "run-123")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	fixture := filepath.Join(tmp, "mod")
	if err := os.MkdirAll(fixture, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckCachedWorkerRefusal(fixture); err != nil {
		t.Fatalf("expected fixture under TMPDIR to be allowed, got: %v", err)
	}
	forksMod := filepath.Join(tmp, ".forks", "mod")
	if err := CheckCachedWorkerRefusal(forksMod); err == nil || !strings.Contains(err.Error(), ".forks/") {
		t.Fatalf("expected .forks/ refusal, got: %v", err)
	}
}

func TestComputeEdgeDigestReadTests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}
	edge := Edge{
		Reads:     []string{"a.txt", "b.txt"},
		ReadTests: map[string][]string{"b.txt": {"TestB"}},
	}
	dA := ComputeEdgeDigest(root, edge, nil, []string{"TestA"}, nil)
	dB := ComputeEdgeDigest(root, edge, nil, []string{"TestB"}, nil)
	if dA == dB {
		t.Fatal("expected different digests when TestB is active vs inactive")
	}
}

func TestRunCLIRefusesMissingScratchCode2AndMessage(t *testing.T) {
	durPath := filepath.Join(t.TempDir(), "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLIInSubprocess(t, []string{"--durations", durPath, "tools/tillr"}, map[string]string{
		"TMPDIR":           "",
		"YOLO_DURABLE_DIR": "",
	}, "")
	if code != 2 {
		t.Fatalf("expected exit 2, got %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "never defaults to /tmp") {
		t.Fatalf("expected missing scratch refusal message, got %q", stderr)
	}
}

func TestPackageEvidenceDistinguishesShardsFromGoResultCache(t *testing.T) {
	root := requireTempDir(t)
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeScript(t, fakeBin, "go", "#!/bin/sh", `
if [ "$1" = "list" ]; then
  printf '{"ImportPath":"demo/test","TestGoFiles":["x_test.go"]}\n{"ImportPath":"demo/library"}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-gitconfig"))

	target := filepath.Join(root, "packages.json")
	_, err := Run(root, 1, map[string]map[string]float64{}, root, "0", RunOptions{EvidenceOut: target})
	if err == nil {
		t.Fatal("expected error from failing go test -c")
	}
	raw, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var got []map[string]string
	if uerr := json.Unmarshal(raw, &got); uerr != nil {
		t.Fatal(uerr)
	}
	want := []map[string]string{
		{"import_path": "demo/test", "test_cache": "bypassed"},
		{"import_path": "demo/library", "test_cache": "not-applicable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFailedPackageListKeepsCompleteRecordsWithoutBuilding(t *testing.T) {
	root := requireTempDir(t)
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	compileMarker := filepath.Join(root, "compiled")
	writeFakeScript(t, fakeBin, "go", "#!/bin/sh", fmt.Sprintf(`
if [ "$1" = "list" ]; then
  printf '{"ImportPath":"demo/good","TestGoFiles":["x_test.go"]}\n{"ImportPath":"demo/broken"}\n{'
  exit 1
fi
touch %q
exit 0
`, compileMarker))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	target := filepath.Join(root, "packages.json")
	_, err := Run(root, 1, map[string]map[string]float64{}, root, "0", RunOptions{EvidenceOut: target})
	if err == nil {
		t.Fatal("expected error from failing go list")
	}
	if _, serr := os.Stat(compileMarker); !os.IsNotExist(serr) {
		t.Fatalf("expected compile not to be called, stat err=%v", serr)
	}
	raw, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var got struct {
		Discovery string              `json:"discovery"`
		Packages  []map[string]string `json:"packages"`
	}
	if uerr := json.Unmarshal(raw, &got); uerr != nil {
		t.Fatal(uerr)
	}
	want := []map[string]string{
		{"import_path": "demo/good", "test_cache": "not-started"},
		{"import_path": "demo/broken", "test_cache": "not-started"},
	}
	if got.Discovery != "partial" || !reflect.DeepEqual(got.Packages, want) {
		t.Fatalf("got %+v, want partial with %v", got, want)
	}
}

func TestPackageEvidenceWriteErrorDoesNotFailRun(t *testing.T) {
	root := requireTempDir(t)
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeScript(t, fakeBin, "go", "#!/bin/sh", `
if [ "$1" = "list" ]; then
  printf '{"ImportPath":"demo/library"}\n'
  exit 0
fi
exit 1
`)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-gitconfig"))

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	events, runErr := Run(root, 1, map[string]map[string]float64{}, root, "0", RunOptions{EvidenceOut: root})
	_ = w.Close()
	stderrBytes, _ := io.ReadAll(r)
	_ = r.Close()

	if runErr != nil {
		t.Fatalf("expected nil error when evidence write fails, got: %v", runErr)
	}
	if len(events) != 0 {
		t.Fatalf("expected empty events, got %v", events)
	}
	if !strings.Contains(string(stderrBytes), "cannot write package evidence") {
		t.Fatalf("expected warning in stderr, got %q", string(stderrBytes))
	}
}

func startHungRunnerSubprocess(t *testing.T, root string) (*exec.Cmd, []int) {
	t.Helper()
	pidsDir := filepath.Join(root, "pids")
	if err := os.MkdirAll(pidsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(pidsDir, "shard")
	binary := writeFakeScript(t, root, "fake-hung", "#!/usr/bin/env python3", fmt.Sprintf(`
import os, subprocess, sys, time, pathlib
if any(a.startswith('-test.list=') for a in sys.argv[1:]):
    print('TestHung')
    sys.exit(0)
grand = subprocess.Popen(['sleep', '1000'])
pathlib.Path(%q + '.part').write_text(f'{os.getpid()} {grand.pid}')
os.replace(%q + '.part', %q)
time.sleep(1000)
`, marker, marker, marker))

	mod := filepath.Join(root, "hungmod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "hung_test.go"), []byte("package fixture\nimport \"testing\"\nfunc TestHung(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Fake `go` that returns our hung binary for `test -c`, lists fixture, and delegates `tool test2json` to real `go`.
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeScript(t, fakeBin, "go", "#!/bin/sh", fmt.Sprintf(`
if [ "$1" = "list" ]; then
  printf '{"ImportPath":"fixture","Dir":%q,"TestGoFiles":["hung_test.go"]}\n'
  exit 0
fi
if [ "$1" = "test" ] && [ "$2" = "-c" ]; then
  cp %q "$4"
  chmod +x "$4"
  exit 0
fi
exec %q "$@"
`, mod, binary, realGo))

	durPath := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := exec.Command(exe, "shards", mod, "--durations", durPath, "--scratch", root)
	envMap := EnvSliceToMap(os.Environ())
	envMap["PATH"] = fakeBin + string(os.PathListSeparator) + envMap["PATH"]
	runner.Env = EnvMapToSlice(envMap)
	var stderrBuf bytes.Buffer
	runner.Stderr = &stderrBuf
	if err := runner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if runner.Process != nil {
			_ = runner.Process.Kill()
		}
		_ = runner.Wait()
	})

	deadline := time.Now().Add(hangGuard)
	for {
		if raw, rerr := os.ReadFile(marker); rerr == nil {
			fields := strings.Fields(string(raw))
			var pids []int
			for _, f := range fields {
				p, perr := strconv.Atoi(f)
				if perr != nil {
					t.Fatal(perr)
				}
				pids = append(pids, p)
			}
			return runner, pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for hung shard marker; stderr=%s", stderrBuf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestKilledRunnerLeavesNoShardProcessBehind(t *testing.T) {
	root := requireTempDir(t)
	runner, pids := startHungRunnerSubprocess(t, root)
	if err := runner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = runner.Wait()
	assertProcessGone(t, pids)
}

func TestTerminatedRunnerLeavesNoShardProcessBehind(t *testing.T) {
	root := requireTempDir(t)
	runner, pids := startHungRunnerSubprocess(t, root)
	if err := runner.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = runner.Wait()
	assertProcessGone(t, pids)
}

func TestFinishedShardLeavesNoDescendantBehind(t *testing.T) {
	root := requireTempDir(t)
	marker := filepath.Join(root, "grand")
	binary := writeFakeScript(t, root, "fake-leak", "#!/usr/bin/env python3", fmt.Sprintf(`
import subprocess, pathlib
grand = subprocess.Popen(['sleep', '1000'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
pathlib.Path(%q).write_text(str(grand.pid))
print('=== RUN   TestLeaks')
print('--- PASS: TestLeaks (0.00s)')
print('PASS')
`, marker))
	scratch := filepath.Join(root, "scratch")
	if _, err := RunShard("fixture", root, binary, []string{"TestLeaks"}, 0, scratch, "3600s", nil, RunOptions{}, nil); err != nil {
		t.Fatalf("unexpected RunShard error: %v", err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, []int{pid})
}

func TestScratchPreparationFailureNamesShard(t *testing.T) {
	root := requireTempDir(t)
	binary := writeFakeScript(t, root, "fake-ok", "#!/usr/bin/env python3", "print('PASS')\n")
	blocker := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := RunShard("fixture", root, binary, []string{"TestMissing"}, 0, filepath.Join(blocker, "scratch"), "3600s", nil, RunOptions{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "fixture shard 0") {
		t.Fatalf("expected shard name in error, got %q", err.Error())
	}
}

func TestFailureNamesShard(t *testing.T) {
	root := requireTempDir(t)
	binary := writeFakeScript(t, root, "fake-fail", "#!/usr/bin/env python3",
		"print('=== RUN   TestFail'); print('--- FAIL: TestFail (0.00s)'); print('FAIL'); raise SystemExit(1)\n")
	_, err := RunShard("fixture", root, binary, []string{"TestFail"}, 0, filepath.Join(root, "scratch"), "3600s", nil, RunOptions{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !regexp.MustCompile(`fixture shard 0.*failed`).MatchString(err.Error()) {
		t.Fatalf("unexpected error: %q", err.Error())
	}
}

func TestFailureRetainsGoTimeoutStackAfterOrdinaryFailure(t *testing.T) {
	root := requireTempDir(t)
	binary := writeFakeScript(t, root, "fake-timeout-stack", "#!/usr/bin/env python3",
		"print('\\x16=== RUN   TestFail'); print('\\x16--- FAIL: TestFail (0.00s)')\n"+
			"print('\\x16=== RUN   TestHung')\n"+
			"print('panic: test timed out after 10m0s\\nrunning tests:\\n\\tTestHung (10m0s)\\n\\n'\n"+
			"      'goroutine 1 [running]:')\n"+
			"print('\\x16=== NAME  ')\n"+
			"raise SystemExit(2)\n")
	_, err := RunShard("fixture", root, binary, []string{"TestFail", "TestHung"}, 0, filepath.Join(root, "scratch"), "0", nil, RunOptions{}, nil)
	if err == nil {
		t.Fatal("expected ShardError")
	}
	if !strings.Contains(err.Error(), "failed (exit 1): TestFail") {
		t.Fatalf("expected failed (exit 1): TestFail, got %q", err.Error())
	}
	wantStack := "panic: test timed out after 10m0s\nrunning tests:\n\tTestHung (10m0s)\n\ngoroutine 1 [running]:"
	if !strings.Contains(err.Error(), wantStack) {
		t.Fatalf("expected timeout stack retained, got %q", err.Error())
	}
}

func TestCannotStartNamesShard(t *testing.T) {
	root := requireTempDir(t)
	_, err := RunShard("fixture", root, filepath.Join(root, "missing"), []string{"TestMissing"}, 0, filepath.Join(root, "scratch"), "3600s", nil, RunOptions{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !regexp.MustCompile(`fixture shard 0.*cannot start`).MatchString(err.Error()) {
		t.Fatalf("unexpected error: %q", err.Error())
	}
}

func TestMissingCompletionNamesShard(t *testing.T) {
	root := requireTempDir(t)
	binary := writeFakeScript(t, root, "fake-missing", "#!/usr/bin/env python3", "print('PASS')\n")
	_, err := RunShard("fixture", root, binary, []string{"TestMissing"}, 0, filepath.Join(root, "scratch"), "3600s", nil, RunOptions{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !regexp.MustCompile(`fixture shard 0.*TestMissing`).MatchString(err.Error()) {
		t.Fatalf("unexpected error: %q", err.Error())
	}
}

func TestPackagesRunTheirShardsSideBySide(t *testing.T) {
	root := requireTempDir(t)
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	compileCountFile := filepath.Join(root, "compiles")
	testListBin := writeFakeScript(t, root, "fake-list-bin", "#!/bin/sh", `printf 'TestA\nTestB\n'`)
	writeFakeScript(t, fakeBin, "go", "#!/bin/sh", fmt.Sprintf(`
if [ "$1" = "list" ]; then
  printf '{"ImportPath":"one","Dir":%q,"TestGoFiles":["x_test.go"]}\n'
  printf '{"ImportPath":"two","Dir":%q,"TestGoFiles":["x_test.go"]}\n'
  exit 0
fi
if [ "$1" = "test" ] && [ "$2" = "-c" ]; then
  printf 'x\n' >> %q
  cp %q "$4"
  chmod +x "$4"
  exit 0
fi
exit 1
`, root, root, compileCountFile, testListBin))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-gitconfig"))

	var mu sync.Mutex
	arrived := 0
	allArrived := make(chan struct{})
	durations := map[string]map[string]float64{
		"one": {"TestA": 1, "TestB": 1},
		"two": {"TestA": 1, "TestB": 1},
	}
	events, err := RunUncached(root, 2, durations, root, "3600s", RunOptions{
		runShardFn: func(pkg, cwd, binary string, names []string, index int, scratch, timeout string, gitValues [][2]string, opts RunOptions, progress *ProgressTracker) ([]TestEvent, error) {
			mu.Lock()
			arrived++
			if arrived == 4 {
				close(allArrived)
			}
			mu.Unlock()
			select {
			case <-allArrived:
			case <-time.After(hangGuard):
				return nil, fmt.Errorf("barrier timeout")
			}
			return []TestEvent{{Package: pkg, Test: names[0], Action: "pass"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	compiles, err := os.ReadFile(compileCountFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(compiles)), "\n"); len(lines) != 2 {
		t.Fatalf("expected 2 compiles (one per package), got %d", len(lines))
	}
}

func TestBalancingReservesFallback(t *testing.T) {
	groups := Assign([]string{"TestSlow", "TestFast", "TestNew"}, map[string]float64{"TestSlow": 10, "TestFast": 1}, 3)
	if !reflect.DeepEqual(groups[len(groups)-1], []string{"TestNew"}) {
		t.Fatalf("expected last group [TestNew], got %v", groups)
	}
	var flat []string
	for _, g := range groups {
		flat = append(flat, g...)
	}
	sort.Strings(flat)
	want := []string{"TestFast", "TestNew", "TestSlow"}
	if !reflect.DeepEqual(flat, want) {
		t.Fatalf("got %v, want %v", flat, want)
	}
}

func TestPackageWithoutTestNamesStillRunsTestMain(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "module")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package fixture
import ("testing"; "flag"; "os")
func BenchmarkOnly(b *testing.B) {}
func TestMain(m *testing.M) {
	flag.Parse()
	if flag.Lookup("test.list").Value.String() != "" { os.Exit(m.Run()) }
	os.Exit(1)
}
`
	if err := os.WriteFile(filepath.Join(mod, "fixture_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	durPath := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLIInSubprocess(t, []string{mod, "--durations", durPath}, nil, "")
	if code == 0 {
		t.Fatalf("expected non-zero exit when TestMain fails")
	}
	if !regexp.MustCompile(`fixture shard 0.*failed`).MatchString(stderr) {
		t.Fatalf("expected fixture shard 0 failed in stderr, got %q", stderr)
	}
}

func TestFailedRunStillWritesEvents(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "module")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package fixture
import "testing"
func TestPass(t *testing.T) {}
func TestFail(t *testing.T) { t.Fatal("boom") }
`
	if err := os.WriteFile(filepath.Join(mod, "fixture_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	durPath := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(root, "events.jsonl")
	code, _, _ := runCLIInSubprocess(t, []string{mod, "--durations", durPath, "--events", eventsPath}, nil, "")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	raw, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("failed run lost its events: %v", err)
	}
	passed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var ev TestEvent
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Action == "pass" && ev.Test != "" {
			passed[ev.Test] = true
		}
	}
	if !passed["TestPass"] {
		t.Fatalf("expected TestPass in events.jsonl, got %v", passed)
	}
}

func TestCLIHasNoNestedWallDeadline(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "module")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package fixture
import ("flag"; "testing")
func TestTimeoutZero(t *testing.T) {
	if got := flag.Lookup("test.timeout").Value.String(); got != "0s" {
		t.Fatalf("expected test.timeout=0s, got %q", got)
	}
}
`
	if err := os.WriteFile(filepath.Join(mod, "fixture_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	durPath := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLIInSubprocess(t, []string{mod, "--durations", durPath}, nil, "")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
}

func TestDrainNameParity(t *testing.T) {
	drainMod := filepath.Join("..", "..", "tillr-drain")
	if _, err := os.Stat(filepath.Join(drainMod, "go.mod")); os.IsNotExist(err) {
		t.Skip("tillr-drain not present")
	}
	drainAbs, err := filepath.Abs(drainMod)
	if err != nil {
		t.Fatal(err)
	}
	listCmd := exec.Command("go", "test", "-json", "-list", ".", "./...")
	listCmd.Dir = drainAbs
	listOut, err := listCmd.Output()
	if err != nil {
		t.Fatalf("go test -json -list failed: %v", err)
	}
	native := map[[2]string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(listOut)), "\n") {
		if line == "" {
			continue
		}
		var ev TestEvent
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Action == "output" {
			name := strings.TrimSpace(ev.Output)
			if testListNameRe.MatchString(name) {
				native[[2]string{ev.Package, name}] = true
			}
		}
	}
	if len(native) == 0 {
		t.Fatal("Go listed no drain tests")
	}

	root := requireTempDir(t)
	durPath := filepath.Join("..", "..", "go-test-durations.json")
	durations, err := LoadDurations(durPath)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var planned [][2]string
	_, err = RunUncached(drainAbs, 6, durations, root, "0", RunOptions{
		runShardFn: func(pkg, cwd, binary string, names []string, index int, scratch, timeout string, gitValues [][2]string, opts RunOptions, progress *ProgressTracker) ([]TestEvent, error) {
			mu.Lock()
			for _, n := range names {
				planned = append(planned, [2]string{pkg, n})
			}
			mu.Unlock()
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("RunUncached planning failed: %v", err)
	}
	plannedSet := map[[2]string]bool{}
	for _, p := range planned {
		plannedSet[p] = true
	}
	if len(planned) != len(plannedSet) {
		t.Fatalf("duplicate tests planned: total=%d unique=%d", len(planned), len(plannedSet))
	}
	if !reflect.DeepEqual(plannedSet, native) {
		t.Fatalf("planned tests differ from native go test -list: planned=%d native=%d", len(plannedSet), len(native))
	}
}

func TestShardRunnerBalancesUnrecordedTests(t *testing.T) {
	for _, known := range [][]string{
		nil,
		{"TestKnown0", "TestKnown1", "TestKnown2", "TestKnown3", "TestKnown4", "TestKnown5"},
	} {
		var unknown []string
		for i := 0; i < 120; i++ {
			unknown = append(unknown, fmt.Sprintf("TestNew%d", i))
		}
		names := append(append([]string(nil), known...), unknown...)
		durs := make(map[string]float64, len(known))
		for _, k := range known {
			durs[k] = 1
		}
		groups := Assign(names, durs, 6)
		var flat []string
		for _, g := range groups {
			flat = append(flat, g...)
			if len(g) < 18 || len(g) > 22 {
				t.Fatalf("unbalanced group size %d in %v", len(g), groups)
			}
		}
		sort.Strings(flat)
		sortedNames := append([]string(nil), names...)
		sort.Strings(sortedNames)
		if !reflect.DeepEqual(flat, sortedNames) {
			t.Fatalf("assigned tests mismatch: got %d, want %d", len(flat), len(sortedNames))
		}
	}
}

func TestShardRunnerFailureNamesFailingTestsAndOmitsPassingOutput(t *testing.T) {
	root := requireTempDir(t)
	var lines []string
	lines = append(lines,
		"print('=== RUN   TestAlphaFail')",
		"print('    alpha_test.go:12: broken invariant')",
		"print('--- FAIL: TestAlphaFail (0.01s)')",
	)
	names := []string{"TestAlphaFail"}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("TestPass%d", i)
		names = append(names, name)
		lines = append(lines,
			fmt.Sprintf("print('=== RUN   %s')", name),
			fmt.Sprintf("print('--- PASS: %s (0.01s)')", name),
		)
	}
	lines = append(lines, "print('FAIL')", "raise SystemExit(1)")
	binary := writeFakeScript(t, root, "fake.test", "#!/usr/bin/env python3", strings.Join(lines, "\n")+"\n")

	_, err := RunShard("example.com/pkg", root, binary, names, 2, filepath.Join(root, "scratch"), "0", nil, RunOptions{}, nil)
	if err == nil {
		t.Fatal("expected ShardError")
	}
	var se *ShardError
	if !errors.As(err, &se) {
		t.Fatalf("expected *ShardError, got %T: %v", err, err)
	}
	firstLine := strings.SplitN(se.Message, "\n", 2)[0]
	wantFirst := "example.com/pkg shard 2 failed (exit 1): TestAlphaFail"
	if firstLine != wantFirst {
		t.Fatalf("bad first line: got %q, want %q", firstLine, wantFirst)
	}
	if !strings.Contains(se.Message, "alpha_test.go:12: broken invariant") {
		t.Fatalf("missing failure output in %q", se.Message)
	}
	if strings.Contains(se.Message, "TestPass0") || strings.Contains(se.Message, "--- PASS:") {
		t.Fatalf("passing output leaked into %q", se.Message)
	}
	passed := map[string]bool{}
	for _, ev := range se.Events {
		if ev.Action == "pass" && ev.Test != "" {
			passed[ev.Test] = true
		}
	}
	if !passed["TestPass29"] {
		t.Fatalf("expected TestPass29 in events, got %v", passed)
	}
}

func TestShardExitHandlesProgressEnvSetAndUnset(t *testing.T) {
	root := requireTempDir(t)
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	failTestBin := writeFakeScript(t, root, "fake-fail-bin", "#!/usr/bin/env python3", `import sys
if any(a.startswith('-test.list=') for a in sys.argv[1:]):
    print('TestFail')
    sys.exit(0)
print('=== RUN   TestFail')
print('--- FAIL: TestFail (0.00s)')
print('FAIL')
sys.exit(1)
`)
	writeFakeScript(t, fakeBin, "go", "#!/bin/sh", fmt.Sprintf(`
if [ "$1" = "list" ]; then
  printf '{"ImportPath":"fixture","Dir":%q,"TestGoFiles":["x_test.go"]}\n'
  exit 0
fi
if [ "$1" = "test" ] && [ "$2" = "-c" ]; then
  cp %q "$4"
  chmod +x "$4"
  exit 0
fi
if [ "$1" = "tool" ] && [ "$2" = "test2json" ]; then
  printf '{"Action":"run","Package":"fixture","Test":"TestFail"}\n'
  printf '{"Action":"output","Package":"fixture","Test":"TestFail","Output":"--- FAIL: TestFail (0.00s)\\n"}\n'
  printf '{"Action":"fail","Package":"fixture","Test":"TestFail","Elapsed":0}\n'
  printf '{"Action":"fail","Package":"fixture","Elapsed":0}\n'
  exit 1
fi
exit 1
`, root, failTestBin))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-gitconfig"))

	record := filepath.Join(root, "record.json")
	if err := os.WriteFile(record, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scratchSet := filepath.Join(root, "scratch-set")
	if err := os.MkdirAll(scratchSet, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ProgressOutEnv, filepath.Join(root, "env-progress.json"))
	if code := RunCLI([]string{root, "--scratch", scratchSet, "--record", record}); code != 1 {
		t.Fatalf("expected exit 1 with %s set, got %d", ProgressOutEnv, code)
	}
	if _, ok := os.LookupEnv(ProgressOutEnv); ok {
		t.Fatalf("expected %s to be unset after RunCLI", ProgressOutEnv)
	}

	scratchUnset := filepath.Join(root, "scratch-unset")
	if err := os.MkdirAll(scratchUnset, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv(ProgressOutEnv)
	if code := RunCLI([]string{root, "--scratch", scratchUnset, "--record", record}); code != 1 {
		t.Fatalf("expected exit 1 with %s unset, got %d", ProgressOutEnv, code)
	}
}

func TestSingleTestShardSetsGOMAXPROCSAndP1(t *testing.T) {
	root := requireTempDir(t)
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	captureFile := filepath.Join(root, "captured.json")
	writeFakeScript(t, fakeBin, "go", "#!/usr/bin/env python3", fmt.Sprintf(`
import json, os, sys
with open(%q, 'w') as f:
    json.dump({'argv': sys.argv[1:], 'GOMAXPROCS': os.environ.get('GOMAXPROCS')}, f)
print('{"Action":"pass","Package":"example.com/pkg","Test":"TestOne"}')
print('{"Action":"pass","Package":"example.com/pkg"}')
`, captureFile))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-gitconfig"))

	pkgTest := writeFakeScript(t, root, "pkg.test", "#!/bin/sh", "exit 0\n")
	shardScratch := filepath.Join(root, "shard-scratch")
	if err := os.MkdirAll(shardScratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RunShard("example.com/pkg", root, pkgTest, []string{"TestOne"}, 0, shardScratch, "10s", nil, RunOptions{}, nil); err != nil {
		t.Fatalf("RunShard failed: %v", err)
	}
	rawShard, err := os.ReadFile(captureFile)
	if err != nil {
		t.Fatal(err)
	}
	var capShard struct {
		Argv       []string `json:"argv"`
		GOMAXPROCS string   `json:"GOMAXPROCS"`
	}
	if err := json.Unmarshal(rawShard, &capShard); err != nil {
		t.Fatal(err)
	}
	hasParallel1 := false
	for _, a := range capShard.Argv {
		if a == "-test.parallel=1" {
			hasParallel1 = true
			break
		}
	}
	if !hasParallel1 {
		t.Fatalf("expected -test.parallel=1 in RunShard argv %v", capShard.Argv)
	}
	if capShard.GOMAXPROCS != "1" {
		t.Fatalf("expected GOMAXPROCS=1 in RunShard, got %q", capShard.GOMAXPROCS)
	}

	durable := filepath.Join(root, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	baseEnv := map[string]string{"PATH": fakeBin + string(os.PathListSeparator) + os.Getenv("PATH")}
	if _, _, err := RunCachedGroup("example.com/pkg", root, []string{"TestOne"}, 0, "k1", durable, "10s", baseEnv, nil, false, "", "^TestOne$", RunOptions{}, nil); err != nil {
		t.Fatalf("RunCachedGroup failed: %v", err)
	}
	rawGroup, err := os.ReadFile(captureFile)
	if err != nil {
		t.Fatal(err)
	}
	var capGroup struct {
		Argv       []string `json:"argv"`
		GOMAXPROCS string   `json:"GOMAXPROCS"`
	}
	if err := json.Unmarshal(rawGroup, &capGroup); err != nil {
		t.Fatal(err)
	}
	checkFlag := func(flagName, want string) {
		for i := 0; i < len(capGroup.Argv)-1; i++ {
			if capGroup.Argv[i] == flagName && capGroup.Argv[i+1] == want {
				return
			}
		}
		t.Fatalf("expected %s %s in RunCachedGroup argv %v", flagName, want, capGroup.Argv)
	}
	checkFlag("-parallel", "1")
	checkFlag("-p", "1")
	if capGroup.GOMAXPROCS != "1" {
		t.Fatalf("expected GOMAXPROCS=1 in RunCachedGroup, got %q", capGroup.GOMAXPROCS)
	}
}
