package goaffected

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// GoPackageVerdictsFile is the per-run passing Go package verdicts file name.
	GoPackageVerdictsFile = "gate.go-packages.json"
	// GoVerdictsInEnv is the environment variable naming the input verdicts JSON path.
	GoVerdictsInEnv = "TILLR_GO_VERDICTS_IN"
	// GoVerdictsOutEnv is the environment variable naming the output verdicts JSON path.
	GoVerdictsOutEnv = "TILLR_GO_VERDICTS_OUT"
)

var (
	runIDMarkerRE   = regexp.MustCompile(`(?:-([0-9]{3,12}))?-(failover|restart|fresh|interrupted)$`)
	briefFirstRunRE = regexp.MustCompile(`\bIts first run was ([^\s,]+)`)
	briefItemRE     = regexp.MustCompile(`(?m)^\s*-\s*\*\*Item:\*\*\s*(\S+)`)
)

// NarrowedVerdict records a passing verdict for a specific subset of tests in a package.
type NarrowedVerdict struct {
	Digest string   `json:"digest"`
	Tests  []string `json:"tests"`
}

// PackageVerdictEntry records whole-package and narrowed test verdicts for one import path.
type PackageVerdictEntry struct {
	Narrowed     map[string]NarrowedVerdict `json:"narrowed"`
	WholeDigests []string                   `json:"whole_digests"`
}

// PackageVerdicts is the schema-1 gate.go-packages.json document.
type PackageVerdicts struct {
	Packages map[string]PackageVerdictEntry `json:"packages"`
	Schema   int                            `json:"schema"`
}

// VerdictDigestCache caches directory, module-file, and Go toolchain version lookups across calls.
type VerdictDigestCache struct {
	DirHashes    map[dirHashKey]string
	DirFailed    map[dirHashKey]bool
	ModHashes    map[string]string
	ModFailed    map[string]bool
	GoVersion    string
	HasGoVersion bool
}

type dirHashKey struct {
	DirRel        string
	IncludeTestGo bool
}

// NewVerdictDigestCache creates an initialized VerdictDigestCache.
func NewVerdictDigestCache() *VerdictDigestCache {
	return &VerdictDigestCache{
		DirHashes: make(map[dirHashKey]string),
		DirFailed: make(map[dirHashKey]bool),
		ModHashes: make(map[string]string),
		ModFailed: make(map[string]bool),
	}
}

// SplitRunIDMarkers strips trailing continuation markers (-restart/-fresh/-interrupted/-failover) from runID.
func SplitRunIDMarkers(runID string) string {
	cur := runID
	for {
		loc := runIDMarkerRE.FindStringIndex(cur)
		if loc == nil {
			return cur
		}
		cur = cur[:loc[0]]
	}
}

// IsCfixRunID reports whether runID denotes a conflict-repair (-cfix) run.
func IsCfixRunID(runID string) bool {
	return strings.HasSuffix(SplitRunIDMarkers(runID), "-cfix")
}

// ReadRunItemID reads item_id from gate.face.json, run.record.json, or brief.md when present.
func ReadRunItemID(runDir string) string {
	for _, fname := range []string{"gate.face.json", "run.record.json"} {
		p := filepath.Join(runDir, fname)
		if !isFile(p) {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			continue
		}
		if itemID, ok := obj["item_id"].(string); ok && strings.TrimSpace(itemID) != "" {
			return strings.TrimSpace(itemID)
		}
		if payload, ok := obj["payload"].(map[string]any); ok && payload != nil {
			if itemID, ok := payload["item_id"].(string); ok && strings.TrimSpace(itemID) != "" {
				return strings.TrimSpace(itemID)
			}
		}
	}
	briefPath := filepath.Join(runDir, "brief.md")
	if isFile(briefPath) {
		if raw, err := os.ReadFile(briefPath); err == nil {
			if m := briefItemRE.FindSubmatch(raw); len(m) > 1 {
				return strings.TrimSpace(string(m[1]))
			}
		}
	}
	return ""
}

