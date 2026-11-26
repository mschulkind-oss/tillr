package goaffected

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// ExcludedPackMirrorPrefix is the pack mirror tree excluded from read-rule matching
// unless the read rule itself explicitly targets it.
const ExcludedPackMirrorPrefix = "yolo-packs/tillr/files/local/share/tillr/"

// strippedOuterEnv lists outer gate, drain, and git-override variables that child
// `go list` / `go env` processes must never inherit.
var strippedOuterEnv = []string{
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

// EdgeSpec describes a declared test edge from tools/gate/go-test-edges.json.
type EdgeSpec struct {
	Builds     []string            `json:"builds"`
	Reads      []string            `json:"reads"`
	BuildTests map[string][]string `json:"build_tests,omitempty"`
	ReadTests  map[string][]string `json:"read_tests,omitempty"`
	rawMap     map[string]any
}

// SourceScanningEdge describes a cross-module source-scanning test that must declare
// every tillr Go module in tools/gate/go-test-edges.json.
type SourceScanningEdge struct {
	Package           string
	TestName          string
	IncludeRootModule bool
}

// SourceScanningEdges lists cross-module source-scanning tests enforced by go-edges.
var SourceScanningEdges = []SourceScanningEdge{
	{
		Package:           "github.com/mschulkind-oss/tillr/definition",
		TestName:          "TestNameOnlyInOneConstantAndOnlyLifecycleConsumers",
		IncludeRootModule: true,
	},
	{
		Package:           "github.com/mschulkind-oss/tillr/store",
		TestName:          "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit",
		IncludeRootModule: false,
	},
}

// GoListPackage holds the subset of `go list -deps -test -json ./...` fields used by goaffected.
type GoListPackage struct {
	Standard     bool     `json:"Standard"`
	Dir          string   `json:"Dir"`
	ImportPath   string   `json:"ImportPath"`
	Name         string   `json:"Name"`
	ForTest      string   `json:"ForTest"`
	Deps         []string `json:"Deps"`
	TestGoFiles  []string `json:"TestGoFiles"`
	XTestGoFiles []string `json:"XTestGoFiles"`
}

// PkgInfo holds normalized package metadata for an in-repo Go package.
type PkgInfo struct {
	ImportPath string
	Module     string
	DirRel     string
	HasTests   bool
	TestFiles  []string
	Deps       map[string]bool
	Name       string
}

// Graph holds the discovered module and package dependency graph.
type Graph struct {
	Modules        []string
	FailedModules  map[string]bool
	PkgInfo        map[string]PkgInfo
	TestDeps       map[string]map[string]bool
	DirToPkg       map[string]string
	ModuleTestPkgs map[string][]string
}

// DiscoverTillrModules returns tools/tillr and every tools/tillr-* directory holding a go.mod.
func DiscoverTillrModules(root string) ([]string, error) {
	var modules []string
	if isFile(filepath.Join(root, "tools", "tillr", "go.mod")) {
		modules = append(modules, "tools/tillr")
	}
	toolsDir := filepath.Join(root, "tools")
	if isDir(toolsDir) {
		matches, err := filepath.Glob(filepath.Join(toolsDir, "tillr-*", "go.mod"))
		if err != nil {
			return nil, err
		}
		var extra []string
		for _, m := range matches {
			if isFile(m) {
				extra = append(extra, "tools/"+filepath.Base(filepath.Dir(m)))
			}
		}
		sort.Strings(extra)
		modules = append(modules, extra...)
	}
	return modules, nil
}

// LoadEdges reads and validates tools/gate/go-test-edges.json.
func LoadEdges(root string) (map[string]EdgeSpec, error) {
	edgePath := filepath.Join(root, "tools", "gate", "go-test-edges.json")
	if !isFile(edgePath) {
		return map[string]EdgeSpec{}, nil
	}
	raw, err := os.ReadFile(edgePath)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s: expected JSON object", edgePath)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	if top == nil {
		return nil, fmt.Errorf("%s: expected JSON object", edgePath)
	}
	out := make(map[string]EdgeSpec, len(top))
	for pkg, rawEntry := range top {
		entryTrimmed := bytes.TrimSpace(rawEntry)
		if len(entryTrimmed) == 0 || entryTrimmed[0] != '{' {
			return nil, fmt.Errorf("%s: invalid entry for %q", edgePath, pkg)
		}
		var entryMap map[string]json.RawMessage
		if err := json.Unmarshal(rawEntry, &entryMap); err != nil || entryMap == nil {
			return nil, fmt.Errorf("%s: invalid entry for %q", edgePath, pkg)
		}
		var rawAny map[string]any
		if err := json.Unmarshal(rawEntry, &rawAny); err != nil {
			return nil, fmt.Errorf("%s: invalid entry for %q", edgePath, pkg)
		}

		var spec EdgeSpec
		spec.rawMap = rawAny

		builds, err := decodeStringList(entryMap, "builds", edgePath, pkg)
		if err != nil {
			return nil, err
		}
		spec.Builds = builds

		reads, err := decodeStringList(entryMap, "reads", edgePath, pkg)
		if err != nil {
			return nil, err
		}
		spec.Reads = reads

		buildTests, err := decodeScopedTestsMap(entryMap, "build_tests", "builds", builds, edgePath, pkg)
		if err != nil {
			return nil, err
		}
		spec.BuildTests = buildTests

		readTests, err := decodeScopedTestsMap(entryMap, "read_tests", "reads", reads, edgePath, pkg)
		if err != nil {
			return nil, err
		}
		spec.ReadTests = readTests

		out[pkg] = spec
	}
	return out, nil
}

