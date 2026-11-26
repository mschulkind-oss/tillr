package shards

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultShardTimeout     = 480.0
	DefaultProgressInterval = 60.0
	ProgressOutEnv          = "TILLR_GO_SHARDS_PROGRESS_OUT"
	EvidenceOutEnv          = "TILLR_GO_SHARDS_EVIDENCE_OUT"

	goTestActionRun  = "run"
	goTestActionPass = "pass"
	goTestActionFail = "fail"
	goTestActionSkip = "skip"
)

var (
	testListNameRe = regexp.MustCompile(`^(Test|Example|Fuzz)\w*$`)
	timeoutPanicRe = regexp.MustCompile(`(?m)^panic: test timed out after `)
)

// ShardError indicates a named shard failed or did not prove completion of its assigned tests.
type ShardError struct {
	Message  string
	Events   []TestEvent
	TimedOut bool
}

func (e *ShardError) Error() string {
	return e.Message
}

// TestEvent represents one JSON line emitted by `go test -json` or `go tool test2json`.
type TestEvent struct {
	Time    string   `json:"Time,omitempty"`
	Action  string   `json:"Action,omitempty"`
	Package string   `json:"Package,omitempty"`
	Test    string   `json:"Test,omitempty"`
	Elapsed *float64 `json:"Elapsed,omitempty"`
	Output  string   `json:"Output,omitempty"`
	Raw     json.RawMessage
}

func (e *TestEvent) UnmarshalJSON(data []byte) error {
	type alias TestEvent
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*e = TestEvent(a)
	e.Raw = append([]byte(nil), data...)
	return nil
}

func (e TestEvent) MarshalJSON() ([]byte, error) {
	if len(e.Raw) > 0 {
		return e.Raw, nil
	}
	type alias TestEvent
	a := alias(e)
	a.Raw = nil
	return json.Marshal(a)
}

// ProgressTracker is a thread-safe monotonic completion counter with rate-limited atomic JSON snapshots.
type ProgressTracker struct {
	path               string
	interval           time.Duration
	now                func() time.Time
	mu                 sync.Mutex
	nextPublishAt      time.Time
	lastPublishedUnits int
	seq                int
	completedPackages  int
	completedTests     int
	completedShards    int
	completedUnits     int
	lastDetail         string
	onMaybePublish     func(*ProgressTracker)
}

// NewProgressTracker creates a ProgressTracker writing to progressFile at most once per intervalSeconds.
func NewProgressTracker(progressFile string, intervalSeconds float64, now func() time.Time) *ProgressTracker {
	if now == nil {
		now = time.Now
	}
	interval := time.Duration(intervalSeconds * float64(time.Second))
	return &ProgressTracker{
		path:          progressFile,
		interval:      interval,
		now:           now,
		nextPublishAt: now().Add(interval),
	}
}

func (p *ProgressTracker) RecordPackage(detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completedPackages++
	p.completedUnits = p.completedPackages + p.completedTests + p.completedShards
	p.lastDetail = detail
	p.maybePublishLocked()
}

func (p *ProgressTracker) RecordTest(detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completedTests++
	p.completedUnits = p.completedPackages + p.completedTests + p.completedShards
	p.lastDetail = detail
	p.maybePublishLocked()
}

func (p *ProgressTracker) RecordShard(detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completedShards++
	p.completedUnits = p.completedPackages + p.completedTests + p.completedShards
	p.lastDetail = detail
	p.maybePublishLocked()
}

func (p *ProgressTracker) MaybePublish() {
	if p.onMaybePublish != nil {
		p.onMaybePublish(p)
		return
	}
	p.maybePublish()
}

func (p *ProgressTracker) maybePublish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.maybePublishLocked()
}

