package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr/checkresult"
)

var goToolDirs = []string{
	"tools/tillr", "tools/tillr-store", "tools/tillr-item",
	"tools/tillr-run", "tools/tillr-gate", "tools/tillr-view", "tools/tillr-land",
	"tools/tillr-loop", "tools/tillr-drain", "tools/tillr-cli", "tools/tillr-watch", "tools/tillr-trace",
	"tools/tillr-roll", "tools/gate",
}

var goBuildCodePaths = []string{
	"tools/gate/go_build.go",
	"tools/gate/change_common.go",
	"tools/gate/roadmap_sections.go",
	"tools/gate/main.go",
	"tools/gate/repo-gate",
}

var (
	testStatusLineRe = regexp.MustCompile(`^(ok|\?)\s+(\S+)\s+(\(cached\)|[\d.]+s|\[no test files\])(?:\s.*)?$`)
	testCoverLineRe  = regexp.MustCompile(`^(\S+)\t+coverage: (?:0(?:\.0+)?% of statements|\[no statements\])$`)
)

type goCommandFunc func(args []string, dir string, env []string) (int, string, string, error)

var defaultGoCommand goCommandFunc = func(args []string, dir string, env []string) (int, string, string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			return 0, "", "", err
		}
	}
	return code, stdout.String(), stderr.String(), nil
}

func buildEnvFrom(baseEnv []string) []string {
	var out []string
	goflags := ""
	for _, e := range baseEnv {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		if k == "CGO_ENABLED" || k == "LAND_BATCH_LIMIT" {
			continue
		}
		if k == "GOFLAGS" {
			goflags = v
			continue
		}
		out = append(out, e)
	}
	out = append(out, "CGO_ENABLED=0")
	if goflags != "" {
		out = append(out, "GOFLAGS="+goflags+" -trimpath")
	} else {
		out = append(out, "GOFLAGS=-trimpath")
	}
	return out
}

func testCacheEvidence(stdout string) map[string]string {
	cache := make(map[string]string)
	for _, line := range splitLines(stdout) {
		hasPkgField := false
		pkgField := ""
		if strings.HasPrefix(line, "{") {
			var event struct {
				Action  string `json:"Action"`
				Test    string `json:"Test"`
				Package string `json:"Package"`
				Output  string `json:"Output"`
			}
			if json.Unmarshal([]byte(line), &event) != nil {
				continue
			}
			if event.Action != "output" || event.Test != "" || event.Package == "" {
				continue
			}
			hasPkgField = true
			pkgField = event.Package
			line = strings.TrimRight(event.Output, "\n")
		}
		if m := testStatusLineRe.FindStringSubmatch(line); m != nil {
			status, pkg, detail := m[1], m[2], m[3]
			if hasPkgField && pkg != pkgField {
				continue
			}
			if status == "?" && detail == "[no test files]" {
				cache[pkg] = "not-applicable"
			} else if status == "ok" && detail != "[no test files]" {
				if detail == "(cached)" {
					cache[pkg] = "hit"
				} else {
					cache[pkg] = "miss"
				}
			}
		} else if m := testCoverLineRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if !hasPkgField || pkgField == m[1] {
				cache[m[1]] = "not-applicable"
			}
		}
	}
	return cache
}

type packageEvidence struct {
	ImportPath string `json:"import_path"`
	TestCache  string `json:"test_cache"`
}

type moduleEvidence struct {
	Module    string            `json:"module"`
	Execution string            `json:"execution"`
	Mode      string            `json:"mode,omitempty"`
	Discovery string            `json:"discovery,omitempty"`
	Packages  []packageEvidence `json:"packages,omitempty"`
}

func writeGoBuildEvidence(outPath string, modules []moduleEvidence) {
	f, err := os.CreateTemp(filepath.Dir(outPath), ".evidence-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-build: could not record package evidence: %v\n", err)
		return
	}
	tmpName := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	data, err := json.Marshal(modules)
	if err != nil {
		_ = f.Close()
		fmt.Fprintf(os.Stderr, "go-build: could not record package evidence: %v\n", err)
		return
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		fmt.Fprintf(os.Stderr, "go-build: could not record package evidence: %v\n", err)
		return
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "go-build: could not record package evidence: %v\n", err)
		return
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "go-build: could not record package evidence: %v\n", err)
		return
	}
	cleanup = false
}

