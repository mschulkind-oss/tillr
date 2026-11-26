package goaffected_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-gate/goaffected"
)

const excerptLines = 60

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == goaffected.InvocationGuardFlag {
		if len(os.Args) < 5 || os.Args[3] != "--" {
			fmt.Fprintln(os.Stderr, "go-affected: invalid --invocation-guard arguments")
			os.Exit(2)
		}
		fd, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-affected: invalid lifeline fd %q: %v\n", os.Args[2], err)
			os.Exit(2)
		}
		os.Exit(goaffected.InvocationGuardMain(fd, os.Args[4:]))
	}
	os.Exit(m.Run())
}

var (
	goFailLineRE        = regexp.MustCompile(`^\s*--- FAIL: (?P<name>\S+) \(`)
	goLocationLineRE    = regexp.MustCompile(`^\s+(?P<file>[\w./-]+_test\.go):(?P<line>\d+): `)
	goPackageFailLineRE = regexp.MustCompile(`^FAIL\t(?P<package>\S+)`)
)

type goFailureCase struct {
	Name    string
	File    string
	Line    int
	Package string
}

func goFailureCases(output string) []goFailureCase {
	var cases []goFailureCase
	var openIdx []int
	curIdx := -1
	for _, line := range strings.Split(output, "\n") {
		if m := goFailLineRE.FindStringSubmatch(line); m != nil {
			cases = append(cases, goFailureCase{Name: m[1]})
			curIdx = len(cases) - 1
			openIdx = append(openIdx, curIdx)
			continue
		}
		if curIdx >= 0 && cases[curIdx].File == "" {
			if m := goLocationLineRE.FindStringSubmatch(line); m != nil {
				lineNum, _ := strconv.Atoi(m[2])
				cases[curIdx].File = m[1]
				cases[curIdx].Line = lineNum
				continue
			}
		}
		if m := goPackageFailLineRE.FindStringSubmatch(line); m != nil {
			for _, idx := range openIdx {
				cases[idx].Package = m[1]
			}
			openIdx = nil
			curIdx = -1
		}
	}
	return cases
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// tools/tillr-gate/goaffected -> repo root is 3 levels up.
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "tools", "gate", "go-test-edges.json")); err != nil {
		t.Fatalf("unexpected repo root %s: %v", root, err)
	}
	return root
}

func scratchRoot(t *testing.T) string {
	t.Helper()
	if d := strings.TrimSpace(os.Getenv("YOLO_DURABLE_DIR")); d != "" {
		if err := os.MkdirAll(d, 0o755); err == nil {
			return d
		}
	}
	return t.TempDir()
}