func (p *ProgressTracker) maybePublishLocked() {
	now := p.now()
	if now.Before(p.nextPublishAt) || p.completedUnits <= p.lastPublishedUnits {
		return
	}
	p.seq++
	p.lastPublishedUnits = p.completedUnits
	p.nextPublishAt = now.Add(p.interval)
	snapshot := struct {
		Seq               int    `json:"seq"`
		CompletedUnits    int    `json:"completed_units"`
		CompletedPackages int    `json:"completed_packages"`
		CompletedTests    int    `json:"completed_tests"`
		CompletedShards   int    `json:"completed_shards"`
		LastDetail        string `json:"last_detail"`
	}{
		Seq:               p.seq,
		CompletedUnits:    p.completedUnits,
		CompletedPackages: p.completedPackages,
		CompletedTests:    p.completedTests,
		CompletedShards:   p.completedShards,
		LastDetail:        p.lastDetail,
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	payload := append(data, '\n')
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: cannot write progress file: %v\n", err)
		return
	}
	tmp, err := os.CreateTemp(dir, ".go-progress-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: cannot write progress file: %v\n", err)
		return
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(payload)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmpName)
		if werr != nil {
			fmt.Fprintf(os.Stderr, "go-test-shards: cannot write progress file: %v\n", werr)
		} else {
			fmt.Fprintf(os.Stderr, "go-test-shards: cannot write progress file: %v\n", cerr)
		}
		return
	}
	if err := os.Rename(tmpName, p.path); err != nil {
		_ = os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "go-test-shards: cannot write progress file: %v\n", err)
	}
}

// Assign balances test names across count shards using recorded durations.
func Assign(names []string, durations map[string]float64, count int) [][]string {
	groups := make([][]string, count)
	var known []string
	var unknown []string
	for _, name := range names {
		if _, ok := durations[name]; ok {
			known = append(known, name)
		} else {
			unknown = append(unknown, name)
		}
	}
	sort.Slice(known, func(i, j int) bool {
		di, dj := durations[known[i]], durations[known[j]]
		if di != dj {
			return di > dj
		}
		return known[i] < known[j]
	})
	loads := make([]float64, count)
	minSlot := func() int {
		best := 0
		for i := 1; i < count; i++ {
			if loads[i] < loads[best] {
				best = i
			}
		}
		return best
	}
	for _, name := range known {
		slot := minSlot()
		groups[slot] = append(groups[slot], name)
		loads[slot] += durations[name]
	}
	estimate := 1.0
	if len(known) > 0 {
		sum := 0.0
		for _, name := range known {
			sum += durations[name]
		}
		mean := sum / float64(len(known))
		if mean != 0 {
			estimate = mean
		}
	}
	for _, name := range unknown {
		slot := minSlot()
		groups[slot] = append(groups[slot], name)
		loads[slot] += estimate
	}
	return groups
}

// LoadDurations reads and validates a go-test-durations.json file.
func LoadDurations(path string) (map[string]map[string]float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("durations must map package names to test durations: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("durations must map package names to test durations")
	}
	out := make(map[string]map[string]float64, len(raw))
	for pkg, tests := range raw {
		if tests == nil {
			return nil, fmt.Errorf("%s: durations must be an object", pkg)
		}
		pkgMap := make(map[string]float64, len(tests))
		for name, val := range tests {
			num, ok := val.(float64)
			if !ok || math.IsNaN(num) || math.IsInf(num, 0) || num < 0 {
				return nil, fmt.Errorf("%s/%s: invalid duration %v", pkg, name, val)
			}
			pkgMap[name] = num
		}
		out[pkg] = pkgMap
	}
	return out, nil
}

func writeEvidenceSafe(evidenceOut string, payload any) {
	if evidenceOut == "" {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: cannot write package evidence: %v\n", err)
		return
	}
	if err := os.WriteFile(evidenceOut, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: cannot write package evidence: %v\n", err)
	}
}

// RunOptions configures a sharded test run.
type RunOptions struct {
	ShardTimeout     float64
	EvidenceOut      string
	ProgressFile     string
	ProgressInterval float64
	Cache            bool
	ExplicitScratch  bool
	DurableDir       string
	UncachedEvents   *[]TestEvent
	ExePath          string
	Now              func() time.Time
	PollInterval     time.Duration
	onMaybePublish   func(*ProgressTracker)
	runShardFn       func(pkg, cwd, binary string, names []string, index int, scratch, timeout string, gitValues [][2]string, opts RunOptions, progress *ProgressTracker) ([]TestEvent, error)
	runCachedGroupFn func(pkg, cwd string, names []string, index int, scratchKey, durable, timeout string, baseEnv map[string]string, gitValues [][2]string, explicitScratch bool, edgeDigest, runPattern string, opts RunOptions, progress *ProgressTracker) ([]TestEvent, string, error)
}

