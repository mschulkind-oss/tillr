package goaffected

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	// ShardThreshold is the minimum number of tests in a package before sharding.
	ShardThreshold = 100
	// MinShards is the minimum shard count when sharding a large package.
	MinShards = 6
	// NarrowedShardDurationThreshold is the estimated-seconds threshold for sharding narrowed test subsets.
	NarrowedShardDurationThreshold = 25.0
	// TriageCases is the maximum number of failing cases rendered in a triage block.
	TriageCases = 10
	// SigquitAssertion is the assertion message recorded for tests interrupted by SIGQUIT.
	SigquitAssertion = "blocked: the test binary was ended by SIGQUIT while this test was running"
)

var (
	sigquitFrameRe = regexp.MustCompile(`^(?P<pkg>(?:[^\s()]+/)?[^\s./()]+)\.(?P<name>Test(?:[^a-z\W]\w*)?)\(`)
	goFailHeaderRe = regexp.MustCompile(`^\s*--- FAIL: (?P<name>\S+) \(`)
	goFailLocRe    = regexp.MustCompile(`^\s+(?P<file>[\w./-]+\.go):(?P<line>\d+):\s*(?P<msg>.*)$`)
	goPkgFailRe    = regexp.MustCompile(`^FAIL\t(?P<package>\S+)`)
)

// TriageCase represents one failing test case in a bounded triage block.
type TriageCase struct {
	Package   string
	Name      string
	Where     string
	Assertion string
	Sigquit   bool
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func reconstructGoTestOutput(stdout string) string {
	var chunks []string
	for _, line := range strings.SplitAfter(stdout, "\n") {
		if line == "" {
			continue
		}
		stripped := strings.TrimLeft(line, " \t\r\n")
		if !strings.HasPrefix(stripped, "{") {
			chunks = append(chunks, line)
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(stripped), &ev); err != nil || ev == nil {
			chunks = append(chunks, line)
			continue
		}
		if action, _ := ev["Action"].(string); action == goTestActionOutput {
			if outChunk, ok := ev["Output"].(string); ok {
				chunks = append(chunks, outChunk)
			}
		}
	}
	return strings.Join(chunks, "")
}

// ExtractSigquitCases returns one failing case per unique top-level Test* frame in a SIGQUIT goroutine dump.
func ExtractSigquitCases(stdout string, knownPackages map[string]bool) []TriageCase {
	lines := splitLines(reconstructGoTestOutput(stdout))
	sigquitIdx := -1
	for idx, line := range lines {
		if strings.TrimSpace(line) == "SIGQUIT: quit" {
			sigquitIdx = idx
			break
		}
	}
	if sigquitIdx < 0 {
		return nil
	}

	type caseKey struct {
		pkg  string
		name string
	}
	var cases []TriageCase
	seen := make(map[caseKey]bool)
	for idx := sigquitIdx + 1; idx < len(lines); idx++ {
		m := sigquitFrameRe.FindStringSubmatch(lines[idx])
		if m == nil {
			continue
		}
		rawPkg := m[1]
		name := m[2]
		if name == "TestMain" {
			continue
		}
		pkg := rawPkg
		if strings.HasSuffix(rawPkg, "_test") && !knownPackages[rawPkg] {
			pkg = rawPkg[:len(rawPkg)-5]
		}
		key := caseKey{pkg: pkg, name: name}
		if seen[key] {
			continue
		}
		seen[key] = true
		where := ""
		if idx+1 < len(lines) {
			where = strings.TrimSpace(lines[idx+1])
		}
		cases = append(cases, TriageCase{
			Package:   pkg,
			Name:      name,
			Where:     where,
			Assertion: SigquitAssertion,
			Sigquit:   true,
		})
	}
	return cases
}

// ExtractFailCases extracts ordinary failing test cases from `--- FAIL:` blocks in readable `go test` output.
func ExtractFailCases(readable string, parsed map[string]PackageTestResult) []TriageCase {
	type rawCase struct {
		pkg       string
		name      string
		file      string
		line      string
		assertion string
	}
	var rawCases []*rawCase
	var openCases []*rawCase
	var current *rawCase

	for _, line := range splitLines(readable) {
		if m := goFailHeaderRe.FindStringSubmatch(line); m != nil {
			rc := &rawCase{name: m[1]}
			rawCases = append(rawCases, rc)
			openCases = append(openCases, rc)
			current = rc
			continue
		}
		if current != nil && current.file == "" {
			if m := goFailLocRe.FindStringSubmatch(line); m != nil {
				current.file = m[1]
				current.line = m[2]
				current.assertion = strings.TrimSpace(m[3])
				continue
			}
		}
		if m := goPkgFailRe.FindStringSubmatch(line); m != nil {
			pkgName := m[1]
			for _, c := range openCases {
				c.pkg = pkgName
			}
			openCases = nil
			current = nil
		}
	}

	if len(openCases) > 0 && len(parsed) > 0 {
		solePkg := ""
		if len(parsed) == 1 {
			for p := range parsed {
				solePkg = p
			}
		}
		for _, c := range openCases {
			if c.pkg != "" {
				continue
			}
			if solePkg != "" {
				c.pkg = solePkg
				continue
			}
			var matches []string
			for p, rec := range parsed {
				if containsString(rec.FailedTests, c.name) {
					matches = append(matches, p)
				}
			}
			if len(matches) == 1 {
				c.pkg = matches[0]
			}
		}
	}

	namesByPkg := make(map[string][]string)
	for _, c := range rawCases {
		namesByPkg[c.pkg] = append(namesByPkg[c.pkg], c.name)
	}
	leavesByPkg := make(map[string]map[string]bool, len(namesByPkg))
	for pkg, names := range namesByPkg {
		leafSet := make(map[string]bool)
		for _, leaf := range LeafFailedTests(names) {
			leafSet[leaf] = true
		}
		leavesByPkg[pkg] = leafSet
	}

	type caseKey struct {
		pkg  string
		name string
	}
	var cases []TriageCase
	seen := make(map[caseKey]bool)
	for _, c := range rawCases {
		if !leavesByPkg[c.pkg][c.name] {
			continue
		}
		key := caseKey{pkg: c.pkg, name: c.name}
		if seen[key] {
			continue
		}
		seen[key] = true
		loc := ""
		if c.file != "" && c.line != "" {
			loc = c.file + ":" + c.line
		}
		where := ""
		switch {
		case c.pkg != "" && loc != "":
			where = c.pkg + " (" + loc + ")"
		case c.pkg != "":
			where = c.pkg
		case loc != "":
			where = loc
		}
		cases = append(cases, TriageCase{
			Package:   c.pkg,
			Name:      c.name,
			Where:     where,
			Assertion: c.assertion,
			Sigquit:   false,
		})
	}
	return cases
}

// FormatTriageBlock renders a bounded `fork-check: triage --` block for a failing `go-affected` run.
func FormatTriageBlock(cases []TriageCase, pkgLocations map[string][2]string) string {
	var b strings.Builder
	b.WriteString("fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:\n")
	b.WriteString("  triage go-affected: Go test packages affected by the change pass\n")
	if len(cases) == 0 {
		b.WriteString("       no case named in the output; reproduce with: python3 tools/gate/go_affected.py\n")
		return b.String()
	}
	kept := cases
	if len(kept) > TriageCases {
		kept = cases[:TriageCases]
	}
	for _, c := range kept {
		where := c.Where
		if where == "" {
			where = "(no file named)"
		}
		fmt.Fprintf(&b, "       case: %s -- %s\n", c.Name, where)
		if c.Assertion != "" {
			firstLine := c.Assertion
			if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
				firstLine = firstLine[:idx]
			}
			firstLine = strings.TrimSuffix(firstLine, "\r")
			fmt.Fprintf(&b, "       assertion: %s\n", firstLine)
		}
		if loc, ok := pkgLocations[c.Package]; ok {
			fmt.Fprintf(&b, "       one case: go -C %s test %s -run '^%s$'\n", loc[0], loc[1], c.Name)
		}
	}
	if dropped := len(cases) - len(kept); dropped > 0 {
		fmt.Fprintf(&b, "       (%d more failing case(s) not listed; the packet keeps %d)\n", dropped, len(kept))
	}
	return b.String()
}