func freshNonce(t *testing.T) string {
	t.Helper()
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

var (
	sharedGocacheOnce sync.Once
	sharedGocacheDir  string
)

func sharedGocache(t *testing.T) string {
	t.Helper()
	sharedGocacheOnce.Do(func() {
		cand := strings.TrimSpace(os.Getenv("GOCACHE"))
		if cand == "" {
			if out, err := exec.Command("go", "env", "GOCACHE").Output(); err == nil {
				cand = strings.TrimSpace(string(out))
			}
		}
		if cand != "" && cand != "off" {
			if err := os.MkdirAll(cand, 0o755); err == nil {
				probe := filepath.Join(cand, ".write-probe-"+strconv.Itoa(os.Getpid()))
				if err := os.WriteFile(probe, []byte("ok"), 0o644); err == nil {
					_ = os.Remove(probe)
					sharedGocacheDir = cand
					return
				}
			}
		}
		tmp, err := os.MkdirTemp("", "go-affected-gocache-")
		if err == nil {
			sharedGocacheDir = tmp
		}
	})
	if sharedGocacheDir == "" {
		return t.TempDir()
	}
	return sharedGocacheDir
}

var (
	rootGraphOnce sync.Once
	rootGraphVal  *goaffected.Graph
	rootGraphErr  error
)

func getRootGraph(t *testing.T) *goaffected.Graph {
	t.Helper()
	root := repoRoot(t)
	rootGraphOnce.Do(func() {
		rootGraphVal, rootGraphErr = goaffected.LoadTillrGraph(root, nil)
	})
	if rootGraphErr != nil {
		t.Fatalf("LoadTillrGraph: %v", rootGraphErr)
	}
	return rootGraphVal
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture",
		"GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture",
		"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestDiscoverModules(t *testing.T) {
	mods, err := goaffected.DiscoverTillrModules(repoRoot(t))
	if err != nil {
		t.Fatalf("DiscoverTillrModules: %v", err)
	}
	expected := []string{
		"tools/tillr",
		"tools/tillr-cli",
		"tools/tillr-drain",
		"tools/tillr-gate",
		"tools/tillr-item",
		"tools/tillr-land",
		"tools/tillr-loop",
		"tools/tillr-roll",
		"tools/tillr-run",
		"tools/tillr-store",
		"tools/tillr-trace",
		"tools/tillr-view",
		"tools/tillr-watch",
	}
	if !reflect.DeepEqual(mods, expected) {
		t.Fatalf("got %v, want %v", mods, expected)
	}
}

func TestBuildEnvStripsOuterGateVars(t *testing.T) {
	polluted := map[string]string{
		"TILLR_FORK_DRIFT":            "1",
		"PI_FORKS_ROOT":               "/workspace",
		"PI_FORKS_SERIES_ROOT":        "/workspace",
		"LAND_BATCH_LIMIT":            "4",
		"TILLR_RUN_ID":                "run-outer-1",
		"TILLR_GATE_SPANS_OUT":        "/workspace/spans.json",
		"TILLR_GO_VERDICTS_IN":        "/workspace/verdicts-in.json",
		"TILLR_GO_VERDICTS_OUT":       "/workspace/verdicts-out.json",
		"TILLR_SELF_SUITE_TIMING_OUT": "/workspace/self-suite.json",
		"GO_TEST_SHARDS_HOLDER":       "gate:run-outer-1",
		"GIT_DIR":                     "/workspace/.git",
		"GIT_WORK_TREE":               "/workspace",
	}
	for k, v := range polluted {
		t.Setenv(k, v)
	}
	envSlice := goaffected.BuildEnv()
	env := make(map[string]string, len(envSlice))
	for _, kv := range envSlice {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for k := range polluted {
		if _, exists := env[k]; exists {
			t.Fatalf("%s leaked into BuildEnv()", k)
		}
	}
	if env["CGO_ENABLED"] != "0" {
		t.Fatalf("CGO_ENABLED = %q, want 0", env["CGO_ENABLED"])
	}
	if !slices.Contains(strings.Fields(env["GOFLAGS"]), "-trimpath") {
		t.Fatalf("-trimpath missing from GOFLAGS=%q", env["GOFLAGS"])
	}
}

func TestSelectionRules(t *testing.T) {
	root := repoRoot(t)
	rootGraph := getRootGraph(t)

	byImportPath := func(sel *goaffected.Selection) map[string]goaffected.SelectedPackage {
		m := make(map[string]goaffected.SelectedPackage, len(sel.Selected))
		for _, item := range sel.Selected {
			m[item.ImportPath] = item
		}
		return m
	}

	// 1. Unrelated file selects nothing (exercises uncached entry path too).
	sel, err := goaffected.SelectAffectedPackages(root, []string{"README.md"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(README.md): %v", err)
	}
	if len(sel.Selected) != 0 {
		t.Fatalf("expected empty selection for README.md, got %+v", sel.Selected)
	}

	// 2. Pack-sync mirror and non-tillr Go tools are excluded.
	for _, excluded := range []string{
		"yolo-packs/tillr/files/local/share/tillr/tillr-drain/main.go",
		"tools/dotmod/main.go",
		"tools/bedrock-spend/main.go",
	} {
		sel, err := goaffected.SelectAffectedPackages(root, []string{excluded}, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", excluded, err)
		}
		if len(sel.Selected) != 0 {
			t.Fatalf("%s: expected empty selection, got %+v", excluded, sel.Selected)
		}
	}

	// 3. Drain package change selects non-deferred drain packages and tillr-view (via import or builds edge),
	// while omitting deferred tillr-drain/drain pre-merge unless dropPackages is empty,
	// exercising full end-to-end graph loading without preloaded graph.
	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-drain/drain/loop.go"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(drain/loop.go): %v", err)
	}
	byPkg := byImportPath(sel)
	if _, ok := byPkg["github.com/mschulkind-oss/tillr-drain/drain"]; ok {
		t.Fatalf("expected deferred drain to be omitted pre-merge, got %+v", byPkg)
	}
	if byPkg["github.com/mschulkind-oss/tillr-drain"].Reason != "import" {
		t.Fatalf("expected import for tillr-drain, got %+v", byPkg)
	}
	viewItem, ok := byPkg["github.com/mschulkind-oss/tillr-view"]
	if !ok || (viewItem.Reason != "import" && viewItem.Reason != "edge") {
		t.Fatalf("expected tillr-view with import/edge, got %+v", byPkg)
	}
	selAll, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{"tools/tillr-drain/drain/loop.go"}, nil, map[string]bool{}, nil, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackagesWithDrop(drain/loop.go): %v", err)
	}
	if byImportPath(selAll)["github.com/mschulkind-oss/tillr-drain/drain"].Reason != "changed" {
		t.Fatalf("expected changed for drain with dropPackages={}, got %+v", byImportPath(selAll))
	}

	// 4. Item package change selects item packages and tillr-view (via import or builds edge).
	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-item/item/doc_triage.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(doc_triage.go): %v", err)
	}
	byPkg = byImportPath(sel)
	if byPkg["github.com/mschulkind-oss/tillr-item/item"].Reason != "changed" {
		t.Fatalf("expected changed for tillr-item/item, got %+v", byPkg)
	}
	viewItem, ok = byPkg["github.com/mschulkind-oss/tillr-view"]
	if !ok || (viewItem.Reason != "import" && viewItem.Reason != "edge") {
		t.Fatalf("expected tillr-view with import/edge, got %+v", byPkg)
	}

	// 5. Unplaceable go.mod change triggers floor for module and replacing modules.
	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-drain/go.mod"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(tillr-drain/go.mod): %v", err)
	}
	floorMods := make(map[string]bool, len(sel.FloorModules))
	for _, fm := range sel.FloorModules {
		floorMods[fm] = true
	}
	for _, wantMod := range []string{
		"tools/tillr-drain",
		"tools/tillr-land",
		"tools/tillr-store",
		"tools/tillr-view",
		"tools/tillr-watch",
	} {
		if !floorMods[wantMod] {
			t.Fatalf("floor_modules %v missing %s", sel.FloorModules, wantMod)
		}
	}
	for _, item := range sel.Selected {
		if floorMods[item.Module] {
			if item.Reason != "floor" {
				t.Fatalf("expected floor reason for %s, got %+v", item.Module, item)
			}
		} else if item.Reason != "edge" {
			t.Fatalf("expected edge reason for non-floor %s, got %+v", item.Module, item)
		}
	}

	// 6. Deleted package path triggers floor.
	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-drain/deletedpkg/foo.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(deletedpkg): %v", err)
	}
	if !slices.Contains(sel.FloorModules, "tools/tillr-drain") {
		t.Fatalf("expected tools/tillr-drain in floor_modules, got %v", sel.FloorModules)
	}

	// 7. drop_modules excludes tools/tillr when go-substrate covers it.
	sel, err = goaffected.SelectAffectedPackages(
		root,
		[]string{"tools/tillr/event/event.go"},
		map[string]bool{"tools/tillr": true},
		rootGraph,
		nil,
	)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(drop_modules): %v", err)
	}
	for _, item := range sel.Selected {
		if item.Module == "tools/tillr" {
			t.Fatalf("tools/tillr should have been dropped: %+v", sel.Selected)
		}
	}
	if len(sel.Selected) == 0 {
		t.Fatalf("expected non-empty selection after dropping tools/tillr")
	}

	// 8. Sibling/cross-package builds edges (including drain/item main -> tillr-view).
	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-store/main.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(tillr-store/main.go): %v", err)
	}
	byPkg = byImportPath(sel)
	for _, want := range []string{
		"github.com/mschulkind-oss/tillr-store",
		"github.com/mschulkind-oss/tillr-watch",
		"github.com/mschulkind-oss/tillr-item",
	} {
		if _, ok := byPkg[want]; !ok {
			t.Fatalf("tillr-store/main.go missing %s in %+v", want, byPkg)
		}
	}

	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-view/main.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(tillr-view/main.go): %v", err)
	}
	byPkg = byImportPath(sel)
	for _, want := range []string{
		"github.com/mschulkind-oss/tillr-view",
		"github.com/mschulkind-oss/tillr-store",
	} {
		if _, ok := byPkg[want]; !ok {
			t.Fatalf("tillr-view/main.go missing %s in %+v", want, byPkg)
		}
	}

	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-drain/main.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(tillr-drain/main.go): %v", err)
	}
	byPkg = byImportPath(sel)
	for _, want := range []string{
		"github.com/mschulkind-oss/tillr-drain",
		"github.com/mschulkind-oss/tillr-view",
	} {
		if _, ok := byPkg[want]; !ok {
			t.Fatalf("tillr-drain/main.go missing %s in %+v", want, byPkg)
		}
	}
	if _, ok := byPkg["github.com/mschulkind-oss/tillr-drain/drain"]; ok {
		t.Fatalf("tillr-drain/main.go should omit deferred tillr-drain/drain pre-merge")
	}
	if byPkg["github.com/mschulkind-oss/tillr-view"].Reason != "edge" {
		t.Fatalf("expected edge for tillr-view, got %+v", byPkg["github.com/mschulkind-oss/tillr-view"])
	}

	sel, err = goaffected.SelectAffectedPackages(root, []string{"tools/tillr-item/main.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(tillr-item/main.go): %v", err)
	}
	byPkg = byImportPath(sel)
	rootEdges, err := goaffected.LoadEdges(root)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	if byPkg["github.com/mschulkind-oss/tillr-view"].Reason != "edge" {
		t.Fatalf("expected edge for tillr-view on item/main.go, got %+v", byPkg["github.com/mschulkind-oss/tillr-view"])
	}
	if !sameStringSet(byPkg["github.com/mschulkind-oss/tillr-view"].SelectedTests, rootEdges["github.com/mschulkind-oss/tillr-view"].BuildTests["github.com/mschulkind-oss/tillr-item"]) {
		t.Fatalf("tillr-view SelectedTests = %v, want %v", byPkg["github.com/mschulkind-oss/tillr-view"].SelectedTests, rootEdges["github.com/mschulkind-oss/tillr-view"].BuildTests["github.com/mschulkind-oss/tillr-item"])
	}

	// High-fanout builds edges in go-test-edges.json narrow to the tests that build each sibling.
	for _, bc := range []struct {
		changedSib  string
		consumerPkg string
		sibIP       string
	}{
		{"tools/tillr-view/main.go", "github.com/mschulkind-oss/tillr-drain/drain", "github.com/mschulkind-oss/tillr-view"},
		{"tools/tillr-store/main.go", "github.com/mschulkind-oss/tillr-item", "github.com/mschulkind-oss/tillr-store"},
		{"tools/tillr-gate/main.go", "github.com/mschulkind-oss/tillr-land", "github.com/mschulkind-oss/tillr-gate"},
	} {
		var dropPkgs map[string]bool
		if goaffected.DeferredProofPackages[bc.consumerPkg] {
			dropPkgs = map[string]bool{}
		}
		sel, err = goaffected.SelectAffectedPackagesWithDrop(root, []string{bc.changedSib}, nil, dropPkgs, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", bc.changedSib, err)
		}
		byPkg = byImportPath(sel)
		if byPkg[bc.consumerPkg].Reason != "edge" {
			t.Fatalf("%s: %s reason=%q, want edge", bc.changedSib, bc.consumerPkg, byPkg[bc.consumerPkg].Reason)
		}
		want := rootEdges[bc.consumerPkg].BuildTests[bc.sibIP]
		if !sameStringSet(byPkg[bc.consumerPkg].SelectedTests, want) {
			t.Fatalf("%s: %s SelectedTests=%v, want %v", bc.changedSib, bc.consumerPkg, byPkg[bc.consumerPkg].SelectedTests, want)
		}
	}

	// 9. Non-Go reads edges: non-deferred readers select under default; deferred readers
	// (tillr-drain/drain) stay out of pre-merge selection and select when dropPackages is empty.
	readCases := []struct {
		changed  string
		expected []string
	}{
		{"tools/tillr-run/backends/pi/extract-report", []string{
			"github.com/mschulkind-oss/tillr-run",
			"github.com/mschulkind-oss/tillr-run/run",
		}},
		{"tools/go-test-durations.json", []string{
			"github.com/mschulkind-oss/tillr-gate/shards",
		}},
		{"config/tillr/backend-policy.json", []string{
			"github.com/mschulkind-oss/tillr-drain",
			"github.com/mschulkind-oss/tillr-drain/drain",
		}},
		{"tools/tillr-gate/change-gate.py", []string{"github.com/mschulkind-oss/tillr-drain/drain"}},
		{"tools/tillr-gate/classify.py", []string{"github.com/mschulkind-oss/tillr-drain/drain"}},
		{"tools/tillr-loop/drain-argv.json", []string{"github.com/mschulkind-oss/tillr-drain/drain"}},
		{"yolo-packs/tillr/pack.json", []string{"github.com/mschulkind-oss/tillr-drain/drain"}},
		{"tools/tillr/store/testdata/crash_writer.py", []string{
			"github.com/mschulkind-oss/tillr-store",
			"github.com/mschulkind-oss/tillr-view",
		}},
		{"tools/tillr/examples/recorded-verdict/definition.json", []string{
			"github.com/mschulkind-oss/tillr-drain/drain",
			"github.com/mschulkind-oss/tillr-watch",
		}},
		{"tools/gate/checks/go-build.py", []string{"github.com/mschulkind-oss/tillr-gate"}},
	}
	for _, rc := range readCases {
		sel, err := goaffected.SelectAffectedPackages(root, []string{rc.changed}, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", rc.changed, err)
		}
		byPkg = byImportPath(sel)
		var preMergeExpected, deferredExpected []string
		for _, want := range rc.expected {
			if goaffected.DeferredProofPackages[want] {
				deferredExpected = append(deferredExpected, want)
			} else {
				preMergeExpected = append(preMergeExpected, want)
			}
		}
		for _, want := range preMergeExpected {
			if _, ok := byPkg[want]; !ok {
				t.Fatalf("%s: missing %s in %+v", rc.changed, want, byPkg)
			}
		}
		for _, def := range deferredExpected {
			if _, ok := byPkg[def]; ok {
				t.Fatalf("%s: deferred %s should not be selected pre-merge in %+v", rc.changed, def, byPkg)
			}
		}
		for _, item := range sel.Selected {
			if slices.Contains(preMergeExpected, item.ImportPath) && item.SelectedTests != nil {
				t.Fatalf("unscoped read %s narrowed %+v", rc.changed, item)
			}
		}
		if len(deferredExpected) > 0 {
			selAll, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{rc.changed}, nil, map[string]bool{}, rootGraph, nil)
			if err != nil {
				t.Fatalf("SelectAffectedPackagesWithDrop(%s): %v", rc.changed, err)
			}
			byAll := byImportPath(selAll)
			for _, want := range rc.expected {
				if _, ok := byAll[want]; !ok {
					t.Fatalf("%s (dropPackages={}): missing %s in %+v", rc.changed, want, byAll)
				}
			}
		}
	}

	// 10. Scoped read_tests on one read path select only the reading packages and narrow
	// each to its reader tests, whether the path changes alone or beside an unrelated file.
	scopedRead := "tools/go-test-shards.py"
	drainPkg := "github.com/mschulkind-oss/tillr-drain/drain"
	gatePkg := "github.com/mschulkind-oss/tillr-gate"
	rawDrainTests, err := goaffected.ScanPackageTests(root, "tools/tillr-drain/drain", nil)
	if err != nil {
		t.Fatalf("ScanPackageTests(drain): %v", err)
	}
	rawGateTests, err := goaffected.ScanPackageTests(root, "tools/tillr-gate", nil)
	if err != nil {
		t.Fatalf("ScanPackageTests(gate): %v", err)
	}
	var drainReaders, gateReaders []string
	for _, name := range rawDrainTests {
		if name != "TestEdgeInputs" && len(drainReaders) < 13 {
			drainReaders = append(drainReaders, name)
		}
	}
	for _, name := range rawGateTests {
		if name != "TestEdgeInputs" && len(gateReaders) < 9 {
			gateReaders = append(gateReaders, name)
		}
	}
	if len(drainReaders) == 0 || len(gateReaders) == 0 {
		t.Fatalf("expected non-empty reader tests: drain=%v gate=%v", drainReaders, gateReaders)
	}

	baseEdges, err := goaffected.LoadEdges(root)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	scopedEdges := make(map[string]goaffected.EdgeSpec, len(baseEdges))
	for ip, spec := range baseEdges {
		var reads []string
		for _, r := range spec.Reads {
			if r != scopedRead {
				reads = append(reads, r)
			}
		}
		var rt map[string][]string
		if len(spec.ReadTests) > 0 {
			rt = make(map[string][]string, len(spec.ReadTests))
			for k, v := range spec.ReadTests {
				if k != scopedRead {
					rt[k] = slices.Clone(v)
				}
			}
		}
		var bt map[string][]string
		if len(spec.BuildTests) > 0 {
			bt = make(map[string][]string, len(spec.BuildTests))
			for k, v := range spec.BuildTests {
				bt[k] = slices.Clone(v)
			}
		}
		scopedEdges[ip] = goaffected.EdgeSpec{
			Builds:     slices.Clone(spec.Builds),
			BuildTests: bt,
			Reads:      reads,
			ReadTests:  rt,
		}
	}
	setScoped := func(pkg string, readers []string) {
		e := scopedEdges[pkg]
		e.Reads = append(e.Reads, scopedRead)
		if e.ReadTests == nil {
			e.ReadTests = make(map[string][]string)
		}
		e.ReadTests[scopedRead] = slices.Clone(readers)
		scopedEdges[pkg] = e
	}
	setScoped(drainPkg, drainReaders)
	setScoped(gatePkg, gateReaders)

	for _, scopedChanged := range [][]string{
		{scopedRead},
		{scopedRead, "README.md"},
	} {
		sel, err := goaffected.SelectAffectedPackagesWithDrop(root, scopedChanged, nil, map[string]bool{}, rootGraph, scopedEdges)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%v): %v", scopedChanged, err)
		}
		byPkg = byImportPath(sel)
		if len(byPkg) != 2 {
			t.Fatalf("%v: got %d packages, want 2: %+v", scopedChanged, len(byPkg), byPkg)
		}
		for _, ip := range []string{drainPkg, gatePkg} {
			item, ok := byPkg[ip]
			if !ok {
				t.Fatalf("%v: missing %s", scopedChanged, ip)
			}
			if item.Reason != "edge" {
				t.Fatalf("%s: reason=%q, want edge", ip, item.Reason)
			}
			if slices.Contains(item.SelectedTests, "TestEdgeInputs") {
				t.Fatalf("%s: TestEdgeInputs leaked into SelectedTests", ip)
			}
		}
		if !reflect.DeepEqual(byPkg[drainPkg].SelectedTests, drainReaders) {
			t.Fatalf("drain SelectedTests = %v, want %v", byPkg[drainPkg].SelectedTests, drainReaders)
		}
		if !reflect.DeepEqual(byPkg[gatePkg].SelectedTests, gateReaders) {
			t.Fatalf("gate SelectedTests = %v, want %v", byPkg[gatePkg].SelectedTests, gateReaders)
		}
	}

	// 11. Pairing the scoped read path with a Go package edit, a builds-edge edit, an unscoped
	// reads edit, or a module floor trigger widens affected packages back to whole-package
	// execution (SelectedTests==nil) while unaffected reading packages stay narrowed.
	selGo, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{scopedRead, "tools/tillr-drain/drain/loop.go"}, nil, map[string]bool{}, rootGraph, scopedEdges)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(selGo): %v", err)
	}
	byPkg = byImportPath(selGo)
	if byPkg[drainPkg].Reason != "changed" || byPkg[drainPkg].SelectedTests != nil {
		t.Fatalf("selGo drain: %+v", byPkg[drainPkg])
	}
	if byPkg["github.com/mschulkind-oss/tillr-drain"].Reason != "import" || byPkg["github.com/mschulkind-oss/tillr-drain"].SelectedTests != nil {
		t.Fatalf("selGo root drain: %+v", byPkg["github.com/mschulkind-oss/tillr-drain"])
	}
	var gateUnionWant []string
	gateUnionWant = append(gateUnionWant, gateReaders...)
	gateUnionWant = append(gateUnionWant, scopedEdges[gatePkg].ReadTests["tools/tillr-drain/"]...)
	if !sameStringSet(byPkg[gatePkg].SelectedTests, gateUnionWant) {
		t.Fatalf("selGo gate: %+v, want union %v", byPkg[gatePkg], gateUnionWant)
	}

	// Pairing a scoped read path with a scoped builds edge narrows to the union of both.
	selScopedBoth, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{scopedRead, "tools/tillr-cli/main.go"}, nil, map[string]bool{}, rootGraph, scopedEdges)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(selScopedBoth): %v", err)
	}
	byPkg = byImportPath(selScopedBoth)
	cliBuilders := scopedEdges[drainPkg].BuildTests["github.com/mschulkind-oss/tillr-cli"]
	var unionWant []string
	unionWant = append(unionWant, drainReaders...)
	unionWant = append(unionWant, cliBuilders...)
	if !sameStringSet(byPkg[drainPkg].SelectedTests, unionWant) {
		t.Fatalf("selScopedBoth drain SelectedTests = %v, want union %v", byPkg[drainPkg].SelectedTests, unionWant)
	}
	if !reflect.DeepEqual(byPkg[gatePkg].SelectedTests, gateReaders) {
		t.Fatalf("selScopedBoth gate: %+v", byPkg[gatePkg])
	}

	// Pairing with an unscoped builds edge widens back to whole-package execution.
	unscopedBuildEdges := make(map[string]goaffected.EdgeSpec, len(scopedEdges))
	for ip, spec := range scopedEdges {
		var bt map[string][]string
		if len(spec.BuildTests) > 0 {
			bt = make(map[string][]string, len(spec.BuildTests))
			for k, v := range spec.BuildTests {
				if ip == drainPkg && k == "github.com/mschulkind-oss/tillr-cli" {
					continue
				}
				bt[k] = slices.Clone(v)
			}
		}
		unscopedBuildEdges[ip] = goaffected.EdgeSpec{
			Builds:     slices.Clone(spec.Builds),
			BuildTests: bt,
			Reads:      slices.Clone(spec.Reads),
			ReadTests:  spec.ReadTests,
		}
	}
	selBuilds, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{scopedRead, "tools/tillr-cli/main.go"}, nil, map[string]bool{}, rootGraph, unscopedBuildEdges)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(selBuilds): %v", err)
	}
	byPkg = byImportPath(selBuilds)
	if _, ok := byPkg["github.com/mschulkind-oss/tillr-drain"]; ok {
		t.Fatalf("selBuilds should not include tillr-drain: %+v", byPkg)
	}
	if byPkg[drainPkg].SelectedTests != nil {
		t.Fatalf("selBuilds drain should not be narrowed: %+v", byPkg[drainPkg])
	}
	if !reflect.DeepEqual(byPkg[gatePkg].SelectedTests, gateReaders) {
		t.Fatalf("selBuilds gate: %+v", byPkg[gatePkg])
	}

	selUnscoped, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{scopedRead, "config/tillr/backend-policy.json"}, nil, map[string]bool{}, rootGraph, scopedEdges)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(selUnscoped): %v", err)
	}
	byPkg = byImportPath(selUnscoped)
	if byPkg["github.com/mschulkind-oss/tillr-drain"].SelectedTests != nil || byPkg[drainPkg].SelectedTests != nil {
		t.Fatalf("selUnscoped drain should not be narrowed: %+v", byPkg)
	}
	if !reflect.DeepEqual(byPkg[gatePkg].SelectedTests, gateReaders) {
		t.Fatalf("selUnscoped gate: %+v", byPkg[gatePkg])
	}

	selFloor, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{scopedRead, "tools/tillr-drain/go.mod"}, nil, map[string]bool{}, rootGraph, scopedEdges)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(selFloor): %v", err)
	}
	byPkg = byImportPath(selFloor)
	if byPkg["github.com/mschulkind-oss/tillr-drain"].SelectedTests != nil || byPkg[drainPkg].SelectedTests != nil {
		t.Fatalf("selFloor drain should not be narrowed: %+v", byPkg)
	}

	// 12. Alert rule changes under config/tillr/watch/ (alone or with tools/tillr-watch/)
	// select tillr-watch and no tillr-drain package, while every config/tillr/ path read by
	// tillr-drain tests still selects that package.
	for _, watchChanged := range [][]string{
		{"config/tillr/watch/fast-gate-slow.toml", "tools/tillr-watch/main_test.go"},
		{"config/tillr/watch/new-alert-rule.toml", "tools/tillr-watch/new_alert_rule_test.go"},
		{"config/tillr/watch/fast-gate-slow.toml"},
	} {
		selWatch, err := goaffected.SelectAffectedPackages(root, watchChanged, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%v): %v", watchChanged, err)
		}
		byPkg = byImportPath(selWatch)
		if _, ok := byPkg["github.com/mschulkind-oss/tillr-watch"]; !ok {
			t.Fatalf("%v: missing tillr-watch in %+v", watchChanged, byPkg)
		}
		for ip := range byPkg {
			if ip == "github.com/mschulkind-oss/tillr-drain" || strings.HasPrefix(ip, "github.com/mschulkind-oss/tillr-drain/") {
				t.Fatalf("%v selected tillr-drain package %s", watchChanged, ip)
			}
		}
		if slices.Contains(selWatch.FloorModules, "tools/tillr-drain") {
			t.Fatalf("%v triggered floor for tools/tillr-drain", watchChanged)
		}
	}

	policyMatches, err := filepath.Glob(filepath.Join(root, "config", "tillr", "backend-policy*.json"))
	if err != nil || len(policyMatches) == 0 {
		t.Fatalf("expected config/tillr/backend-policy*.json files: %v", err)
	}
	for _, m := range policyMatches {
		rel, _ := filepath.Rel(root, m)
		rel = filepath.ToSlash(rel)
		selPol, err := goaffected.SelectAffectedPackages(root, []string{rel}, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", rel, err)
		}
		byPkg = byImportPath(selPol)
		if _, ok := byPkg["github.com/mschulkind-oss/tillr-drain"]; !ok {
			t.Fatalf("%s: missing github.com/mschulkind-oss/tillr-drain", rel)
		}
		if _, ok := byPkg["github.com/mschulkind-oss/tillr-drain/drain"]; ok {
			t.Fatalf("%s: deferred tillr-drain/drain should not be selected pre-merge", rel)
		}
		selPolAll, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{rel}, nil, map[string]bool{}, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackagesWithDrop(%s): %v", rel, err)
		}
		byAll := byImportPath(selPolAll)
		if _, ok := byAll["github.com/mschulkind-oss/tillr-drain/drain"]; !ok {
			t.Fatalf("%s (dropPackages={}): missing tillr-drain/drain", rel)
		}
	}

	wfMatches, err := filepath.Glob(filepath.Join(root, "config", "tillr", "workflows", "*.json"))
	if err != nil || len(wfMatches) == 0 {
		t.Fatalf("expected config/tillr/workflows/*.json files: %v", err)
	}
	var wfPaths []string
	for _, m := range wfMatches {
		rel, _ := filepath.Rel(root, m)
		wfPaths = append(wfPaths, filepath.ToSlash(rel))
	}
	wfPaths = append(wfPaths, "config/tillr/workflows/new-workflow@v1.json")
	for _, p := range wfPaths {
		selWf, err := goaffected.SelectAffectedPackages(root, []string{p}, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", p, err)
		}
		byPkg = byImportPath(selWf)
		if _, ok := byPkg["github.com/mschulkind-oss/tillr-drain/drain"]; ok {
			t.Fatalf("%s: deferred tillr-drain/drain should not be selected pre-merge", p)
		}
		for _, want := range []string{
			"github.com/mschulkind-oss/tillr-item",
			"github.com/mschulkind-oss/tillr-item/item",
		} {
			if _, ok := byPkg[want]; !ok {
				t.Fatalf("%s: missing %s", p, want)
			}
		}
		selWfAll, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{p}, nil, map[string]bool{}, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackagesWithDrop(%s): %v", p, err)
		}
		byAll := byImportPath(selWfAll)
		if _, ok := byAll["github.com/mschulkind-oss/tillr-drain/drain"]; !ok {
			t.Fatalf("%s (dropPackages={}): missing tillr-drain/drain", p)
		}
	}

	drainOnlyReadPaths := []string{
		"config/tillr/commands.json",
		"config/tillr/deny-list.json",
		"config/tillr/gate-policy.json",
		"config/tillr/plan-guidance.md",
	}
	for _, p := range drainOnlyReadPaths {
		selCfg, err := goaffected.SelectAffectedPackages(root, []string{p}, nil, nil, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", p, err)
		}
		if len(selCfg.Selected) != 0 {
			t.Fatalf("%s: expected no pre-merge selected packages, got %+v", p, selCfg.Selected)
		}
		selCfgAll, err := goaffected.SelectAffectedPackagesWithDrop(root, []string{p}, nil, map[string]bool{}, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackagesWithDrop(%s): %v", p, err)
		}
		byAll := byImportPath(selCfgAll)
		if _, ok := byAll["github.com/mschulkind-oss/tillr-drain/drain"]; !ok {
			t.Fatalf("%s (dropPackages={}): missing tillr-drain/drain", p)
		}
	}
}