// MissingSourceScanningEdges returns refusal messages for any tillr Go module absent
// from cross-module source-scanning edges in tools/gate/go-test-edges.json.
func MissingSourceScanningEdges(root string) ([]string, error) {
	edges, err := LoadEdges(root)
	if err != nil {
		return nil, err
	}
	modules, err := DiscoverTillrModules(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, item := range SourceScanningEdges {
		entry, ok := edges[item.Package]
		if !ok {
			findings = append(findings, fmt.Sprintf("%s: missing entry in tools/gate/go-test-edges.json", item.Package))
			continue
		}
		reads := make(map[string]bool, len(entry.Reads))
		for _, r := range entry.Reads {
			reads[r] = true
		}
		for _, mod := range modules {
			if !item.IncludeRootModule && mod == "tools/tillr" {
				continue
			}
			rule := mod + "/"
			if !reads[rule] {
				findings = append(findings, fmt.Sprintf("%s: missing '%s' in tools/gate/go-test-edges.json reads", item.Package, rule))
			} else if !containsString(entry.ReadTests[rule], item.TestName) {
				findings = append(findings, fmt.Sprintf("%s: missing '%s' in tools/gate/go-test-edges.json read_tests['%s']", item.Package, item.TestName, rule))
			}
		}
	}
	return findings, nil
}

var (
	goWalkRE       = regexp.MustCompile(`\b(?:filepath\.Walk(?:Dir)?|fs\.WalkDir)\s*\(`)
	goExtRE        = regexp.MustCompile(`(?:HasSuffix|Ext)\([^)]*"\.go"|==\s*"\.go"`)
	goParseRE      = regexp.MustCompile(`\b\w+\.Parse(?:File|Dir)\s*\(`)
	repoRelModRE   = regexp.MustCompile(`"tools/(tillr(?:-[a-z0-9-]+)?)(?:/([^"]*))?"`)
	parentRelModRE = regexp.MustCompile(`"(?:\.\./)+(tillr(?:-[a-z0-9-]+)?)(?:/([^"]*))?"`)
	filepathJoinRE = regexp.MustCompile(`filepath\.Join\(([^)]*)\)`)
	modGlobRE      = regexp.MustCompile(`"(?:tools/)?(tillr-\*|tillr\*)"`)
	strLitRE       = regexp.MustCompile(`"([^"]+)"`)
	tillrImportRE  = regexp.MustCompile(`"(github\.com/mschulkind-oss/[^"]+)"`)
)

func packageTillrImports(pkgDir string) map[string]bool {
	imports := make(map[string]bool)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return imports
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(pkgDir, entry.Name()))
		if err != nil {
			continue
		}
		for _, match := range tillrImportRE.FindAllStringSubmatch(string(raw), -1) {
			imports[match[1]] = true
		}
	}
	return imports
}