func buildModule(module, scratch string, runCmd goCommandFunc, evidence *moduleEvidence, publish func(moduleEvidence)) (int, string, error) {
	if runCmd == nil {
		runCmd = defaultGoCommand
	}
	env := buildEnvFrom(os.Environ())
	outputDir, err := os.MkdirTemp(scratch, "gate-go-build-")
	if err != nil {
		return 0, "", err
	}
	defer os.RemoveAll(outputDir)

	listArgs := []string{"go", "list", "-f", `{{if eq .Name "main"}}M{{end}}	{{.ImportPath}}`, "./..."}
	listCode, listOut, listErr, err := runCmd(listArgs, module, env)
	if err != nil {
		return 0, "", err
	}
	complete := listOut
	if listCode != 0 {
		if idx := strings.LastIndex(listOut, "\n"); idx >= 0 {
			complete = listOut[:idx]
		} else {
			complete = ""
		}
	}
	type listRow struct {
		isMain bool
		pkg    string
	}
	var rows []listRow
	for _, line := range splitLines(complete) {
		marker, pkg, _ := strings.Cut(line, "\t")
		if pkg != "" {
			rows = append(rows, listRow{isMain: marker == "M", pkg: pkg})
		}
	}
	if evidence != nil {
		evidence.Mode = "compile-only"
		if listCode != 0 {
			evidence.Discovery = "partial"
		} else {
			evidence.Discovery = "complete"
		}
		pkgs := make([]packageEvidence, 0, len(rows))
		for _, r := range rows {
			pkgs = append(pkgs, packageEvidence{ImportPath: r.pkg, TestCache: "not-started"})
		}
		evidence.Packages = pkgs
		if publish != nil {
			publish(*evidence)
		}
	}
	if listCode != 0 {
		return listCode, listOut + listErr, nil
	}
	if evidence != nil {
		for i := range evidence.Packages {
			evidence.Packages[i].TestCache = "unknown"
		}
		if publish != nil {
			publish(*evidence)
		}
	}

	testArgs := []string{"go", "test", "-exec", "true", "./..."}
	testCode, testOut, testErr, err := runCmd(testArgs, module, env)
	if err != nil {
		return 0, "", err
	}
	if evidence != nil {
		cache := testCacheEvidence(testOut)
		for i := range evidence.Packages {
			if status, ok := cache[evidence.Packages[i].ImportPath]; ok {
				evidence.Packages[i].TestCache = status
			} else {
				evidence.Packages[i].TestCache = "unknown"
			}
		}
	}
	if testCode != 0 {
		return testCode, testOut + testErr, nil
	}

	var mains []string
	for _, r := range rows {
		if r.isMain {
			mains = append(mains, r.pkg)
		}
	}
	if len(mains) > 0 {
		buildArgs := append([]string{"go", "build", "-o", outputDir}, mains...)
		buildCode, buildOut, buildErr, err := runCmd(buildArgs, module, env)
		if err != nil {
			return 0, "", err
		}
		if buildCode != 0 {
			return buildCode, buildOut + buildErr, nil
		}
	}
	return 0, testOut + testErr, nil
}

func goMetadata(root string, env []string, runCmd goCommandFunc) (map[string]string, string, error) {
	if runCmd == nil {
		runCmd = defaultGoCommand
	}
	code, out, errOut, err := runCmd([]string{"go", "env", "-json"}, root, env)
	if err != nil {
		return nil, "", err
	}
	vCode, vOut, vErr, err := runCmd([]string{"go", "version"}, root, env)
	if err != nil {
		return nil, "", err
	}
	if code != 0 || vCode != 0 {
		return nil, "", fmt.Errorf("could not read Go's effective environment/version: %s%s", errOut, vErr)
	}
	var settings map[string]string
	if err := json.Unmarshal([]byte(out), &settings); err != nil {
		return nil, "", err
	}
	for _, name := range []string{"GOMOD", "GOTMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOENV", "GOROOT", "GOTOOLDIR", "GOGCCFLAGS"} {
		delete(settings, name)
	}
	return settings, vOut, nil
}

func localReplaces(module string, env []string, runCmd goCommandFunc) ([]string, bool) {
	if runCmd == nil {
		runCmd = defaultGoCommand
	}
	if _, err := os.ReadFile(filepath.Join(module, "go.mod")); err != nil {
		return nil, false
	}
	code, out, _, err := runCmd([]string{"go", "mod", "edit", "-json"}, module, env)
	if err != nil || code != 0 {
		return nil, false
	}
	var parsed struct {
		Replace []struct {
			New struct {
				Path    string `json:"Path"`
				Version string `json:"Version"`
			} `json:"New"`
		} `json:"Replace"`
	}
	if json.Unmarshal([]byte(out), &parsed) != nil {
		return nil, false
	}
	var rels []string
	for _, r := range parsed.Replace {
		if r.New.Version == "" && r.New.Path != "" {
			rels = append(rels, r.New.Path)
		}
	}
	return rels, true
}