func TestEdgesFileHonestAndEnvRead(t *testing.T) {
	root := repoRoot(t)
	edges, err := goaffected.LoadEdges(root)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("go-test-edges.json must not be empty")
	}

	modules, err := goaffected.DiscoverTillrModules(root)
	if err != nil {
		t.Fatalf("DiscoverTillrModules: %v", err)
	}

	goBuildRunRE := regexp.MustCompile(`"go",\s*"(?:build|run)"`)
	siblingDirRE := regexp.MustCompile(`"(?:\.\./)+(tillr-[a-z]+)"`)
	siblingPkgRE := regexp.MustCompile(`"github\.com/mschulkind-oss/(tillr-[a-z]+)"`)
	parentBuildRE := regexp.MustCompile(`exec\.Command\("go",\s*"build"[^)]*"\.\."\)`)
	helperJoinRE := regexp.MustCompile(`filepath\.Join\((?:"\.\.",\s*)+"tillr-"\s*\+`)
	buildUtilRE := regexp.MustCompile(`buildUtility\([^)]*"([a-z]+)"\)`)
	sliceBodyRE := regexp.MustCompile(`\[\]string\{([^}]+)\}`)
	quotedWordRE := regexp.MustCompile(`"([a-z]+)"`)

	for _, mod := range modules {
		modName := strings.SplitN(mod, "/", 2)[1]
		modRoot := filepath.Join(root, filepath.FromSlash(mod))
		err := filepath.WalkDir(modRoot, func(path string, d os.DirEntry, werr error) error {
			if werr != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
				return werr
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			text := string(raw)
			if !strings.Contains(text, "go") || !goBuildRunRE.MatchString(text) {
				return nil
			}
			relDir, _ := filepath.Rel(modRoot, filepath.Dir(path))
			pkgRel := filepath.ToSlash(relDir)
			pkgImport := "github.com/mschulkind-oss/" + modName
			if pkgRel != "." {
				pkgImport += "/" + pkgRel
			}
			foundTargets := make(map[string]bool)
			for _, m := range siblingDirRE.FindAllStringSubmatch(text, -1) {
				if m[1] != modName {
					foundTargets["github.com/mschulkind-oss/"+m[1]] = true
				}
			}
			for _, m := range siblingPkgRE.FindAllStringSubmatch(text, -1) {
				if m[1] != modName {
					foundTargets["github.com/mschulkind-oss/"+m[1]] = true
				}
			}
			if pkgRel != "." && parentBuildRE.MatchString(text) {
				parentRel := filepath.ToSlash(filepath.Dir(filepath.FromSlash(pkgRel)))
				parentImport := "github.com/mschulkind-oss/" + modName
				if parentRel != "." {
					parentImport += "/" + parentRel
				}
				foundTargets[parentImport] = true
			}
			if helperJoinRE.MatchString(text) {
				for _, m := range buildUtilRE.FindAllStringSubmatch(text, -1) {
					name := m[1]
					if "tillr-"+name != modName {
						if info, serr := os.Stat(filepath.Join(root, "tools", "tillr-"+name)); serr == nil && info.IsDir() {
							foundTargets["github.com/mschulkind-oss/tillr-"+name] = true
						}
					}
				}
				for _, lb := range sliceBodyRE.FindAllStringSubmatch(text, -1) {
					for _, qm := range quotedWordRE.FindAllStringSubmatch(lb[1], -1) {
						name := qm[1]
						if "tillr-"+name != modName {
							if info, serr := os.Stat(filepath.Join(root, "tools", "tillr-"+name)); serr == nil && info.IsDir() {
								foundTargets["github.com/mschulkind-oss/tillr-"+name] = true
							}
						}
					}
				}
			}
			if len(foundTargets) > 0 {
				spec, ok := edges[pkgImport]
				if !ok {
					t.Fatalf("%s: missing entry for %s in go-test-edges.json", path, pkgImport)
				}
				declared := make(map[string]bool, len(spec.Builds))
				for _, b := range spec.Builds {
					declared[b] = true
				}
				for tgt := range foundTargets {
					if !declared[tgt] {
						t.Fatalf("%s: %s missing builds edge %s", path, pkgImport, tgt)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("WalkDir(%s): %v", modRoot, err)
		}
	}

	// Scan *_test.go for cross-module Go source walks/parses without a declared read.
	undeclaredWalks, err := goaffected.UndeclaredCrossModuleGoReads(root, edges)
	if err != nil {
		t.Fatalf("UndeclaredCrossModuleGoReads(root): %v", err)
	}
	if len(undeclaredWalks) != 0 {
		t.Fatalf("undeclared cross-module Go reads:\n%s", strings.Join(undeclaredWalks, "\n"))
	}

	// Prove the honesty check fails when a test walks Go source outside its own module
	// without a declared read, and passes once the read is declared.
	walkRepo := filepath.Join(scratchRoot(t), "ga-honest-walk-"+freshNonce(t))
	t.Cleanup(func() { _ = os.RemoveAll(walkRepo) })
	writeWalkFile := func(rel, content string) {
		t.Helper()
		full := filepath.Join(walkRepo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", full, err)
		}
	}
	writeWalkFile("tools/tillr-alpha/go.mod", "module github.com/mschulkind-oss/tillr-alpha\n\ngo 1.22\n")
	writeWalkFile("tools/tillr-beta/go.mod", "module github.com/mschulkind-oss/tillr-beta\n\ngo 1.22\n")
	writeWalkFile("tools/tillr-alpha/scan_test.go",
		"package alpha\n\nimport (\n\t\"io/fs\"\n\t\"path/filepath\"\n\t\"strings\"\n\t\"testing\"\n)\n\n"+
			"func TestScanBeta(t *testing.T) {\n"+
			"\t_ = filepath.WalkDir(filepath.Join(\"..\", \"..\", \"tools/tillr-beta\"), func(p string, d fs.DirEntry, err error) error {\n"+
			"\t\tif strings.HasSuffix(p, \".go\") { return nil }\n"+
			"\t\treturn nil\n"+
			"\t})\n"+
			"}\n")
	writeWalkFile("tools/gate/go-test-edges.json", "{}\n")
	missingWalk, err := goaffected.UndeclaredCrossModuleGoReads(walkRepo, nil)
	if err != nil {
		t.Fatalf("UndeclaredCrossModuleGoReads(walkRepo): %v", err)
	}
	if len(missingWalk) != 1 || !strings.Contains(missingWalk[0], "tools/tillr-alpha/scan_test.go") || !strings.Contains(missingWalk[0], "tools/tillr-beta") {
		t.Fatalf("unexpected missingWalk: %v", missingWalk)
	}
	writeWalkFile("tools/gate/go-test-edges.json", `{"github.com/mschulkind-oss/tillr-alpha":{"builds":[],"reads":["tools/tillr-beta/"],"read_tests":{"tools/tillr-beta/":["TestScanBeta"]}}}`+"\n")
	clearedWalk, err := goaffected.UndeclaredCrossModuleGoReads(walkRepo, nil)
	if err != nil {
		t.Fatalf("UndeclaredCrossModuleGoReads(walkRepo cleared): %v", err)
	}
	if len(clearedWalk) != 0 {
		t.Fatalf("expected no undeclared walks after declaring read, got %v", clearedWalk)
	}

	// Every package listed in go-test-edges.json must read TILLR_GATE_EDGE_INPUTS in its tests.
	const prefix = "github.com/mschulkind-oss/"
	for pkgImport, spec := range edges {
		if !strings.HasPrefix(pkgImport, prefix) {
			t.Fatalf("unexpected import path %q", pkgImport)
		}
		rel := strings.TrimPrefix(pkgImport, prefix)
		pkgDir := filepath.Join(root, "tools", filepath.FromSlash(rel))
		info, err := os.Stat(pkgDir)
		if err != nil || !info.IsDir() {
			t.Fatalf("%s: directory %s not found", pkgImport, pkgDir)
		}
		testFiles, err := filepath.Glob(filepath.Join(pkgDir, "*_test.go"))
		if err != nil || len(testFiles) == 0 {
			t.Fatalf("%s: no *_test.go files", pkgImport)
		}
		readsEnv := false
		for _, tf := range testFiles {
			raw, rerr := os.ReadFile(tf)
			if rerr == nil && strings.Contains(string(raw), "TILLR_GATE_EDGE_INPUTS") {
				readsEnv = true
				break
			}
		}
		if !readsEnv {
			t.Fatalf("%s: no *_test.go reads TILLR_GATE_EDGE_INPUTS", pkgImport)
		}

		readRules := make(map[string]bool, len(spec.Reads))
		for _, rule := range spec.Reads {
			readRules[rule] = true
			if strings.HasSuffix(rule, "/") {
				p := filepath.Join(root, filepath.FromSlash(strings.TrimRight(rule, "/")))
				if st, serr := os.Stat(p); serr != nil || !st.IsDir() {
					t.Fatalf("%s: directory reads rule %q missing on disk", pkgImport, rule)
				}
			} else {
				p := filepath.Join(root, filepath.FromSlash(rule))
				if st, serr := os.Stat(p); serr != nil || st.IsDir() {
					t.Fatalf("%s: file reads rule %q missing on disk", pkgImport, rule)
				}
			}
		}

		pkgTests, err := goaffected.ScanPackageTests(root, "tools/"+rel, nil)
		if err != nil {
			t.Fatalf("ScanPackageTests(%s): %v", pkgImport, err)
		}
		pkgTestSet := make(map[string]bool, len(pkgTests))
		for _, n := range pkgTests {
			pkgTestSet[n] = true
		}
		buildRules := make(map[string]bool, len(spec.Builds))
		for _, b := range spec.Builds {
			buildRules[b] = true
		}
		for _, check := range []struct {
			mapKey      string
			scopedTests map[string][]string
			allowed     map[string]bool
		}{
			{"build_tests", spec.BuildTests, buildRules},
			{"read_tests", spec.ReadTests, readRules},
		} {
			for rule, names := range check.scopedTests {
				if !check.allowed[rule] {
					t.Fatalf("%s: %s key %q missing from declaration list", pkgImport, check.mapKey, rule)
				}
				if len(names) == 0 {
					t.Fatalf("%s: %s[%q] must be non-empty", pkgImport, check.mapKey, rule)
				}
				if slices.Contains(names, "TestEdgeInputs") {
					t.Fatalf("%s: omit TestEdgeInputs from %s[%q]", pkgImport, check.mapKey, rule)
				}
				for _, n := range names {
					if !pkgTestSet[n] {
						t.Fatalf("%s: %s[%q] names test %q absent from package", pkgImport, check.mapKey, rule, n)
					}
				}
			}
		}
	}

	// LoadEdges refuses invalid build_tests and read_tests declarations.
	badCases := []string{
		`{"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": ["bad"]}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"other.py": ["TestX"]}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": []}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": [123]}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"reads": ["a.py"], "read_tests": {"a.py": [""]}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": ["bad"]}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/b": ["TestX"]}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": []}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": [123]}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": [""]}}}`,
		`{"github.com/mschulkind-oss/tillr-gate": {"builds": ["example.com/a"], "build_tests": {"example.com/a": [" TestX "]}}}`,
	}
	for _, bad := range badCases {
		badRepo := filepath.Join(scratchRoot(t), "bad-edges-"+freshNonce(t))
		if err := os.MkdirAll(filepath.Join(badRepo, "tools", "gate"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(badRepo, "tools", "gate", "go-test-edges.json"), []byte(bad), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := goaffected.LoadEdges(badRepo); err == nil {
			t.Fatalf("LoadEdges accepted invalid read_tests: %s", bad)
		}
	}
}

func TestEdgeCacheInvalidation(t *testing.T) {
	tmp := filepath.Join(scratchRoot(t), "go-affected-cache-"+freshNonce(t))
	repo := filepath.Join(tmp, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-sib"),
		filepath.Join(repo, "tools", "tillr-consumer"),
		filepath.Join(repo, "tools", "gate"),
		filepath.Join(repo, "config", "watch"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-sib", "go.mod"),
		[]byte("module github.com/mschulkind-oss/tillr-sib\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sibMain := filepath.Join(repo, "tools", "tillr-sib", "main.go")
	if err := os.WriteFile(sibMain,
		[]byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	nonce := freshNonce(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-consumer", "go.mod"),
		[]byte("module github.com/mschulkind-oss/tillr-consumer\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	consumerTest := `package main
import (
  "os"
  "os/exec"
  "path/filepath"
  "strings"
  "testing"
)
func TestEdgeInputs(t *testing.T) {
  if t.Name() == "` + nonce + `" { t.Fatal() }
  _ = os.Getenv("TILLR_GATE_EDGE_INPUTS")
}
func TestBuildsEdge(t *testing.T) {
  bin := filepath.Join(t.TempDir(), "sib")
  b := exec.Command("go", "buil"+"d", "-o", bin, ".")
  b.Dir = "../tillr-sib"
  if out, err := b.CombinedOutput(); err != nil { t.Fatalf("build: %v: %s", err, out) }
  out, err := exec.Command(bin).CombinedOutput()
  if err != nil { t.Fatalf("run: %v: %s", err, out) }
  if strings.TrimSpace(string(out)) != "ok" { t.Fatalf("got %q", out) }
}
func TestReadsEdge(t *testing.T) {
  raw, err := os.ReadFile("../../config/watch/rule.txt")
  if err != nil { t.Fatal(err) }
  if strings.TrimSpace(string(raw)) != "allow" { t.Fatalf("got %q", raw) }
}
`
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-consumer", "consumer_test.go"),
		[]byte(consumerTest), 0o644); err != nil {
		t.Fatal(err)
	}
	ruleFile := filepath.Join(repo, "config", "watch", "rule.txt")
	if err := os.WriteFile(ruleFile, []byte("allow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edgesJSON := `{
  "github.com/mschulkind-oss/tillr-consumer": {
    "builds": ["github.com/mschulkind-oss/tillr-sib"],
    "reads": ["config/watch/"]
  }
}`
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"),
		[]byte(edgesJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	evFile := filepath.Join(tmp, "evidence.json")
	durable := filepath.Join(tmp, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", sharedGocache(t))
	t.Setenv("YOLO_DURABLE_DIR", durable)

	runOnce := func(changed []string) (int, string, *goaffected.Evidence) {
		sel, err := goaffected.SelectAffectedPackages(repo, changed, nil, nil, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages: %v", err)
		}
		rc, out, ev, err := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{
			EvidencePath:    evFile,
			DisableProgress: true,
		})
		if err != nil {
			t.Fatalf("RunSelectedTests: %v", err)
		}
		return rc, out, ev
	}

	// 1. Warm run via builds edge.
	rc, warmOut, ev := runOnce([]string{"tools/tillr-sib/main.go"})
	if rc != 0 || len(ev.Packages) != 1 || ev.Packages[0].Cached {
		t.Fatalf("warm run: rc=%d ev=%+v\nout:\n%s", rc, ev, warmOut)
	}

	// 2. Second run with same inputs -> cached hit!
	rc, _, ev = runOnce([]string{"tools/tillr-sib/main.go"})
	if rc != 0 || !ev.Packages[0].Cached {
		t.Fatalf("cached run: rc=%d ev=%+v", rc, ev)
	}

	// 3. Break sibling main -> digest changes -> cache miss, test fails, evidence + triage case recorded!
	if err := os.WriteFile(sibMain,
		[]byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"broken\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, ev := runOnce([]string{"tools/tillr-sib/main.go"})
	if rc == 0 || ev.Packages[0].Cached {
		t.Fatalf("expected failure and uncached: rc=%d ev=%+v", rc, ev)
	}
	rawEv, err := os.ReadFile(evFile)
	if err != nil {
		t.Fatalf("ReadFile(evFile): %v", err)
	}
	var writtenEv goaffected.Evidence
	if err := json.Unmarshal(rawEv, &writtenEv); err != nil {
		t.Fatalf("Unmarshal(evFile): %v", err)
	}
	if !reflect.DeepEqual(&writtenEv, ev) {
		t.Fatalf("written evidence mismatch:\ngot:  %+v\nwant: %+v", writtenEv, ev)
	}
	cases := goFailureCases(out)
	if !slices.ContainsFunc(cases, func(c goFailureCase) bool {
		return c.Name == "TestBuildsEdge" && c.File == "consumer_test.go" && c.Package == "github.com/mschulkind-oss/tillr-consumer"
	}) {
		t.Fatalf("expected TestBuildsEdge in cases=%+v\nout:\n%s", cases, out)
	}

	// 4. Restore sibling main -> passes, uncached first, then cached!
	if err := os.WriteFile(sibMain,
		[]byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"ok\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, _ = runOnce([]string{"tools/tillr-sib/main.go"})
	if rc != 0 {
		t.Fatalf("restore run failed: rc=%d", rc)
	}
	rc, _, ev = runOnce([]string{"tools/tillr-sib/main.go"})
	if rc != 0 || !ev.Packages[0].Cached {
		t.Fatalf("post-restore cached run: rc=%d ev=%+v", rc, ev)
	}

	// 5. Break reads edge input -> digest changes -> cache miss and test fails!
	if err := os.WriteFile(ruleFile, []byte("deny\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, out, ev = runOnce([]string{"config/watch/rule.txt"})
	if rc == 0 || ev.Packages[0].Cached {
		t.Fatalf("reads break: rc=%d ev=%+v", rc, ev)
	}
	cases = goFailureCases(out)
	if !slices.ContainsFunc(cases, func(c goFailureCase) bool {
		return c.Name == "TestReadsEdge" && c.File == "consumer_test.go" && c.Package == "github.com/mschulkind-oss/tillr-consumer"
	}) {
		t.Fatalf("expected TestReadsEdge in cases=%+v\nout:\n%s", cases, out)
	}
}

func TestFailedModuleFloorFallback(t *testing.T) {
	tmp := filepath.Join(scratchRoot(t), "go-affected-fail-"+freshNonce(t))
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "tools", "tillr-broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tools", "gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-broken", "go.mod"),
		[]byte("module github.com/mschulkind-oss/tillr-broken\n\nbad_directive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-broken", "main.go"),
		[]byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sel, err := goaffected.SelectAffectedPackages(repo, []string{"tools/tillr-broken/main.go"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages: %v", err)
	}
	if !slices.Contains(sel.FloorModules, "tools/tillr-broken") {
		t.Fatalf("expected tools/tillr-broken in floor_modules, got %v", sel.FloorModules)
	}
	if !slices.ContainsFunc(sel.Selected, func(p goaffected.SelectedPackage) bool {
		return p.Module == "tools/tillr-broken" && p.ImportPath == "./..." && p.Reason == "floor"
	}) {
		t.Fatalf("expected ./... floor entry in %+v", sel.Selected)
	}

	t.Setenv("GOCACHE", sharedGocache(t))
	t.Setenv("YOLO_DURABLE_DIR", filepath.Join(tmp, "durable"))
	rc, out, _, err := goaffected.RunSelectedTests(repo, sel, goaffected.RunOptions{DisableProgress: true})
	if err != nil {
		t.Fatalf("RunSelectedTests: %v", err)
	}
	if rc == 0 {
		t.Fatalf("expected nonzero rc on broken module, got 0:\n%s", out)
	}
}

func TestCheckDeclarationsCoverSelectionInputs(t *testing.T) {
	root := repoRoot(t)
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	rawDecl, err := os.ReadFile(filepath.Join(checksDir, "go-affected.json"))
	if err != nil {
		t.Fatalf("read go-affected.json: %v", err)
	}
	var decl struct {
		ID      string   `json:"id"`
		Scope   string   `json:"scope"`
		Tier    string   `json:"tier"`
		Tools   []string `json:"tools"`
		Command []string `json:"command"`
		Reads   []string `json:"reads"`
	}
	if err := json.Unmarshal(rawDecl, &decl); err != nil {
		t.Fatalf("unmarshal go-affected.json: %v", err)
	}
	if decl.ID != "go-affected" || decl.Scope != "change" || decl.Tier != "heavy" {
		t.Fatalf("unexpected decl metadata: %+v", decl)
	}
	for _, tool := range []string{"sh", "go", "git"} {
		if !slices.Contains(decl.Tools, tool) {
			t.Fatalf("go-affected.json tools %v missing %s", decl.Tools, tool)
		}
	}
	if len(decl.Command) < 3 || decl.Command[0] != "sh" || decl.Command[1] != "-c" {
		t.Fatalf("unexpected command: %v", decl.Command)
	}
	isGoRun := strings.Contains(decl.Command[2], "go run -C tools/tillr-gate . go-affected") &&
		strings.Contains(decl.Command[2], `--root "$PWD"`)
	if !isGoRun ||
		!strings.Contains(decl.Command[2], `--base "$1"`) ||
		!strings.Contains(decl.Command[2], `--head "$2"`) ||
		strings.Contains(decl.Command[2], "--drop-module") {
		t.Fatalf("unexpected command[2]: %q", decl.Command[2])
	}

	sortedUnique := slices.Clone(decl.Reads)
	sort.Strings(sortedUnique)
	sortedUnique = slices.Compact(sortedUnique)
	if !reflect.DeepEqual(decl.Reads, sortedUnique) {
		t.Fatalf("go-affected.json reads must be sorted and unique")
	}
	for _, rule := range decl.Reads {
		if strings.HasSuffix(rule, "/") {
			p := filepath.Join(root, filepath.FromSlash(strings.TrimRight(rule, "/")))
			if st, serr := os.Stat(p); serr != nil || !st.IsDir() {
				t.Fatalf("go-affected.json reads %q missing dir on disk", rule)
			}
		} else {
			p := filepath.Join(root, filepath.FromSlash(rule))
			if st, serr := os.Stat(p); serr != nil || st.IsDir() {
				t.Fatalf("go-affected.json reads %q missing file on disk", rule)
			}
		}
	}

	covered := func(p string, rules []string) bool {
		for _, r := range rules {
			if goaffected.PathMatchesRead(p, r) {
				return true
			}
		}
		return false
	}

	edges, err := goaffected.LoadEdges(root)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	modules, err := goaffected.DiscoverTillrModules(root)
	if err != nil {
		t.Fatalf("DiscoverTillrModules: %v", err)
	}
	selectionInputs := []string{
		"tools/gate/go-test-edges.json",
		"tools/gate/checks/go-affected.json",
	}
	for _, mod := range modules {
		selectionInputs = append(selectionInputs, mod+"/go.mod", mod+"/synthetic_trigger_test.go")
	}
	for _, entry := range edges {
		selectionInputs = append(selectionInputs, entry.Reads...)
	}
	for _, p := range selectionInputs {
		if !covered(p, decl.Reads) {
			t.Fatalf("go-affected.json reads do not cover %s", p)
		}
	}
	for _, outside := range []string{
		"README.md",
		"tools/dotmod/main_test.go",
		"yolo-packs/tillr/files/local/share/tillr/tillr-drain/drain/gate_timing_test.go",
	} {
		if covered(outside, decl.Reads) {
			t.Fatalf("go-affected.json reads cover %s", outside)
		}
	}

	if _, serr := os.Stat(filepath.Join(checksDir, "go-affected-test.json")); !os.IsNotExist(serr) {
		t.Fatalf("go-affected-test.json should be deleted, got stat err=%v", serr)
	}
	{
		rawSuite, rerr := os.ReadFile(filepath.Join(checksDir, "go-test-tillr-gate.json"))
		if rerr != nil {
			t.Fatalf("read go-test-tillr-gate.json: %v", rerr)
		}
		var suiteDecl struct {
			ID    string   `json:"id"`
			Scope string   `json:"scope"`
			Tier  string   `json:"tier"`
			Reads []string `json:"reads"`
		}
		if uerr := json.Unmarshal(rawSuite, &suiteDecl); uerr != nil {
			t.Fatalf("unmarshal go-test-tillr-gate.json: %v", uerr)
		}
		if suiteDecl.ID != "go-test-tillr-gate" || suiteDecl.Scope != "tree" || suiteDecl.Tier != "heavy" {
			t.Fatalf("unexpected go-test-tillr-gate.json metadata: %+v", suiteDecl)
		}
		suiteInputs := []string{
			"tools/tillr-gate/goaffected/selection_test.go",
			"tools/gate/go-test-edges.json",
		}
		for pkg, entry := range edges {
			for _, imp := range append([]string{pkg}, entry.Builds...) {
				if strings.HasPrefix(imp, "github.com/mschulkind-oss/") {
					modName := strings.SplitN(strings.TrimPrefix(imp, "github.com/mschulkind-oss/"), "/", 2)[0]
					suiteInputs = append(suiteInputs, "tools/"+modName+"/go.mod")
				}
			}
		}
		for _, p := range suiteInputs {
			if !covered(p, suiteDecl.Reads) {
				t.Fatalf("go-test-tillr-gate.json reads do not cover %s", p)
			}
		}
	}

	// After-merge proof for deferred packages (D-GR53, D-GR54): go-test-tillr-drain runs in
	// the heavy tree tier and its reads cover every builds module directory and every reads
	// rule declared for DeferredProofPackages in go-test-edges.json.
	{
		rawDrainDecl, derr := os.ReadFile(filepath.Join(checksDir, "go-test-tillr-drain.json"))
		if derr != nil {
			t.Fatalf("read go-test-tillr-drain.json: %v", derr)
		}
		var drainDecl struct {
			Scope string   `json:"scope"`
			Tier  string   `json:"tier"`
			Reads []string `json:"reads"`
		}
		if uerr := json.Unmarshal(rawDrainDecl, &drainDecl); uerr != nil {
			t.Fatalf("unmarshal go-test-tillr-drain.json: %v", uerr)
		}
		if drainDecl.Scope != "tree" || drainDecl.Tier != "heavy" {
			t.Fatalf("unexpected go-test-tillr-drain.json metadata: %+v", drainDecl)
		}
		sortedDrainReads := slices.Clone(drainDecl.Reads)
		sort.Strings(sortedDrainReads)
		sortedDrainReads = slices.Compact(sortedDrainReads)
		if !reflect.DeepEqual(drainDecl.Reads, sortedDrainReads) {
			t.Fatalf("go-test-tillr-drain.json reads must be sorted and unique")
		}
		for deferredPkg := range goaffected.DeferredProofPackages {
			edge := edges[deferredPkg]
			for _, target := range edge.Builds {
				parts := strings.Split(target, "/")
				if len(parts) < 3 {
					continue
				}
				probePath := "tools/" + parts[2] + "/main.go"
				if !covered(probePath, drainDecl.Reads) {
					t.Fatalf("go-test-tillr-drain.json reads do not cover builds target %s (%s)", target, probePath)
				}
			}
			for _, rule := range edge.Reads {
				if !covered(rule, drainDecl.Reads) {
					t.Fatalf("go-test-tillr-drain.json reads do not cover reads rule %q of %s", rule, deferredPkg)
				}
			}
		}
	}

	// Run the declared command against a synthetic two-commit repo with tools/tillr and tools/tillr-gate staged.
	tmp := filepath.Join(scratchRoot(t), "go-affected-decl-"+freshNonce(t))
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "tools", "tillr-watch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "tools", "gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	stageNonTestModule := func(modRel string) {
		srcMod := filepath.Join(root, filepath.FromSlash(modRel))
		dstMod := filepath.Join(repo, filepath.FromSlash(modRel))
		werr := filepath.WalkDir(srcMod, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := d.Name()
			if d.IsDir() {
				if name != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
					return filepath.SkipDir
				}
				return nil
			}
			isNonTestGo := strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
			if !isNonTestGo && name != "go.mod" && name != "go.sum" && name != "landing.json" {
				return nil
			}
			rel, rerr := filepath.Rel(srcMod, path)
			if rerr != nil {
				return rerr
			}
			dstPath := filepath.Join(dstMod, rel)
			if merr := os.MkdirAll(filepath.Dir(dstPath), 0o755); merr != nil {
				return merr
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			return os.WriteFile(dstPath, data, 0o644)
		})
		if werr != nil {
			t.Fatalf("stage %s: %v", modRel, werr)
		}
	}
	stageNonTestModule("tools/tillr")
	stageNonTestModule("tools/tillr-gate")
	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-watch", "go.mod"),
		[]byte("module github.com/mschulkind-oss/tillr-watch\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nonce := freshNonce(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-watch", "main.go"),
		[]byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-watch", "main_test.go"),
		[]byte("package main\n\nimport \"testing\"\n\nfunc TestWatch(t *testing.T) { if t.Name() == \""+nonce+"\" { t.Fatal() } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "base")
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-watch", "main.go"),
		[]byte("package main\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "change")

	args := append(slices.Clone(decl.Command[1:]), "HEAD~1", "HEAD")
	cmd := exec.Command(decl.Command[0], args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GOCACHE="+sharedGocache(t),
		"YOLO_DURABLE_DIR="+filepath.Join(tmp, "durable"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("declared command failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "go-affected: 1 package(s)") {
		t.Fatalf("expected 'go-affected: 1 package(s)' in output:\n%s", out)
	}
}

func TestHeavyFailuresReplay(t *testing.T) {
	root := repoRoot(t)
	rootGraph := getRootGraph(t)

	cases := []struct {
		sha         string
		expectedPkg string
	}{
		{"9628978cfc", "github.com/mschulkind-oss/tillr-view"},
		{"9cadb24d06", ""},
		{"129509992c", "github.com/mschulkind-oss/tillr-drain"},
		{"68fb25f77d", "github.com/mschulkind-oss/tillr-view"},
		{"315a213fe4", "github.com/mschulkind-oss/tillr-drain"},
		{"a0b415fc3d", "github.com/mschulkind-oss/tillr-view"},
		{"9298e19241", "github.com/mschulkind-oss/tillr-drain/drain"},
		{"cc31e10d8b", ""},
		{"2838315c85", ""},
		{"dcd11d187a", "github.com/mschulkind-oss/tillr-drain"},
		{"9434198857", ""},
		{"5514f6eda3", "github.com/mschulkind-oss/tillr-run/run"},
		{"a427c165a3", "github.com/mschulkind-oss/tillr-item"},
	}

	probe := exec.Command("git", "-C", root, "cat-file", "-e", cases[0].sha+"^{commit}")
	if err := probe.Run(); err != nil {
		t.Skip("shallow or synthetic fixture clone without historical trunk commits")
	}

	goHits := 0
	deferredHits := 0
	for _, c := range cases {
		changed, err := goaffected.GitChangedPaths(root, c.sha+"~1", c.sha)
		if err != nil {
			t.Fatalf("GitChangedPaths(%s): %v", c.sha, err)
		}
		sel, err := goaffected.SelectAffectedPackages(root, changed, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", c.sha, err)
		}
		if c.expectedPkg != "" {
			found := slices.ContainsFunc(sel.Selected, func(p goaffected.SelectedPackage) bool {
				return p.ImportPath == c.expectedPkg
			})
			if goaffected.DeferredProofPackages[c.expectedPkg] {
				if found {
					t.Fatalf("%s: deferred %s should not be selected pre-merge in %+v", c.sha, c.expectedPkg, sel.Selected)
				}
				selAll, err := goaffected.SelectAffectedPackagesWithDrop(root, changed, nil, map[string]bool{}, rootGraph, nil)
				if err != nil {
					t.Fatalf("SelectAffectedPackagesWithDrop(%s): %v", c.sha, err)
				}
				foundAll := slices.ContainsFunc(selAll.Selected, func(p goaffected.SelectedPackage) bool {
					return p.ImportPath == c.expectedPkg
				})
				if !foundAll {
					t.Fatalf("%s (dropPackages={}): expected %s in %+v", c.sha, c.expectedPkg, selAll.Selected)
				}
				deferredHits++
			} else {
				if !found {
					t.Fatalf("%s: expected %s in %+v", c.sha, c.expectedPkg, sel.Selected)
				}
				goHits++
			}
		}
	}
	if goHits != 8 {
		t.Fatalf("goHits = %d, want 8", goHits)
	}
	if deferredHits != 1 {
		t.Fatalf("deferredHits = %d, want 1", deferredHits)
	}
}

func TestFailureRefusalNamesPackagesAndTestsFarAboveTail(t *testing.T) {
	pkgMain := "github.com/mschulkind-oss/tillr-drain"
	pkgDrain := "github.com/mschulkind-oss/tillr-drain/drain"
	pkgBroken := "github.com/mschulkind-oss/tillr-broken"

	events := []map[string]any{
		{"Action": "run", "Package": pkgMain, "Test": "TestEarlyMainFailure"},
		{"Action": "output", "Package": pkgMain, "Test": "TestEarlyMainFailure",
			"Output": "=== RUN   TestEarlyMainFailure\n"},
		{"Action": "output", "Package": pkgMain, "Test": "TestEarlyMainFailure",
			"Output": "    hook_test.go:42: expected directory match\n"},
		{"Action": "output", "Package": pkgMain, "Test": "TestEarlyMainFailure",
			"Output": "--- FAIL: TestEarlyMainFailure (0.12s)\n"},
		{"Action": "fail", "Package": pkgMain, "Test": "TestEarlyMainFailure", "Elapsed": 0.12},
		{"Action": "run", "Package": pkgMain, "Test": "TestEdgeInputs"},
		{"Action": "output", "Package": pkgMain, "Test": "TestEdgeInputs",
			"Output": "    edge_inputs_test.go:10: main edge mismatch\n"},
		{"Action": "output", "Package": pkgMain, "Test": "TestEdgeInputs",
			"Output": "--- FAIL: TestEdgeInputs (0.01s)\n"},
		{"Action": "fail", "Package": pkgMain, "Test": "TestEdgeInputs", "Elapsed": 0.01},

		{"Action": "run", "Package": pkgDrain, "Test": "TestEarlyDrainFailure"},
		{"Action": "output", "Package": pkgDrain, "Test": "TestEarlyDrainFailure",
			"Output": "    fixer_test.go:99: expected named check\n"},
		{"Action": "output", "Package": pkgDrain, "Test": "TestEarlyDrainFailure",
			"Output": "--- FAIL: TestEarlyDrainFailure (0.25s)\n"},
		{"Action": "fail", "Package": pkgDrain, "Test": "TestEarlyDrainFailure", "Elapsed": 0.25},
		{"Action": "run", "Package": pkgDrain, "Test": "TestEdgeInputs"},
		{"Action": "output", "Package": pkgDrain, "Test": "TestEdgeInputs",
			"Output": "    edge_inputs_test.go:10: drain edge mismatch\n"},
		{"Action": "output", "Package": pkgDrain, "Test": "TestEdgeInputs",
			"Output": "--- FAIL: TestEdgeInputs (0.01s)\n"},
		{"Action": "fail", "Package": pkgDrain, "Test": "TestEdgeInputs", "Elapsed": 0.01},

		{"Action": "run", "Package": pkgDrain, "Test": "TestSubParent"},
		{"Action": "run", "Package": pkgDrain, "Test": "TestSubParent/leaf"},
		{"Action": "output", "Package": pkgDrain, "Test": "TestSubParent/leaf",
			"Output": "        sub_test.go:15: leaf failed\n"},
		{"Action": "output", "Package": pkgDrain, "Test": "TestSubParent/leaf",
			"Output": "    --- FAIL: TestSubParent/leaf (0.01s)\n"},
		{"Action": "fail", "Package": pkgDrain, "Test": "TestSubParent/leaf", "Elapsed": 0.01},
		{"Action": "output", "Package": pkgDrain, "Test": "TestSubParent",
			"Output": "--- FAIL: TestSubParent (0.02s)\n"},
		{"Action": "fail", "Package": pkgDrain, "Test": "TestSubParent", "Elapsed": 0.02},
	}

	for i := 0; i < 120; i++ {
		events = append(events,
			map[string]any{"Action": "output", "Package": pkgMain, "Output": "main trailing log line " + strconv.Itoa(i) + "\n"},
			map[string]any{"Action": "output", "Package": pkgDrain, "Output": "drain trailing log line " + strconv.Itoa(i) + "\n"},
		)
	}
	events = append(events,
		map[string]any{"Action": "output", "Package": pkgMain, "Output": "FAIL\t" + pkgMain + "\t1.23s\n"},
		map[string]any{"Action": "fail", "Package": pkgMain, "Elapsed": 1.23},
		map[string]any{"Action": "output", "Package": pkgDrain, "Output": "FAIL\t" + pkgDrain + "\t2.34s\n"},
		map[string]any{"Action": "fail", "Package": pkgDrain, "Elapsed": 2.34},
		map[string]any{"Action": "output", "Package": pkgBroken, "Output": "FAIL\t" + pkgBroken + " [setup failed]\n"},
		map[string]any{"Action": "fail", "Package": pkgBroken, "Elapsed": 0.01},
	)

	var sb strings.Builder
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(raw)
		sb.WriteByte('\n')
	}

	parsed, readable := goaffected.ParseGoTestJSON(sb.String())
	if !reflect.DeepEqual(parsed[pkgMain].FailedTests, []string{"TestEarlyMainFailure", "TestEdgeInputs"}) {
		t.Fatalf("pkgMain failed_tests = %v", parsed[pkgMain].FailedTests)
	}
	if !reflect.DeepEqual(parsed[pkgDrain].FailedTests, []string{"TestEarlyDrainFailure", "TestEdgeInputs", "TestSubParent/leaf"}) {
		t.Fatalf("pkgDrain failed_tests = %v", parsed[pkgDrain].FailedTests)
	}
	if len(parsed[pkgBroken].FailedTests) != 0 || parsed[pkgBroken].Passed == nil || *parsed[pkgBroken].Passed {
		t.Fatalf("pkgBroken = %+v", parsed[pkgBroken])
	}

	summary := goaffected.FormatFailureSummary([]goaffected.FailedPackage{
		{Package: pkgMain, Tests: parsed[pkgMain].FailedTests},
		{Package: pkgDrain, Tests: parsed[pkgDrain].FailedTests},
		{Package: pkgBroken, Tests: parsed[pkgBroken].FailedTests},
	})
	fullOutput := "go-affected: 3 package(s) (0 cached, selection 10ms, slot_wait 0ms)\n" + readable + summary
	allLines := strings.Split(strings.TrimSuffix(fullOutput, "\n"), "\n")
	if len(allLines) <= excerptLines*3 {
		t.Fatalf("expected > %d lines, got %d", excerptLines*3, len(allLines))
	}
	earlyIdx := -1
	for i, ln := range allLines {
		if strings.Contains(ln, "TestEarlyMainFailure") {
			earlyIdx = i
			break
		}
	}
	if earlyIdx < 0 || len(allLines)-earlyIdx <= excerptLines*2 {
		t.Fatalf("earlyIdx=%d len=%d", earlyIdx, len(allLines))
	}

	tailExcerpt := strings.Join(allLines[len(allLines)-excerptLines:], "\n")
	for _, needle := range []string{
		"go-affected: failing packages and tests:",
		"  " + pkgMain + ": TestEarlyMainFailure, TestEdgeInputs",
		"  " + pkgDrain + ": TestEarlyDrainFailure, TestEdgeInputs, TestSubParent/leaf",
		"  " + pkgBroken + ": (no failing test named)",
	} {
		if !strings.Contains(tailExcerpt, needle) {
			t.Fatalf("tail excerpt missing %q:\n%s", needle, tailExcerpt)
		}
	}

	cases := goFailureCases(fullOutput)
	for _, want := range []goFailureCase{
		{Name: "TestEarlyMainFailure", File: "hook_test.go", Line: 42},
		{Name: "TestEdgeInputs", File: "edge_inputs_test.go", Line: 10},
		{Name: "TestEarlyDrainFailure", File: "fixer_test.go", Line: 99},
		{Name: "TestSubParent/leaf", File: "sub_test.go", Line: 15},
	} {
		if !slices.ContainsFunc(cases, func(c goFailureCase) bool {
			return c.Name == want.Name && c.File == want.File && c.Line == want.Line
		}) {
			t.Fatalf("missing failure case %+v in %+v", want, cases)
		}
	}
}

func TestDrainSubpackageIsolation(t *testing.T) {
	root := repoRoot(t)
	rootGraph := getRootGraph(t)

	subpkgs := []string{"workflow", "remotesync", "fixer", "heavyrun"}
	subpkgIPs := make(map[string]bool, len(subpkgs))
	for _, sub := range subpkgs {
		subpkgIPs["github.com/mschulkind-oss/tillr-drain/"+sub] = true
	}

	// 1. Changing drain production or test files defers tillr-drain/drain and selects none
	//    of the four extracted subpackages.
	for _, drainPath := range []string{
		"tools/tillr-drain/drain/loop.go",
		"tools/tillr-drain/drain/loop_test.go",
	} {
		sel, err := goaffected.SelectAffectedPackages(root, []string{drainPath}, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", drainPath, err)
		}
		for _, item := range sel.Selected {
			if item.ImportPath == "github.com/mschulkind-oss/tillr-drain/drain" {
				t.Fatalf("%s unexpectedly selected deferred tillr-drain/drain", drainPath)
			}
			if subpkgIPs[item.ImportPath] {
				t.Fatalf("%s unexpectedly selected %s", drainPath, item.ImportPath)
			}
		}
	}

	// 2. Changing a *_test.go file in any extracted subpackage selects ONLY that subpackage
	//    within tillr-drain (cross-module source-scanning tests in tools/tillr also run narrowed).
	subpkgTests := map[string]string{
		"workflow":   "tools/tillr-drain/workflow/workflow_test.go",
		"remotesync": "tools/tillr-drain/remotesync/remote_sync_test.go",
		"fixer":      "tools/tillr-drain/fixer/fixer_test.go",
		"heavyrun":   "tools/tillr-drain/heavyrun/heavy_ack_test.go",
	}
	for sub, testPath := range subpkgTests {
		sel, err := goaffected.SelectAffectedPackages(root, []string{testPath}, nil, rootGraph, nil)
		if err != nil {
			t.Fatalf("SelectAffectedPackages(%s): %v", testPath, err)
		}
		want := "github.com/mschulkind-oss/tillr-drain/" + sub
		var drainIPs []string
		for _, item := range sel.Selected {
			if strings.HasPrefix(item.ImportPath, "github.com/mschulkind-oss/tillr-drain") {
				drainIPs = append(drainIPs, item.ImportPath)
			}
		}
		if len(drainIPs) != 1 || drainIPs[0] != want {
			t.Fatalf("%s: got drainIPs=%v (all=%+v), want only %s", sub, drainIPs, sel.Selected, want)
		}
	}

	// 3. Changing workflow.go selects workflow and root tillr-drain (fast pre-merge packages),
	// and does NOT select deferred tillr-drain/drain, remotesync, fixer, heavyrun, or heavy.
	sel, err := goaffected.SelectAffectedPackages(root, []string{"tools/tillr-drain/workflow/workflow.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(workflow.go): %v", err)
	}
	selectedSet := make(map[string]bool, len(sel.Selected))
	for _, item := range sel.Selected {
		selectedSet[item.ImportPath] = true
	}
	for _, want := range []string{
		"github.com/mschulkind-oss/tillr-drain/workflow",
		"github.com/mschulkind-oss/tillr-drain",
	} {
		if !selectedSet[want] {
			t.Fatalf("workflow.go missing %s in %+v", want, sel.Selected)
		}
	}
	for _, forbidden := range []string{
		"github.com/mschulkind-oss/tillr-drain/drain",
		"github.com/mschulkind-oss/tillr-drain/remotesync",
		"github.com/mschulkind-oss/tillr-drain/fixer",
		"github.com/mschulkind-oss/tillr-drain/heavyrun",
		"github.com/mschulkind-oss/tillr-drain/heavy",
	} {
		if selectedSet[forbidden] {
			t.Fatalf("workflow.go unexpectedly selected %s", forbidden)
		}
	}
}

func TestMaxShardsScalesWithCPUCount(t *testing.T) {
	hostCPUs := runtime.NumCPU()
	expectedHost := 12
	if hostCPUs > 0 {
		expectedHost = 2 * hostCPUs
	}
	if got := goaffected.MaxShards(0); got != expectedHost {
		t.Fatalf("MaxShards(0) = %d, want %d (NumCPU=%d)", got, expectedHost, hostCPUs)
	}
	for _, cpus := range []int{1, 4, 8, 16, 32} {
		if got := goaffected.MaxShards(cpus); got != 2*cpus {
			t.Fatalf("MaxShards(%d) = %d, want %d", cpus, got, 2*cpus)
		}
	}
	// Negative cpuCount falls back to runtime.NumCPU().
	if got := goaffected.MaxShards(-1); got != expectedHost {
		t.Fatalf("MaxShards(-1) = %d, want %d", got, expectedHost)
	}

	// Shard count calculation scales past 12 up to MaxShards(cpus) for both unnarrowed and narrowed sets.
	shardCountUnnarrowed := func(totalTests, cpus int) int {
		sc := totalTests / 125
		if sc < goaffected.MinShards {
			sc = goaffected.MinShards
		}
		if maxS := goaffected.MaxShards(cpus); sc > maxS {
			sc = maxS
		}
		return sc
	}
	if got := shardCountUnnarrowed(2500, 8); got != 16 {
		t.Fatalf("unnarrowed(2500, 8) = %d, want 16", got)
	}
	if got := shardCountUnnarrowed(2500, 16); got != 20 {
		t.Fatalf("unnarrowed(2500, 16) = %d, want 20", got)
	}
	if got := shardCountUnnarrowed(5000, 16); got != 32 {
		t.Fatalf("unnarrowed(5000, 16) = %d, want 32", got)
	}

	shardCountNarrowed := func(estTotal float64, cpus int) int {
		sc := int((estTotal + goaffected.NarrowedShardDurationThreshold - 1e-9) / goaffected.NarrowedShardDurationThreshold)
		if sc < 2 {
			sc = 2
		}
		if maxS := goaffected.MaxShards(cpus); sc > maxS {
			sc = maxS
		}
		return sc
	}
	if got := shardCountNarrowed(500.0, 8); got != 16 {
		t.Fatalf("narrowed(500s, 8) = %d, want 16", got)
	}
	if got := shardCountNarrowed(500.0, 16); got != 20 {
		t.Fatalf("narrowed(500s, 16) = %d, want 20", got)
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := slices.Clone(a)
	sb := slices.Clone(b)
	slices.Sort(sa)
	slices.Sort(sb)
	return reflect.DeepEqual(sa, sb)
}

func TestCrossModuleSourceScanningEdges(t *testing.T) {
	root := repoRoot(t)
	rootGraph := getRootGraph(t)
	edges, err := goaffected.LoadEdges(root)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	modules, err := goaffected.DiscoverTillrModules(root)
	if err != nil {
		t.Fatalf("DiscoverTillrModules: %v", err)
	}
	const (
		defPkg    = "github.com/mschulkind-oss/tillr/definition"
		defTest   = "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers"
		storePkg  = "github.com/mschulkind-oss/tillr/store"
		storeTest = "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit"
	)
	if _, ok := edges[defPkg]; !ok {
		t.Fatalf("missing %s in edges", defPkg)
	}
	if _, ok := edges[storePkg]; !ok {
		t.Fatalf("missing %s in edges", storePkg)
	}

	for _, mod := range modules {
		rule := mod + "/"
		if !slices.Contains(edges[defPkg].Reads, rule) {
			t.Fatalf("%s missing %q in reads: %v", defPkg, rule, edges[defPkg].Reads)
		}
		if !slices.Contains(edges[defPkg].ReadTests[rule], defTest) {
			t.Fatalf("%s missing %s in read_tests[%q]: %v", defPkg, defTest, rule, edges[defPkg].ReadTests[rule])
		}
		sampleDir := mod
		if mod == "tools/tillr" {
			sampleDir = "tools/tillr/vantage"
		}
		for _, samplePath := range []string{
			sampleDir + "/synthetic_charter_probe_test.go",
			sampleDir + "/synthetic_charter_probe.go",
		} {
			sel, err := goaffected.SelectAffectedPackages(root, []string{samplePath}, nil, rootGraph, nil)
			if err != nil {
				t.Fatalf("SelectAffectedPackages(%s): %v", samplePath, err)
			}
			byPkg := make(map[string]goaffected.SelectedPackage, len(sel.Selected))
			for _, item := range sel.Selected {
				byPkg[item.ImportPath] = item
			}
			defItem, ok := byPkg[defPkg]
			if !ok {
				t.Fatalf("%s: missing %s in selection", samplePath, defPkg)
			}
			if defItem.Reason != "edge" {
				t.Fatalf("%s: %s reason=%q, want edge", samplePath, defPkg, defItem.Reason)
			}
			if !reflect.DeepEqual(defItem.SelectedTests, []string{defTest}) {
				t.Fatalf("%s: %s SelectedTests=%v, want [%s]", samplePath, defPkg, defItem.SelectedTests, defTest)
			}
			if mod != "tools/tillr" {
				if !slices.Contains(edges[storePkg].Reads, rule) {
					t.Fatalf("%s missing %q in reads: %v", storePkg, rule, edges[storePkg].Reads)
				}
				if !slices.Contains(edges[storePkg].ReadTests[rule], storeTest) {
					t.Fatalf("%s missing %s in read_tests[%q]: %v", storePkg, storeTest, rule, edges[storePkg].ReadTests[rule])
				}
				storeItem, ok := byPkg[storePkg]
				if !ok {
					t.Fatalf("%s: missing %s in selection", samplePath, storePkg)
				}
				if storeItem.Reason != "edge" {
					t.Fatalf("%s: %s reason=%q, want edge", samplePath, storePkg, storeItem.Reason)
				}
				if !reflect.DeepEqual(storeItem.SelectedTests, []string{storeTest}) {
					t.Fatalf("%s: %s SelectedTests=%v, want [%s]", samplePath, storePkg, storeItem.SelectedTests, storeTest)
				}
			}
		}
	}

	// TestClassificationScanGoSites in tools/tillr-gate walks tools/tillr-gate and tools/tillr-drain.
	const gatePkg = "github.com/mschulkind-oss/tillr-gate"
	const scanTest = "TestClassificationScanGoSites"
	if _, ok := edges[gatePkg]; !ok {
		t.Fatalf("missing %s in go-test-edges.json", gatePkg)
	}
	for _, rule := range []string{"tools/tillr-drain/", "tools/tillr-gate/"} {
		if !slices.Contains(edges[gatePkg].Reads, rule) {
			t.Fatalf("%s missing %q in reads: %v", gatePkg, rule, edges[gatePkg].Reads)
		}
		if !slices.Contains(edges[gatePkg].ReadTests[rule], scanTest) {
			t.Fatalf("%s missing %s in read_tests[%q]: %v", gatePkg, scanTest, rule, edges[gatePkg].ReadTests[rule])
		}
	}
	refusalSel, err := goaffected.SelectAffectedPackages(root, []string{"tools/tillr-drain/drain/refusal_cases.go"}, nil, rootGraph, nil)
	if err != nil {
		t.Fatalf("SelectAffectedPackages(refusal_cases.go): %v", err)
	}
	refusalByPkg := make(map[string]goaffected.SelectedPackage, len(refusalSel.Selected))
	for _, item := range refusalSel.Selected {
		refusalByPkg[item.ImportPath] = item
	}
	gateItem, ok := refusalByPkg[gatePkg]
	if !ok {
		t.Fatalf("missing %s in refusal_cases.go selection", gatePkg)
	}
	if gateItem.Reason != "edge" {
		t.Fatalf("%s reason=%q, want edge", gatePkg, gateItem.Reason)
	}
	if !reflect.DeepEqual(gateItem.SelectedTests, []string{scanTest}) {
		t.Fatalf("%s SelectedTests=%v, want [%s]", gatePkg, gateItem.SelectedTests, scanTest)
	}
}

func TestCandidateGateFailsOnUndeclaredGoModule(t *testing.T) {
	root := repoRoot(t)
	declPath := filepath.Join(root, "tools", "gate", "checks", "go-edges.json")
	declRaw, err := os.ReadFile(declPath)
	if err != nil {
		t.Fatalf("ReadFile(go-edges.json): %v", err)
	}
	var decl map[string]any
	if err := json.Unmarshal(declRaw, &decl); err != nil {
		t.Fatalf("Unmarshal(go-edges.json): %v", err)
	}
	if decl["id"] != "go-edges" || decl["scope"] != "tree" || decl["tier"] != "fast" {
		t.Fatalf("unexpected go-edges.json declaration: %v", decl)
	}

	tmpPath := filepath.Join(scratchRoot(t), "ga-cand-edges-"+freshNonce(t))
	if err := os.MkdirAll(tmpPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	runner := buildTillrGateBinary(t, filepath.Join(tmpPath, "tillr-gate"))

	repo := filepath.Join(tmpPath, "repo")
	checksDir := filepath.Join(repo, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyInto := func(src, dst string) {
		raw, rerr := os.ReadFile(src)
		if rerr != nil {
			t.Fatalf("ReadFile(%s): %v", src, rerr)
		}
		if werr := os.WriteFile(dst, raw, 0o755); werr != nil {
			t.Fatalf("WriteFile(%s): %v", dst, werr)
		}
	}
	copyInto(declPath, filepath.Join(checksDir, "go-edges.json"))
	copyInto(filepath.Join(root, "tools", "gate", "checks", "go-edges.py"), filepath.Join(checksDir, "go-edges.py"))

	for _, mod := range []struct {
		dir string
		ip  string
	}{
		{"tools/tillr", "github.com/mschulkind-oss/tillr"},
		{"tools/tillr-existing", "github.com/mschulkind-oss/tillr-existing"},
	} {
		d := filepath.Join(repo, filepath.FromSlash(mod.dir))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module "+mod.ip+"\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	const (
		defPkg    = "github.com/mschulkind-oss/tillr/definition"
		defTest   = "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers"
		storePkg  = "github.com/mschulkind-oss/tillr/store"
		storeTest = "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit"
	)
	edges := map[string]goaffected.EdgeSpec{
		defPkg: {
			Builds: []string{},
			Reads:  []string{"tools/tillr-existing/", "tools/tillr/"},
			ReadTests: map[string][]string{
				"tools/tillr-existing/": {defTest},
				"tools/tillr/":          {defTest},
			},
		},
		storePkg: {
			Builds: []string{},
			Reads:  []string{"tools/tillr-existing/"},
			ReadTests: map[string][]string{
				"tools/tillr-existing/": {storeTest},
			},
		},
	}
	edgesPath := filepath.Join(repo, "tools", "gate", "go-test-edges.json")
	writeEdges := func() {
		raw, merr := json.MarshalIndent(edges, "", "  ")
		if merr != nil {
			t.Fatal(merr)
		}
		if werr := os.WriteFile(edgesPath, append(raw, '\n'), 0o644); werr != nil {
			t.Fatal(werr)
		}
	}
	writeEdges()

	env := goaffected.BuildEnv()
	gitEnv := append(slices.Clone(env),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = gitEnv
		out, gerr := cmd.CombinedOutput()
		if gerr != nil {
			t.Fatalf("git %v: %v\n%s", args, gerr, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	git("init")
	git("add", ".")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")

	// Add a new Go module under tools/ without declaring it in go-test-edges.json.
	newModDir := filepath.Join(repo, "tools", "tillr-new")
	if err := os.MkdirAll(newModDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newModDir, "go.mod"), []byte("module github.com/mschulkind-oss/tillr-new\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "tools/tillr-new/go.mod")
	git("commit", "-m", "add undeclared module")
	headMissing := git("rev-parse", "HEAD")

	runGate := func(headRev, logsDir string) (int, map[string]map[string]any) {
		cmd := exec.Command(runner, "checks", "--fast-tree", "--scope-to-change",
			"--base", base, "--head", headRev, "--worktree", repo,
			"--checks", checksDir, "--logs", logsDir)
		cmd.Dir = repo
		cmd.Env = env
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		rerr := cmd.Run()
		rc := 0
		if rerr != nil {
			var exitErr *exec.ExitError
			if errors.As(rerr, &exitErr) {
				rc = exitErr.ExitCode()
			} else {
				rc = 1
			}
		}
		firstLine := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)[0]
		var verdict struct {
			Payload struct {
				Checks []map[string]any `json:"checks"`
			} `json:"payload"`
		}
		if jerr := json.Unmarshal([]byte(firstLine), &verdict); jerr != nil {
			t.Fatalf("parse verdict JSON (%v): stdout=%q stderr=%q", jerr, stdout.String(), stderr.String())
		}
		byID := make(map[string]map[string]any, len(verdict.Payload.Checks))
		for _, c := range verdict.Payload.Checks {
			if id, ok := c["check_id"].(string); ok {
				byID[id] = c
			}
		}
		return rc, byID
	}

	logsFail := filepath.Join(tmpPath, "logs-fail")
	rcFail, checksFail := runGate(headMissing, logsFail)
	if rcFail == 0 {
		t.Fatalf("expected non-zero rc for undeclared module, got 0")
	}
	if checksFail["go-edges"]["result"] != "fail" {
		t.Fatalf("expected go-edges fail, got %v", checksFail["go-edges"])
	}
	logFiles, _ := filepath.Glob(filepath.Join(logsFail, "*.log"))
	var logBuilder strings.Builder
	for _, lf := range logFiles {
		raw, _ := os.ReadFile(lf)
		logBuilder.Write(raw)
		logBuilder.WriteByte('\n')
	}
	logTexts := logBuilder.String()
	if !strings.Contains(logTexts, "tools/tillr-new/") || !strings.Contains(logTexts, "tools/gate/go-test-edges.json") {
		t.Fatalf("expected log to mention tools/tillr-new/ and go-test-edges.json, got:\n%s", logTexts)
	}

	// Now declare tools/tillr-new/ in both source-scanning packages; the candidate gate passes.
	eDef := edges[defPkg]
	eDef.Reads = append(eDef.Reads, "tools/tillr-new/")
	eDef.ReadTests["tools/tillr-new/"] = []string{defTest}
	edges[defPkg] = eDef
	eStore := edges[storePkg]
	eStore.Reads = append(eStore.Reads, "tools/tillr-new/")
	eStore.ReadTests["tools/tillr-new/"] = []string{storeTest}
	edges[storePkg] = eStore
	writeEdges()

	git("add", "tools/gate/go-test-edges.json")
	git("commit", "-m", "declare new module in edges")
	headDeclared := git("rev-parse", "HEAD")

	logsPass := filepath.Join(tmpPath, "logs-pass")
	rcPass, checksPass := runGate(headDeclared, logsPass)
	if rcPass != 0 {
		t.Fatalf("expected rc 0 after declaring module, got %d (%v)", rcPass, checksPass)
	}
	if checksPass["go-edges"]["result"] != "pass" {
		t.Fatalf("expected go-edges pass, got %v", checksPass["go-edges"])
	}
}