// UndeclaredCrossModuleGoReads returns findings for *_test.go files that walk or parse Go
// source outside their own module without a declared reads edge in tools/gate/go-test-edges.json.
func UndeclaredCrossModuleGoReads(root string, edges map[string]EdgeSpec) ([]string, error) {
	if edges == nil {
		var err error
		edges, err = LoadEdges(root)
		if err != nil {
			return nil, err
		}
	}
	modules, err := DiscoverTillrModules(root)
	if err != nil {
		return nil, err
	}
	modNames := make(map[string]bool, len(modules))
	for _, mod := range modules {
		if _, name, ok := strings.Cut(mod, "/"); ok {
			modNames[name] = true
		}
	}
	var findings []string

	type targetKey struct {
		mod string
		sub string
	}

	for _, mod := range modules {
		_, modName, ok := strings.Cut(mod, "/")
		if !ok {
			continue
		}
		modRoot := filepath.Join(root, filepath.FromSlash(mod))
		var testFiles []string
		err := filepath.WalkDir(modRoot, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), "_test.go") {
				testFiles = append(testFiles, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(testFiles)

		for _, testFile := range testFiles {
			raw, err := os.ReadFile(testFile)
			if err != nil {
				return nil, err
			}
			text := string(raw)
			isGoParse := goParseRE.MatchString(text)
			isGoWalk := goWalkRE.MatchString(text) && goExtRE.MatchString(text)
			if !isGoParse && !isGoWalk {
				continue
			}

			pkgDir := filepath.Dir(testFile)
			pkgRelRaw, err := filepath.Rel(modRoot, pkgDir)
			if err != nil {
				return nil, err
			}
			pkgRel := filepath.ToSlash(pkgRelRaw)
			pkgImport := "github.com/mschulkind-oss/" + modName
			if pkgRel != "." {
				pkgImport += "/" + pkgRel
			}

			targets := make(map[targetKey]bool)
			for _, match := range repoRelModRE.FindAllStringSubmatch(text, -1) {
				if modNames[match[1]] && match[1] != modName {
					targets[targetKey{mod: match[1], sub: strings.TrimRight(match[2], "/")}] = true
				}
			}
			for _, match := range parentRelModRE.FindAllStringSubmatch(text, -1) {
				if modNames[match[1]] && match[1] != modName {
					targets[targetKey{mod: match[1], sub: strings.TrimRight(match[2], "/")}] = true
				}
			}
			for _, match := range filepathJoinRE.FindAllStringSubmatch(text, -1) {
				argMatches := strLitRE.FindAllStringSubmatch(match[1], -1)
				args := make([]string, 0, len(argMatches))
				hasParentOrTools := false
				for _, am := range argMatches {
					args = append(args, am[1])
					if am[1] == ".." || am[1] == "tools" {
						hasParentOrTools = true
					}
				}
				if !hasParentOrTools {
					continue
				}
				for idx, arg := range args {
					if modNames[arg] && arg != modName {
						var parts []string
						for _, a := range args[idx+1:] {
							if !strings.Contains(a, "*") {
								parts = append(parts, a)
							}
						}
						sub := strings.TrimRight(strings.Join(parts, "/"), "/")
						targets[targetKey{mod: arg, sub: sub}] = true
						break
					}
				}
			}
			for _, match := range modGlobRE.FindAllStringSubmatch(text, -1) {
				globPat := match[1]
				for other := range modNames {
					if other == modName {
						continue
					}
					if globPat == "tillr-*" && !strings.HasPrefix(other, "tillr-") {
						continue
					}
					targets[targetKey{mod: other, sub: ""}] = true
				}
			}

			if len(targets) == 0 {
				continue
			}

			wholeMods := make(map[string]bool)
			for tk := range targets {
				if tk.sub == "" {
					wholeMods[tk.mod] = true
				}
			}
			var deduped []targetKey
			for tk := range targets {
				if tk.sub == "" || !wholeMods[tk.mod] {
					deduped = append(deduped, tk)
				}
			}
			sort.Slice(deduped, func(i, j int) bool {
				if deduped[i].mod != deduped[j].mod {
					return deduped[i].mod < deduped[j].mod
				}
				return deduped[i].sub < deduped[j].sub
			})

			readRules := edges[pkgImport].Reads
			pkgImports := packageTillrImports(pkgDir)
			relTestRaw, err := filepath.Rel(root, testFile)
			if err != nil {
				return nil, err
			}
			relTest := filepath.ToSlash(relTestRaw)

			for _, tk := range deduped {
				if tk.sub != "" && strings.Contains(path.Base(tk.sub), ".") && !strings.HasSuffix(tk.sub, ".go") {
					continue
				}
				targetPath := "tools/" + tk.mod
				if tk.sub != "" {
					targetPath += "/" + tk.sub
				}
				probe := targetPath
				if !strings.HasSuffix(probe, ".go") {
					probe = targetPath + "/synthetic.go"
				}
				matched := false
				for _, rule := range readRules {
					if PathMatchesRead(probe, rule) {
						matched = true
						break
					}
				}
				if matched {
					continue
				}
				targetPkgSub := tk.sub
				if strings.HasSuffix(tk.sub, ".go") {
					targetPkgSub = path.Dir(tk.sub)
					if targetPkgSub == "." {
						targetPkgSub = ""
					}
				}
				targetImp := "github.com/mschulkind-oss/" + tk.mod
				if targetPkgSub != "" {
					targetImp += "/" + targetPkgSub
				}
				if !isGoWalk && pkgImports[targetImp] {
					continue
				}
				findings = append(findings, fmt.Sprintf(
					"%s: %s reads Go source in %s without a declared read in tools/gate/go-test-edges.json",
					relTest, pkgImport, targetPath,
				))
			}
		}
	}
	return findings, nil
}

func decodeScopedTestsMap(
	entryMap map[string]json.RawMessage,
	mapKey, listKey string,
	allowedList []string,
	edgePath, pkg string,
) (map[string][]string, error) {
	rawMapVal, hasMap := entryMap[mapKey]
	if !hasMap {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(rawMapVal)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s: %s.%s must be an object", edgePath, pkg, mapKey)
	}
	var rawEntries map[string]json.RawMessage
	if err := json.Unmarshal(rawMapVal, &rawEntries); err != nil || rawEntries == nil {
		return nil, fmt.Errorf("%s: %s.%s must be an object", edgePath, pkg, mapKey)
	}
	allowedSet := make(map[string]bool, len(allowedList))
	for _, item := range allowedList {
		allowedSet[item] = true
	}
	out := make(map[string][]string, len(rawEntries))
	for rule, rawNames := range rawEntries {
		if !allowedSet[rule] {
			return nil, fmt.Errorf("%s: %s.%s key %q must appear in %s.%s", edgePath, pkg, mapKey, rule, pkg, listKey)
		}
		namesTrimmed := bytes.TrimSpace(rawNames)
		if len(namesTrimmed) == 0 || namesTrimmed[0] != '[' {
			return nil, fmt.Errorf("%s: %s.%s[%q] must be a non-empty list of test names", edgePath, pkg, mapKey, rule)
		}
		var rawList []json.RawMessage
		if err := json.Unmarshal(rawNames, &rawList); err != nil || len(rawList) == 0 {
			return nil, fmt.Errorf("%s: %s.%s[%q] must be a non-empty list of test names", edgePath, pkg, mapKey, rule)
		}
		names := make([]string, 0, len(rawList))
		for _, rn := range rawList {
			var s string
			if err := json.Unmarshal(rn, &s); err != nil || strings.TrimSpace(s) == "" || s != strings.TrimSpace(s) {
				return nil, fmt.Errorf("%s: %s.%s[%q] must be a non-empty list of test names", edgePath, pkg, mapKey, rule)
			}
			names = append(names, s)
		}
		out[rule] = names
	}
	return out, nil
}

func decodeStringList(entryMap map[string]json.RawMessage, key, edgePath, pkg string) ([]string, error) {
	rawVal, ok := entryMap[key]
	if !ok {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(rawVal)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("%s: %s.%s must be a list of strings", edgePath, pkg, key)
	}
	var rawList []json.RawMessage
	if err := json.Unmarshal(rawVal, &rawList); err != nil {
		return nil, fmt.Errorf("%s: %s.%s must be a list of strings", edgePath, pkg, key)
	}
	out := make([]string, 0, len(rawList))
	for _, item := range rawList {
		var s string
		if err := json.Unmarshal(item, &s); err != nil {
			return nil, fmt.Errorf("%s: %s.%s must be a list of strings", edgePath, pkg, key)
		}
		out = append(out, s)
	}
	return out, nil
}

// PathMatchesRead reports whether a repository-relative path matches a declared read rule.
func PathMatchesRead(p, rule string) bool {
	if strings.HasPrefix(p, ExcludedPackMirrorPrefix) && !strings.HasPrefix(rule, ExcludedPackMirrorPrefix) {
		return false
	}
	if strings.HasSuffix(rule, "/") {
		return strings.HasPrefix(p, rule)
	}
	return p == rule || strings.HasPrefix(p, rule+"/")
}

// ModuleReplaceDependents maps each module to the set of modules that replace it (directly or transitively).
func ModuleReplaceDependents(root string, modules []string) map[string]map[string]bool {
	direct := make(map[string]map[string]bool, len(modules))
	modSet := make(map[string]bool, len(modules))
	for _, m := range modules {
		direct[m] = make(map[string]bool)
		modSet[m] = true
	}
	for _, mod := range modules {
		gomod := filepath.Join(root, filepath.FromSlash(mod), "go.mod")
		raw, err := os.ReadFile(gomod)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "replace ") {
				line = strings.TrimPrefix(line, "replace ")
			}
			arrow := strings.Index(line, "=>")
			if arrow < 0 {
				continue
			}
			targetFields := strings.Fields(line[arrow+2:])
			if len(targetFields) == 0 {
				continue
			}
			t0 := targetFields[0]
			if !strings.HasPrefix(t0, "./") && !strings.HasPrefix(t0, "../") {
				continue
			}
			rel := path.Clean(path.Join(mod, t0))
			if modSet[rel] && rel != mod {
				direct[rel][mod] = true
			}
		}
	}
	closure := make(map[string]map[string]bool, len(modules))
	for _, target := range modules {
		seen := make(map[string]bool)
		var stack []string
		for cur := range direct[target] {
			stack = append(stack, cur)
		}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			for next := range direct[cur] {
				stack = append(stack, next)
			}
		}
		closure[target] = seen
	}
	return closure
}