// MaxShards returns 2*cpuCount when cpuCount > 0, else 12.
// Pass 0 to default to runtime.NumCPU().
func MaxShards(cpuCount int) int {
	if cpuCount <= 0 {
		cpuCount = runtime.NumCPU()
	}
	if cpuCount > 0 {
		return 2 * cpuCount
	}
	return 12
}

// LeafFailedTests drops parent test names when a subtest of that parent also failed.
func LeafFailedTests(names []string) []string {
	seen := make(map[string]bool, len(names))
	deduped := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			deduped = append(deduped, n)
		}
	}
	out := make([]string, 0, len(deduped))
	for _, name := range deduped {
		hasChild := false
		prefix := name + "/"
		for _, other := range deduped {
			if other != name && strings.HasPrefix(other, prefix) {
				hasChild = true
				break
			}
		}
		if !hasChild {
			out = append(out, name)
		}
	}
	return out
}

// FailedPackage pairs a package name with its failing leaf test names.
type FailedPackage struct {
	Package string
	Tests   []string
}

// FormatFailureSummary renders the failing packages and tests block appended to failed check output.
func FormatFailureSummary(failedPackages []FailedPackage) string {
	if len(failedPackages) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("go-affected: failing packages and tests:\n")
	for _, fp := range failedPackages {
		if len(fp.Tests) > 0 {
			fmt.Fprintf(&b, "  %s: %s\n", fp.Package, strings.Join(fp.Tests, ", "))
		} else {
			fmt.Fprintf(&b, "  %s: (no failing test named)\n", fp.Package)
		}
	}
	return b.String()
}