func dependencyWaves(modules []string, moduleDeps map[string][]string) [][]string {
	if len(modules) == 0 {
		return nil
	}
	if len(modules) == 1 {
		return [][]string{{modules[0]}}
	}
	byResolved := make(map[string]string, len(modules))
	for _, m := range modules {
		if abs, err := filepath.Abs(m); err == nil {
			byResolved[abs] = m
		}
	}
	deps := make(map[string][]string, len(modules))
	for _, m := range modules {
		abs, _ := filepath.Abs(m)
		for _, target := range moduleDeps[abs] {
			if targetMod, ok := byResolved[target]; ok && target != abs {
				deps[m] = append(deps[m], targetMod)
			}
		}
	}

	index := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var sccs [][]string
	counter := 0

	var strongconnect func(node string)
	strongconnect = func(node string) {
		index[node] = counter
		lowlink[node] = counter
		counter++
		stack = append(stack, node)
		onStack[node] = true
		for _, dep := range deps[node] {
			if _, seen := index[dep]; !seen {
				strongconnect(dep)
				if lowlink[dep] < lowlink[node] {
					lowlink[node] = lowlink[dep]
				}
			} else if onStack[dep] {
				if index[dep] < lowlink[node] {
					lowlink[node] = index[dep]
				}
			}
		}
		if lowlink[node] == index[node] {
			var comp []string
			for {
				member := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[member] = false
				comp = append(comp, member)
				if member == node {
					break
				}
			}
			sccs = append(sccs, comp)
		}
	}

	for _, m := range modules {
		if _, seen := index[m]; !seen {
			strongconnect(m)
		}
	}

	sccOf := make(map[string]int, len(modules))
	for idx, comp := range sccs {
		for _, m := range comp {
			sccOf[m] = idx
		}
	}
	sccLevel := make(map[int]int, len(sccs))
	for idx, comp := range sccs {
		maxDep := -1
		for _, member := range comp {
			for _, dep := range deps[member] {
				if sccOf[dep] != idx && sccLevel[sccOf[dep]] > maxDep {
					maxDep = sccLevel[sccOf[dep]]
				}
			}
		}
		sccLevel[idx] = maxDep + 1
	}
	maxLevel := 0
	for _, lvl := range sccLevel {
		if lvl > maxLevel {
			maxLevel = lvl
		}
	}
	waves := make([][]string, maxLevel+1)
	for _, m := range modules {
		lvl := sccLevel[sccOf[m]]
		waves[lvl] = append(waves[lvl], m)
	}
	return waves
}

type moduleKey struct {
	key   string
	bound bool
}