// ListModulePackages runs `go list -deps -test -json ./...` in root/module and stream-decodes the JSON objects.
func ListModulePackages(root, module string, env []string) (bool, []GoListPackage) {
	if env == nil {
		env = BuildEnv()
	}
	cmd := exec.Command("go", "list", "-deps", "-test", "-json", "./...")
	cmd.Dir = filepath.Join(root, filepath.FromSlash(module))
	cmd.Env = env
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	dec := json.NewDecoder(&stdout)
	var pkgs []GoListPackage
	for {
		var pkg GoListPackage
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, nil
		}
		pkgs = append(pkgs, pkg)
	}
	return true, pkgs
}

// LoadTillrGraph loads the package and test-dependency graph across all modules.
func LoadTillrGraph(root string, modules []string) (*Graph, error) {
	resolvedRoot := resolvePath(root)
	if modules == nil {
		var err error
		modules, err = DiscoverTillrModules(resolvedRoot)
		if err != nil {
			return nil, err
		}
	} else {
		cp := make([]string, len(modules))
		copy(cp, modules)
		modules = cp
	}
	env := BuildEnv()

	type listResult struct {
		mod  string
		ok   bool
		objs []GoListPackage
	}
	results := make([]listResult, len(modules))
	var wg sync.WaitGroup
	for i, mod := range modules {
		wg.Add(1)
		go func(idx int, m string) {
			defer wg.Done()
			ok, objs := ListModulePackages(resolvedRoot, m, env)
			results[idx] = listResult{mod: m, ok: ok, objs: objs}
		}(i, mod)
	}
	wg.Wait()

	failedModules := make(map[string]bool)
	pkgInfo := make(map[string]PkgInfo)
	testDeps := make(map[string]map[string]bool)
	dirToPkg := make(map[string]string)
	moduleTestPkgs := make(map[string][]string, len(modules))
	for _, m := range modules {
		moduleTestPkgs[m] = nil
	}

	for _, res := range results {
		mod := res.mod
		if !res.ok {
			failedModules[mod] = true
			continue
		}
		modRoot := resolvePath(filepath.Join(resolvedRoot, filepath.FromSlash(mod)))
		for _, obj := range res.objs {
			if obj.Standard || obj.Dir == "" {
				continue
			}
			dPath := resolvePath(obj.Dir)
			relToRoot, err := filepath.Rel(resolvedRoot, dPath)
			if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
				continue
			}
			dirRel := filepath.ToSlash(relToRoot)
			ip := obj.ImportPath
			deps := make(map[string]bool, len(obj.Deps))
			for _, d := range obj.Deps {
				deps[d] = true
			}
			if obj.ForTest != "" {
				td := testDeps[obj.ForTest]
				if td == nil {
					td = make(map[string]bool)
					testDeps[obj.ForTest] = td
				}
				for d := range deps {
					td[d] = true
				}
				continue
			}
			if strings.HasSuffix(ip, ".test") && obj.Name == "main" {
				continue
			}
			var ownerMod string
			if isSubpath(dPath, modRoot) {
				ownerMod = mod
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
			tfSet := make(map[string]bool, len(obj.TestGoFiles)+len(obj.XTestGoFiles))
			for _, f := range obj.TestGoFiles {
				tfSet[f] = true
			}
			for _, f := range obj.XTestGoFiles {
				tfSet[f] = true
			}
			testFiles := make([]string, 0, len(tfSet))
			for f := range tfSet {
				testFiles = append(testFiles, f)
			}
			sort.Strings(testFiles)
			hasTests := len(testFiles) > 0
			if _, exists := pkgInfo[ip]; !exists {
				pkgInfo[ip] = PkgInfo{
					ImportPath: ip,
					Module:     ownerMod,
					DirRel:     dirRel,
					HasTests:   hasTests,
					TestFiles:  testFiles,
					Deps:       deps,
					Name:       obj.Name,
				}
				dirToPkg[dirRel] = ip
				td := testDeps[ip]
				if td == nil {
					td = make(map[string]bool)
					testDeps[ip] = td
				}
				for d := range deps {
					td[d] = true
				}
				if hasTests && !containsString(moduleTestPkgs[ownerMod], ip) {
					moduleTestPkgs[ownerMod] = append(moduleTestPkgs[ownerMod], ip)
				}
			}
		}
	}

	return &Graph{
		Modules:        modules,
		FailedModules:  failedModules,
		PkgInfo:        pkgInfo,
		TestDeps:       testDeps,
		DirToPkg:       dirToPkg,
		ModuleTestPkgs: moduleTestPkgs,
	}, nil
}