// PackageTestResult holds parsed `go test -json` summary for one package.
type PackageTestResult struct {
	ElapsedMS   int64
	Cached      bool
	Passed      *bool
	FailedTests []string
}

const (
	goTestActionOutput = "output"
	goTestActionPass   = "pass"
	goTestActionFail   = "fail"
	goTestActionSkip   = "skip"
)

// ParseGoTestJSON parses `go test -json` output into per-package results and readable failure text.
func ParseGoTestJSON(stdout string) (map[string]PackageTestResult, string) {
	type pkgState struct {
		rec    PackageTestResult
		chunks []string
		failed []string
	}
	type testKey struct {
		pkg  string
		test string
	}

	results := make(map[string]*pkgState)
	var pkgOrder []string
	var nonPkgChunks []string
	testHeaders := make(map[testKey]string)
	testBodies := make(map[testKey][]string)
	var testKeyOrder []testKey

	getOrCreate := func(pkg string) *pkgState {
		st, ok := results[pkg]
		if !ok {
			st = &pkgState{}
			results[pkg] = st
			pkgOrder = append(pkgOrder, pkg)
		}
		return st
	}

	for _, line := range strings.SplitAfter(stdout, "\n") {
		if line == "" {
			continue
		}
		stripped := strings.TrimLeft(line, " \t\r\n")
		if !strings.HasPrefix(stripped, "{") {
			nonPkgChunks = append(nonPkgChunks, line)
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(stripped), &ev); err != nil || ev == nil {
			nonPkgChunks = append(nonPkgChunks, line)
			continue
		}
		outChunk, hasOut := ev["Output"].(string)
		pkg, _ := ev["Package"].(string)
		testName, _ := ev["Test"].(string)
		action, _ := ev["Action"].(string)
		if pkg == "" {
			if hasOut {
				nonPkgChunks = append(nonPkgChunks, outChunk)
			}
			continue
		}
		st := getOrCreate(pkg)
		if testName != "" {
			key := testKey{pkg: pkg, test: testName}
			switch action {
			case "output":
				if hasOut {
					s := strings.TrimLeft(outChunk, " \t\r\n")
					if strings.HasPrefix(s, "--- FAIL:") {
						if _, ok := testHeaders[key]; !ok {
							testKeyOrder = append(testKeyOrder, key)
						}
						testHeaders[key] = outChunk
					} else if !strings.HasPrefix(s, "=== RUN") &&
						!strings.HasPrefix(s, "=== PAUSE") &&
						!strings.HasPrefix(s, "=== CONT") &&
						!strings.HasPrefix(s, "--- PASS:") &&
						!strings.HasPrefix(s, "--- SKIP:") {
						if _, ok := testBodies[key]; !ok {
							testKeyOrder = append(testKeyOrder, key)
						}
						testBodies[key] = append(testBodies[key], outChunk)
					}
				}
			case goTestActionFail:
				st.failed = append(st.failed, testName)
				dur := "0.00s"
				if elapsed, ok := ev["Elapsed"].(float64); ok {
					dur = fmt.Sprintf("%.2fs", elapsed)
				}
				header, ok := testHeaders[key]
				if !ok {
					header = fmt.Sprintf("--- FAIL: %s (%s)\n", testName, dur)
				} else {
					delete(testHeaders, key)
				}
				st.chunks = append(st.chunks, header)
				if body, bok := testBodies[key]; bok {
					st.chunks = append(st.chunks, body...)
					delete(testBodies, key)
				}
			case goTestActionPass, goTestActionSkip:
				delete(testHeaders, key)
				delete(testBodies, key)
			}
		} else {
			switch action {
			case "output":
				if hasOut {
					if strings.Contains(outChunk, "(cached)") {
						st.rec.Cached = true
					}
					st.chunks = append(st.chunks, outChunk)
				}
			case goTestActionPass, goTestActionFail, goTestActionSkip:
				if elapsed, ok := ev["Elapsed"].(float64); ok {
					st.rec.ElapsedMS = int64(math.Round(elapsed * 1000.0))
				}
				passed := action != goTestActionFail
				st.rec.Passed = &passed
			}
		}
	}

	knownPkgs := make(map[string]bool, len(results))
	for pkg := range results {
		knownPkgs[pkg] = true
	}
	sigquitByPkg := make(map[string][]string)
	for _, c := range ExtractSigquitCases(stdout, knownPkgs) {
		sigquitByPkg[c.Package] = append(sigquitByPkg[c.Package], c.Name)
	}
	for pkg := range sigquitByPkg {
		st := getOrCreate(pkg)
		if st.rec.Passed == nil || *st.rec.Passed {
			passed := false
			st.rec.Passed = &passed
		}
	}

	outMap := make(map[string]PackageTestResult, len(results))
	var textChunks []string
	textChunks = append(textChunks, nonPkgChunks...)

	seenFlush := make(map[testKey]bool)
	for _, pkg := range pkgOrder {
		st := results[pkg]
		normalFailed := LeafFailedTests(st.failed)
		sqFailed := sigquitByPkg[pkg]
		combined := make([]string, 0, len(sqFailed)+len(normalFailed))
		combined = append(combined, sqFailed...)
		combined = append(combined, normalFailed...)
		st.rec.FailedTests = dedupeStrings(combined)
		if st.rec.Passed != nil && !*st.rec.Passed {
			for _, key := range testKeyOrder {
				if key.pkg != pkg || seenFlush[key] {
					continue
				}
				seenFlush[key] = true
				if header, ok := testHeaders[key]; ok {
					st.chunks = append(st.chunks, header)
					delete(testHeaders, key)
				}
				if body, ok := testBodies[key]; ok {
					st.chunks = append(st.chunks, body...)
					delete(testBodies, key)
				}
			}
		}
		outMap[pkg] = st.rec
		textChunks = append(textChunks, st.chunks...)
	}
	return outMap, strings.Join(textChunks, "")
}

