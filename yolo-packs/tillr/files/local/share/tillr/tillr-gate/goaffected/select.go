package goaffected

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// topLevelTestRE matches top-level Test*, Fuzz*, and Example* functions in Go test files,
// matching TOP_LEVEL_TEST_RE in tools/gate/go_affected.py without lookahead.
var topLevelTestRE = regexp.MustCompile(
	`(?m)^func\s+(?:(Test(?:[^a-z\W]\w*)?)\s*\(\s*\w+\s+\*testing\.T\b|(Fuzz(?:[^a-z\W]\w*)?)\s*\(\s*\w+\s+\*testing\.F\b|(Example(?:[^a-z\W]\w*)?)\s*\(\s*\))`,
)

// ResolvedBuilds holds the per-target resolved build input files and missing build targets for an edge.
type ResolvedBuilds struct {
	PerTarget map[string][]string
	Missing   map[string]bool
}

// SelectedPackage represents one selected test package in the selection output.
type SelectedPackage struct {
	ImportPath     string          `json:"import_path"`
	Module         string          `json:"module"`
	DirRel         string          `json:"dir_rel"`
	TestFiles      []string        `json:"test_files"`
	Reason         string          `json:"reason"`
	SelectedTests  []string        `json:"selected_tests"`
	HasEdge        bool            `json:"has_edge"`
	EdgeDigest     *string         `json:"edge_digest"`
	ResolvedBuilds *ResolvedBuilds `json:"-"`
}

// Selection is the result of SelectAffectedPackages, matching the JSON schema of --select-only.
type Selection struct {
	SelectionMs  int64             `json:"selection_ms"`
	Modules      []string          `json:"modules"`
	Selected     []SelectedPackage `json:"selected"`
	FloorModules []string          `json:"floor_modules"`
}

// DeferredProofPackages lists packages whose subprocess-heavy integration suites are
// proved after the merge (go-test-tillr-drain) rather than in pre-merge go-affected
// (D-GR53, D-GR54).
var DeferredProofPackages = map[string]bool{
	"github.com/mschulkind-oss/tillr-drain/drain": true,
}

// SelectAffectedPackages computes which Go test packages are affected by changed paths,
// using DeferredProofPackages as the default dropped-package set.
func SelectAffectedPackages(
	root string,
	changed []string,
	dropModules map[string]bool,
	graph *Graph,
	edges map[string]EdgeSpec,
) (*Selection, error) {
	return SelectAffectedPackagesWithDrop(root, changed, dropModules, nil, graph, edges)
}