// GitChangedPaths returns the paths changed between base and head in root.
func GitChangedPaths(root, base, head string) ([]string, error) {
	env := BuildEnv()
	cmd := exec.Command("git", "-C", root, "diff", "--name-only", "--no-renames", "-z", base, head, "--")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(out, []byte{0})
	var changed []string
	for _, p := range parts {
		if len(p) > 0 {
			changed = append(changed, string(p))
		}
	}
	return changed, nil
}

// PosixDirname returns the POSIX directory name of path, or "" if path has no slash.
func PosixDirname(p string) string {
	idx := strings.LastIndexByte(p, '/')
	if idx < 0 {
		return ""
	}
	return p[:idx]
}

// BuildEnv returns an environment suitable for child `go` and `git` commands:
// CGO_ENABLED=0, outer gate/drain/git-override variables stripped, -trimpath in GOFLAGS,
// and git shims bypassed when present.
func BuildEnv() []string {
	envMap := make(map[string]string)
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			envMap[kv[:idx]] = kv[idx+1:]
		}
	}
	envMap["CGO_ENABLED"] = "0"
	for _, k := range strippedOuterEnv {
		delete(envMap, k)
	}
	goflags := strings.Fields(envMap["GOFLAGS"])
	hasTrimpath := false
	for _, f := range goflags {
		if f == "-trimpath" {
			hasTrimpath = true
			break
		}
	}
	if !hasTrimpath {
		goflags = append(goflags, "-trimpath")
	}
	envMap["GOFLAGS"] = strings.Join(goflags, " ")

	var durableScratch string
	if d := envMap["YOLO_DURABLE_DIR"]; d != "" {
		durableScratch = filepath.Join(d, ".go-affected-scratch")
	} else if d := envMap["TMPDIR"]; d != "" {
		durableScratch = filepath.Join(d, ".go-affected-scratch")
	}
	bypassGitShimEnv(envMap, sharedCacheDir(envMap), durableScratch)

	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+envMap[k])
	}
	return out
}