// ResolveCfixPriorRunDirs returns pre-conflict run directories for the same item when runDir is a -cfix run.
func ResolveCfixPriorRunDirs(runDir string) []string {
	resolved := resolvePath(runDir)
	runID := filepath.Base(resolved)
	if !IsCfixRunID(runID) {
		return nil
	}
	loopDir := filepath.Dir(resolved)
	expectedItem := ReadRunItemID(resolved)
	queue := []string{resolved}
	visited := map[string]bool{runID: true}
	var priorDirs []string

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		curName := filepath.Base(cur)
		stem := SplitRunIDMarkers(curName)
		var candidates []string
		if curName != stem {
			candidates = append(candidates, stem)
		}
		if strings.HasSuffix(stem, "-cfix") {
			pred := strings.TrimSuffix(stem, "-cfix")
			candidates = append(candidates, pred)
			predStem := SplitRunIDMarkers(pred)
			if predStem != pred {
				candidates = append(candidates, predStem)
			}
		}
		briefPath := filepath.Join(cur, "brief.md")
		if isFile(briefPath) {
			if raw, err := os.ReadFile(briefPath); err == nil {
				if m := briefFirstRunRE.FindSubmatch(raw); len(m) > 1 {
					candidates = append(candidates, strings.TrimSpace(string(m[1])))
				}
			}
		}
		for _, candID := range candidates {
			if candID == "" || visited[candID] {
				continue
			}
			visited[candID] = true
			candDir := filepath.Join(loopDir, candID)
			if !isDir(candDir) {
				continue
			}
			candItem := ReadRunItemID(candDir)
			if expectedItem != "" && candItem != "" && candItem != expectedItem {
				continue
			}
			priorDirs = append(priorDirs, candDir)
			if IsCfixRunID(candID) {
				queue = append(queue, candDir)
			}
		}
	}
	return priorDirs
}

// MergePackageVerdictsInto merges passing package verdict entries from source into target.
func MergePackageVerdictsInto(target, source *PackageVerdicts) bool {
	if target == nil || source == nil || source.Packages == nil {
		return false
	}
	if target.Packages == nil {
		target.Packages = make(map[string]PackageVerdictEntry)
	}
	added := false
	for ip, rec := range source.Packages {
		if ip == "" {
			continue
		}
		dst, ok := target.Packages[ip]
		if !ok {
			dst = PackageVerdictEntry{
				WholeDigests: []string{},
				Narrowed:     make(map[string]NarrowedVerdict),
			}
		}
		if dst.WholeDigests == nil {
			dst.WholeDigests = []string{}
		}
		if dst.Narrowed == nil {
			dst.Narrowed = make(map[string]NarrowedVerdict)
		}
		for _, d := range rec.WholeDigests {
			if d != "" {
				added = true
				if !containsString(dst.WholeDigests, d) {
					dst.WholeDigests = append(dst.WholeDigests, d)
				}
			}
		}
		for k, nrec := range rec.Narrowed {
			if nrec.Tests != nil && nrec.Digest != "" {
				added = true
				testsCopy := make([]string, len(nrec.Tests))
				copy(testsCopy, nrec.Tests)
				dst.Narrowed[k] = NarrowedVerdict{
					Tests:  testsCopy,
					Digest: nrec.Digest,
				}
			}
		}
		target.Packages[ip] = dst
	}
	return added
}

// LoadPackageVerdictsFile reads and normalizes a gate.go-packages.json file.
func LoadPackageVerdictsFile(path string) *PackageVerdicts {
	if !isFile(path) {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return nil
	}
	rawPkgs, hasPkgs := top["packages"]
	if !hasPkgs {
		return nil
	}
	trimmedPkgs := bytes.TrimSpace(rawPkgs)
	if len(trimmedPkgs) == 0 || trimmedPkgs[0] != '{' {
		return nil
	}
	var pkgMap map[string]json.RawMessage
	if err := json.Unmarshal(rawPkgs, &pkgMap); err != nil || pkgMap == nil {
		return nil
	}
	src := &PackageVerdicts{
		Schema:   1,
		Packages: make(map[string]PackageVerdictEntry),
	}
	for ip, rawEntry := range pkgMap {
		trimmedEntry := bytes.TrimSpace(rawEntry)
		if len(trimmedEntry) == 0 || trimmedEntry[0] != '{' {
			continue
		}
		var rawStruct struct {
			WholeDigests []string                   `json:"whole_digests"`
			WholeDigest  string                     `json:"whole_digest"`
			Narrowed     map[string]NarrowedVerdict `json:"narrowed"`
		}
		if err := json.Unmarshal(rawEntry, &rawStruct); err != nil {
			continue
		}
		entry := PackageVerdictEntry{
			WholeDigests: []string{},
			Narrowed:     make(map[string]NarrowedVerdict),
		}
		for _, d := range rawStruct.WholeDigests {
			if d != "" && !containsString(entry.WholeDigests, d) {
				entry.WholeDigests = append(entry.WholeDigests, d)
			}
		}
		if rawStruct.WholeDigest != "" && !containsString(entry.WholeDigests, rawStruct.WholeDigest) {
			entry.WholeDigests = append(entry.WholeDigests, rawStruct.WholeDigest)
		}
		for k, nrec := range rawStruct.Narrowed {
			if nrec.Tests != nil && nrec.Digest != "" {
				entry.Narrowed[k] = nrec
			}
		}
		src.Packages[ip] = entry
	}
	merged := &PackageVerdicts{
		Schema:   1,
		Packages: make(map[string]PackageVerdictEntry),
	}
	MergePackageVerdictsInto(merged, src)
	return merged
}