// SelectAffectedPackagesWithDrop computes which Go test packages are affected by changed paths.
// When dropPackages is nil, DeferredProofPackages is used; pass an empty non-nil map to include
// deferred packages.
func SelectAffectedPackagesWithDrop(
	root string,
	changed []string,
	dropModules map[string]bool,
	dropPackages map[string]bool,
	graph *Graph,
	edges map[string]EdgeSpec,
) (*Selection, error) {
	started := time.Now()
	resolvedRoot := resolvePath(root)

	var modules []string
	if graph != nil {
		modules = make([]string, len(graph.Modules))
		copy(modules, graph.Modules)
	} else {
		var err error
		modules, err = DiscoverTillrModules(resolvedRoot)
		if err != nil {
			return nil, err
		}
	}
	if modules == nil {
		modules = []string{}
	}

	if edges == nil {
		var err error
		edges, err = LoadEdges(resolvedRoot)
		if err != nil {
			return nil, err
		}
	}

	dropPkgSet := DeferredProofPackages
	if dropPackages != nil {
		dropPkgSet = dropPackages
	}

	normChanged := make([]string, 0, len(changed))
	for _, p := range changed {
		if strings.TrimSpace(p) == "" {
			continue
		}
		cleaned := path.Clean(filepath.ToSlash(p))
		cleaned = strings.TrimLeft(cleaned, "./")
		normChanged = append(normChanged, cleaned)
	}

	var allReads []string
	for ip, e := range edges {
		if dropPkgSet[ip] {
			continue
		}
		allReads = append(allReads, e.Reads...)
	}

	var relevant []string
	for _, p := range normChanged {
		isMod := false
		for _, m := range modules {
			if p == m || strings.HasPrefix(p, m+"/") {
				isMod = true
				break
			}
		}
		if isMod {
			relevant = append(relevant, p)
			continue
		}
		for _, r := range allReads {
			if PathMatchesRead(p, r) {
				relevant = append(relevant, p)
				break
			}
		}
	}

	if len(relevant) == 0 {
		return &Selection{
			SelectionMs:  time.Since(started).Milliseconds(),
			Modules:      modules,
			Selected:     []SelectedPackage{},
			FloorModules: []string{},
		}, nil
	}

	if graph == nil {
		var err error
		graph, err = LoadTillrGraph(resolvedRoot, modules)
		if err != nil {
			return nil, err
		}
	}

	failedModules := graph.FailedModules
	pkgInfo := graph.PkgInfo
	testDeps := graph.TestDeps
	dirToPkg := graph.DirToPkg
	moduleTestPkgs := graph.ModuleTestPkgs

	reasons := make(map[string]string)
	priority := map[string]int{"changed": 4, "import": 3, "edge": 2, "floor": 1}
	mark := func(pkg, reason string) {
		if prev, ok := reasons[pkg]; !ok || priority[reason] > priority[prev] {
			reasons[pkg] = reason
		}
	}

	changedPkgs := make(map[string]bool)
	floorTriggers := make(map[string]bool)
	wholePkgSelected := make(map[string]bool)
	matchedScopedBuilds := make(map[string][]string)
	matchedScopedReads := make(map[string][]string)

	// Step 1: Changed paths -> packages or floor triggers.
	for _, p := range relevant {
		var ownerMod string
		for _, m := range modules {
			if p == m || strings.HasPrefix(p, m+"/") {
				ownerMod = m
				break
			}
		}
		if ownerMod == "" {
			continue
		}
		if failedModules[ownerMod] {
			floorTriggers[ownerMod] = true
			continue
		}
		if p == ownerMod+"/go.mod" || p == ownerMod+"/go.sum" {
			floorTriggers[ownerMod] = true
			continue
		}
		immDir := PosixDirname(p)
		if !isDir(filepath.Join(resolvedRoot, filepath.FromSlash(immDir))) {
			floorTriggers[ownerMod] = true
			continue
		}
		cur := immDir
		var placed string
		for {
			if ip, ok := dirToPkg[cur]; ok {
				placed = ip
				break
			}
			if cur == ownerMod || !strings.Contains(cur, "/") {
				break
			}
			cur = PosixDirname(cur)
		}
		if placed == "" {
			floorTriggers[ownerMod] = true
		} else {
			if !strings.HasSuffix(p, "_test.go") {
				changedPkgs[placed] = true
			}
			if pkgInfo[placed].HasTests {
				mark(placed, "changed")
				wholePkgSelected[placed] = true
			}
		}
	}

	// Step 2: Importers (reverse import graph across all tillr modules).
	if len(changedPkgs) > 0 {
		for ip, info := range pkgInfo {
			if !info.HasTests {
				continue
			}
			if setsIntersect(testDeps[ip], changedPkgs) {
				mark(ip, "import")
				wholePkgSelected[ip] = true
			}
		}
	}

	// Step 3: Declared edges (builds + reads).
	for testPkg, edge := range edges {
		if dropPkgSet[testPkg] {
			continue
		}
		info, ok := pkgInfo[testPkg]
		if !ok || !info.HasTests {
			continue
		}
		var matchedBuilds []string
		for _, target := range edge.Builds {
			if changedPkgs[target] {
				matchedBuilds = append(matchedBuilds, target)
				continue
			}
			if tinfo, tok := pkgInfo[target]; tok && setsIntersect(tinfo.Deps, changedPkgs) {
				matchedBuilds = append(matchedBuilds, target)
			}
		}
		var matchedReads []string
		for _, rule := range edge.Reads {
			for _, p := range normChanged {
				if PathMatchesRead(p, rule) {
					matchedReads = append(matchedReads, rule)
					break
				}
			}
		}
		if len(matchedBuilds) > 0 || len(matchedReads) > 0 {
			mark(testPkg, "edge")
			allScoped := true
			for _, target := range matchedBuilds {
				if _, has := edge.BuildTests[target]; !has {
					allScoped = false
					break
				}
			}
			if allScoped {
				for _, r := range matchedReads {
					if _, has := edge.ReadTests[r]; !has {
						allScoped = false
						break
					}
				}
			}
			if !allScoped {
				wholePkgSelected[testPkg] = true
			} else {
				if len(matchedBuilds) > 0 {
					matchedScopedBuilds[testPkg] = matchedBuilds
				}
				if len(matchedReads) > 0 {
					matchedScopedReads[testPkg] = matchedReads
				}
			}
		}
	}

	// Step 4: Floor (unplaceable changes -> module + modules replacing it).
	floorModules := []string{}
	var failedFloorMods []string
	if len(floorTriggers) > 0 {
		dependents := ModuleReplaceDependents(resolvedRoot, modules)
		expanded := make(map[string]bool, len(floorTriggers))
		for trig := range floorTriggers {
			expanded[trig] = true
			for dep := range dependents[trig] {
				expanded[dep] = true
			}
		}
		floorModules = make([]string, 0, len(expanded))
		for mod := range expanded {
			floorModules = append(floorModules, mod)
		}
		sort.Strings(floorModules)
		for _, mod := range floorModules {
			if failedModules[mod] {
				if !dropModules[mod] {
					failedFloorMods = append(failedFloorMods, mod)
				}
				continue
			}
			for _, ip := range moduleTestPkgs[mod] {
				mark(ip, "floor")
				wholePkgSelected[ip] = true
			}
		}
	}

	reasonKeys := make([]string, 0, len(reasons))
	for ip := range reasons {
		reasonKeys = append(reasonKeys, ip)
	}
	sort.Strings(reasonKeys)

	selected := []SelectedPackage{}
	for _, ip := range reasonKeys {
		info := pkgInfo[ip]
		if dropModules[info.Module] || dropPkgSet[ip] {
			continue
		}
		edge, hasEdge := edges[ip]
		var selectedTests []string
		if !wholePkgSelected[ip] {
			buildTargets := matchedScopedBuilds[ip]
			readRules := matchedScopedReads[ip]
			if len(buildTargets) > 0 || len(readRules) > 0 {
				var wantedList []string
				wantedSet := make(map[string]bool)
				for _, target := range buildTargets {
					for _, tname := range edge.BuildTests[target] {
						if tname != "TestEdgeInputs" && !wantedSet[tname] {
							wantedSet[tname] = true
							wantedList = append(wantedList, tname)
						}
					}
				}
				for _, rule := range readRules {
					for _, tname := range edge.ReadTests[rule] {
						if tname != "TestEdgeInputs" && !wantedSet[tname] {
							wantedSet[tname] = true
							wantedList = append(wantedList, tname)
						}
					}
				}
				pkgTests, err := ScanPackageTests(resolvedRoot, info.DirRel, info.TestFiles)
				if err != nil {
					return nil, err
				}
				ordered := make([]string, 0, len(wantedList))
				orderedSet := make(map[string]bool, len(wantedList))
				for _, t := range pkgTests {
					if wantedSet[t] {
						ordered = append(ordered, t)
						orderedSet[t] = true
					}
				}
				for _, t := range wantedList {
					if !orderedSet[t] {
						ordered = append(ordered, t)
					}
				}
				selectedTests = ordered
			}
		}

		var rb *ResolvedBuilds
		var digestPtr *string
		if hasEdge {
			rb = ResolveEdgeBuilds(resolvedRoot, edge, pkgInfo)
			dig, err := ComputeEdgeDigest(resolvedRoot, edge, pkgInfo, selectedTests, rb)
			if err != nil {
				return nil, err
			}
			digestPtr = &dig
		}
		testFiles := info.TestFiles
		if testFiles == nil {
			testFiles = []string{}
		}
		selected = append(selected, SelectedPackage{
			ImportPath:     ip,
			Module:         info.Module,
			DirRel:         info.DirRel,
			TestFiles:      testFiles,
			Reason:         reasons[ip],
			SelectedTests:  selectedTests,
			HasEdge:        hasEdge,
			EdgeDigest:     digestPtr,
			ResolvedBuilds: rb,
		})
	}

	for _, mod := range failedFloorMods {
		selected = append(selected, SelectedPackage{
			ImportPath:    "./...",
			Module:        mod,
			DirRel:        mod,
			TestFiles:     []string{},
			Reason:        "floor",
			SelectedTests: nil,
			HasEdge:       false,
			EdgeDigest:    nil,
		})
	}

	return &Selection{
		SelectionMs:  time.Since(started).Milliseconds(),
		Modules:      modules,
		Selected:     selected,
		FloorModules: floorModules,
	}, nil
}

