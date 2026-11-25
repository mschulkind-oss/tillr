package shards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	ShardThreshold           = 100
	ExcludedPackMirrorPrefix = "yolo-packs/tillr/files/local/share/tillr/"
)

var MaxShards = maxShardsForCPUs(runtime.NumCPU())

func maxShardsForCPUs(cpus int) int {
	if cpus > 0 {
		return 2 * cpus
	}
	return 12
}

var StrippedOuterEnv = []string{
	"LAND_BATCH_LIMIT",
	"TILLR_FORK_DRIFT",
	"PI_FORKS_ROOT",
	"PI_FORKS_SERIES_ROOT",
	"TILLR_LOOP_REGISTRY",
	"TILLR_LOOP_ADAPTER_BUILD",
	"TILLR_ORDINARY_GATE_TIMING",
	"TILLR_FAST_TREE_TIMING",
	"TILLR_GATE_SPANS_OUT",
	"TILLR_FAST_VERDICT_OUT",
	"TILLR_GO_AFFECTED_EVIDENCE_OUT",
	"TILLR_GO_SHARDS_EVIDENCE_OUT",
	"TILLR_GO_BUILD_EVIDENCE_OUT",
	"TILLR_GO_VERDICTS_IN",
	"TILLR_GO_VERDICTS_OUT",
	"TILLR_SELF_SUITE_TIMING_OUT",
	"TILLR_SQLITE_TRACE",
	"GO_TEST_SHARDS_HOLDER",
	"GO_TEST_SHARDS_CLASS",
	"TILLR_RUN_ID",
	"TILLR_LOOP_STORE",
	"TILLR_ITEM_ID",
	"TILLR_LANE_ID",
	"TILLR_NODE_ID",
	"TILLR_PROGRESS_FILE",
	"TILLR_GATE_EDGE_INPUTS",
	"GIT_DIR",
	"GIT_WORK_TREE",
	"GIT_INDEX_FILE",
	"GIT_COMMON_DIR",
	"GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

var topLevelTestRe = regexp.MustCompile(
	`(?m)^func\s+(?:(Test(?:[^\Wa-z]\w*)?)\s*\(\s*\w+\s+\*testing\.T\b|(Fuzz(?:[^\Wa-z]\w*)?)\s*\(\s*\w+\s+\*testing\.F\b|(Example(?:[^\Wa-z]\w*)?)\s*\(\s*\))`,
)

// Edge represents one package's entry in tools/gate/go-test-edges.json.
type Edge struct {
	Builds    []string            `json:"builds"`
	Reads     []string            `json:"reads"`
	ReadTests map[string][]string `json:"read_tests"`
}

// PkgInfo describes a non-standard Go package inside the repository.
type PkgInfo struct {
	ImportPath string
	Module     string
	DirRel     string
	HasTests   bool
	TestFiles  []string
	Deps       map[string]struct{}
	Name       string
}

// TillrGraph holds the package dependency graph across tillr modules.
type TillrGraph struct {
	Modules        []string
	FailedModules  map[string]struct{}
	PkgInfo        map[string]PkgInfo
	TestDeps       map[string]map[string]struct{}
	DirToPkg       map[string]string
	ModuleTestPkgs map[string][]string
}

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

func isRelativeTo(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func pathComponents(p string) []string {
	cleaned := filepath.Clean(p)
	parts := strings.Split(cleaned, string(filepath.Separator))
	var out []string
	for _, part := range parts {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}

func containsPart(p, target string) bool {
	for _, part := range pathComponents(p) {
		if part == target {
			return true
		}
	}
	return false
}

// LocateRepoRoot walks up from startPath looking for tools/gate/go-test-edges.json or .git.
func LocateRepoRoot(startPath string, fallbackToExe bool, stopAtTempRoots bool) string {
	start := resolvePath(startPath)
	tempRoots := make(map[string]struct{})
	if stopAtTempRoots {
		for _, k := range []string{"TMPDIR", "YOLO_DURABLE_DIR"} {
			if val := strings.TrimSpace(os.Getenv(k)); val != "" {
				tempRoots[resolvePath(val)] = struct{}{}
			}
		}
	}
	cur := start
	for {
		if stopAtTempRoots {
			if _, ok := tempRoots[cur]; ok {
				break
			}
		}
		edgesPath := filepath.Join(cur, "tools", "gate", "go-test-edges.json")
		if info, err := os.Stat(edgesPath); err == nil && !info.IsDir() {
			return cur
		}
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	if fallbackToExe {
		if exe, err := os.Executable(); err == nil {
			if found := LocateRepoRoot(filepath.Dir(exe), false, false); found != "" {
				return found
			}
		}
	}
	return ""
}

// CheckCachedWorkerRefusal enforces D-ER9: --cache is refused on repository modules
// or .forks/ when TILLR_RUN_ID is set, while allowing fixtures under TMPDIR/YOLO_DURABLE_DIR.
func CheckCachedWorkerRefusal(module string) error {
	runID := strings.TrimSpace(os.Getenv("TILLR_RUN_ID"))
	if runID == "" {
		return nil
	}
	absModule, err := filepath.Abs(module)
	if err != nil {
		absModule = module
	}
	resolvedModule := resolvePath(module)
	if containsPart(absModule, ".forks") || containsPart(resolvedModule, ".forks") {
		return &ShardError{Message: fmt.Sprintf("--cache is refused inside .forks/ when TILLR_RUN_ID is set: %s", module)}
	}
	repoRootSet := make(map[string]struct{})
	if cwd, err := os.Getwd(); err == nil {
		if r := LocateRepoRoot(cwd, true, false); r != "" {
			repoRootSet[resolvePath(r)] = struct{}{}
		}
	}
	if envRepo := strings.TrimSpace(os.Getenv("TILLR_REPO_ROOT")); envRepo != "" {
		repoRootSet[resolvePath(envRepo)] = struct{}{}
	}
	if cwd, err := os.Getwd(); err == nil {
		cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
		if out, err := cmd.Output(); err == nil {
			if top := strings.TrimSpace(string(out)); top != "" {
				repoRootSet[resolvePath(top)] = struct{}{}
			}
		}
	}

	var matchingRepos []string
	for r := range repoRootSet {
		if isRelativeTo(resolvedModule, r) {
			matchingRepos = append(matchingRepos, r)
		}
	}
	if len(matchingRepos) == 0 {
		return nil
	}
	deepestRepo := matchingRepos[0]
	for _, r := range matchingRepos[1:] {
		if len(pathComponents(r)) > len(pathComponents(deepestRepo)) {
			deepestRepo = r
		}
	}

	var matchingTemps []string
	for _, k := range []string{"TMPDIR", "YOLO_DURABLE_DIR"} {
		if val := strings.TrimSpace(os.Getenv(k)); val != "" {
			t := resolvePath(val)
			if isRelativeTo(resolvedModule, t) {
				matchingTemps = append(matchingTemps, t)
			}
		}
	}
	if len(matchingTemps) > 0 {
		deepestTemp := matchingTemps[0]
		for _, t := range matchingTemps[1:] {
			if len(pathComponents(t)) > len(pathComponents(deepestTemp)) {
				deepestTemp = t
			}
		}
		if deepestTemp != deepestRepo && isRelativeTo(deepestTemp, deepestRepo) {
			return nil
		}
	}
	return &ShardError{Message: fmt.Sprintf("--cache is refused on repository module %s when TILLR_RUN_ID is set", module)}
}

// BuildEnv returns a clean Go build/test environment with CGO_ENABLED=0, -trimpath,
// stripped outer gate variables, and bypassed git shims.
func BuildEnv() map[string]string {
	env := EnvSliceToMap(os.Environ())
	env["CGO_ENABLED"] = "0"
	for _, key := range StrippedOuterEnv {
		delete(env, key)
	}
	existing := strings.Fields(env["GOFLAGS"])
	hasTrimpath := false
	for _, f := range existing {
		if f == "-trimpath" {
			hasTrimpath = true
			break
		}
	}
	if !hasTrimpath {
		existing = append(existing, "-trimpath")
	}
	env["GOFLAGS"] = strings.Join(existing, " ")
	durable := env["YOLO_DURABLE_DIR"]
	if durable == "" {
		durable = env["TMPDIR"]
	}
	var scratchRoot string
	if durable != "" {
		scratchRoot = filepath.Join(durable, ".go-affected-scratch")
	}
	return BypassGitShimEnv(env, SharedCacheDir(env), scratchRoot)
}

// DiscoverTillrModules returns tools/tillr and every tools/tillr-* directory holding a go.mod.
func DiscoverTillrModules(root string) []string {
	var modules []string
	if info, err := os.Stat(filepath.Join(root, "tools", "tillr", "go.mod")); err == nil && !info.IsDir() {
		modules = append(modules, "tools/tillr")
	}
	matches, err := filepath.Glob(filepath.Join(root, "tools", "tillr-*", "go.mod"))
	if err == nil {
		sort.Strings(matches)
		for _, m := range matches {
			modules = append(modules, "tools/"+filepath.Base(filepath.Dir(m)))
		}
	}
	return modules
}

// LoadEdges reads and validates tools/gate/go-test-edges.json if present.
func LoadEdges(root string) (map[string]Edge, error) {
	path := filepath.Join(root, "tools", "gate", "go-test-edges.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Edge{}, nil
		}
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: expected JSON object: %w", path, err)
	}
	edges := make(map[string]Edge, len(raw))
	for pkg, entryRaw := range raw {
		var edge Edge
		if err := json.Unmarshal(entryRaw, &edge); err != nil {
			return nil, fmt.Errorf("%s: invalid entry for %q: %w", path, pkg, err)
		}
		readsSet := make(map[string]struct{}, len(edge.Reads))
		for _, r := range edge.Reads {
			readsSet[r] = struct{}{}
		}
		for rule, names := range edge.ReadTests {
			if _, ok := readsSet[rule]; !ok {
				return nil, fmt.Errorf("%s: %s.read_tests key %q must appear in %s.reads", path, pkg, rule, pkg)
			}
			if len(names) == 0 {
				return nil, fmt.Errorf("%s: %s.read_tests[%q] must be a non-empty list of test names", path, pkg, rule)
			}
			for _, t := range names {
				if strings.TrimSpace(t) == "" || t != strings.TrimSpace(t) {
					return nil, fmt.Errorf("%s: %s.read_tests[%q] must be a non-empty list of test names", path, pkg, rule)
				}
			}
		}
		edges[pkg] = edge
	}
	return edges, nil
}

type goListPkg struct {
	ImportPath   string   `json:"ImportPath"`
	Name         string   `json:"Name"`
	Dir          string   `json:"Dir"`
	Standard     bool     `json:"Standard"`
	ForTest      string   `json:"ForTest"`
	Deps         []string `json:"Deps"`
	TestGoFiles  []string `json:"TestGoFiles"`
	XTestGoFiles []string `json:"XTestGoFiles"`
}

func parseGoListStream(data []byte, allowPartial bool) ([]goListPkg, error) {
	dec := json.NewDecoder(bytesReader(data))
	var pkgs []goListPkg
	for {
		var pkg goListPkg
		err := dec.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			if allowPartial {
				break
			}
			return pkgs, err
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

func bytesReader(b []byte) io.Reader {
	return strings.NewReader(string(b))
}

func listModulePackages(root, module string, env []string) (bool, []goListPkg) {
	cmd := exec.Command("go", "list", "-deps", "-test", "-json", "./...")
	cmd.Dir = filepath.Join(root, module)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	pkgs, err := parseGoListStream(out, false)
	if err != nil {
		return false, nil
	}
	return true, pkgs
}

// LoadTillrGraph builds the package dependency graph across tillr modules.
func LoadTillrGraph(root string, modules []string) (*TillrGraph, error) {
	root = resolvePath(root)
	if modules == nil {
		modules = DiscoverTillrModules(root)
	}
	env := EnvMapToSlice(BuildEnv())

	type modResult struct {
		mod  string
		ok   bool
		pkgs []goListPkg
	}
	results := make([]modResult, len(modules))
	var wg sync.WaitGroup
	for i, m := range modules {
		wg.Add(1)
		go func(idx int, mod string) {
			defer wg.Done()
			ok, pkgs := listModulePackages(root, mod, env)
			results[idx] = modResult{mod: mod, ok: ok, pkgs: pkgs}
		}(i, m)
	}
	wg.Wait()

	graph := &TillrGraph{
		Modules:        modules,
		FailedModules:  make(map[string]struct{}),
		PkgInfo:        make(map[string]PkgInfo),
		TestDeps:       make(map[string]map[string]struct{}),
		DirToPkg:       make(map[string]string),
		ModuleTestPkgs: make(map[string][]string, len(modules)),
	}
	for _, m := range modules {
		graph.ModuleTestPkgs[m] = nil
	}

	for _, res := range results {
		if !res.ok {
			graph.FailedModules[res.mod] = struct{}{}
			continue
		}
		modRoot := resolvePath(filepath.Join(root, res.mod))
		for _, obj := range res.pkgs {
			if obj.Standard || obj.Dir == "" {
				continue
			}
			dPath := resolvePath(obj.Dir)
			if !isRelativeTo(dPath, root) {
				continue
			}
			dirRel, err := filepath.Rel(root, dPath)
			if err != nil {
				continue
			}
			dirRel = filepath.ToSlash(dirRel)
			ip := obj.ImportPath
			deps := make(map[string]struct{}, len(obj.Deps))
			for _, d := range obj.Deps {
				deps[d] = struct{}{}
			}
			if obj.ForTest != "" {
				if graph.TestDeps[obj.ForTest] == nil {
					graph.TestDeps[obj.ForTest] = make(map[string]struct{})
				}
				for d := range deps {
					graph.TestDeps[obj.ForTest][d] = struct{}{}
				}
				continue
			}
			if strings.HasSuffix(ip, ".test") && obj.Name == "main" {
				continue
			}
			var ownerMod string
			if isRelativeTo(dPath, modRoot) {
				ownerMod = res.mod
			} else {
				for _, cand := range modules {
					if dirRel == cand || strings.HasPrefix(dirRel, cand+"/") {
						ownerMod = cand
						break
					}
				}
			}
			if ownerMod == "" {
				continue
			}
			tfSet := make(map[string]struct{}, len(obj.TestGoFiles)+len(obj.XTestGoFiles))
			for _, f := range obj.TestGoFiles {
				tfSet[f] = struct{}{}
			}
			for _, f := range obj.XTestGoFiles {
				tfSet[f] = struct{}{}
			}
			testFiles := make([]string, 0, len(tfSet))
			for f := range tfSet {
				testFiles = append(testFiles, f)
			}
			sort.Strings(testFiles)
			hasTests := len(testFiles) > 0
			if _, exists := graph.PkgInfo[ip]; !exists {
				graph.PkgInfo[ip] = PkgInfo{
					ImportPath: ip,
					Module:     ownerMod,
					DirRel:     dirRel,
					HasTests:   hasTests,
					TestFiles:  testFiles,
					Deps:       deps,
					Name:       obj.Name,
				}
				graph.DirToPkg[dirRel] = ip
				if graph.TestDeps[ip] == nil {
					graph.TestDeps[ip] = make(map[string]struct{})
				}
				for d := range deps {
					graph.TestDeps[ip][d] = struct{}{}
				}
				if hasTests {
					graph.ModuleTestPkgs[ownerMod] = append(graph.ModuleTestPkgs[ownerMod], ip)
				}
			}
		}
	}
	return graph, nil
}

// ResolveEdgeBuilds resolves all source and module files for an edge's `builds` targets.
func ResolveEdgeBuilds(root string, edge Edge, pkgInfo map[string]PkgInfo) ([]string, []string) {
	files := make(map[string]struct{})
	mods := make(map[string]struct{})
	var missing []string
	for _, target := range edge.Builds {
		info, ok := pkgInfo[target]
		if !ok {
			missing = append(missing, target)
			continue
		}
		closure := make(map[string]struct{}, len(info.Deps)+1)
		closure[target] = struct{}{}
		for d := range info.Deps {
			closure[d] = struct{}{}
		}
		for dep := range closure {
			dinfo, ok := pkgInfo[dep]
			if !ok {
				continue
			}
			mods[dinfo.Module] = struct{}{}
			dpath := filepath.Join(root, filepath.FromSlash(dinfo.DirRel))
			entries, err := os.ReadDir(dpath)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				full := filepath.Join(dpath, entry.Name())
				info, serr := os.Stat(full)
				if serr != nil || !info.Mode().IsRegular() {
					continue
				}
				rel := dinfo.DirRel + "/" + entry.Name()
				if dinfo.DirRel == "." {
					rel = entry.Name()
				}
				files[rel] = struct{}{}
			}
		}
	}
	for mod := range mods {
		for _, mf := range []string{"go.mod", "go.sum"} {
			mp := filepath.Join(root, filepath.FromSlash(mod), mf)
			if info, err := os.Stat(mp); err == nil && !info.IsDir() {
				files[mod+"/"+mf] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, missing
}

// ComputeEdgeDigest computes the sha256 hex digest of all build and read inputs for an edge.
func ComputeEdgeDigest(root string, edge Edge, pkgInfo map[string]PkgInfo, activeTests []string, resolvedBuilds *[2][]string) string {
	hasher := sha256.New()
	var buildFiles, missingBuilds []string
	if resolvedBuilds != nil {
		buildFiles = resolvedBuilds[0]
		missingBuilds = resolvedBuilds[1]
	} else {
		buildFiles, missingBuilds = ResolveEdgeBuilds(root, edge, pkgInfo)
	}
	for _, target := range missingBuilds {
		_, _ = fmt.Fprintf(hasher, "missing-build:%s\n", target)
	}
	files := make(map[string]struct{}, len(buildFiles))
	for _, f := range buildFiles {
		files[f] = struct{}{}
	}
	var activeSet map[string]struct{}
	if activeTests != nil {
		activeSet = make(map[string]struct{}, len(activeTests))
		for _, t := range activeTests {
			activeSet[t] = struct{}{}
		}
	}
	for _, rule := range edge.Reads {
		if activeSet != nil {
			if names, hasRule := edge.ReadTests[rule]; hasRule {
				matched := false
				for _, n := range names {
					if _, ok := activeSet[n]; ok {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
		}
		trimmed := strings.TrimRight(rule, "/")
		rp := filepath.Join(root, filepath.FromSlash(trimmed))
		info, err := os.Stat(rp)
		if err != nil {
			_, _ = fmt.Fprintf(hasher, "missing-read:%s\n", rule)
			continue
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(root, rp)
			if err == nil {
				files[filepath.ToSlash(rel)] = struct{}{}
			}
			continue
		}
		_ = filepath.WalkDir(rp, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if name == ".git" || name == "node_modules" || name == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			relSlash := filepath.ToSlash(rel)
			if strings.HasPrefix(relSlash, ExcludedPackMirrorPrefix) && !strings.HasPrefix(rule, ExcludedPackMirrorPrefix) {
				return nil
			}
			files[relSlash] = struct{}{}
			return nil
		})
	}

	sortedFiles := make([]string, 0, len(files))
	for f := range files {
		sortedFiles = append(sortedFiles, f)
	}
	sort.Strings(sortedFiles)
	for _, rel := range sortedFiles {
		_, _ = hasher.Write([]byte(rel))
		_, _ = hasher.Write([]byte{0})
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			_, _ = hasher.Write([]byte("<unreadable>"))
		} else {
			_, _ = hasher.Write(data)
		}
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// ScanPackageTests extracts top-level Test*/Fuzz*/Example* names from Go test files.
func ScanPackageTests(root, dirRel, importPath, module string, testFiles []string) []string {
	if importPath == "./..." {
		return nil
	}
	if dirRel == "" {
		const prefix = "github.com/mschulkind-oss/"
		if strings.HasPrefix(importPath, prefix) {
			dirRel = "tools/" + importPath[len(prefix):]
		} else {
			dirRel = module
		}
	}
	pkgDir := filepath.Join(root, filepath.FromSlash(dirRel))
	if info, err := os.Stat(pkgDir); err != nil || !info.IsDir() {
		return nil
	}
	if len(testFiles) == 0 {
		matches, err := filepath.Glob(filepath.Join(pkgDir, "*_test.go"))
		if err == nil {
			sort.Strings(matches)
			for _, m := range matches {
				testFiles = append(testFiles, filepath.Base(m))
			}
		}
	}
	var seen []string
	seenSet := make(map[string]struct{})
	for _, fname := range testFiles {
		data, err := os.ReadFile(filepath.Join(pkgDir, fname))
		if err != nil {
			continue
		}
		matches := topLevelTestRe.FindAllStringSubmatch(string(data), -1)
		for _, m := range matches {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			if name == "" {
				name = m[3]
			}
			if name == "" || name == "TestMain" {
				continue
			}
			if _, ok := seenSet[name]; !ok {
				seenSet[name] = struct{}{}
				seen = append(seen, name)
			}
		}
	}
	return seen
}

func clearDirectoryContents(path string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = os.RemoveAll(filepath.Join(path, entry.Name()))
	}
}

// AcquireInvocationScratch acquires an exclusive per-invocation scratch directory
// using a locked slot pool so non-concurrent runs reuse slot-0 for Go test cache hits.
func AcquireInvocationScratch(durable, scratchKey string) (string, func(), error) {
	if durable == "" {
		tmp, err := os.MkdirTemp("", "go-affected-scratch-")
		if err != nil {
			return "", nil, err
		}
		return tmp, func() { _ = os.RemoveAll(tmp) }, nil
	}
	sum := sha256.Sum256([]byte(scratchKey))
	slug := hex.EncodeToString(sum[:])[:12]
	poolDir := filepath.Join(durable, ".go-affected-scratch", slug)
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		return "", nil, err
	}
	slot := 0
	var fd int
	for {
		lockPath := filepath.Join(poolDir, "slot-"+strconv.Itoa(slot)+".lock")
		cand, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR, 0o666)
		if err != nil {
			return "", nil, err
		}
		err = syscall.Flock(cand, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			fd = cand
			break
		}
		_ = syscall.Close(cand)
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			slot++
			continue
		}
		return "", nil, err
	}
	scratchDir := filepath.Join(poolDir, "slot-"+strconv.Itoa(slot))
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = syscall.Close(fd)
		return "", nil, err
	}
	clearDirectoryContents(scratchDir)
	cleanup := func() {
		clearDirectoryContents(scratchDir)
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = syscall.Close(fd)
	}
	return scratchDir, cleanup, nil
}