// LoadPriorCfixVerdicts loads and merges gate.go-packages.json from prior -cfix run directories.
func LoadPriorCfixVerdicts(runDir string) *PackageVerdicts {
	priorDirs := ResolveCfixPriorRunDirs(runDir)
	if len(priorDirs) == 0 {
		return nil
	}
	merged := &PackageVerdicts{
		Schema:   1,
		Packages: make(map[string]PackageVerdictEntry),
	}
	anyFound := false
	for _, candDir := range priorDirs {
		loaded := LoadPackageVerdictsFile(filepath.Join(candDir, GoPackageVerdictsFile))
		if loaded != nil && MergePackageVerdictsInto(merged, loaded) {
			anyFound = true
		}
	}
	if !anyFound {
		return nil
	}
	return merged
}

// SavePackageVerdictsAtomic writes data as formatted JSON atomically to path.
func SavePackageVerdictsAtomic(path string, data *PackageVerdicts) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".go-packages-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// ComputePackageVerdictDigest computes SHA-256 over a Go test package's files, transitive in-repo
// Go dependencies, enclosing go.mod/go.sum files, Go toolchain version, and declared builds/reads edges.
// Returns "" if any module or input cannot be resolved cleanly.
func ComputePackageVerdictDigest(
	root string,
	importPath string,
	graph *Graph,
	edges map[string]EdgeSpec,
	activeTests []string,
	cache *VerdictDigestCache,
) string {
	resolvedRoot := resolvePath(root)
	if importPath == "./..." {
		return ""
	}
	info, ok := graph.PkgInfo[importPath]
	if !ok {
		return ""
	}
	if graph.FailedModules[info.Module] {
		return ""
	}
	depClosure := map[string]bool{importPath: true}
	for d := range graph.TestDeps[importPath] {
		depClosure[d] = true
	}
	var inRepoDeps []string
	for d := range depClosure {
		if _, exists := graph.PkgInfo[d]; exists {
			inRepoDeps = append(inRepoDeps, d)
		}
	}
	sort.Strings(inRepoDeps)
	for _, dep := range inRepoDeps {
		if graph.FailedModules[graph.PkgInfo[dep].Module] {
			return ""
		}
	}

	if cache == nil {
		cache = NewVerdictDigestCache()
	}
	if !cache.HasGoVersion {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, "go", "env", "GOVERSION")
		cmd.Env = BuildEnv()
		out, err := cmd.Output()
		cancel()
		if err == nil {
			cache.GoVersion = strings.TrimSpace(string(out))
		} else {
			cache.GoVersion = ""
		}
		cache.HasGoVersion = true
	}

	pkgDirDigest := func(dirRel string, includeTestGo bool) (string, bool) {
		key := dirHashKey{DirRel: dirRel, IncludeTestGo: includeTestGo}
		if cache.DirFailed[key] {
			return "", false
		}
		if dig, exists := cache.DirHashes[key]; exists {
			return dig, true
		}
		baseDir := filepath.Join(resolvedRoot, filepath.FromSlash(dirRel))
		if !isDir(baseDir) {
			cache.DirFailed[key] = true
			return "", false
		}
		h := sha256.New()
		excludedTrim := strings.TrimRight(ExcludedPackMirrorPrefix, "/")
		var walkDir func(curDir string) bool
		walkDir = func(curDir string) bool {
			entries, err := os.ReadDir(curDir)
			if err != nil {
				return false
			}
			var filenames []string
			var subDirs []string
			for _, ent := range entries {
				name := ent.Name()
				full := filepath.Join(curDir, name)
				info, serr := os.Stat(full)
				if serr == nil && info.IsDir() {
					// Python os.walk(..., followlinks=False) puts symlinks to dirs in dirnames
					// and does not recurse into them.
					if ent.Type()&os.ModeSymlink != 0 {
						continue
					}
					if name == ".git" || name == "node_modules" || name == "__pycache__" {
						continue
					}
					rel, rerr := filepath.Rel(resolvedRoot, full)
					if rerr != nil {
						return false
					}
					subRel := filepath.ToSlash(rel)
					if subRel != dirRel {
						if _, isPkg := graph.DirToPkg[subRel]; isPkg {
							continue
						}
					}
					if subRel == excludedTrim || strings.HasPrefix(subRel, ExcludedPackMirrorPrefix) {
						continue
					}
					subDirs = append(subDirs, full)
					continue
				}
				if !includeTestGo && strings.HasSuffix(name, "_test.go") {
					continue
				}
				filenames = append(filenames, name)
			}
			sort.Strings(filenames)
			sort.Strings(subDirs)
			for _, fn := range filenames {
				full := filepath.Join(curDir, fn)
				rel, rerr := filepath.Rel(resolvedRoot, full)
				if rerr != nil {
					return false
				}
				frel := filepath.ToSlash(rel)
				raw, rerr := os.ReadFile(full)
				if rerr != nil {
					return false
				}
				h.Write([]byte(frel))
				h.Write([]byte{0})
				h.Write([]byte(strconv.Itoa(len(raw))))
				h.Write([]byte{0})
				h.Write(raw)
				h.Write([]byte{0})
			}
			for _, sd := range subDirs {
				if !walkDir(sd) {
					return false
				}
			}
			return true
		}
		if !walkDir(baseDir) {
			cache.DirFailed[key] = true
			return "", false
		}
		resHex := hex.EncodeToString(h.Sum(nil))
		cache.DirHashes[key] = resHex
		return resHex, true
	}

	modFilesDigest := func(mod string) (string, bool) {
		if cache.ModFailed[mod] {
			return "", false
		}
		if dig, exists := cache.ModHashes[mod]; exists {
			return dig, true
		}
		h := sha256.New()
		for _, fn := range []string{"go.mod", "go.sum"} {
			rel := mod + "/" + fn
			fpath := filepath.Join(resolvedRoot, filepath.FromSlash(rel))
			h.Write([]byte(rel))
			h.Write([]byte{0})
			if _, serr := os.Stat(fpath); serr == nil {
				raw, rerr := os.ReadFile(fpath)
				if rerr != nil {
					cache.ModFailed[mod] = true
					return "", false
				}
				h.Write([]byte("present\x00"))
				h.Write([]byte(strconv.Itoa(len(raw))))
				h.Write([]byte{0})
				h.Write(raw)
			} else {
				h.Write([]byte("absent"))
			}
			h.Write([]byte{0})
		}
		resHex := hex.EncodeToString(h.Sum(nil))
		cache.ModHashes[mod] = resHex
		return resHex, true
	}

	scopeTag := "whole"
	if activeTests != nil {
		scopeTag = "tests:" + strings.Join(sortedUniqueStrings(activeTests), ",")
	}
	hasher := sha256.New()
	fmt.Fprintf(hasher, "v1\x00pkg=%s\x00goversion=%s\x00scope=%s\n", importPath, cache.GoVersion, scopeTag)

	modSet := make(map[string]bool)
	for _, dep := range inRepoDeps {
		includeTests := dep == importPath
		dd, ok := pkgDirDigest(graph.PkgInfo[dep].DirRel, includeTests)
		if !ok {
			return ""
		}
		incInt := 0
		if includeTests {
			incInt = 1
		}
		fmt.Fprintf(hasher, "dep:%s:%d:%s\n", dep, incInt, dd)
		modSet[graph.PkgInfo[dep].Module] = true
	}

	mods := make([]string, 0, len(modSet))
	for m := range modSet {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, mod := range mods {
		md, ok := modFilesDigest(mod)
		if !ok {
			return ""
		}
		fmt.Fprintf(hasher, "mod:%s:%s\n", mod, md)
	}

	if edge, hasEdge := edges[importPath]; hasEdge {
		for _, btarget := range edge.Builds {
			binfo, ok := graph.PkgInfo[btarget]
			if !ok || graph.FailedModules[binfo.Module] {
				return ""
			}
		}
		edgeSpec, err := canonicalEdgeJSON(edge)
		if err != nil {
			return ""
		}
		edgeDig, err := ComputeEdgeDigest(resolvedRoot, edge, graph.PkgInfo, activeTests, nil)
		if err != nil {
			return ""
		}
		fmt.Fprintf(hasher, "edge:%s:%s\n", edgeSpec, edgeDig)
	}

	return hex.EncodeToString(hasher.Sum(nil))
}