func isRelativeTo(p, base string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func moduleKeys(root string, modules []string, runCmd goCommandFunc) (map[string]moduleKey, map[string][]string, error) {
	if len(modules) == 0 {
		return map[string]moduleKey{}, map[string][]string{}, nil
	}
	entries, err := treeEntries(root, "HEAD")
	if err != nil {
		return nil, nil, err
	}
	code := make(map[string]string, len(goBuildCodePaths))
	codeSet := make(map[string]bool, len(goBuildCodePaths))
	for _, p := range goBuildCodePaths {
		codeSet[p] = true
		if e, ok := entries[p]; ok {
			code[p] = e.obj
		} else {
			code[p] = "absent"
		}
	}

	env := buildEnvFrom(os.Environ())
	settings, versionStdout, err := goMetadata(root, env, runCmd)
	if err != nil {
		return nil, nil, err
	}
	workspace := settings["GOWORK"]
	outsideWorkspace := false
	if workspace != "" && workspace != "off" {
		absWS, err := filepath.Abs(workspace)
		if err == nil && isRelativeTo(absWS, root) {
			rel, _ := filepath.Rel(root, absWS)
			settings["GOWORK"] = filepath.ToSlash(rel)
		} else {
			outsideWorkspace = true
		}
	}
	goflags := settings["GOFLAGS"]
	fileFlags := false
	for _, flag := range []string{"-overlay", "-modfile", "-toolexec", "-pkgdir", "-asmflags", "-gcflags", "-ldflags", "-gccgoflags", "-compiler", "-C"} {
		if strings.Contains(goflags, flag) {
			fileFlags = true
			break
		}
	}

	type parsedInfo struct {
		ok          bool
		ownPrefix   string
		depPrefixes []string
	}
	parsedModules := make(map[string]parsedInfo, len(modules))
	moduleDeps := make(map[string][]string, len(modules))
	var parseMu sync.Mutex
	var wg sync.WaitGroup

	for _, mod := range modules {
		wg.Add(1)
		go func(module string) {
			defer wg.Done()
			resolved, _ := filepath.Abs(module)
			relPaths, ok := localReplaces(module, env, runCmd)
			if !ok {
				parseMu.Lock()
				parsedModules[module] = parsedInfo{ok: false}
				parseMu.Unlock()
				return
			}
			var depRoots []string
			for _, rp := range relPaths {
				absDep, _ := filepath.Abs(filepath.Join(module, rp))
				depRoots = append(depRoots, absDep)
			}
			parseMu.Lock()
			moduleDeps[resolved] = depRoots
			parseMu.Unlock()

			if workspace == "" {
				cur := resolved
				for cur != root && isRelativeTo(cur, root) {
					if info, err := os.Stat(filepath.Join(cur, "go.work")); err == nil && !info.IsDir() {
						parseMu.Lock()
						parsedModules[module] = parsedInfo{ok: false}
						parseMu.Unlock()
						return
					}
					parent := filepath.Dir(cur)
					if parent == cur {
						break
					}
					cur = parent
				}
			}

			roots := append([]string{resolved}, depRoots...)
			if workspace != "" && workspace != "off" {
				for _, m := range modules {
					absM, _ := filepath.Abs(m)
					roots = append(roots, absM)
				}
			}
			for _, p := range roots {
				if !isRelativeTo(p, root) {
					parseMu.Lock()
					parsedModules[module] = parsedInfo{ok: false}
					parseMu.Unlock()
					return
				}
			}
			ownRel, _ := filepath.Rel(root, resolved)
			ownPrefix := filepath.ToSlash(ownRel)
			depSet := make(map[string]bool)
			for _, p := range roots[1:] {
				rel, _ := filepath.Rel(root, p)
				sp := filepath.ToSlash(rel)
				if sp != ownPrefix {
					depSet[sp] = true
				}
			}
			depPrefixes := make([]string, 0, len(depSet))
			for sp := range depSet {
				depPrefixes = append(depPrefixes, sp)
			}
			sort.Strings(depPrefixes)
			parseMu.Lock()
			parsedModules[module] = parsedInfo{ok: true, ownPrefix: ownPrefix, depPrefixes: depPrefixes}
			parseMu.Unlock()
		}(mod)
	}
	wg.Wait()

	watchSet := make(map[string]bool)
	for _, p := range goBuildCodePaths {
		watchSet[p] = true
	}
	watchSet["go.work"] = true
	watchSet["go.work.sum"] = true
	for _, info := range parsedModules {
		if info.ok {
			watchSet[info.ownPrefix] = true
			for _, dp := range info.depPrefixes {
				watchSet[dp] = true
			}
		}
	}
	var watchPaths []string
	for p := range watchSet {
		if _, inEntries := entries[p]; inEntries {
			watchPaths = append(watchPaths, p)
		} else if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			watchPaths = append(watchPaths, p)
		}
	}
	sort.Strings(watchPaths)

	dirty := make(map[string]bool)
	if len(watchPaths) > 0 {
		args := append([]string{"status", "--porcelain", "-z", "--no-renames", "--untracked-files=all", "--"}, watchPaths...)
		statusOut, err := gitRun(root, true, args...)
		if err != nil {
			return nil, nil, err
		}
		for _, entry := range strings.Split(statusOut, "\x00") {
			if len(entry) > 3 {
				dirty[entry[3:]] = true
			}
		}
	}

	isSymlinkOnDisk := func(p string) bool {
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		return err == nil && fi.Mode()&os.ModeSymlink != 0
	}

	matchesDirty := func(p, ownPrefix string, depPrefixes []string) bool {
		if codeSet[p] || p == "go.work" || p == "go.work.sum" {
			return true
		}
		if p == ownPrefix || strings.HasPrefix(p, ownPrefix+"/") {
			return true
		}
		if workspace != "" && workspace != "off" {
			for _, prefix := range depPrefixes {
				if p == prefix || strings.HasPrefix(p, prefix+"/") {
					return true
				}
			}
			return false
		}
		for _, prefix := range depPrefixes {
			if p == prefix || (strings.HasPrefix(p, prefix+"/") && (!strings.HasSuffix(p, "_test.go") || isSymlinkOnDisk(p))) {
				return true
			}
		}
		return false
	}

	matchesEntry := func(p string, e treeEntry, ownPrefix string, depPrefixes []string) bool {
		if codeSet[p] || p == "go.work" || p == "go.work.sum" {
			return true
		}
		if p == ownPrefix || strings.HasPrefix(p, ownPrefix+"/") {
			return true
		}
		if workspace != "" && workspace != "off" {
			for _, prefix := range depPrefixes {
				if p == prefix || strings.HasPrefix(p, prefix+"/") {
					return true
				}
			}
			return false
		}
		if e.kind == "tree" || (e.mode != "120000" && strings.HasSuffix(p, "_test.go")) {
			return false
		}
		for _, prefix := range depPrefixes {
			if strings.HasPrefix(p, prefix+"/") {
				return true
			}
		}
		return false
	}

	out := make(map[string]moduleKey, len(modules))
	for _, module := range modules {
		info := parsedModules[module]
		if !info.ok {
			out[module] = moduleKey{key: "", bound: false}
			continue
		}
		allPrefixes := append([]string{info.ownPrefix}, info.depPrefixes...)
		selected := make(map[string][3]string)
		for p, e := range entries {
			if matchesEntry(p, e, info.ownPrefix, info.depPrefixes) {
				selected[p] = [3]string{e.mode, e.kind, e.obj}
			}
		}
		bound := !outsideWorkspace && !(workspace != "" && workspace != "off") && !fileFlags
		if bound {
			for _, e := range selected {
				if e[0] == "120000" {
					bound = false
					break
				}
			}
		}
		if bound {
			for _, prefix := range allPrefixes {
				if _, ok := entries[prefix+"/go.mod"]; !ok {
					bound = false
					break
				}
			}
		}
		if bound {
			for p := range dirty {
				if matchesDirty(p, info.ownPrefix, info.depPrefixes) {
					bound = false
					break
				}
			}
		}
		identity := map[string]any{
			"format": "go-build-module-2",
			"module": filepath.ToSlash(module),
			"inputs": selected,
			"code":   code,
			"go":     versionStdout,
			"env":    settings,
		}
		raw, err := json.Marshal(identity)
		if err != nil {
			return nil, nil, err
		}
		sum := sha256.Sum256(raw)
		out[module] = moduleKey{key: hex.EncodeToString(sum[:]), bound: bound}
	}
	return out, moduleDeps, nil
}