func (o *RunOptions) normalized() RunOptions {
	out := *o
	if out.ShardTimeout <= 0 {
		out.ShardTimeout = DefaultShardTimeout
	}
	if out.ProgressInterval <= 0 {
		out.ProgressInterval = DefaultProgressInterval
	}
	if out.ExePath == "" {
		if exe, err := os.Executable(); err == nil {
			out.ExePath = exe
		}
	}
	if out.Now == nil {
		out.Now = time.Now
	}
	if out.PollInterval <= 0 {
		out.PollInterval = time.Second
	}
	return out
}

func startGuardedWithWait(exePath, cwd string, env []string, stdout, stderr *os.File, command []string) (int, <-chan error, func(), error) {
	r, w, err := os.Pipe()
	if err != nil {
		return 0, nil, nil, err
	}
	args := make([]string, 0, 4+len(command))
	args = append(args, "shards", GuardFlag, strconv.Itoa(3), "--")
	args = append(args, command...)

	cmd := exec.Command(exePath, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.ExtraFiles = []*os.File{r}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return 0, nil, nil, err
	}
	_ = r.Close()

	pid := cmd.Process.Pid
	registerLiveGroup(pid)

	waitCh := make(chan error, 1)
	doneCh := make(chan struct{})
	go func() {
		waitCh <- cmd.Wait()
		close(doneCh)
	}()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			KillGroup(pid)
			_ = w.Close()
			unregisterLiveGroup(pid)
			<-doneCh
		})
	}
	return pid, waitCh, cleanup, nil
}

func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return -int(status.Signal())
		}
		return exitErr.ExitCode()
	}
	return 1
}

func formatTimeoutFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func buildFailureMessage(label string, returnCode int, events []TestEvent, stderr string) string {
	var rawFailed []string
	seenFailed := make(map[string]struct{})
	var allOutput strings.Builder
	for _, ev := range events {
		allOutput.WriteString(ev.Output)
		if ev.Action == goTestActionFail && ev.Test != "" {
			if _, ok := seenFailed[ev.Test]; !ok {
				seenFailed[ev.Test] = struct{}{}
				rawFailed = append(rawFailed, ev.Test)
			}
		}
	}
	var failedTests []string
	for _, name := range rawFailed {
		isParent := false
		for _, other := range rawFailed {
			if other != name && strings.HasPrefix(other, name+"/") {
				isParent = true
				break
			}
		}
		if !isParent {
			failedTests = append(failedTests, name)
		}
	}

	output := allOutput.String()
	if len(rawFailed) > 0 && !timeoutPanicRe.MatchString(output) {
		var filtered strings.Builder
		for _, ev := range events {
			if ev.Output == "" {
				continue
			}
			belongs := ev.Test == ""
			if !belongs {
				for _, failed := range rawFailed {
					if ev.Test == failed || strings.HasPrefix(ev.Test, failed+"/") || strings.HasPrefix(failed, ev.Test+"/") {
						belongs = true
						break
					}
				}
			}
			if belongs {
				filtered.WriteString(ev.Output)
			}
		}
		output = filtered.String()
	}

	headline := fmt.Sprintf("%s failed (exit %d)", label, returnCode)
	if len(failedTests) > 0 {
		headline += ": " + strings.Join(failedTests, ", ")
	}
	detail := strings.TrimRight(output+stderr, "\n")
	if detail != "" {
		return headline + "\n" + detail
	}
	return headline
}

