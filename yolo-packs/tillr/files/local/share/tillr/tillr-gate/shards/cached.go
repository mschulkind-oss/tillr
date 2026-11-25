package shards

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var cacheOutputLineRe = regexp.MustCompile(`(?m)^(?:ok|FAIL|\?)\s`)

// ClassifyGroupCache classifies a cached go test -json event stream as "hit", "miss", or "unknown".
func ClassifyGroupCache(events []TestEvent) string {
	var pkgOutputs []string
	hasTerminal := false
	for _, ev := range events {
		if ev.Test != "" {
			continue
		}
		if ev.Action == "output" && ev.Output != "" {
			pkgOutputs = append(pkgOutputs, ev.Output)
		}
		if ev.Action == goTestActionPass || ev.Action == goTestActionFail || ev.Action == goTestActionSkip {
			hasTerminal = true
		}
	}
	for _, chunk := range pkgOutputs {
		if strings.Contains(chunk, "(cached)") {
			return "hit"
		}
	}
	for _, chunk := range pkgOutputs {
		if cacheOutputLineRe.MatchString(chunk) || strings.Contains(chunk, "[no tests to run]") {
			return "miss"
		}
	}
	if hasTerminal {
		return "miss"
	}
	return "unknown"
}

// RunCachedGroup runs one cached `go test -json` group under an invocation scratch slot.
func RunCachedGroup(
	pkg, cwd string,
	names []string,
	index int,
	scratchKey, durable, timeout string,
	baseEnv map[string]string,
	gitValues [][2]string,
	explicitScratch bool,
	edgeDigest, runPattern string,
	opts RunOptions,
	progress *ProgressTracker,
) ([]TestEvent, string, error) {
	opts = opts.normalized()
	label := fmt.Sprintf("%s shard %d", pkg, index)
	command := []string{"go", "test", "-json", "-timeout", timeout, "-p", "1", "-parallel", "1"}
	if runPattern != "" {
		command = append(command, "-run", runPattern)
	}
	command = append(command, pkg)

	scratchDir, scratchCleanup, err := AcquireInvocationScratch(durable, scratchKey)
	if err != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	defer scratchCleanup()

	env := BypassGitShimEnv(baseEnv, scratchDir)
	env["GOMAXPROCS"] = "1"
	env["YOLO_DURABLE_DIR"] = scratchDir
	if explicitScratch {
		env["TMPDIR"] = scratchDir
	}
	gitconfig, err := WritePrivateGitConfig(scratchDir, gitValues, env)
	if err != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot prepare private git config: %v", label, err)}
	}
	env["GIT_CONFIG_GLOBAL"] = gitconfig
	if edgeDigest != "" {
		env["TILLR_GATE_EDGE_INPUTS"] = edgeDigest
	} else {
		delete(env, "TILLR_GATE_EDGE_INPUTS")
	}

	outFile, err := os.CreateTemp(scratchDir, "cached-out-*")
	if err != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	outPath := outFile.Name()
	defer func() { _ = outFile.Close(); _ = os.Remove(outPath) }()

	errFile, err := os.CreateTemp(scratchDir, "cached-err-*")
	if err != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	errPath := errFile.Name()
	defer func() { _ = errFile.Close(); _ = os.Remove(errPath) }()

	reader, err := os.Open(outPath)
	if err != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	defer reader.Close()

	errReader, err := os.Open(errPath)
	if err != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	defer errReader.Close()

	pid, waitCh, cleanup, err := startGuardedWithWait(opts.ExePath, cwd, EnvMapToSlice(env), outFile, errFile, command)
	if err != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}

	assigned := make(map[string]struct{}, len(names))
	for _, n := range names {
		assigned[n] = struct{}{}
	}

	stdoutStr, stderrStr, timedOut, procStatus, monErr := monitorGuardedProcess(
		label, pkg, assigned, len(assigned) == 0, pid, waitCh,
		outFile, errFile, reader, errReader,
		opts.ShardTimeout, progress, opts.Now, opts.PollInterval,
	)
	cleanup()
	if monErr != nil {
		return nil, "", &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, monErr)}
	}

	var events []TestEvent
	for _, line := range strings.Split(stdoutStr, "\n") {
		if line == "" {
			continue
		}
		var ev TestEvent
		if uerr := json.Unmarshal([]byte(line), &ev); uerr != nil {
			if !timedOut {
				return nil, "", &ShardError{Message: fmt.Sprintf("%s invalid test report: %v", label, uerr), Events: events}
			}
			break
		}
		events = append(events, ev)
	}

	if timedOut {
		active := make(map[string]struct{})
		doneSet := make(map[string]struct{})
		for _, ev := range events {
			if ev.Test != "" && ev.Action == goTestActionRun {
				active[ev.Test] = struct{}{}
			} else if ev.Test != "" && (ev.Action == goTestActionPass || ev.Action == goTestActionSkip || ev.Action == goTestActionFail) {
				delete(active, ev.Test)
				doneSet[ev.Test] = struct{}{}
			}
		}
		var activeList, pendingList []string
		for a := range active {
			activeList = append(activeList, a)
		}
		sort.Strings(activeList)
		for _, n := range names {
			if _, ok := doneSet[n]; !ok {
				pendingList = append(pendingList, n)
			}
		}
		sort.Strings(pendingList)
		detail := strings.Join(activeList, ", ")
		if detail == "" {
			detail = "none reported"
		}
		remaining := strings.Join(pendingList, ", ")
		if remaining == "" {
			remaining = "none"
		}
		return nil, "", &ShardError{
			Message: fmt.Sprintf("%s timed out after %ss without top-level test progress; running: %s; not completed: %s\n%s",
				label, formatTimeoutFloat(opts.ShardTimeout), detail, remaining, stderrStr),
			Events:   events,
			TimedOut: true,
		}
	}

	hasFail := false
	for _, ev := range events {
		if ev.Action == goTestActionFail {
			hasFail = true
			break
		}
	}
	if procStatus != 0 || hasFail {
		return nil, "", &ShardError{
			Message: buildFailureMessage(label, procStatus, events, stderrStr),
			Events:  events,
		}
	}

	pkgDone := false
	completed := make(map[string]struct{})
	for _, ev := range events {
		if ev.Test == "" && (ev.Action == goTestActionPass || ev.Action == goTestActionSkip) {
			pkgDone = true
		}
		if ev.Test != "" && (ev.Action == goTestActionPass || ev.Action == goTestActionSkip) {
			completed[ev.Test] = struct{}{}
		}
	}
	if !pkgDone {
		var missing []string
		for _, n := range names {
			if _, ok := completed[n]; !ok {
				missing = append(missing, n)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			return nil, "", &ShardError{
				Message: fmt.Sprintf("%s missing completion: %s", label, strings.Join(missing, ", ")),
				Events:  events,
			}
		}
	}
	return events, ClassifyGroupCache(events), nil
}