func runGoBuild(toolDirs []string, memoryStore string, runCmd goCommandFunc) int {
	failJudge := func(err error) int {
		fmt.Fprintf(os.Stderr, "go-build: could not judge: %v\n", err)
		return 4
	}
	scratch := os.Getenv("YOLO_DURABLE_DIR")
	if scratch == "" {
		return failJudge(errors.New("YOLO_DURABLE_DIR is not set"))
	}
	var modules []string
	for _, dir := range toolDirs {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil || info.IsDir() {
			return failJudge(fmt.Errorf("%s/go.mod is missing", dir))
		}
		var found []string
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && d.Name() == "go.mod" {
				found = append(found, filepath.Dir(p))
			}
			return nil
		})
		if err != nil {
			return failJudge(err)
		}
		sort.Strings(found)
		modules = append(modules, found...)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return failJudge(err)
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		root = cwd
	}
	inside := true
	for _, m := range modules {
		absM, _ := filepath.Abs(m)
		if evalM, err := filepath.EvalSymlinks(absM); err == nil {
			absM = evalM
		}
		if !isRelativeTo(absM, root) {
			inside = false
			break
		}
	}

	var keys map[string]moduleKey
	var moduleDeps map[string][]string
	if inside {
		keys, moduleDeps, err = moduleKeys(root, modules, runCmd)
		if err != nil {
			return failJudge(err)
		}
	} else {
		keys = map[string]moduleKey{}
		moduleDeps = map[string][]string{}
	}

	store := memoryStore
	if store == "" {
		store, err = resultStoreDir(root)
		if err != nil {
			return failJudge(err)
		}
	}
	tree := ""
	if inside {
		tOut, err := gitRun(root, true, "rev-parse", "HEAD^{tree}")
		if err != nil {
			return failJudge(err)
		}
		tree = strings.TrimSpace(tOut)
	}

	evidencePath := os.Getenv("TILLR_GO_BUILD_EVIDENCE_OUT")
	snapshots := make(map[string]moduleEvidence)
	var evidenceMu sync.Mutex
	publish := func(observed moduleEvidence) {
		evidenceMu.Lock()
		defer evidenceMu.Unlock()
		snapshots[observed.Module] = observed
		var ordered []moduleEvidence
		for _, m := range modules {
			if s, ok := snapshots[filepath.ToSlash(m)]; ok {
				ordered = append(ordered, s)
			}
		}
		writeGoBuildEvidence(evidencePath, ordered)
	}

	type moduleResult struct {
		code       int
		hasCode    bool
		output     string
		remembered bool
		durationMS int64
		err        error
	}

	judge := func(module string) moduleResult {
		mk := keys[module]
		if mk.bound && readStoredResult(store, mk.key, "pass", "go-build") != nil && readStoredResult(store, mk.key, "fail", "go-build") == nil {
			if evidencePath != "" {
				publish(moduleEvidence{Module: filepath.ToSlash(module), Execution: "reused"})
			}
			return moduleResult{remembered: true}
		}
		if !mk.bound {
			fmt.Fprintf(os.Stderr, "go-build: %s: inputs not bound to the committed tree; not remembered\n", module)
		}
		started := time.Now()
		var code int
		var output string
		var err error
		if evidencePath != "" {
			observed := moduleEvidence{Module: filepath.ToSlash(module), Execution: "ran"}
			code, output, err = buildModule(module, scratch, runCmd, &observed, publish)
			publish(observed)
		} else {
			code, output, err = buildModule(module, scratch, runCmd, nil, nil)
		}
		if err != nil {
			return moduleResult{err: err}
		}
		return moduleResult{
			code:       code,
			hasCode:    true,
			output:     output,
			durationMS: time.Since(started).Milliseconds(),
		}
	}

	resultsByModule := make(map[string]moduleResult, len(modules))
	var resMu sync.Mutex
	for _, wave := range dependencyWaves(modules, moduleDeps) {
		var wg sync.WaitGroup
		for _, mod := range wave {
			wg.Add(1)
			go func(m string) {
				defer wg.Done()
				res := judge(m)
				resMu.Lock()
				resultsByModule[m] = res
				resMu.Unlock()
			}(mod)
		}
		wg.Wait()
	}

	hasRan := false
	for _, m := range modules {
		res := resultsByModule[m]
		if res.err != nil {
			return failJudge(res.err)
		}
		if !res.remembered {
			hasRan = true
		}
	}

	var fresh map[string]moduleKey
	if inside && hasRan {
		fresh, _, err = moduleKeys(root, modules, runCmd)
		if err != nil {
			return failJudge(err)
		}
	} else {
		fresh = keys
	}

	var unjudged []string
	failed := false
	for _, m := range modules {
		res := resultsByModule[m]
		outcome := checkresult.Pass
		if !res.remembered {
			c := res.code
			ans := checkresult.Classify(checkresult.Observation{
				Check:    "go-build",
				Source:   checkresult.GoModuleBuild,
				ExitCode: &c,
			})
			outcome = ans.Outcome
		}
		if outcome != checkresult.Pass && outcome != checkresult.Fail {
			unjudged = append(unjudged, m)
			if res.output != "" && !strings.HasSuffix(res.output, "\n") {
				fmt.Println(res.output)
			} else {
				fmt.Print(res.output)
			}
			continue
		}
		mk := keys[m]
		if !res.remembered && mk.bound && fresh[m] == (moduleKey{key: mk.key, bound: true}) {
			storeResultVerb := string(outcome)
			writeStoredResult(store, mk.key, storeResultVerb, res.durationMS, tree, "go-build-module-2", "go-build")
		}
		statusLabel := "built and test files compiled"
		if res.remembered {
			statusLabel = "remembered pass"
		} else if outcome == checkresult.Fail {
			statusLabel = "FAILED"
		}
		fmt.Printf("go-build: %s: %s\n", m, statusLabel)
		if outcome == checkresult.Fail {
			failed = true
			if res.output != "" && !strings.HasSuffix(res.output, "\n") {
				fmt.Println(res.output)
			} else {
				fmt.Print(res.output)
			}
		}
	}
	if len(unjudged) > 0 {
		return failJudge(fmt.Errorf("no Go build verdict for %s", strings.Join(unjudged, ", ")))
	}
	if failed {
		return 1
	}
	return 0
}

func cmdGoBuild(args []string) int {
	return runGoBuild(goToolDirs, "", nil)
}
