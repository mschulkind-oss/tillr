package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	goTestPrefix  = "go-test:"
	goTestAbsent  = "test not present at this commit"
	goTestSkipped = "test or enclosing test skipped at this commit"
)

// The shard runner reports package-qualified leaf failures. Use its shard headers,
// not --- FAIL lines (which repeat a failing subtest's parents and omit the package).
var goShardFailure = regexp.MustCompile(`^([^ ]+) shard [0-9]+ failed \(exit 1\): (.+)$`)
var goTestName = regexp.MustCompile(`^(Test|Example|Fuzz)[\pL\pN_]*(/[^[:space:]]*)*$`)
var goToolModule = regexp.MustCompile(`^tools/[a-z0-9-]+$`)
var goImport = regexp.MustCompile(`^[a-zA-Z0-9._~-]+(/[a-zA-Z0-9._~-]+)*$`)

// goSuiteProofID is the legacy declaration id for the suite that emitted a
// named test. A passing suite is positive evidence even though it does not
// enumerate tests. A red or unavailable suite provides no such evidence.
func goSuiteProofID(id string) string {
	test, ok := strings.CutPrefix(id, goTestPrefix)
	if !ok {
		return ""
	}
	module, _, ok := strings.Cut(test, ":")
	if !ok || !goToolModule.MatchString(module) {
		return ""
	}
	// Match fork-check.py's declaration_id for the go-tool suite label.
	slug := strings.Join(strings.FieldsFunc(module, func(r rune) bool { return r == '/' || r == '-' }), "-")
	return "go-tool/" + slug + "-s-whole-suite-passes"
}

func goSuiteFailures(label string, headers []string, suite HeavyCheck) []HeavyCheck {
	module, ok := strings.CutSuffix(label, "'s whole suite passes")
	if !ok || !goToolModule.MatchString(module) {
		return []HeavyCheck{suite}
	}
	var checks []HeavyCheck
	seen := map[string]bool{}
	for _, line := range headers {
		header, ok := strings.CutPrefix(line, "go-test-shards: ")
		if !ok || strings.Contains(header, "waiting for a test slot") {
			continue
		}
		match := goShardFailure.FindStringSubmatch(header)
		if match == nil || !goImport.MatchString(match[1]) {
			// A timeout, build error or crash without named failures is not
			// evidence about an individual test. Keep the whole check's verdict.
			return []HeavyCheck{suite}
		}
		for _, name := range strings.Split(match[2], ", ") {
			if !goTestName.MatchString(name) {
				return []HeavyCheck{suite}
			}
			id := goTestPrefix + module + ":" + match[1] + ":" + name
			if !seen[id] {
				seen[id] = true
				check := suite
				check.CheckID = id
				// A fixture may read outside its module (such as bin/pi-forks).
				// No declared read set exists at individual test granularity.
				check.Reads = nil
				checks = append(checks, check)
			}
		}
	}
	if len(checks) == 0 {
		return []HeavyCheck{suite}
	}
	return checks
}