// RunCached runs tests with Go's result cache enabled and edge-digest tracking.
func RunCached(module string, count int, durations map[string]map[string]float64, scratch, timeout string, opts RunOptions) ([]TestEvent, error) {
	opts = opts.normalized()
	if err := CheckCachedWorkerRefusal(module); err != nil {
		return nil, err
	}
	var progress *ProgressTracker
	if opts.ProgressFile != "" {
		if math.IsNaN(opts.ProgressInterval) || math.IsInf(opts.ProgressInterval, 0) || opts.ProgressInterval <= 0 {
			return nil, fmt.Errorf("progress interval must be a positive finite number of seconds")
		}
		progress = NewProgressTracker(opts.ProgressFile, opts.ProgressInterval, opts.Now)
		progress.onMaybePublish = opts.onMaybePublish
	}

	rawEnvSnap := EnvSliceToMap(os.Environ())
	baseEnv := BuildEnv()

	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = module
	cmd.Env = EnvMapToSlice(baseEnv)
	out, listErr := cmd.Output()
	if listErr != nil && opts.EvidenceOut == "" {
		return nil, listErr
	}

	packages, parseErr := parseGoListStream(out, listErr != nil)
	if listErr == nil && parseErr != nil {
		return nil, parseErr
	}

	initialInventory := make([]evidencePackage, 0, len(packages))
	for _, p := range packages {
		if p.ImportPath == "" {
			continue
		}
		status := "not-applicable"
		if listErr != nil || len(p.TestGoFiles) > 0 || len(p.XTestGoFiles) > 0 {
			status = "not-started"
		}
		initialInventory = append(initialInventory, evidencePackage{
			ImportPath: p.ImportPath,
			TestCache:  status,
		})
	}
	if opts.EvidenceOut != "" {
		if listErr != nil {
			writeEvidenceSafe(opts.EvidenceOut, map[string]any{
				"discovery": "partial",
				"packages":  initialInventory,
			})
		} else {
			writeEvidenceSafe(opts.EvidenceOut, map[string]any{
				"mode":     "cached-shards",
				"packages": initialInventory,
			})
		}
	}
	if listErr != nil {
		return nil, listErr
	}

	gitValues, err := GlobalGitConfig(rawEnvSnap)
	if err != nil {
		return nil, err
	}
	durable := opts.DurableDir
	if durable == "" {
		if v := rawEnvSnap["YOLO_DURABLE_DIR"]; v != "" {
			durable = v
		} else if v := rawEnvSnap["TMPDIR"]; v != "" {
			durable = v
		} else {
			durable = scratch
		}
	}

	repoRoot := LocateRepoRoot(module, false, true)
	var edges map[string]Edge
	if repoRoot != "" {
		edges, err = LoadEdges(repoRoot)
		if err != nil {
			return nil, err
		}
	} else {
		edges = map[string]Edge{}
	}

	needsGraph := false
	for _, p := range packages {
		if p.ImportPath == "" || (len(p.TestGoFiles) == 0 && len(p.XTestGoFiles) == 0) {
			continue
		}
		if edge, ok := edges[p.ImportPath]; ok && len(edge.Builds) > 0 {
			needsGraph = true
			break
		}
	}
	var pkgInfo map[string]PkgInfo
	if needsGraph && repoRoot != "" {
		graph, gerr := LoadTillrGraph(repoRoot, nil)
		if gerr != nil {
			return nil, gerr
		}
		pkgInfo = graph.PkgInfo
	} else {
		pkgInfo = map[string]PkgInfo{}
	}

	resolvedBuildsCache := make(map[string][2][]string)
	digestFor := func(importPath string, activeTests []string) string {
		edge, ok := edges[importPath]
		if !ok || repoRoot == "" {
			return ""
		}
		resolved, cached := resolvedBuildsCache[importPath]
		if !cached {
			bf, mb := ResolveEdgeBuilds(repoRoot, edge, pkgInfo)
			resolved = [2][]string{bf, mb}
			resolvedBuildsCache[importPath] = resolved
		}
		return ComputeEdgeDigest(repoRoot, edge, pkgInfo, activeTests, &resolved)
	}

	type cachedJob struct {
		ip         string
		names      []string
		shardIdx   int
		scratchKey string
		edgeDigest string
		runPattern string
	}
	var jobs []cachedJob
	plannedGroups := make(map[string]int)

	for _, pkg := range packages {
		if len(pkg.TestGoFiles) == 0 && len(pkg.XTestGoFiles) == 0 {
			continue
		}
		ip := pkg.ImportPath
		tfSet := make(map[string]struct{}, len(pkg.TestGoFiles)+len(pkg.XTestGoFiles))
		for _, f := range pkg.TestGoFiles {
			tfSet[f] = struct{}{}
		}
		for _, f := range pkg.XTestGoFiles {
			tfSet[f] = struct{}{}
		}
		testFiles := make([]string, 0, len(tfSet))
		for f := range tfSet {
			testFiles = append(testFiles, f)
		}
		sort.Strings(testFiles)

		names := ScanPackageTests(pkg.Dir, ".", ip, "", testFiles)
		if progress != nil {
			progress.RecordPackage("discovered " + ip)
		}

		if len(names) < ShardThreshold || count <= 1 {
			plannedGroups[ip] = 1
			runPattern := ""
			if len(names) == 0 {
				runPattern = "^$"
			}
			jobs = append(jobs, cachedJob{
				ip:         ip,
				names:      names,
				shardIdx:   0,
				scratchKey: ip,
				edgeDigest: digestFor(ip, nil),
				runPattern: runPattern,
			})
			continue
		}

		hasEdgeTest := false
		var nonEdge []string
		for _, n := range names {
			if n == "TestEdgeInputs" {
				hasEdgeTest = true
			} else {
				nonEdge = append(nonEdge, n)
			}
		}
		shardCount := count
		if calc := len(nonEdge) / 125; calc > shardCount {
			shardCount = calc
		}
		if shardCount > MaxShards {
			shardCount = MaxShards
		}
		buckets := Assign(nonEdge, durations[ip], shardCount)
		pkgJobCount := 0
		for shardIdx, bucket := range buckets {
			if len(bucket) == 0 {
				continue
			}
			shardNames := append([]string(nil), bucket...)
			if hasEdgeTest {
				shardNames = append(shardNames, "TestEdgeInputs")
			}
			quoted := make([]string, len(shardNames))
			for i, n := range shardNames {
				quoted[i] = regexp.QuoteMeta(n)
			}
			pattern := "^(" + strings.Join(quoted, "|") + ")$"
			jobs = append(jobs, cachedJob{
				ip:         ip,
				names:      shardNames,
				shardIdx:   shardIdx,
				scratchKey: fmt.Sprintf("%s#shard-%d", ip, shardIdx),
				edgeDigest: digestFor(ip, bucket),
				runPattern: pattern,
			})
			pkgJobCount++
		}
		plannedGroups[ip] = pkgJobCount
	}

	type jobResult struct {
		jobIndex   int
		ip         string
		shardIdx   int
		events     []TestEvent
		groupCache string
		err        error
	}
	resCh := make(chan jobResult, len(jobs))
	var wg sync.WaitGroup
	runGroup := RunCachedGroup
	if opts.runCachedGroupFn != nil {
		runGroup = opts.runCachedGroupFn
	}
	for i, job := range jobs {
		wg.Add(1)
		go func(idx int, j cachedJob) {
			defer wg.Done()
			evs, gc, rerr := runGroup(
				j.ip, module, j.names, j.shardIdx, j.scratchKey,
				durable, timeout, baseEnv, gitValues,
				opts.ExplicitScratch, j.edgeDigest, j.runPattern, opts, progress,
			)
			resCh <- jobResult{
				jobIndex:   idx,
				ip:         j.ip,
				shardIdx:   j.shardIdx,
				events:     evs,
				groupCache: gc,
				err:        rerr,
			}
		}(i, job)
	}
	go func() {
		wg.Wait()
		close(resCh)
	}()

	var events []TestEvent
	groupResults := make(map[string][]string)
	type indexedErr struct {
		index int
		text  string
	}
	var shardErrors []indexedErr

	for res := range resCh {
		if res.err != nil {
			var se *ShardError
			if errors.As(res.err, &se) {
				events = append(events, se.Events...)
				groupResults[res.ip] = append(groupResults[res.ip], ClassifyGroupCache(se.Events))
				shardErrors = append(shardErrors, indexedErr{index: res.jobIndex, text: se.Message})
				if progress != nil && !se.TimedOut {
					progress.RecordShard(fmt.Sprintf("%s shard %d", res.ip, res.shardIdx))
				}
			} else {
				shardErrors = append(shardErrors, indexedErr{index: res.jobIndex, text: res.err.Error()})
			}
		} else {
			events = append(events, res.events...)
			if opts.UncachedEvents != nil && res.groupCache != "hit" {
				*opts.UncachedEvents = append(*opts.UncachedEvents, res.events...)
			}
			groupResults[res.ip] = append(groupResults[res.ip], res.groupCache)
			if progress != nil {
				progress.RecordShard(fmt.Sprintf("%s shard %d", res.ip, res.shardIdx))
			}
		}
	}

	if opts.EvidenceOut != "" {
		finalPackages := make([]evidencePackage, 0, len(initialInventory))
		for _, entry := range initialInventory {
			if entry.TestCache == "not-applicable" {
				finalPackages = append(finalPackages, entry)
				continue
			}
			statuses := groupResults[entry.ImportPath]
			expected := plannedGroups[entry.ImportPath]
			status := "miss"
			if expected == 0 || len(statuses) < expected {
				status = "not-started"
			} else {
				hasUnknown := false
				allHit := true
				for _, s := range statuses {
					if s == "unknown" {
						hasUnknown = true
					}
					if s != "hit" {
						allHit = false
					}
				}
				if hasUnknown {
					status = "unknown"
				} else if allHit {
					status = "hit"
				}
			}
			finalPackages = append(finalPackages, evidencePackage{
				ImportPath: entry.ImportPath,
				TestCache:  status,
			})
		}
		writeEvidenceSafe(opts.EvidenceOut, map[string]any{
			"mode":     "cached-shards",
			"packages": finalPackages,
		})
	}

	if len(shardErrors) > 0 {
		sort.Slice(shardErrors, func(i, j int) bool { return shardErrors[i].index < shardErrors[j].index })
		msgs := make([]string, len(shardErrors))
		for i, se := range shardErrors {
			msgs[i] = se.text
		}
		return nil, &ShardError{
			Message: strings.Join(msgs, "\ngo-test-shards: "),
			Events:  events,
		}
	}
	return events, nil
}