// PackageVerdictReusable reports whether priorVerdicts carries a passing verdict for importPath
// whose transitive input + edge digest matches the current tree.
func PackageVerdictReusable(
	root string,
	importPath string,
	graph *Graph,
	edges map[string]EdgeSpec,
	priorVerdicts *PackageVerdicts,
	selectedTests []string,
	cache *VerdictDigestCache,
) bool {
	if priorVerdicts == nil || priorVerdicts.Packages == nil {
		return false
	}
	entry, ok := priorVerdicts.Packages[importPath]
	if !ok {
		return false
	}
	if len(entry.WholeDigests) > 0 {
		wholeCur := ComputePackageVerdictDigest(root, importPath, graph, edges, nil, cache)
		if wholeCur != "" && containsString(entry.WholeDigests, wholeCur) {
			return true
		}
	}
	if selectedTests != nil {
		reqSet := make(map[string]bool, len(selectedTests))
		for _, t := range selectedTests {
			reqSet[t] = true
		}
		for _, rec := range entry.Narrowed {
			if rec.Tests == nil || rec.Digest == "" {
				continue
			}
			recSet := make(map[string]bool, len(rec.Tests))
			for _, t := range rec.Tests {
				recSet[t] = true
			}
			subset := true
			for t := range reqSet {
				if !recSet[t] {
					subset = false
					break
				}
			}
			if !subset {
				continue
			}
			cur := ComputePackageVerdictDigest(root, importPath, graph, edges, rec.Tests, cache)
			if cur != "" && cur == rec.Digest {
				return true
			}
		}
	}
	return false
}