// ScanPackageTests scans top-level Test*, Fuzz*, and Example* functions from Go test source files.
func ScanPackageTests(root string, dirRel string, testFiles []string) ([]string, error) {
	pkgDir := filepath.Join(root, filepath.FromSlash(dirRel))
	if !isDir(pkgDir) {
		return []string{}, nil
	}
	files := testFiles
	if len(files) == 0 {
		matches, err := filepath.Glob(filepath.Join(pkgDir, "*_test.go"))
		if err != nil {
			return nil, err
		}
		files = make([]string, 0, len(matches))
		for _, m := range matches {
			files = append(files, filepath.Base(m))
		}
		sort.Strings(files)
	}
	var seen []string
	seenSet := make(map[string]bool)
	for _, fname := range files {
		raw, err := os.ReadFile(filepath.Join(pkgDir, fname))
		if err != nil {
			continue
		}
		for _, match := range topLevelTestRE.FindAllStringSubmatch(string(raw), -1) {
			name := match[1]
			if name == "" {
				name = match[2]
			}
			if name == "" {
				name = match[3]
			}
			if name != "" && name != "TestMain" && !seenSet[name] {
				seenSet[name] = true
				seen = append(seen, name)
			}
		}
	}
	return seen, nil
}

// ResolveEdgeBuilds resolves all files in the transitive build dependency closure of each target in edge.Builds.
func ResolveEdgeBuilds(root string, edge EdgeSpec, pkgInfo map[string]PkgInfo) *ResolvedBuilds {
	perTarget := make(map[string][]string, len(edge.Builds))
	missing := make(map[string]bool)

	for _, target := range edge.Builds {
		info, ok := pkgInfo[target]
		if !ok {
			missing[target] = true
			continue
		}
		fileSet := make(map[string]bool)
		mods := make(map[string]bool)
		closure := make(map[string]bool, len(info.Deps)+1)
		closure[target] = true
		for d := range info.Deps {
			closure[d] = true
		}
		for dep := range closure {
			dinfo, dok := pkgInfo[dep]
			if !dok {
				continue
			}
			mods[dinfo.Module] = true
			dpath := filepath.Join(root, filepath.FromSlash(dinfo.DirRel))
			entries, err := os.ReadDir(dpath)
			if err != nil {
				continue
			}
			for _, ent := range entries {
				full := filepath.Join(dpath, ent.Name())
				if isFile(full) {
					rel, rerr := filepath.Rel(root, full)
					if rerr == nil {
						fileSet[filepath.ToSlash(rel)] = true
					}
				}
			}
		}
		for mod := range mods {
			for _, mf := range []string{"go.mod", "go.sum"} {
				mp := filepath.Join(root, filepath.FromSlash(mod), mf)
				if isFile(mp) {
					fileSet[mod+"/"+mf] = true
				}
			}
		}
		files := make([]string, 0, len(fileSet))
		for f := range fileSet {
			files = append(files, f)
		}
		sort.Strings(files)
		perTarget[target] = files
	}

	return &ResolvedBuilds{
		PerTarget: perTarget,
		Missing:   missing,
	}
}