func sharedCacheDir(envMap map[string]string) string {
	if gc := strings.TrimSpace(envMap["GOCACHE"]); gc != "" && gc != "off" {
		return resolvePath(gc)
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return resolvePath(filepath.Join(home, "Library", "Caches", "go-build"))
	}
	xdg := strings.TrimSpace(envMap["XDG_CACHE_HOME"])
	base := xdg
	if base == "" {
		base = filepath.Join(home, ".cache")
	}
	return resolvePath(filepath.Join(base, "go-build"))
}

func isGitShim(p string) bool {
	cleaned := filepath.Clean(p)
	parts := strings.Split(cleaned, string(filepath.Separator))
	if len(parts) >= 3 && parts[len(parts)-3] == ".yolo" && parts[len(parts)-2] == "bin" && parts[len(parts)-1] == "block" {
		return true
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	head := buf[:n]
	return bytes.HasPrefix(head, []byte("#!")) &&
		(bytes.Contains(head, []byte("_yg_tool=git")) || bytes.Contains(head, []byte("# tillr-git-guard")))
}

func isTillrGitGuard(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, err := f.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return bytes.HasPrefix(buf[:n], []byte("#!/bin/sh\n# tillr-git-guard"))
}

func resolveRealGit(pathValue string) (string, bool) {
	sawShim := false
	for _, entry := range filepath.SplitList(pathValue) {
		if entry == "" {
			continue
		}
		candidate := filepath.Join(entry, "git")
		if !isExecutableFile(candidate) {
			continue
		}
		if isGitShim(candidate) {
			sawShim = true
			continue
		}
		return candidate, sawShim
	}
	if sawShim {
		for _, fallback := range []string{"/bin/git", "/usr/bin/git"} {
			if isExecutableFile(fallback) && !isGitShim(fallback) {
				return fallback, true
			}
		}
	}
	return "", sawShim
}

func bypassGitShimEnv(envMap map[string]string, candidateRoots ...string) {
	pathVal := envMap["PATH"]
	realGit, sawShim := resolveRealGit(pathVal)
	if !sawShim {
		return
	}
	var kept []string
	for _, entry := range filepath.SplitList(pathVal) {
		if entry != "" && !isTillrGitGuard(filepath.Join(entry, "git")) {
			kept = append(kept, entry)
		}
	}
	cleanedPath := strings.Join(kept, string(os.PathListSeparator))
	realAfter, stillShimmed := resolveRealGit(cleanedPath)
	if !stillShimmed && realAfter != "" {
		envMap["PATH"] = cleanedPath
		return
	}
	if realGit != "" {
		for _, rootDir := range candidateRoots {
			if rootDir == "" {
				continue
			}
			gitBin, err := ensureGitBinDir(rootDir, realGit)
			if err != nil {
				continue
			}
			if cleanedPath != "" {
				envMap["PATH"] = gitBin + string(os.PathListSeparator) + cleanedPath
			} else {
				envMap["PATH"] = gitBin
			}
			return
		}
	}
	envMap["PATH"] = cleanedPath
	envMap["YOLO_BYPASS_SHIMS"] = "1"
}

func ensureGitBinDir(baseDir, realGit string) (string, error) {
	sum := sha256.Sum256([]byte(realGit))
	digest := hex.EncodeToString(sum[:])[:12]
	binDir := filepath.Join(baseDir, "go-test-git-bin", digest)
	link := filepath.Join(binDir, "git")
	if target, err := os.Readlink(link); err == nil && target == realGit {
		return binDir, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	tmpLink := filepath.Join(binDir, fmt.Sprintf(".git.%d", os.Getpid()))
	_ = os.Remove(tmpLink)
	if err := os.Symlink(realGit, tmpLink); err != nil {
		if target, rerr := os.Readlink(link); rerr == nil && target == realGit {
			return binDir, nil
		}
		return "", err
	}
	if err := os.Rename(tmpLink, link); err != nil {
		_ = os.Remove(tmpLink)
		if target, rerr := os.Readlink(link); rerr == nil && target == realGit {
			return binDir, nil
		}
		return "", err
	}
	return binDir, nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func isExecutableFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && (info.Mode()&0o111 != 0)
}

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		return realPath
	}
	return abs
}

func isSubpath(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
