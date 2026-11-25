package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func moduleFixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	root := initTempRepo(t)
	mod := filepath.Join(root, "tool")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture/tool\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFixture(t, root, "fixture")
	return root, mod
}

func commitFixture(t *testing.T, root, msg string) {
	t.Helper()
	for _, args := range [][]string{
		{"-C", root, "add", "."},
		{"-C", root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", msg},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestGoBuildModuleMemoryDetectsNestedAutoWorkspace(t *testing.T) {
	root, mod := moduleFixtureRepo(t)
	if err := os.WriteFile(filepath.Join(mod, "go.work"), []byte("go 1.24\nuse .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFixture(t, root, "workspace")
	t.Setenv("GOWORK", "")
	keys, _, err := moduleKeys(root, []string{mod}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if keys[mod].bound {
		t.Fatalf("a nested auto-discovered workspace was remembered")
	}
}

func TestGoBuildExternalReplacementsAndFileOptionsBuildWithoutMemory(t *testing.T) {
	root, mod := moduleFixtureRepo(t)
	external := t.TempDir()
	overlay := filepath.Join(external, "overlay.json")
	if err := os.WriteFile(overlay, []byte(`{"Replace":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	customWork := filepath.Join(root, "custom.work")
	if err := os.WriteFile(customWork, []byte("go 1.24\nuse ./tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flags     string
		workspace string
	}{
		{"-overlay=" + overlay, "off"},
		{"-gcflags=all=-N", "off"},
		{"", customWork},
	} {
		t.Setenv("GOFLAGS", tc.flags)
		t.Setenv("GOWORK", tc.workspace)
		keys, _, err := moduleKeys(root, []string{mod}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if keys[mod].bound {
			t.Fatalf("flags=%q workspace=%q: expected bound=false", tc.flags, tc.workspace)
		}
	}
}

func TestGoBuildModuleMemoryRebuildsLocalDependentsButNotUnrelatedModules(t *testing.T) {
	root := initTempRepo(t)
	mods := []string{filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")}
	for i, m := range mods {
		name := string(rune('a' + i))
		if err := os.MkdirAll(m, 0o755); err != nil {
			t.Fatal(err)
		}
		gomod := "module fixture/" + name + "\n\ngo 1.24\n"
		if name == "b" {
			gomod += "require fixture/a v0.0.0\nreplace fixture/a => ../a\n"
		}
		if err := os.WriteFile(filepath.Join(m, "go.mod"), []byte(gomod), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, root, "fixture")

	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	var mu sync.Mutex
	seen := make(map[string]bool)
	stubCmd := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && args[1] == "test" {
			mu.Lock()
			seen[filepath.Base(dir)] = true
			mu.Unlock()
		}
		return defaultGoCommand(args, dir, env)
	}

	if code := runGoBuild(mods, "", stubCmd); code != 0 {
		t.Fatalf("first run: code=%d", code)
	}
	if !seen["a"] || !seen["b"] || !seen["c"] {
		t.Fatalf("first run: seen=%v", seen)
	}

	seen = make(map[string]bool)
	if err := os.WriteFile(filepath.Join(mods[0], "main.go"), []byte("package main\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFixture(t, root, "edit a")
	if code := runGoBuild(mods, "", stubCmd); code != 0 {
		t.Fatalf("second run: code=%d", code)
	}
	if !seen["a"] || !seen["b"] || seen["c"] {
		t.Fatalf("after editing a: expected a,b only, got %v", seen)
	}

	seen = make(map[string]bool)
	trackFile(t, root, "docs/guide.md", "# Markdown only\n")
	commitFixture(t, root, "docs")
	if code := runGoBuild(mods, "", stubCmd); code != 0 {
		t.Fatalf("third run: code=%d", code)
	}
	if len(seen) != 0 {
		t.Fatalf("docs only: expected no builds, got %v", seen)
	}

	// Uncommitted source cannot consume a remembered pass from HEAD.
	if err := os.WriteFile(filepath.Join(mods[2], "main.go"), []byte("package main\nfunc main() { println(2) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runGoBuild(mods, "", stubCmd); code != 0 {
		t.Fatalf("dirty c run: code=%d", code)
	}
	if !seen["c"] || len(seen) != 1 {
		t.Fatalf("dirty c: expected c only, got %v", seen)
	}
}

func TestGoBuildCompilesTestFilesAndReportsMissingModules(t *testing.T) {
	root := initTempRepo(t)
	mod := filepath.Join(root, "tool")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture/tool\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main_test.go"), []byte("package main\nvar _ = undefinedTestSymbol\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, output, err := buildModule(mod, t.TempDir(), nil, nil, nil)
	if err != nil || code == 0 || !strings.Contains(output, "undefinedTestSymbol") {
		t.Fatalf("code=%d err=%v out=%s", code, err, output)
	}
	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	if code := runGoBuild([]string{filepath.Join(root, "missing")}, "", nil); code != 4 {
		t.Fatalf("missing module: code=%d want=4", code)
	}
}

func TestGoBuildDoesNotExecuteTestMain(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "tool")
	marker := filepath.Join(root, "testmain-ran")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture/tool\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markerJSON, _ := json.Marshal(marker)
	testSrc := "package main\nimport (\"os\"; \"testing\")\nfunc TestMain(m *testing.M) { os.WriteFile(" + string(markerJSON) + ", []byte(\"ran\"), 0600); os.Exit(m.Run()) }\n"
	if err := os.WriteFile(filepath.Join(mod, "main_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, output, err := buildModule(mod, t.TempDir(), nil, nil, nil)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v out=%s", code, err, output)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("a compile check ran TestMain")
	}
}

func TestGoBuildCatchesSourceCompileError(t *testing.T) {
	mod := filepath.Join(t.TempDir(), "tool")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture/tool\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\nfunc main() { undefinedSourceSymbol() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, output, err := buildModule(mod, t.TempDir(), nil, nil, nil)
	if err != nil || code == 0 || !strings.Contains(output, "undefinedSourceSymbol") {
		t.Fatalf("code=%d err=%v out=%s", code, err, output)
	}
}

func TestGoBuildTrimsPathsAndLinksOnlyMainPackages(t *testing.T) {
	mod := filepath.Join(t.TempDir(), "tool")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", "-mod=mod")
	var calls [][]string
	var envs [][]string
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		calls = append(calls, args)
		envs = append(envs, env)
		if args[1] == "list" {
			return 0, "M\tfixture/tool/cmd/a\n\tfixture/tool/lib\nM\tfixture/tool/cmd/b\n", "", nil
		}
		return 0, "", "", nil
	}
	code, _, err := buildModule(mod, t.TempDir(), stub, nil, nil)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if len(calls) != 3 || calls[0][1] != "list" || calls[1][1] != "test" || calls[2][1] != "build" {
		t.Fatalf("calls=%v", calls)
	}
	if calls[1][2] != "-exec" || calls[1][3] != "true" {
		t.Fatalf("test args=%v", calls[1])
	}
	if len(calls[2]) < 6 || calls[2][4] != "fixture/tool/cmd/a" || calls[2][5] != "fixture/tool/cmd/b" {
		t.Fatalf("build args=%v", calls[2])
	}
	for _, env := range envs {
		hasTrim := false
		for _, e := range env {
			if strings.HasPrefix(e, "GOFLAGS=") && strings.Contains(e, "-mod=mod") && strings.Contains(e, "-trimpath") {
				hasTrim = true
			}
		}
		if !hasTrim {
			t.Fatalf("missing -mod=mod -trimpath in %v", env)
		}
	}
}

func TestGoBuildHandlesLibraryMissingMainAndHealthyMain(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name     string
		source   string
		expected int
	}{
		{"library", "package library\nfunc F() {}\n", 0},
		{"missing", "package main\nfunc helper() {}\n", 1},
		{"healthy", "package main\nfunc main() {}\n", 0},
	}
	for _, tc := range cases {
		mod := filepath.Join(root, tc.name)
		if err := os.MkdirAll(mod, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fixture/"+tc.name+"\n\ngo 1.24\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte(tc.source), 0o644); err != nil {
			t.Fatal(err)
		}
		code, output, err := buildModule(mod, root, nil, nil, nil)
		if err != nil || code != tc.expected {
			t.Fatalf("%s: code=%d want=%d err=%v out=%s", tc.name, code, tc.expected, err, output)
		}
	}
}

func TestGoBuildRecordsPackageAndCacheEvidence(t *testing.T) {
	stdout := "{\"Action\":\"output\",\"Package\":\"fixture/tool\",\"Output\":\"ok  \\tfixture/tool\\t(cached)\\n\"}\n" +
		"{\"Action\":\"output\",\"Package\":\"fixture/tool/lib\",\"Output\":\"ok  \\tfixture/tool/lib\\t0.001s\\n\"}\n" +
		"{\"Action\":\"output\",\"Package\":\"fixture/tool/json-cover\",\"Output\":\"\\tfixture/tool/json-cover\\t\\tcoverage: 0.0% of statements\\n\"}\n" +
		"\tfixture/tool/cover\t\tcoverage: 0.0% of statements\n" +
		"?\tfixture/tool/none\t[no test files]\n" +
		"FAIL\tfixture/tool/failed [build failed]\n"
	cache := testCacheEvidence(stdout)
	want := map[string]string{
		"fixture/tool":            "hit",
		"fixture/tool/lib":        "miss",
		"fixture/tool/none":       "not-applicable",
		"fixture/tool/cover":      "not-applicable",
		"fixture/tool/json-cover": "not-applicable",
	}
	for k, v := range want {
		if cache[k] != v {
			t.Fatalf("cache[%q] = %q, want %q", k, cache[k], v)
		}
	}
}

func TestGoBuildModuleMemoryRechecksBindingBeforePublication(t *testing.T) {
	root, mod := moduleFixtureRepo(t)
	original := "package main\nfunc main() {}\n"
	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	calls := 0
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && args[1] == "test" {
			calls++
			_ = os.WriteFile(filepath.Join(mod, "main.go"), []byte(original+"// changed during compilation\n"), 0o644)
			return 0, "", "", nil
		}
		if len(args) >= 2 && args[1] == "list" {
			return 0, "M\tfixture/tool\n", "", nil
		}
		if len(args) >= 2 && args[1] == "build" {
			return 0, "", "", nil
		}
		return defaultGoCommand(args, dir, env)
	}
	if code := runGoBuild([]string{mod}, "", stub); code != 0 {
		t.Fatalf("first: code=%d", code)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.go"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runGoBuild([]string{mod}, "", stub); code != 0 {
		t.Fatalf("second: code=%d", code)
	}
	if calls != 2 {
		t.Fatalf("expected 2 builds when dirty during compile, got %d", calls)
	}
	matches, _ := filepath.Glob(filepath.Join(root, ".git", "tillr-gate", "results", "*.json"))
	if len(matches) != 0 {
		t.Fatalf("expected no stored results, got %v", matches)
	}
}

func TestGoBuildModulesStartInParallelIncludingNestedModule(t *testing.T) {
	root := t.TempDir()
	r0 := filepath.Join(root, "0")
	r1 := filepath.Join(root, "1")
	nested := filepath.Join(r0, "nested")
	for _, d := range []string{r0, r1, nested} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	var mu sync.Mutex
	seen := make(map[string]bool)
	arrived := make(chan struct{}, 3)
	release := make(chan struct{})
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && args[1] == "list" {
			mu.Lock()
			seen[dir] = true
			mu.Unlock()
			arrived <- struct{}{}
			<-release
			return 0, "\tfixture\n", "", nil
		}
		return 0, "", "", nil
	}
	go func() {
		for i := 0; i < 3; i++ {
			<-arrived
		}
		close(release)
	}()
	if code := runGoBuild([]string{r0, r1}, t.TempDir(), stub); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 modules in parallel, got %v", seen)
	}
}

func TestGoBuildEndToEndRecordsPackageAndCacheEvidence(t *testing.T) {
	root, mod := moduleFixtureRepo(t)
	evPath := filepath.Join(root, "go-build-evidence.json")
	store := filepath.Join(root, ".git", "tillr-gate", "results")
	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	t.Setenv("TILLR_GO_BUILD_EVIDENCE_OUT", evPath)
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	var cmds []string
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && (args[1] == "list" || args[1] == "test") {
			cmds = append(cmds, args[1])
		}
		if len(args) >= 2 && args[1] == "list" {
			return 0, "M\tfixture/tool\n\tfixture/tool/lib\n\tfixture/tool/none\n\tfixture/tool/failed\n\tfixture/tool/cover\n\tfixture/tool/json-cover\n", "", nil
		}
		if len(args) >= 2 && args[1] == "test" {
			out := "{\"Action\":\"output\",\"Package\":\"fixture/tool\",\"Output\":\"ok  \\tfixture/tool\\t(cached)\\n\"}\n" +
				"{\"Action\":\"output\",\"Package\":\"fixture/tool/lib\",\"Output\":\"ok  \\tfixture/tool/lib\\t0.001s\\n\"}\n" +
				"{\"Action\":\"output\",\"Package\":\"fixture/tool/json-cover\",\"Output\":\"\\tfixture/tool/json-cover\\t\\tcoverage: 0.0% of statements\\n\"}\n" +
				"\tfixture/tool/cover\t\tcoverage: 0.0% of statements\n" +
				"?\tfixture/tool/none\t[no test files]\n" +
				"FAIL\tfixture/tool/failed [build failed]\n"
			return 1, out, "", nil
		}
		return defaultGoCommand(args, dir, env)
	}
	if code := runGoBuild([]string{mod}, store, stub); code != 1 {
		t.Fatalf("expected fail 1, got %d", code)
	}
	if strings.Join(cmds, ",") != "list,test" {
		t.Fatalf("cmds=%v", cmds)
	}
	raw, err := os.ReadFile(evPath)
	if err != nil {
		t.Fatal(err)
	}
	var ev []moduleEvidence
	if err := json.Unmarshal(raw, &ev); err != nil || len(ev) != 1 {
		t.Fatalf("evidence: %v %s", err, raw)
	}
	if ev[0].Execution != "ran" || ev[0].Mode != "compile-only" {
		t.Fatalf("ev=%+v", ev[0])
	}
	got := make(map[string]string)
	for _, p := range ev[0].Packages {
		got[p.ImportPath] = p.TestCache
	}
	want := map[string]string{
		"fixture/tool": "hit", "fixture/tool/lib": "miss",
		"fixture/tool/none": "not-applicable", "fixture/tool/failed": "unknown",
		"fixture/tool/cover": "not-applicable", "fixture/tool/json-cover": "not-applicable",
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("pkg %s: got %q want %q", k, got[k], v)
		}
	}
}

func TestGoBuildPublishesInventoryBeforeAnyCompileResult(t *testing.T) {
	root, mod1 := moduleFixtureRepo(t)
	mod2 := filepath.Join(root, "other")
	if err := os.MkdirAll(mod2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod2, "go.mod"), []byte("module demo/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evPath := filepath.Join(root, "go-build-evidence.json")
	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	t.Setenv("TILLR_GO_BUILD_EVIDENCE_OUT", evPath)
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && args[1] == "list" {
			return 0, "\tdemo/" + filepath.Base(dir) + "\n", "", nil
		}
		if len(args) >= 2 && args[1] == "test" {
			arrived <- struct{}{}
			<-release
			return 1, "aborted", "", nil
		}
		return defaultGoCommand(args, dir, env)
	}
	var snapshotDuringCompile []moduleEvidence
	go func() {
		<-arrived
		<-arrived
		raw, _ := os.ReadFile(evPath)
		_ = json.Unmarshal(raw, &snapshotDuringCompile)
		close(release)
	}()
	runGoBuild([]string{mod1, mod2}, filepath.Join(root, "results"), stub)
	if len(snapshotDuringCompile) != 2 {
		t.Fatalf("expected 2 modules published before compile finished, got %+v", snapshotDuringCompile)
	}
	for _, row := range snapshotDuringCompile {
		if len(row.Packages) != 1 || row.Packages[0].TestCache != "unknown" {
			t.Fatalf("unexpected pre-compile snapshot: %+v", row)
		}
	}
}

func TestGoBuildPreservesPartialDiscoveryWithoutCompiling(t *testing.T) {
	root, mod := moduleFixtureRepo(t)
	evPath := filepath.Join(root, "go-build-evidence.json")
	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	t.Setenv("TILLR_GO_BUILD_EVIDENCE_OUT", evPath)
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	var cmds []string
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && (args[1] == "list" || args[1] == "test" || args[1] == "build") {
			cmds = append(cmds, args[1])
		}
		if len(args) >= 2 && args[1] == "list" {
			return 1, "M\tdemo/good\n\tdemo/broken\n\tdemo/trunc", "missing dependency\n", nil
		}
		return defaultGoCommand(args, dir, env)
	}
	if code := runGoBuild([]string{mod}, filepath.Join(root, "results"), stub); code != 1 {
		t.Fatalf("expected 1, got %d", code)
	}
	if strings.Join(cmds, ",") != "list" {
		t.Fatalf("expected list only, got %v", cmds)
	}
	raw, _ := os.ReadFile(evPath)
	var ev []moduleEvidence
	_ = json.Unmarshal(raw, &ev)
	if len(ev) != 1 || ev[0].Discovery != "partial" || len(ev[0].Packages) != 2 {
		t.Fatalf("unexpected evidence: %+v", ev)
	}
}

func TestGoBuildModuleReuseDoesNotClaimGoTestCacheHits(t *testing.T) {
	root, mod := moduleFixtureRepo(t)
	evPath := filepath.Join(root, "go-build-evidence.json")
	store := filepath.Join(root, ".git", "tillr-gate", "results")
	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	t.Setenv("TILLR_GO_BUILD_EVIDENCE_OUT", evPath)
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	// First run seeds the store.
	if code := runGoBuild([]string{mod}, store, nil); code != 0 {
		t.Fatalf("seed run: code=%d", code)
	}
	// Second run must reuse without calling test/build.
	called := false
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && (args[1] == "list" || args[1] == "test" || args[1] == "build") {
			called = true
		}
		return defaultGoCommand(args, dir, env)
	}
	if code := runGoBuild([]string{mod}, store, stub); code != 0 {
		t.Fatalf("reuse run: code=%d", code)
	}
	if called {
		t.Fatalf("reuse run called compiler")
	}
	raw, _ := os.ReadFile(evPath)
	var ev []moduleEvidence
	_ = json.Unmarshal(raw, &ev)
	if len(ev) != 1 || ev[0].Execution != "reused" || len(ev[0].Packages) != 0 {
		t.Fatalf("unexpected reuse evidence: %+v", ev)
	}
}

func TestGoBuildModuleMemoryIgnoresReplacedDepTestFilesAndOrdersWaves(t *testing.T) {
	root := initTempRepo(t)
	a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	for _, d := range []string{a, b, c} {
		_ = os.MkdirAll(d, 0o755)
	}
	_ = os.WriteFile(filepath.Join(a, "go.mod"), []byte("module fixture/a\n\ngo 1.24\n"), 0o644)
	_ = os.WriteFile(filepath.Join(a, "a.go"), []byte("package a\nfunc A() int { return 1 }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(a, "a_test.go"), []byte("package a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(b, "go.mod"), []byte("module fixture/b\n\ngo 1.24\nrequire fixture/a v0.0.0\nreplace fixture/a => ../a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(b, "b.go"), []byte("package b\nimport \"fixture/a\"\nfunc B() int { return a.A() }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(c, "go.mod"), []byte("module fixture/c\n\ngo 1.24\nrequire (\n\tfixture/a v0.0.0\n\tfixture/b v0.0.0\n)\nreplace fixture/a => ../a\nreplace fixture/b => ../b\n"), 0o644)
	_ = os.WriteFile(filepath.Join(c, "c.go"), []byte("package main\nimport \"fixture/b\"\nfunc main() { _ = b.B() }\n"), 0o644)
	commitFixture(t, root, "fixture")

	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	var mu sync.Mutex
	var events []string
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && args[1] == "test" {
			mu.Lock()
			events = append(events, filepath.Base(dir))
			mu.Unlock()
		}
		return defaultGoCommand(args, dir, env)
	}

	// Reverse order (c, b, a) proves scheduling follows replace waves.
	if code := runGoBuild([]string{c, b, a}, "", stub); code != 0 {
		t.Fatalf("wave run: code=%d", code)
	}
	if strings.Join(events, ",") != "a,b,c" {
		t.Fatalf("expected wave order a,b,c, got %v", events)
	}

	// Editing a's test file rebuilds only a.
	events = nil
	_ = os.WriteFile(filepath.Join(a, "a_test.go"), []byte("package a\n// test-only edit\n"), 0o644)
	commitFixture(t, root, "test edit")
	if code := runGoBuild([]string{c, b, a}, "", stub); code != 0 {
		t.Fatalf("test edit: code=%d", code)
	}
	if strings.Join(events, ",") != "a" {
		t.Fatalf("expected only a rebuilt on test edit, got %v", events)
	}

	// Untracked test file in a invalidates only a; untracked prod file invalidates all.
	events = nil
	_ = os.WriteFile(filepath.Join(a, "extra_test.go"), []byte("package a\n"), 0o644)
	_ = runGoBuild([]string{c, b, a}, "", stub)
	if strings.Join(events, ",") != "a" {
		t.Fatalf("expected only a on untracked test, got %v", events)
	}
	_ = os.Remove(filepath.Join(a, "extra_test.go"))

	events = nil
	_ = os.WriteFile(filepath.Join(a, "extra.go"), []byte("package a\n"), 0o644)
	_ = runGoBuild([]string{c, b, a}, "", stub)
	if strings.Join(events, ",") != "a,b,c" {
		t.Fatalf("expected a,b,c on untracked prod, got %v", events)
	}
	_ = os.Remove(filepath.Join(a, "extra.go"))
}

func TestGoBuildMutualReplaceCycleRunsInParallelBeforeDownstreamWave(t *testing.T) {
	root := initTempRepo(t)
	a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	for _, d := range []string{a, b, c} {
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "p.go"), []byte("package "+filepath.Base(d)+"\n"), 0o644)
	}
	_ = os.WriteFile(filepath.Join(a, "go.mod"), []byte("module fixture/a\n\ngo 1.24\nreplace fixture/b => ../b\n"), 0o644)
	_ = os.WriteFile(filepath.Join(b, "go.mod"), []byte("module fixture/b\n\ngo 1.24\nreplace fixture/a => ../a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(c, "go.mod"), []byte("module fixture/c\n\ngo 1.24\nreplace fixture/a => ../a\n"), 0o644)
	commitFixture(t, root, "cycle")

	t.Setenv("YOLO_DURABLE_DIR", t.TempDir())
	origWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	stub := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 2 && args[1] == "test" {
			name := filepath.Base(dir)
			if name == "a" || name == "b" {
				arrived <- struct{}{}
				<-release
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return 0, "", "", nil
		}
		if len(args) >= 2 && args[1] == "list" {
			return 0, "\tfixture/" + filepath.Base(dir) + "\n", "", nil
		}
		if len(args) >= 2 && args[1] == "build" {
			return 0, "", "", nil
		}
		return defaultGoCommand(args, dir, env)
	}
	go func() {
		<-arrived
		<-arrived
		close(release)
	}()
	if code := runGoBuild([]string{c, b, a}, "", stub); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(order) != 3 || order[2] != "c" {
		t.Fatalf("expected a,b before c, got %v", order)
	}
}

func TestGoBuildModuleKeysAvoidsPerModuleGoworkSubprocesses(t *testing.T) {
	root := initTempRepo(t)
	var mods []string
	for _, name := range []string{"m1", "m2", "m3"} {
		m := filepath.Join(root, name)
		_ = os.MkdirAll(m, 0o755)
		_ = os.WriteFile(filepath.Join(m, "go.mod"), []byte("module fixture/"+name+"\n\ngo 1.24\n"), 0o644)
		_ = os.WriteFile(filepath.Join(m, "p.go"), []byte("package "+name+"\n"), 0o644)
		mods = append(mods, m)
	}
	commitFixture(t, root, "mods")
	t.Setenv("GOWORK", "")

	goworkCalls := 0
	spy := func(args []string, dir string, env []string) (int, string, string, error) {
		if len(args) >= 3 && args[0] == "go" && args[1] == "env" && args[2] == "GOWORK" {
			goworkCalls++
		}
		return defaultGoCommand(args, dir, env)
	}
	first, _, err := moduleKeys(root, mods, spy)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := moduleKeys(root, mods, spy)
	if err != nil {
		t.Fatal(err)
	}
	if goworkCalls != 0 {
		t.Fatalf("expected 0 per-module go env GOWORK calls, got %d", goworkCalls)
	}
	for _, m := range mods {
		if !first[m].bound || first[m] != second[m] {
			t.Fatalf("unstable/unbound key for %s: %+v vs %+v", m, first[m], second[m])
		}
	}
}