func monitorGuardedProcess(
	label, pkg string,
	assigned map[string]struct{},
	allowAnyTopLevel bool,
	pid int,
	waitCh <-chan error,
	outFile, errFile, reader, errReader *os.File,
	wallTimeout float64,
	progress *ProgressTracker,
	now func() time.Time,
	pollInterval time.Duration,
) (stdoutStr, stderrStr string, timedOut bool, exitCode int, err error) {
	wallDur := time.Duration(wallTimeout * float64(time.Second))
	deadline := now().Add(wallDur)
	var pending []byte
	type progressKey struct {
		test   string
		action string
	}
	seenProgress := make(map[progressKey]struct{})
	seenCompleted := make(map[string]struct{})

	processDone := false
	var waitErr error

	readNewLines := func() {
		info, serr := outFile.Stat()
		if serr != nil {
			return
		}
		curOff, _ := reader.Seek(0, 1)
		toRead := info.Size() - curOff
		if toRead <= 0 {
			return
		}
		buf := make([]byte, toRead)
		n, _ := reader.Read(buf)
		if n <= 0 {
			return
		}
		combined := append(pending, buf[:n]...)
		lines := bytes.Split(combined, []byte{'\n'})
		pending = append([]byte(nil), lines[len(lines)-1]...)
		for _, line := range lines[:len(lines)-1] {
			var ev TestEvent
			if json.Unmarshal(line, &ev) != nil {
				continue
			}
			_, inAssigned := assigned[ev.Test]
			if (inAssigned || (allowAnyTopLevel && ev.Test != "")) &&
				(ev.Action == goTestActionRun || ev.Action == goTestActionPass || ev.Action == goTestActionSkip || ev.Action == goTestActionFail) {
				k := progressKey{test: ev.Test, action: ev.Action}
				if _, ok := seenProgress[k]; !ok {
					seenProgress[k] = struct{}{}
					deadline = now().Add(wallDur)
				}
			}
			isTarget := inAssigned || (allowAnyTopLevel && ev.Test != "")
			if isTarget && ev.Test != "" && !strings.Contains(ev.Test, "/") &&
				(ev.Action == goTestActionPass || ev.Action == goTestActionSkip || ev.Action == goTestActionFail) {
				if _, ok := seenCompleted[ev.Test]; !ok {
					seenCompleted[ev.Test] = struct{}{}
					if progress != nil {
						progress.RecordTest(fmt.Sprintf("%s %s %s", pkg, ev.Test, ev.Action))
					}
				}
			}
		}
	}

	for {
		readNewLines()
		if progress != nil {
			progress.MaybePublish()
		}
		if processDone {
			break
		}
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			KillGroup(pid)
			waitErr = <-waitCh
			processDone = true
			timedOut = true
			break
		}
		waitDur := pollInterval
		if remaining < waitDur {
			waitDur = remaining
		}
		if progress != nil && progress.interval < waitDur {
			waitDur = progress.interval
		}
		if waitDur < 0 {
			waitDur = 0
		}
		timer := time.NewTimer(waitDur)
		select {
		case waitErr = <-waitCh:
			timer.Stop()
			processDone = true
		case <-timer.C:
		}
	}

	_, _ = reader.Seek(0, 0)
	var outSize, errSize int64
	if info, serr := outFile.Stat(); serr == nil {
		outSize = info.Size()
	}
	if info, serr := errFile.Stat(); serr == nil {
		errSize = info.Size()
	}
	outBuf := make([]byte, outSize)
	if outSize > 0 {
		_, _ = reader.Read(outBuf)
	}
	errBuf := make([]byte, errSize)
	if errSize > 0 {
		_, _ = errReader.Read(errBuf)
	}
	return string(outBuf), string(errBuf), timedOut, exitCodeFromErr(waitErr), nil
}