// FormatProgressEvent returns a short progress line for a completed test or package JSON event.
func FormatProgressEvent(line string) (string, bool) {
	stripped := strings.TrimLeft(line, " \t\r\n")
	if !strings.HasPrefix(stripped, "{") {
		return "", false
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(stripped), &ev); err != nil || ev == nil {
		return "", false
	}
	action, _ := ev["Action"].(string)
	if action != goTestActionPass && action != goTestActionFail && action != goTestActionSkip {
		return "", false
	}
	pkg, _ := ev["Package"].(string)
	if pkg == "" {
		return "", false
	}
	dur := ""
	if elapsed, ok := ev["Elapsed"].(float64); ok {
		dur = fmt.Sprintf(" (%.2fs)", elapsed)
	}
	if testName, ok := ev["Test"].(string); ok && testName != "" {
		return fmt.Sprintf("go-affected: %s %s %s%s\n", action, pkg, testName, dur), true
	}
	return fmt.Sprintf("go-affected: %s %s%s\n", action, pkg, dur), true
}

// EvidencePackage represents one package entry in go-affected evidence JSON.
type EvidencePackage struct {
	ImportPath string    `json:"import_path"`
	Module     string    `json:"module"`
	Reason     string    `json:"reason"`
	ElapsedMS  int64     `json:"elapsed_ms"`
	Cached     bool      `json:"cached"`
	Reused     bool      `json:"reused,omitempty"`
	Tests      *[]string `json:"tests,omitempty"`
}

// Evidence is the go-affected evidence JSON payload.
type Evidence struct {
	Mode        string            `json:"mode"`
	SelectionMs int64             `json:"selection_ms"`
	SlotWaitMs  int64             `json:"slot_wait_ms"`
	Packages    []EvidencePackage `json:"packages"`
}

// FormatEvidencePackage builds an EvidencePackage from a SelectedPackage and execution metrics.
func FormatEvidencePackage(item SelectedPackage, elapsedMS int64, cached bool, reused bool) EvidencePackage {
	ep := EvidencePackage{
		ImportPath: item.ImportPath,
		Module:     item.Module,
		Reason:     item.Reason,
		ElapsedMS:  elapsedMS,
		Cached:     cached,
		Reused:     reused,
	}
	if item.SelectedTests != nil {
		cp := make([]string, len(item.SelectedTests))
		copy(cp, item.SelectedTests)
		ep.Tests = &cp
	}
	return ep
}

// WriteEvidenceAtomic writes evidence JSON atomically to evPath, logging errors to stderr.
func WriteEvidenceAtomic(evPath string, evidence *Evidence) {
	if evPath == "" || evidence == nil {
		return
	}
	dir := filepath.Dir(evPath)
	base := filepath.Base(evPath)
	tmpPath := filepath.Join(dir, fmt.Sprintf(".%s.tmp.%d", base, os.Getpid()))
	raw, err := json.Marshal(evidence)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: could not write evidence: %v\n", err)
		return
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(tmpPath, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "go-affected: could not write evidence: %v\n", err)
		return
	}
	if err := os.Rename(tmpPath, evPath); err != nil {
		_ = os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "go-affected: could not write evidence: %v\n", err)
	}
}

// RunOptions configures RunSelectedTests.
type RunOptions struct {
	EvidencePath    string
	ProgressStream  io.Writer
	DisableProgress bool
	Graph           *Graph
	VerdictsIn      string
	VerdictsOut     string
	RunCommand      func(cmd []string, cwd string, env []string, onLine func(string)) (int, string, error)
}