// judgeHeavyGo replays a single package-qualified case, including in the required
// gate of a previously reverted item. Its ID is self-contained across restarts.
func (s *ProcessSteps) judgeHeavyGo(ctx context.Context, dir, id string) ([]HeavyCheck, error) {
	parts := strings.SplitN(strings.TrimPrefix(id, goTestPrefix), ":", 3)
	if len(parts) != 3 || !goToolModule.MatchString(parts[0]) || !goImport.MatchString(parts[1]) || !goTestName.MatchString(parts[2]) {
		return nil, Externalf("invalid Go test selector %q", id)
	}
	module, pkg, name := parts[0], parts[1], parts[2]
	root := filepath.Join(dir, module)
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	var modulePath string
	for _, line := range strings.Split(string(mod), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "module" {
			modulePath = strings.Trim(fields[1], `"`)
			break
		}
	}
	rel := "."
	if pkg != modulePath {
		suffix, ok := strings.CutPrefix(pkg, modulePath+"/")
		if modulePath == "" || !ok || !filepath.IsLocal(suffix) || filepath.Clean(suffix) != suffix || suffix == "." {
			return nil, Externalf("Go package %q is not in module %q", pkg, modulePath)
		}
		rel = "./" + suffix
	}
	if _, err := os.Stat(filepath.Join(root, rel)); os.IsNotExist(err) {
		return []HeavyCheck{{CheckID: id, Result: "pass", Detail: "package not present at this commit"}}, nil
	} else if err != nil {
		return nil, err
	}
	patterns := strings.Split(name, "/")
	for i, part := range patterns {
		patterns[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	// Like a proof shard, isolate the test and TestMain's scratch and global Git
	// config. A historical fixture must not be able to change the owner's config.
	parent := s.LaneRoot
	if parent == "" {
		parent = filepath.Dir(dir)
	}
	scratch, err := os.MkdirTemp(parent, "go-replay-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	config := filepath.Join(scratch, "gitconfig")
	if err := privateGoReplayGitConfig(ctx, root, config); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-run", strings.Join(patterns, "/"), rel)
	cmd.Dir = root
	cmd.Env = s.checkProcessEnv(os.Environ(), "CGO_ENABLED=0", "TMPDIR="+scratch, "YOLO_DURABLE_DIR="+scratch, "GIT_CONFIG_GLOBAL="+config)
	var stdout strings.Builder
	var stderr stderrTail
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := runOwnedHeavy(ctx, cmd, "declaration")
	if cleanupLost(runErr) {
		return nil, runErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	failed, passed, skipped := false, false, false
	for _, line := range strings.Split(stdout.String(), "\n") {
		var event struct{ Package, Test, Action string }
		if json.Unmarshal([]byte(line), &event) != nil || event.Package != pkg {
			continue
		}
		if event.Action == "skip" && event.Test != "" && (event.Test == name || strings.HasPrefix(name, event.Test+"/")) {
			skipped = true
		}
		if event.Test == name {
			failed = failed || event.Action == "fail"
			passed = passed || event.Action == "pass"
		}
	}
	check := HeavyCheck{CheckID: id, Result: "pass"}
	switch {
	case runErr == nil && !failed:
		if !passed {
			check.Detail = goTestAbsent
			if skipped {
				check.Detail = goTestSkipped
			}
		}
	case failed && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 1:
		check.Result = "fail"
		check.Detail = stdout.String() + stderr.String()
		if s.LaneRoot != "" && ctx.Value(heavyInverseKey{}) == nil {
			logPath := legacyLogPath(ctx, s.LaneRoot, dir, id)
			if logPath != "" && os.WriteFile(logPath, []byte(check.Detail), 0644) == nil {
				check.LogPath = logPath
			}
		}
	default:
		return nil, Externalf("focused Go test %s gave no verdict: %v: %s%s", name, runErr, stderr.String(), stdout.String())
	}
	return []HeavyCheck{check}, nil
}

// Flatten global includes so their values are visible but their files stay untouched.
func privateGoReplayGitConfig(ctx context.Context, dir, dest string) error {
	if err := os.WriteFile(dest, nil, 0600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "config", "--list", "--includes", "--show-scope", "--null")
	cmd.Dir = dir
	raw, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("read Git config for focused replay: %w", err)
	}
	fields := strings.Split(string(raw), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] != "global" {
			continue
		}
		key, value, explicit := strings.Cut(fields[i+1], "\n")
		lower := strings.ToLower(key)
		if lower == "include.path" || strings.HasPrefix(lower, "includeif.") {
			continue
		}
		if !explicit {
			value = "true"
		}
		if out, err := exec.CommandContext(ctx, "git", "config", "--file", dest, "--add", key, value).CombinedOutput(); err != nil {
			return fmt.Errorf("prepare private Git config for focused replay: %w: %s", err, out)
		}
	}
	return nil
}

// additionalGoCulprits searches for a later first-red while removing the earlier
// culprits, newest inverse first. Inverses live only in the detached proof tree;
// these outcomes must never enter the ordinary, commit-keyed replay cache.
func (r HeavyRunner) additionalGoCulprits(ctx context.Context, run HeavyRun, check HeavyCheck, first string) ([]string, error) {
	if first == run.TipSHA {
		return nil, nil // The ordinary revert replay will check this inverse.
	}
	var other []string
	removed := []string{first}
	for {
		replay := func(sha, why string) (bool, error) {
			r.progressf("loop: heavy %s replaying at %s without %s (%s)\n", check.CheckID, shortSHA(sha), strings.Join(removed, ", "), why)
			red, _, err := r.oneLogged(ctx, sha, check.CheckID, removed...)
			return red, err
		}
		red, err := replay(run.TipSHA, "checking whether the known culprits explain the tip")
		if err != nil || !red {
			return other, err
		}
		base := removed[len(removed)-1]
		if base == run.TipSHA {
			return other, fmt.Errorf("test still red after removing %s", strings.Join(removed, ", "))
		}
		next, fault, reason, err := r.searchFirstRed(ctx, base, run.TipSHA, check.Reads, replay)
		if err != nil {
			return other, err
		}
		if fault {
			return other, fmt.Errorf("cannot explain residual red after removing %s: %s", strings.Join(removed, ", "), reason)
		}
		other = append(other, next)
		removed = append(removed, next)
	}
}