// RecordPackageVerdict records a passing package verdict into verdictsStore.
func RecordPackageVerdict(
	root string,
	importPath string,
	graph *Graph,
	edges map[string]EdgeSpec,
	verdictsStore *PackageVerdicts,
	selectedTests []string,
	cache *VerdictDigestCache,
) bool {
	if verdictsStore == nil {
		return false
	}
	digest := ComputePackageVerdictDigest(root, importPath, graph, edges, selectedTests, cache)
	if digest == "" {
		return false
	}
	if verdictsStore.Packages == nil {
		verdictsStore.Packages = make(map[string]PackageVerdictEntry)
	}
	entry, ok := verdictsStore.Packages[importPath]
	if !ok {
		entry = PackageVerdictEntry{
			WholeDigests: []string{},
			Narrowed:     make(map[string]NarrowedVerdict),
		}
	}
	if entry.WholeDigests == nil {
		entry.WholeDigests = []string{}
	}
	if entry.Narrowed == nil {
		entry.Narrowed = make(map[string]NarrowedVerdict)
	}
	if selectedTests == nil {
		if !containsString(entry.WholeDigests, digest) {
			entry.WholeDigests = append(entry.WholeDigests, digest)
		}
	} else {
		ordered := sortedUniqueStrings(selectedTests)
		key := strings.Join(ordered, "\x00")
		entry.Narrowed[key] = NarrowedVerdict{
			Tests:  ordered,
			Digest: digest,
		}
	}
	verdictsStore.Packages[importPath] = entry
	return true
}

func canonicalEdgeJSON(edge EdgeSpec) (string, error) {
	var target any
	if edge.rawMap != nil {
		target = edge.rawMap
	} else {
		builds := edge.Builds
		if builds == nil {
			builds = []string{}
		}
		reads := edge.Reads
		if reads == nil {
			reads = []string{}
		}
		m := map[string]any{
			"builds": builds,
			"reads":  reads,
		}
		if len(edge.BuildTests) > 0 {
			m["build_tests"] = edge.BuildTests
		}
		if len(edge.ReadTests) > 0 {
			m["read_tests"] = edge.ReadTests
		}
		target = m
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(target); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func sortedUniqueStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