// RunSelectedTests runs the selected Go test packages with sharding, caching,
// slot locking, subreaper guards, and optional cfix verdict reuse/recording.
func RunSelectedTests(root string, selection *Selection, opts RunOptions) (int, string, *Evidence, error) {
	resolvedRoot := resolvePath(root)
	selected := selection.Selected

	initialPackages := make([]EvidencePackage, 0, len(selected))
	for _, item := range selected {
		initialPackages = append(initialPackages, FormatEvidencePackage(item, 0, false, false))
	}
	initialEvidence := &Evidence{
		Mode:        "go-affected",
		SelectionMs: selection.SelectionMs,
		SlotWaitMs:  0,
		Packages:    initialPackages,
	}
	WriteEvidenceAtomic(opts.EvidencePath, initialEvidence)

	if len(selected) == 0 {
		if opts.VerdictsOut != "" {
			_ = SavePackageVerdictsAtomic(opts.VerdictsOut, &PackageVerdicts{
				Schema:   1,
				Packages: map[string]PackageVerdictEntry{},
			})
		}
		return 0, "go-affected: no affected tillr Go test packages\n", initialEvidence, nil
	}

	durations := LoadDurations(resolvedRoot)
	edges, err := LoadEdges(resolvedRoot)
	if err != nil {
		return 1, "", initialEvidence, err
	}

	fallbackGraph := opts.Graph
	if (opts.VerdictsIn != "" || opts.VerdictsOut != "") && fallbackGraph == nil {
		fallbackGraph, err = LoadTillrGraph(resolvedRoot, nil)
		if err != nil {
			return 1, "", initialEvidence, err
		}
	}

	var priorVerdicts *PackageVerdicts
	if opts.VerdictsIn != "" {
		priorVerdicts = LoadPackageVerdictsFile(opts.VerdictsIn)
	}
	digestCache := NewVerdictDigestCache()
	reusedIPs := make(map[string]bool)
	var activeSelected []SelectedPackage
	for _, item := range selected {
		if priorVerdicts != nil && fallbackGraph != nil &&
			PackageVerdictReusable(resolvedRoot, item.ImportPath, fallbackGraph, edges, priorVerdicts, item.SelectedTests, digestCache) {
			reusedIPs[item.ImportPath] = true
		} else {
			activeSelected = append(activeSelected, item)
		}
	}

	invocationDigest := func(item SelectedPackage, activeTests []string) *string {
		if !item.HasEdge {
			return nil
		}
		edge, ok := edges[item.ImportPath]
		if !ok {
			return item.EdgeDigest
		}
		rb := item.ResolvedBuilds
		if rb == nil {
			if len(edge.Builds) > 0 {
				if fallbackGraph == nil {
					if g, gerr := LoadTillrGraph(resolvedRoot, nil); gerr == nil {
						fallbackGraph = g
					}
				}
				if fallbackGraph != nil {
					rb = ResolveEdgeBuilds(resolvedRoot, edge, fallbackGraph.PkgInfo)
				} else {
					rb = &ResolvedBuilds{}
				}
			} else {
				rb = &ResolvedBuilds{}
			}
		}
		dig, derr := ComputeEdgeDigest(resolvedRoot, edge, nil, activeTests, rb)
		if derr != nil {
			return item.EdgeDigest
		}
		return &dig
	}

	baseEnv := BuildEnv()
	baseEnvMap := envSliceToMap(baseEnv)
	gitValues, err := GlobalGitConfig(baseEnv)
	if err != nil {
		return 1, "", initialEvidence, err
	}
	durable := baseEnvMap["YOLO_DURABLE_DIR"]
	if durable == "" {
		durable = baseEnvMap["TMPDIR"]
	}

	type invocation struct {
		module     string
		items      []SelectedPackage
		digest     *string
		runPattern string
		hasPattern bool
		scratchKey string
	}

	var invocations []invocation
	byMod := make(map[string][]SelectedPackage)
	maxShards := MaxShards(0)

	for _, item := range activeSelected {
		tests, _ := ScanPackageTests(resolvedRoot, item.DirRel, item.TestFiles)
		hasEdgeTest := containsString(tests, "TestEdgeInputs")
		if item.SelectedTests != nil {
			var readerTests []string
			for _, t := range item.SelectedTests {
				if t != "TestEdgeInputs" {
					readerTests = append(readerTests, t)
				}
			}
			pkgDurations := durations[item.ImportPath]
			subsetDurations := make(map[string]float64, len(readerTests))
			estTotal := 0.0
			for _, t := range readerTests {
				d := 1.0
				if pkgDurations != nil {
					if v, ok := pkgDurations[t]; ok {
						d = v
					}
				}
				subsetDurations[t] = d
				estTotal += d
			}
			sum := sha256.Sum256([]byte(strings.Join(readerTests, "\x00")))
			subsetSig := hex.EncodeToString(sum[:])[:12]

			if len(readerTests) >= 2 && estTotal >= NarrowedShardDurationThreshold {
				shardCount := int(math.Ceil(estTotal / NarrowedShardDurationThreshold))
				if shardCount < 2 {
					shardCount = 2
				}
				if shardCount > maxShards {
					shardCount = maxShards
				}
				buckets := Assign(readerTests, subsetDurations, shardCount)
				for shardIdx, bucket := range buckets {
					if len(bucket) == 0 {
						continue
					}
					shardNames := make([]string, len(bucket))
					copy(shardNames, bucket)
					if hasEdgeTest {
						shardNames = append(shardNames, "TestEdgeInputs")
					}
					escaped := make([]string, len(shardNames))
					for i, t := range shardNames {
						escaped[i] = regexp.QuoteMeta(t)
					}
					pattern := "^(" + strings.Join(escaped, "|") + ")$"
					bsum := sha256.Sum256([]byte(strings.Join(bucket, "\x00")))
					bucketSig := hex.EncodeToString(bsum[:])[:12]
					invocations = append(invocations, invocation{
						module:     item.Module,
						items:      []SelectedPackage{item},
						digest:     invocationDigest(item, bucket),
						runPattern: pattern,
						hasPattern: true,
						scratchKey: fmt.Sprintf("%s#tests-%s-shard-%d-%s", item.ImportPath, subsetSig, shardIdx, bucketSig),
					})
				}
			} else {
				runNames := make([]string, len(readerTests))
				copy(runNames, readerTests)
				if hasEdgeTest {
					runNames = append(runNames, "TestEdgeInputs")
				}
				escaped := make([]string, len(runNames))
				for i, t := range runNames {
					escaped[i] = regexp.QuoteMeta(t)
				}
				pattern := "^(" + strings.Join(escaped, "|") + ")$"
				invocations = append(invocations, invocation{
					module:     item.Module,
					items:      []SelectedPackage{item},
					digest:     invocationDigest(item, readerTests),
					runPattern: pattern,
					hasPattern: true,
					scratchKey: fmt.Sprintf("%s#tests-%s", item.ImportPath, subsetSig),
				})
			}
		} else if len(tests) >= ShardThreshold {
			var nonEdge []string
			for _, t := range tests {
				if t != "TestEdgeInputs" {
					nonEdge = append(nonEdge, t)
				}
			}
			shardCount := len(nonEdge) / 125
			if shardCount < MinShards {
				shardCount = MinShards
			}
			if shardCount > maxShards {
				shardCount = maxShards
			}
			buckets := Assign(nonEdge, durations[item.ImportPath], shardCount)
			for shardIdx, bucket := range buckets {
				if len(bucket) == 0 {
					continue
				}
				shardNames := make([]string, len(bucket))
				copy(shardNames, bucket)
				if hasEdgeTest {
					shardNames = append(shardNames, "TestEdgeInputs")
				}
				escaped := make([]string, len(shardNames))
				for i, t := range shardNames {
					escaped[i] = regexp.QuoteMeta(t)
				}
				pattern := "^(" + strings.Join(escaped, "|") + ")$"
				invocations = append(invocations, invocation{
					module:     item.Module,
					items:      []SelectedPackage{item},
					digest:     invocationDigest(item, bucket),
					runPattern: pattern,
					hasPattern: true,
					scratchKey: fmt.Sprintf("%s#shard-%d", item.ImportPath, shardIdx),
				})
			}
		} else if item.ImportPath != "./..." {
			invocations = append(invocations, invocation{
				module:     item.Module,
				items:      []SelectedPackage{item},
				digest:     item.EdgeDigest,
				scratchKey: item.ImportPath,
			})
		} else {
			byMod[item.Module] = append(byMod[item.Module], item)
		}
	}

	modKeys := make([]string, 0, len(byMod))
	for m := range byMod {
		modKeys = append(modKeys, m)
	}
	sort.Strings(modKeys)
	for _, mod := range modKeys {
		invocations = append(invocations, invocation{
			module:     mod,
			items:      byMod[mod],
			scratchKey: "module:" + mod,
		})
	}

	stream := opts.ProgressStream
	if stream == nil && !opts.DisableProgress {
		stream = os.Stdout
	}
	var streamMu sync.Mutex
	emitProgress := func(line string) {
		if opts.DisableProgress || stream == nil {
			return
		}
		progress, ok := FormatProgressEvent(line)
		if !ok {
			return
		}
		streamMu.Lock()
		_, _ = io.WriteString(stream, progress)
		streamMu.Unlock()
	}

	type invResult struct {
		rc           int
		readable     string
		waitMS       int64
		parsed       map[string]PackageTestResult
		sigquitCases []TriageCase
		failCases    []TriageCase
		err          error
	}

	runCmd := opts.RunCommand
	if runCmd == nil {
		runCmd = RunGuardedCommand
	}

	runOne := func(inv invocation) invResult {
		cmd := []string{"go", "test", "-json", "-timeout", "0", "-p", "1", "-parallel", "1"}
		if inv.hasPattern {
			cmd = append(cmd, "-run", inv.runPattern)
		}
		for _, item := range inv.items {
			cmd = append(cmd, item.ImportPath)
		}

		var waitMS int64
		scratchDir, _, releaseScratch, err := AcquireInvocationScratch(durable, inv.scratchKey)
		if err != nil {
			return invResult{rc: 1, readable: err.Error() + "\n", err: err}
		}
		defer releaseScratch()

		envMap := BypassGitShimEnv(baseEnvMap, scratchDir)
		envMap["GOMAXPROCS"] = "1"
		gotmpDir, _ := filepath.Abs(filepath.Join(scratchDir, "gotmp"))
		_ = os.MkdirAll(gotmpDir, 0o755)
		envMap["GOTMPDIR"] = gotmpDir
		envMap["YOLO_DURABLE_DIR"] = scratchDir
		envMap["GIT_CEILING_DIRECTORIES"] = withGitCeiling(envMap["GIT_CEILING_DIRECTORIES"], scratchDir)
		gitconfig, gerr := WritePrivateGitconfig(scratchDir, gitValues, envMapToSlice(envMap))
		if gerr != nil {
			return invResult{rc: 1, readable: gerr.Error() + "\n", err: gerr}
		}
		envMap["GIT_CONFIG_GLOBAL"] = gitconfig
		if inv.digest != nil {
			envMap["TILLR_GATE_EDGE_INPUTS"] = *inv.digest
		} else {
			delete(envMap, "TILLR_GATE_EDGE_INPUTS")
		}
		envSlice := envMapToSlice(envMap)

		rc, stdout, rerr := runCmd(cmd, filepath.Join(resolvedRoot, filepath.FromSlash(inv.module)), envSlice, emitProgress)
		if rerr != nil && stdout == "" {
			stdout = rerr.Error() + "\n"
		}
		parsed, readable := ParseGoTestJSON(stdout)
		known := make(map[string]bool, len(parsed))
		for p := range parsed {
			known[p] = true
		}
		sigquitCases := ExtractSigquitCases(stdout, known)
		failCases := ExtractFailCases(readable, parsed)
		return invResult{
			rc:           rc,
			readable:     readable,
			waitMS:       waitMS,
			parsed:       parsed,
			sigquitCases: sigquitCases,
			failCases:    failCases,
		}
	}

	results := make([]invResult, len(invocations))
	if len(invocations) > 0 {
		workers := len(invocations)
		if workers > 16 {
			workers = 16
		}
		jobs := make(chan int, len(invocations))
		for i := range invocations {
			jobs <- i
		}
		close(jobs)

		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					results[idx] = runOne(invocations[idx])
				}
			}()
		}
		wg.Wait()
	}

	var outputs []string
	pkgShards := make(map[string][]PackageTestResult)
	pkgSigquitTests := make(map[string][]string)
	pkgFailedTests := make(map[string][]string)
	var failedPkgsOrder []string
	failedPkgsSet := make(map[string]bool)
	var allSigquitCases []TriageCase
	var allFailCases []TriageCase
	pkgLocations := make(map[string][2]string)
	for _, item := range selected {
		ip := item.ImportPath
		mod := item.Module
		if ip == "./..." || mod == "" || item.DirRel == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.FromSlash(mod), filepath.FromSlash(item.DirRel))
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		pkgRel := "."
		if rel != "." {
			pkgRel = "./" + rel
		}
		pkgLocations[ip] = [2]string{mod, pkgRel}
	}
	var maxSlotWaitMS int64
	rcAll := 0

	recordFailedPkg := func(pkgName string, tests []string, sigquitTests []string) {
		if !failedPkgsSet[pkgName] {
			failedPkgsSet[pkgName] = true
			failedPkgsOrder = append(failedPkgsOrder, pkgName)
		}
		if len(sigquitTests) > 0 {
			sqBucket := pkgSigquitTests[pkgName]
			for _, name := range sigquitTests {
				if !containsString(sqBucket, name) {
					sqBucket = append(sqBucket, name)
				}
			}
			pkgSigquitTests[pkgName] = sqBucket
		}
		bucket := pkgFailedTests[pkgName]
		for _, name := range tests {
			if !containsString(bucket, name) {
				bucket = append(bucket, name)
			}
		}
		pkgFailedTests[pkgName] = bucket
	}

	for i, inv := range invocations {
		res := results[i]
		if res.waitMS > maxSlotWaitMS {
			maxSlotWaitMS = res.waitMS
		}
		invRecordedFailure := false
		invSigquitByPkg := make(map[string][]string)
		for _, c := range res.sigquitCases {
			invSigquitByPkg[c.Package] = append(invSigquitByPkg[c.Package], c.Name)
		}
		for pkgName, rec := range res.parsed {
			pkgShards[pkgName] = append(pkgShards[pkgName], rec)
			if (rec.Passed != nil && !*rec.Passed) || len(rec.FailedTests) > 0 {
				recordFailedPkg(pkgName, rec.FailedTests, invSigquitByPkg[pkgName])
				invRecordedFailure = true
			}
		}
		if res.rc != 0 {
			rcAll = res.rc
			outputs = append(outputs, res.readable)
			allSigquitCases = append(allSigquitCases, res.sigquitCases...)
			allFailCases = append(allFailCases, res.failCases...)
			if !invRecordedFailure {
				for _, item := range inv.items {
					pkgLabel := item.ImportPath
					if pkgLabel == "./..." {
						pkgLabel = inv.module
					}
					recordFailedPkg(pkgLabel, nil, nil)
				}
			}
		}
	}

	packagesEvidence := make([]EvidencePackage, 0, len(selected))
	cachedCount := 0
	for _, item := range selected {
		ip := item.ImportPath
		if reusedIPs[ip] {
			packagesEvidence = append(packagesEvidence, FormatEvidencePackage(item, 0, true, true))
			cachedCount++
			continue
		}
		shardsList := pkgShards[ip]
		var maxElapsed int64
		allCached := len(shardsList) > 0
		for _, s := range shardsList {
			if s.ElapsedMS > maxElapsed {
				maxElapsed = s.ElapsedMS
			}
			if !s.Cached {
				allCached = false
			}
		}
		if allCached {
			cachedCount++
		}
		packagesEvidence = append(packagesEvidence, FormatEvidencePackage(item, maxElapsed, allCached, false))
	}

	if opts.VerdictsOut != "" && fallbackGraph != nil {
		outVerdicts := &PackageVerdicts{
			Schema:   1,
			Packages: make(map[string]PackageVerdictEntry),
		}
		for _, item := range selected {
			ip := item.ImportPath
			if ip == "./..." || failedPkgsSet[ip] {
				continue
			}
			if !reusedIPs[ip] {
				shardsList := pkgShards[ip]
				if len(shardsList) == 0 {
					continue
				}
				allPassed := true
				for _, s := range shardsList {
					if s.Passed == nil || !*s.Passed || len(s.FailedTests) > 0 {
						allPassed = false
						break
					}
				}
				if !allPassed {
					continue
				}
			}
			RecordPackageVerdict(resolvedRoot, ip, fallbackGraph, edges, outVerdicts, item.SelectedTests, digestCache)
		}
		_ = SavePackageVerdictsAtomic(opts.VerdictsOut, outVerdicts)
	}

	evidence := &Evidence{
		Mode:        "go-affected",
		SelectionMs: selection.SelectionMs,
		SlotWaitMs:  maxSlotWaitMS,
		Packages:    packagesEvidence,
	}
	WriteEvidenceAtomic(opts.EvidencePath, evidence)

	reusedNote := ""
	if len(reusedIPs) > 0 {
		reusedNote = fmt.Sprintf(", %d reused", len(reusedIPs))
	}
	summary := fmt.Sprintf(
		"go-affected: %d package(s) (%d cached%s, selection %dms, slot_wait %dms)\n",
		len(selected), cachedCount, reusedNote, selection.SelectionMs, maxSlotWaitMS,
	)
	var triageBlock string
	var failureSummary string
	if rcAll != 0 {
		var orderedPkgTests []FailedPackage
		for _, pkgName := range failedPkgsOrder {
			sq := pkgSigquitTests[pkgName]
			ft := pkgFailedTests[pkgName]
			combined := make([]string, 0, len(sq)+len(ft))
			combined = append(combined, sq...)
			combined = append(combined, ft...)
			orderedPkgTests = append(orderedPkgTests, FailedPackage{
				Package: pkgName,
				Tests:   dedupeStrings(combined),
			})
		}

		type caseKey struct {
			pkg  string
			name string
		}
		var triageCases []TriageCase
		seenCases := make(map[caseKey]bool)
		for _, c := range allSigquitCases {
			key := caseKey{pkg: c.Package, name: c.Name}
			if !seenCases[key] {
				seenCases[key] = true
				triageCases = append(triageCases, c)
			}
		}
		for _, c := range allFailCases {
			key := caseKey{pkg: c.Package, name: c.Name}
			if !seenCases[key] {
				seenCases[key] = true
				triageCases = append(triageCases, c)
			}
		}
		for _, fp := range orderedPkgTests {
			for _, name := range fp.Tests {
				key := caseKey{pkg: fp.Package, name: name}
				if !seenCases[key] {
					seenCases[key] = true
					triageCases = append(triageCases, TriageCase{
						Package:   fp.Package,
						Name:      name,
						Where:     fp.Package,
						Assertion: "",
						Sigquit:   false,
					})
				}
			}
		}
		triageBlock = FormatTriageBlock(triageCases, pkgLocations)
		failureSummary = FormatFailureSummary(orderedPkgTests)
	}
	body := strings.Join(outputs, "")
	if body != "" && triageBlock != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return rcAll, summary + body + triageBlock + failureSummary, evidence, nil
}
