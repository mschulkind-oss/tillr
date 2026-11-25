package shards

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
}

func TestCachedSubThresholdRunsSingleGroupAndHitsAcrossWorktrees(t *testing.T) {
	root := requireTempDir(t)
	repo := filepath.Join(root, "wt-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "config", "user.email", "test@example.com")

	mod := filepath.Join(repo, "mod")
	if err := os.MkdirAll(filepath.Join(mod, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mod, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/wtmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "lib", "lib.go"), []byte("package lib\nfunc Val() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subTest := `package sub
import (
	"os"
	"testing"
)
func TestAlpha(t *testing.T) {}
func Example() {
	// Output:
}
func TestMain(m *testing.M) { os.Exit(m.Run()) }
`
	if err := os.WriteFile(filepath.Join(mod, "sub", "sub_test.go"), []byte(subTest), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "init")
	wt2 := filepath.Join(root, "wt-two")
	runGit(t, repo, "worktree", "add", "-q", wt2, "HEAD")

	gocache := filepath.Join(root, "wt-gocache")
	durable := filepath.Join(root, "wt-durable")
	ev1 := filepath.Join(root, "ev1.json")
	ev2 := filepath.Join(root, "ev2.json")
	ev3 := filepath.Join(root, "ev3.json")
	eventsFile := filepath.Join(root, "events.jsonl")
	progressFile := filepath.Join(root, "progress.json")
	durationsFile := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durationsFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run 1 in worktree 1: miss (1 group, no go test -c).
	code1, out1, err1 := runCLIInSubprocess(t, []string{
		mod, "-k", "6", "--cache", "--durations", durationsFile,
		"--events", eventsFile, "--progress-file", progressFile,
		"--progress-interval", "0.01", "--record", durationsFile,
	}, map[string]string{
		"GOCACHE":                      gocache,
		"YOLO_DURABLE_DIR":             durable,
		"TILLR_RUN_ID":                 "",
		"TILLR_GO_SHARDS_EVIDENCE_OUT": ev1,
	}, root)
	if code1 != 0 {
		t.Fatalf("run1 failed (%d): %s\n%s", code1, out1, err1)
	}
	rawEv1, err := os.ReadFile(ev1)
	if err != nil {
		t.Fatal(err)
	}
	var data1 struct {
		Mode     string            `json:"mode"`
		Packages []evidencePackage `json:"packages"`
	}
	if err := json.Unmarshal(rawEv1, &data1); err != nil {
		t.Fatal(err)
	}
	if data1.Mode != "cached-shards" {
		t.Fatalf("expected mode cached-shards, got %q", data1.Mode)
	}
	byIP1 := map[string]string{}
	for _, p := range data1.Packages {
		byIP1[p.ImportPath] = p.TestCache
	}
	wantIP1 := map[string]string{
		"example.com/wtmod/lib": "not-applicable",
		"example.com/wtmod/sub": "miss",
	}
	if !reflect.DeepEqual(byIP1, wantIP1) {
		t.Fatalf("byIP1=%v, want %v", byIP1, wantIP1)
	}
	recorded, err := LoadDurations(durationsFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := recorded["example.com/wtmod/sub"]["TestAlpha"]; !ok {
		t.Fatalf("expected TestAlpha in recorded durations: %+v", recorded)
	}
	if evBytes, rerr := os.ReadFile(eventsFile); rerr != nil || strings.TrimSpace(string(evBytes)) == "" {
		t.Fatalf("expected non-empty events file (err=%v)", rerr)
	}
	if progBytes, perr := os.ReadFile(progressFile); perr == nil {
		var prog progressSnap
		if err := json.Unmarshal(progBytes, &prog); err != nil {
			t.Fatal(err)
		}
		if prog.CompletedPackages < 1 || prog.CompletedShards < 1 {
			t.Fatalf("unexpected progress snap: %+v", prog)
		}
	}

	// Run 2 in worktree 2: cross-worktree cache hit under -trimpath!
	// Overwrite durations with sentinel to verify --record does not overwrite from (cached) groups.
	if err := os.WriteFile(durationsFile, []byte(`{"example.com/wtmod/sub": {"TestAlpha": 9.99}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code2, out2, err2 := runCLIInSubprocess(t, []string{
		filepath.Join(wt2, "mod"), "-k", "6", "--cache", "--durations", durationsFile,
		"--record", durationsFile,
	}, map[string]string{
		"GOCACHE":                      gocache,
		"YOLO_DURABLE_DIR":             durable,
		"TILLR_RUN_ID":                 "",
		"TILLR_GO_SHARDS_EVIDENCE_OUT": ev2,
	}, root)
	if code2 != 0 {
		t.Fatalf("run2 failed (%d): %s\n%s", code2, out2, err2)
	}
	rawEv2, err := os.ReadFile(ev2)
	if err != nil {
		t.Fatal(err)
	}
	var data2 struct {
		Packages []evidencePackage `json:"packages"`
	}
	if err := json.Unmarshal(rawEv2, &data2); err != nil {
		t.Fatal(err)
	}
	byIP2 := map[string]string{}
	for _, p := range data2.Packages {
		byIP2[p.ImportPath] = p.TestCache
	}
	if byIP2["example.com/wtmod/sub"] != "hit" {
		t.Fatalf("expected cross-worktree cache hit, got %v", byIP2)
	}
	recorded2, err := LoadDurations(durationsFile)
	if err != nil {
		t.Fatal(err)
	}
	if recorded2["example.com/wtmod/sub"]["TestAlpha"] != 9.99 {
		t.Fatalf("expected sentinel 9.99 preserved, got %v", recorded2)
	}

	// Run 3 without --cache: default uncached mode still bypasses Go's test cache.
	code3, out3, err3 := runCLIInSubprocess(t, []string{
		mod, "-k", "2", "--durations", durationsFile,
	}, map[string]string{
		"GOCACHE":                      gocache,
		"YOLO_DURABLE_DIR":             durable,
		"TILLR_RUN_ID":                 "",
		"TILLR_GO_SHARDS_EVIDENCE_OUT": ev3,
	}, root)
	if code3 != 0 {
		t.Fatalf("run3 failed (%d): %s\n%s", code3, out3, err3)
	}
	rawEv3, err := os.ReadFile(ev3)
	if err != nil {
		t.Fatal(err)
	}
	var data3 []evidencePackage
	if err := json.Unmarshal(rawEv3, &data3); err != nil {
		t.Fatal(err)
	}
	byIP3 := map[string]string{}
	for _, p := range data3 {
		byIP3[p.ImportPath] = p.TestCache
	}
	if byIP3["example.com/wtmod/sub"] != "bypassed" {
		t.Fatalf("expected bypassed in uncached mode, got %v", byIP3)
	}
}

func TestCachedLargePackageShardsWithFormulaAndHitsOnRepeat(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "largemod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/largemod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := []string{"package largemod", `import "testing"`}
	for i := 0; i < 105; i++ {
		lines = append(lines, fmt.Sprintf("func TestCase%03d(t *testing.T) {}", i))
	}
	if err := os.WriteFile(filepath.Join(mod, "large_test.go"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gocache := filepath.Join(root, "large-gocache")
	durable := filepath.Join(root, "large-durable")
	ev1 := filepath.Join(root, "large-ev1.json")
	ev2 := filepath.Join(root, "large-ev2.json")

	t.Setenv("GOCACHE", gocache)
	t.Setenv("YOLO_DURABLE_DIR", durable)
	t.Setenv("TILLR_RUN_ID", "")

	var seenMu sync.Mutex
	var seenKeys []string
	spy := func(pkg, cwd string, names []string, index int, scratchKey, durable, timeout string, baseEnv map[string]string, gitValues [][2]string, explicitScratch bool, edgeDigest, runPattern string, opts RunOptions, progress *ProgressTracker) ([]TestEvent, string, error) {
		seenMu.Lock()
		seenKeys = append(seenKeys, scratchKey)
		seenMu.Unlock()
		return RunCachedGroup(pkg, cwd, names, index, scratchKey, durable, timeout, baseEnv, gitValues, explicitScratch, edgeDigest, runPattern, opts, progress)
	}

	if _, err := Run(mod, 3, map[string]map[string]float64{}, filepath.Join(root, "s1"), "0", RunOptions{
		Cache:            true,
		EvidenceOut:      ev1,
		runCachedGroupFn: spy,
	}); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if len(seenKeys) != 3 {
		t.Fatalf("expected 3 shards, got %d (%v)", len(seenKeys), seenKeys)
	}
	raw1, _ := os.ReadFile(ev1)
	var p1 struct {
		Packages []evidencePackage `json:"packages"`
	}
	if err := json.Unmarshal(raw1, &p1); err != nil || len(p1.Packages) == 0 || p1.Packages[0].TestCache != "miss" {
		t.Fatalf("expected miss in ev1, got %s", string(raw1))
	}

	seenKeys = nil
	if _, err := Run(mod, 3, map[string]map[string]float64{}, filepath.Join(root, "s2"), "0", RunOptions{
		Cache:            true,
		EvidenceOut:      ev2,
		runCachedGroupFn: spy,
	}); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if len(seenKeys) != 3 {
		t.Fatalf("expected 3 shards on repeat, got %d (%v)", len(seenKeys), seenKeys)
	}
	raw2, _ := os.ReadFile(ev2)
	var p2 struct {
		Packages []evidencePackage `json:"packages"`
	}
	if err := json.Unmarshal(raw2, &p2); err != nil || len(p2.Packages) == 0 || p2.Packages[0].TestCache != "hit" {
		t.Fatalf("expected hit in ev2, got %s", string(raw2))
	}
}

func TestCachedEdgeDigestInvalidationAndCLIEdgeDigest(t *testing.T) {
	root := requireTempDir(t)
	repo := filepath.Join(root, "edge-repo")
	for _, rel := range []string{
		filepath.Join("tools", "gate"),
		filepath.Join("tools", "tillr-helper"),
		filepath.Join("tools", "tillr-app"),
		"data",
	} {
		if err := os.MkdirAll(filepath.Join(repo, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-helper", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-helper\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-helper", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-app", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-app\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	appTest := `package app
import (
	"os"
	"testing"
)
func TestEdgeInputs(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }
func TestApp(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }
`
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-app", "app_test.go"), []byte(appTest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "data", "input.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edgesJSON := `{
  "github.com/mschulkind-oss/tillr-app": {
    "builds": ["github.com/mschulkind-oss/tillr-helper"],
    "reads": ["data/input.txt"]
  }
}`
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte(edgesJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// CLI --edge-digest
	c1, out1, err1 := runCLIInSubprocess(t, []string{"--edge-digest", "github.com/mschulkind-oss/tillr-app"}, nil, repo)
	if c1 != 0 {
		t.Fatalf("--edge-digest failed (%d): %s", c1, err1)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(strings.TrimSpace(out1)) {
		t.Fatalf("expected 64-hex digest, got %q", out1)
	}
	cMiss, outMiss, errMiss := runCLIInSubprocess(t, []string{"--edge-digest", "github.com/mschulkind-oss/unknown"}, nil, repo)
	if cMiss != 0 {
		t.Fatalf("--edge-digest unknown failed (%d): %s", cMiss, errMiss)
	}
	if outMiss != "" {
		t.Fatalf("expected empty stdout for unknown import path, got %q", outMiss)
	}

	gocache := filepath.Join(root, "edge-gocache")
	durable := filepath.Join(root, "edge-durable")
	appDir := filepath.Join(repo, "tools", "tillr-app")
	durPath := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	runApp := func(evPath string) string {
		code, stdout, stderr := runCLIInSubprocess(t, []string{appDir, "--cache", "--durations", durPath}, map[string]string{
			"GOCACHE":                      gocache,
			"YOLO_DURABLE_DIR":             durable,
			"TILLR_RUN_ID":                 "",
			"TILLR_GO_SHARDS_EVIDENCE_OUT": evPath,
		}, root)
		if code != 0 {
			t.Fatalf("cached run failed (%d)\nstdout=%s\nstderr=%s", code, stdout, stderr)
		}
		raw, err := os.ReadFile(evPath)
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Packages []evidencePackage `json:"packages"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		return p.Packages[0].TestCache
	}

	if got := runApp(filepath.Join(root, "e-ev1.json")); got != "miss" {
		t.Fatalf("ev1=%q, want miss", got)
	}
	if got := runApp(filepath.Join(root, "e-ev2.json")); got != "hit" {
		t.Fatalf("ev2=%q, want hit", got)
	}

	// Changing declared reads invalidates cache.
	if err := os.WriteFile(filepath.Join(repo, "data", "input.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runApp(filepath.Join(root, "e-ev3.json")); got != "miss" {
		t.Fatalf("ev3=%q, want miss", got)
	}

	// Changing declared builds invalidates cache.
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-helper", "main.go"), []byte("package main\nfunc main() { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runApp(filepath.Join(root, "e-ev4.json")); got != "miss" {
		t.Fatalf("ev4=%q, want miss", got)
	}
}

func TestCachedFailureFormattingAndRunIDRefusal(t *testing.T) {
	root := requireTempDir(t)
	mod := filepath.Join(root, "failmod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/failmod\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "fail_test.go"), []byte("package failmod\nimport \"testing\"\nfunc TestBoom(t *testing.T) { t.Fatal(\"kaboom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	durPath := filepath.Join(root, "durations.json")
	if err := os.WriteFile(durPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runCLIInSubprocess(t, []string{mod, "--cache", "--durations", durPath}, map[string]string{
		"GOCACHE":          filepath.Join(root, "fail-gocache"),
		"YOLO_DURABLE_DIR": filepath.Join(root, "fail-durable"),
		"TILLR_RUN_ID":     "",
	}, root)
	if code == 0 {
		t.Fatal("expected non-zero exit code for failing cached test")
	}
	if !strings.Contains(stderr, "example.com/failmod shard 0 failed (exit 1): TestBoom") || !strings.Contains(stderr, "kaboom") {
		t.Fatalf("unexpected failure stderr: %q", stderr)
	}

	// TILLR_RUN_ID refusal on repo module and .forks/, while allowing $TMPDIR fixture.
	fakeRepo := filepath.Join(root, "fake-repo")
	pkgDir := filepath.Join(fakeRepo, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module example.com/repom\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "p_test.go"), []byte("package pkg\nimport \"testing\"\nfunc TestOk(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cRepo, _, errRepo := runCLIInSubprocess(t, []string{pkgDir, "--cache", "--durations", durPath}, map[string]string{
		"TILLR_RUN_ID":    "run-worker-1",
		"TILLR_REPO_ROOT": fakeRepo,
	}, root)
	if cRepo == 0 || !strings.Contains(errRepo, "refused") {
		t.Fatalf("expected repo refusal, got code=%d stderr=%q", cRepo, errRepo)
	}

	forksMod := filepath.Join(root, ".forks", "lanes", "lane-1", "tillr")
	if err := os.MkdirAll(forksMod, 0o755); err != nil {
		t.Fatal(err)
	}
	cForks, _, errForks := runCLIInSubprocess(t, []string{forksMod, "--cache", "--durations", durPath}, map[string]string{
		"TILLR_RUN_ID": "run-worker-1",
	}, root)
	if cForks == 0 || !strings.Contains(errForks, ".forks/") {
		t.Fatalf("expected .forks/ refusal, got code=%d stderr=%q", cForks, errForks)
	}

	// Private fixture under $YOLO_DURABLE_DIR / $TMPDIR is allowed even with TILLR_RUN_ID set.
	realRepoRoot := LocateRepoRoot(".", true, false)
	cAllow, outAllow, errAllow := runCLIInSubprocess(t, []string{pkgDir, "--cache", "--durations", durPath}, map[string]string{
		"TILLR_RUN_ID":     "run-worker-1",
		"TILLR_REPO_ROOT":  realRepoRoot,
		"YOLO_DURABLE_DIR": root,
		"TMPDIR":           root,
		"GOCACHE":          filepath.Join(root, "allow-gocache"),
	}, root)
	if cAllow != 0 {
		t.Fatalf("expected fixture under TMPDIR/YOLO_DURABLE_DIR to be allowed, got code=%d\nstdout=%s\nstderr=%s", cAllow, outAllow, errAllow)
	}
}

func TestCachedCrossToolGoAffectedHitsGoTestShards(t *testing.T) {
	goAffectedPath := filepath.Join("..", "..", "gate", "go_affected.py")
	if _, err := os.Stat(goAffectedPath); os.IsNotExist(err) {
		t.Skip("tools/gate/go_affected.py not present")
	}
	goAffectedAbs, err := filepath.Abs(goAffectedPath)
	if err != nil {
		t.Fatal(err)
	}
	pyShardsPath := filepath.Join("..", "..", "go-test-shards.py")

	root := requireTempDir(t)
	repo := filepath.Join(root, "cross-repo")
	if err := os.MkdirAll(filepath.Join(repo, "tools", "gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rawPy, rerr := os.ReadFile(pyShardsPath); rerr == nil {
		if werr := os.WriteFile(filepath.Join(repo, "tools", "go-test-shards.py"), rawPy, 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "data", "edge.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	smallMod := filepath.Join(repo, "tools", "tillr-small")
	if err := os.MkdirAll(smallMod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(smallMod, "go.mod"), []byte("module github.com/mschulkind-oss/tillr-small\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	smallTest := `package small
import (
	"os"
	"testing"
)
func TestEdgeInputs(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }
func TestSmallA(t *testing.T) {}
func TestSmallB(t *testing.T) {}
`
	if err := os.WriteFile(filepath.Join(smallMod, "small_test.go"), []byte(smallTest), 0o644); err != nil {
		t.Fatal(err)
	}

	largeMod := filepath.Join(repo, "tools", "tillr-large")
	if err := os.MkdirAll(largeMod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(largeMod, "go.mod"), []byte("module github.com/mschulkind-oss/tillr-large\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	largeLines := []string{
		"package large",
		`import (`,
		`	"os"`,
		`	"testing"`,
		`)`,
		`func TestEdgeInputs(t *testing.T) { _ = os.Getenv("TILLR_GATE_EDGE_INPUTS") }`,
	}
	for i := 0; i < 105; i++ {
		largeLines = append(largeLines, fmt.Sprintf("func TestLarge%03d(t *testing.T) {}", i))
	}
	if err := os.WriteFile(filepath.Join(largeMod, "large_test.go"), []byte(strings.Join(largeLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	edges := `{
  "github.com/mschulkind-oss/tillr-small": {"reads": ["data/edge.txt"]},
  "github.com/mschulkind-oss/tillr-large": {"reads": ["data/edge.txt"]}
}`
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte(edges), 0o644); err != nil {
		t.Fatal(err)
	}

	gocache := filepath.Join(root, "cross-gocache")
	durable := filepath.Join(root, "cross-durable")
	t.Setenv("GOCACHE", gocache)
	t.Setenv("YOLO_DURABLE_DIR", durable)
	t.Setenv("TILLR_RUN_ID", "")

	pyScript := fmt.Sprintf(`
import importlib.util, pathlib
spec = importlib.util.spec_from_file_location('go_affected', %q)
ga = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ga)
repo = pathlib.Path(%q)
sel = ga.select_affected_packages(repo, ['tools/tillr-small/small_test.go', 'tools/tillr-large/large_test.go'])
rc, out, ev = ga.run_selected_tests(repo, sel, progress_stream=False)
assert rc == 0, out
assert len(ev['packages']) == 2, ev
`, goAffectedAbs, repo)
	cmd := exec.Command("python3", "-c", pyScript)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go_affected run failed: %v\n%s", err, string(out))
	}

	evSmall := filepath.Join(root, "cross-ev-small.json")
	if _, err := Run(smallMod, 6, map[string]map[string]float64{}, filepath.Join(root, "cs-small"), "0", RunOptions{
		Cache:       true,
		EvidenceOut: evSmall,
	}); err != nil {
		t.Fatalf("small Run failed: %v", err)
	}
	rawSmall, _ := os.ReadFile(evSmall)
	var pSmall struct {
		Packages []evidencePackage `json:"packages"`
	}
	if err := json.Unmarshal(rawSmall, &pSmall); err != nil || len(pSmall.Packages) == 0 || pSmall.Packages[0].TestCache != "hit" {
		t.Fatalf("expected small cache hit, got %s", string(rawSmall))
	}

	evLarge := filepath.Join(root, "cross-ev-large.json")
	if _, err := Run(largeMod, 6, map[string]map[string]float64{}, filepath.Join(root, "cs-large"), "0", RunOptions{
		Cache:       true,
		EvidenceOut: evLarge,
	}); err != nil {
		t.Fatalf("large Run failed: %v", err)
	}
	rawLarge, _ := os.ReadFile(evLarge)
	var pLarge struct {
		Packages []evidencePackage `json:"packages"`
	}
	if err := json.Unmarshal(rawLarge, &pLarge); err != nil || len(pLarge.Packages) == 0 || pLarge.Packages[0].TestCache != "hit" {
		t.Fatalf("expected large cache hit, got %s", string(rawLarge))
	}
}

func TestMaxShardsScalesWithCPUCount(t *testing.T) {
	wantHost := 12
	if n := runtime.NumCPU(); n > 0 {
		wantHost = 2 * n
	}
	if MaxShards != wantHost {
		t.Fatalf("MaxShards = %d, want %d", MaxShards, wantHost)
	}
	for _, cpus := range []int{1, 4, 8, 16, 32} {
		if got := maxShardsForCPUs(cpus); got != 2*cpus {
			t.Fatalf("maxShardsForCPUs(%d) = %d, want %d", cpus, got, 2*cpus)
		}
	}
	for _, missing := range []int{0, -1} {
		if got := maxShardsForCPUs(missing); got != 12 {
			t.Fatalf("maxShardsForCPUs(%d) = %d, want 12", missing, got)
		}
	}

	root := requireTempDir(t)
	pkgDir := filepath.Join(root, "sharded")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module example.com/tx/sharded\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	countCachedShards := func(cpus, totalTests int) int {
		lines := []string{"package sharded", `import "testing"`}
		for i := 0; i < totalTests; i++ {
			lines = append(lines, fmt.Sprintf("func TestCase%04d(t *testing.T) {}", i))
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "sharded_test.go"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		origMax := MaxShards
		MaxShards = maxShardsForCPUs(cpus)
		defer func() { MaxShards = origMax }()

		var mu sync.Mutex
		var seen int
		fakeGroup := func(pkg, cwd string, names []string, index int, scratchKey, durable, timeout string, baseEnv map[string]string, gitValues [][2]string, explicitScratch bool, edgeDigest, runPattern string, opts RunOptions, progress *ProgressTracker) ([]TestEvent, string, error) {
			mu.Lock()
			seen++
			mu.Unlock()
			return []TestEvent{{Action: "pass", Package: pkg}}, "miss", nil
		}
		_, err := Run(pkgDir, 6, map[string]map[string]float64{}, filepath.Join(root, fmt.Sprintf("scratch-%d-%d", cpus, totalTests)), "0", RunOptions{
			Cache:            true,
			DurableDir:       filepath.Join(root, "durable"),
			runCachedGroupFn: fakeGroup,
		})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		return seen
	}

	// 2,500 tests -> 2,500 / 125 = 20 uncapped shards.
	// On 8 processors (MaxShards = 16), scales past 12 and caps at 16;
	// on 16 processors (MaxShards = 32), runs all 20 shards and caps 5,000 tests (40 uncapped) at 32.
	if got := countCachedShards(8, 2500); got != 16 {
		t.Fatalf("8 CPUs / 2500 tests: got %d shards, want 16", got)
	}
	if got := countCachedShards(16, 2500); got != 20 {
		t.Fatalf("16 CPUs / 2500 tests: got %d shards, want 20", got)
	}
	if got := countCachedShards(16, 5000); got != 32 {
		t.Fatalf("16 CPUs / 5000 tests: got %d shards, want 32", got)
	}
}