// RunShard executes one sharded test binary subset under a lifeline-guarded process group.
func RunShard(
	pkg, cwd, binary string,
	names []string,
	index int,
	scratch, timeout string,
	gitValues [][2]string,
	opts RunOptions,
	progress *ProgressTracker,
) ([]TestEvent, error) {
	opts = opts.normalized()
	label := fmt.Sprintf("%s shard %d", pkg, index)
	envMap, err := PrivateEnvironment(label, scratch, gitValues)
	if err != nil {
		return nil, err
	}
	envMap["GOMAXPROCS"] = "1"
	if !isExecutableFile(binary) {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot start: test binary is missing or not executable: %s", label, binary)}
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = regexp.QuoteMeta(n)
	}
	pattern := "^(" + strings.Join(quoted, "|") + ")$"
	command := []string{
		"go", "tool", "test2json", "-t", "-p", pkg, binary,
		"-test.v=test2json", "-test.count=1", "-test.parallel=1",
		"-test.timeout=" + timeout, "-test.run=" + pattern,
	}

	outFile, err := os.CreateTemp(scratch, "shard-out-*")
	if err != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	outPath := outFile.Name()
	defer func() { _ = outFile.Close(); _ = os.Remove(outPath) }()

	errFile, err := os.CreateTemp(scratch, "shard-err-*")
	if err != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	errPath := errFile.Name()
	defer func() { _ = errFile.Close(); _ = os.Remove(errPath) }()

	reader, err := os.Open(outPath)
	if err != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	defer reader.Close()

	errReader, err := os.Open(errPath)
	if err != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}
	defer errReader.Close()

	pid, waitCh, cleanup, err := startGuardedWithWait(opts.ExePath, cwd, EnvMapToSlice(envMap), outFile, errFile, command)
	if err != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, err)}
	}

	assigned := make(map[string]struct{}, len(names))
	for _, n := range names {
		assigned[n] = struct{}{}
	}

	stdoutStr, stderrStr, timedOut, procStatus, monErr := monitorGuardedProcess(
		label, pkg, assigned, false, pid, waitCh,
		outFile, errFile, reader, errReader,
		opts.ShardTimeout, progress, opts.Now, opts.PollInterval,
	)
	cleanup()
	if monErr != nil {
		return nil, &ShardError{Message: fmt.Sprintf("%s cannot start: %v", label, monErr)}
	}

	var events []TestEvent
	for _, line := range strings.Split(stdoutStr, "\n") {
		if line == "" {
			continue
		}
		var ev TestEvent
		if uerr := json.Unmarshal([]byte(line), &ev); uerr != nil {
			if !timedOut {
				return nil, &ShardError{Message: fmt.Sprintf("%s invalid test report: %v", label, uerr), Events: events}
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
				active[ev.Test] = struct{}{}
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
		return nil, &ShardError{
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
		return nil, &ShardError{
			Message: buildFailureMessage(label, procStatus, events, stderrStr),
			Events:  events,
		}
	}

	completed := make(map[string]struct{})
	for _, ev := range events {
		if ev.Test != "" && (ev.Action == goTestActionPass || ev.Action == goTestActionSkip) {
			completed[ev.Test] = struct{}{}
		}
	}
	var missing []string
	for _, n := range names {
		if _, ok := completed[n]; !ok {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return nil, &ShardError{
			Message: fmt.Sprintf("%s missing completion: %s", label, strings.Join(missing, ", ")),
			Events:  events,
		}
	}
	return events, nil
}

// Run executes the uncached or cached sharded test runner.
func Run(module string, count int, durations map[string]map[string]float64, scratch, timeout string, opts RunOptions) ([]TestEvent, error) {
	opts = opts.normalized()
	if opts.Cache {
		return RunCached(module, count, durations, scratch, timeout, opts)
	}
	return RunUncached(module, count, durations, scratch, timeout, opts)
}

type evidencePackage struct {
	ImportPath string `json:"import_path"`
	TestCache  string `json:"test_cache"`
}

// RunUncached compiles each test package once, discovers tests, and runs shards concurrently.
func RunUncached(module string, count int, durations map[string]map[string]float64, scratch, timeout string, opts RunOptions) ([]TestEvent, error) {
	opts = opts.normalized()
	var progress *ProgressTracker
	if opts.ProgressFile != "" {
		if math.IsNaN(opts.ProgressInterval) || math.IsInf(opts.ProgressInterval, 0) || opts.ProgressInterval <= 0 {
			return nil, fmt.Errorf("progress interval must be a positive finite number of seconds")
		}
		progress = NewProgressTracker(opts.ProgressFile, opts.ProgressInterval, opts.Now)
		progress.onMaybePublish = opts.onMaybePublish
	}

	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = module
	out, listErr := cmd.Output()
	if listErr != nil && opts.EvidenceOut == "" {
		return nil, listErr
	}

	packages, parseErr := parseGoListStream(out, listErr != nil)
	if listErr == nil && parseErr != nil {
		return nil, parseErr
	}

	if opts.EvidenceOut != "" {
		inventory := make([]evidencePackage, 0, len(packages))
		for _, p := range packages {
			if p.ImportPath == "" {
				continue
			}
			status := "not-applicable"
			if listErr != nil {
				status = "not-started"
			} else if len(p.TestGoFiles) > 0 || len(p.XTestGoFiles) > 0 {
				status = "bypassed"
			}
			inventory = append(inventory, evidencePackage{
				ImportPath: p.ImportPath,
				TestCache:  status,
			})
		}
		if listErr != nil {
			writeEvidenceSafe(opts.EvidenceOut, map[string]any{
				"discovery": "partial",
				"packages":  inventory,
			})
		} else {
			writeEvidenceSafe(opts.EvidenceOut, inventory)
		}
	}
	if listErr != nil {
		return nil, listErr
	}

	gitValues, err := GlobalGitConfig(nil)
	if err != nil {
		return nil, err
	}

	type shardJob struct {
		pkg     string
		dir     string
		binary  string
		names   []string
		index   int
		scratch string
	}
	var testNumbers []int
	for number, pkg := range packages {
		if len(pkg.TestGoFiles) == 0 && len(pkg.XTestGoFiles) == 0 {
			continue
		}
		testNumbers = append(testNumbers, number)
	}

	pkgJobs := make([][]shardJob, len(packages))
	pkgErrs := make([]error, len(packages))
	if len(testNumbers) > 0 {
		buildWorkers := count
		if buildWorkers < 4 {
			buildWorkers = 4
		}
		if buildWorkers > 8 {
			buildWorkers = 8
		}
		if buildWorkers > len(testNumbers) {
			buildWorkers = len(testNumbers)
		}
		buildCh := make(chan int, len(testNumbers))
		for _, number := range testNumbers {
			buildCh <- number
		}
		close(buildCh)

		var buildWG sync.WaitGroup
		for w := 0; w < buildWorkers; w++ {
			buildWG.Add(1)
			go func() {
				defer buildWG.Done()
				for number := range buildCh {
					pkg := packages[number]
					binary := filepath.Join(scratch, fmt.Sprintf("package-%d.test", number))
					buildCmd := exec.Command("go", "test", "-c", "-o", binary, pkg.ImportPath)
					buildCmd.Dir = module
					if bOut, bErr := buildCmd.CombinedOutput(); bErr != nil {
						pkgErrs[number] = fmt.Errorf("go test -c %s: %w: %s", pkg.ImportPath, bErr, strings.TrimSpace(string(bOut)))
						continue
					}
					discScratch := filepath.Join(scratch, fmt.Sprintf("package-%d-discovery", number))
					discEnv, err := PrivateEnvironment(pkg.ImportPath+" discovery", discScratch, gitValues)
					if err != nil {
						pkgErrs[number] = err
						continue
					}
					listCmd := exec.Command(binary, "-test.list=.", "-test.timeout=0")
					listCmd.Dir = pkg.Dir
					listCmd.Env = EnvMapToSlice(discEnv)
					listedOut, err := listCmd.Output()
					if err != nil {
						pkgErrs[number] = err
						continue
					}
					var names []string
					for _, line := range strings.Split(string(listedOut), "\n") {
						line = strings.TrimRight(line, "\r")
						if testListNameRe.MatchString(line) {
							names = append(names, line)
						}
					}
					if progress != nil {
						progress.RecordPackage("discovered " + pkg.ImportPath)
					}
					groups := Assign(names, durations[pkg.ImportPath], count)
					var localJobs []shardJob
					for idx, group := range groups {
						if len(group) > 0 || (len(names) == 0 && idx == 0) {
							localJobs = append(localJobs, shardJob{
								pkg:     pkg.ImportPath,
								dir:     pkg.Dir,
								binary:  binary,
								names:   group,
								index:   idx,
								scratch: filepath.Join(scratch, fmt.Sprintf("package-%d-shard-%d", number, idx)),
							})
						}
					}
					pkgJobs[number] = localJobs
				}
			}()
		}
		buildWG.Wait()
	}

	var jobs []shardJob
	for _, number := range testNumbers {
		if pkgErrs[number] != nil {
			return nil, pkgErrs[number]
		}
		jobs = append(jobs, pkgJobs[number]...)
	}

	type jobResult struct {
		jobIndex int
		events   []TestEvent
		err      error
	}
	resCh := make(chan jobResult, len(jobs))
	var wg sync.WaitGroup
	runShard := RunShard
	if opts.runShardFn != nil {
		runShard = opts.runShardFn
	}
	for i, job := range jobs {
		wg.Add(1)
		go func(idx int, j shardJob) {
			defer wg.Done()
			evs, rerr := runShard(j.pkg, j.dir, j.binary, j.names, j.index, j.scratch, timeout, gitValues, opts, progress)
			resCh <- jobResult{jobIndex: idx, events: evs, err: rerr}
		}(i, job)
	}
	go func() {
		wg.Wait()
		close(resCh)
	}()

	var events []TestEvent
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
				shardErrors = append(shardErrors, indexedErr{index: res.jobIndex, text: se.Message})
				if progress != nil && !se.TimedOut {
					j := jobs[res.jobIndex]
					progress.RecordShard(fmt.Sprintf("%s shard %d", j.pkg, j.index))
				}
			} else {
				shardErrors = append(shardErrors, indexedErr{index: res.jobIndex, text: res.err.Error()})
			}
		} else {
			events = append(events, res.events...)
			if progress != nil {
				j := jobs[res.jobIndex]
				progress.RecordShard(fmt.Sprintf("%s shard %d", j.pkg, j.index))
			}
		}
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