// ComputeEdgeDigest computes the SHA-256 edge digest matching compute_edge_digest in tools/gate/go_affected.py.
func ComputeEdgeDigest(
	root string,
	edge EdgeSpec,
	pkgInfo map[string]PkgInfo,
	activeTests []string,
	resolvedBuilds *ResolvedBuilds,
) (string, error) {
	hasher := sha256.New()
	if resolvedBuilds == nil {
		resolvedBuilds = ResolveEdgeBuilds(root, edge, pkgInfo)
	}

	var activeSet map[string]bool
	if activeTests != nil {
		activeSet = make(map[string]bool, len(activeTests))
		for _, t := range activeTests {
			activeSet[t] = true
		}
	}

	fileSet := make(map[string]bool)
	for _, target := range edge.Builds {
		if activeSet != nil {
			if btNames, hasBT := edge.BuildTests[target]; hasBT {
				hit := false
				for _, t := range btNames {
					if activeSet[t] {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
		}
		if resolvedBuilds.Missing[target] {
			hasher.Write([]byte("missing-build:" + target + "\n"))
		} else if targetFiles, ok := resolvedBuilds.PerTarget[target]; ok {
			for _, bf := range targetFiles {
				fileSet[bf] = true
			}
		} else {
			hasher.Write([]byte("missing-build:" + target + "\n"))
		}
	}

	for _, rule := range edge.Reads {
		if activeSet != nil {
			if rtNames, hasRT := edge.ReadTests[rule]; hasRT {
				hit := false
				for _, t := range rtNames {
					if activeSet[t] {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
		}
		rp := filepath.Join(root, filepath.FromSlash(strings.TrimRight(rule, "/")))
		info, err := os.Stat(rp)
		if err == nil && !info.IsDir() {
			rel, rerr := filepath.Rel(root, rp)
			if rerr == nil {
				fileSet[filepath.ToSlash(rel)] = true
			}
		} else if err == nil && info.IsDir() {
			walkErr := filepath.WalkDir(rp, func(p string, d fs.DirEntry, werr error) error {
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
				if d.Type()&os.ModeSymlink != 0 && isDir(p) {
					return nil
				}
				rel, rerr := filepath.Rel(root, p)
				if rerr != nil {
					return nil
				}
				relSlash := filepath.ToSlash(rel)
				if strings.HasPrefix(relSlash, ExcludedPackMirrorPrefix) && !strings.HasPrefix(rule, ExcludedPackMirrorPrefix) {
					return nil
				}
				fileSet[relSlash] = true
				return nil
			})
			if walkErr != nil {
				return "", walkErr
			}
		} else {
			hasher.Write([]byte("missing-read:" + rule + "\n"))
		}
	}

	sortedFiles := make([]string, 0, len(fileSet))
	for f := range fileSet {
		sortedFiles = append(sortedFiles, f)
	}
	sort.Strings(sortedFiles)

	for _, rel := range sortedFiles {
		hasher.Write([]byte(rel))
		hasher.Write([]byte{0})
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			hasher.Write([]byte("<unreadable>"))
		} else {
			hasher.Write(raw)
		}
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func setsIntersect(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}
