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

	"github.com/mschulkind-oss/tillr/checkresult"
)

const (
	goTestAbsent  = "test not present at this commit"
	goTestSkipped = "test or enclosing test skipped at this commit"
)

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
	// Match the judging program's declaration id for the go-tool suite label.
	slug := strings.Join(strings.FieldsFunc(module, func(r rune) bool { return r == '/' || r == '-' }), "-")
	return "go-tool/" + slug + "-s-whole-suite-passes"
}

func goSuiteFailures(label string, evidence legacyFailure, suite HeavyCheck) []HeavyCheck {
	module, ok := strings.CutSuffix(label, "'s whole suite passes")
	if !ok || !goToolModule.MatchString(module) {
		return []HeavyCheck{suite}
	}
	var checks []HeavyCheck
	hung := false
	unscoped := evidence.Log != ""
	for _, test := range evidence.Cases {
		if !goImport.MatchString(test.Package) || !goTestName.MatchString(test.Name) || test.Log == "" {
			unscoped = true
			continue
		}
		check := suite
		check.CheckID = goTestPrefix + module + ":" + test.Package + ":" + test.Name
		// A fixture may read outside its module (such as bin/pi-forks).
		// No declared read set exists at individual test granularity.
		check.Reads = nil
		check.LogPath, check.LogRef = test.Log, test.Log
		if test.TimeoutPanic != "" {
			check.GoTimedOut = true
			check.Detail = test.TimeoutPanic + "; not replayed; fixer filed against the tip"
			hung = true
		}
		checks = append(checks, check)
	}
	// Unnamed failures (e.g. build errors, crashes and timeouts) still
	// belong to the suite. They must not erase any named cases it reported.
	if unscoped || len(checks) == 0 {
		if hung {
			// Replaying the suite would also rerun its named hung test.
			suite.GoTimedOut = true
			suite.Detail = "unnamed failure; not replaying a suite with a named hung Go test"
		}
		checks = append(checks, suite)
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
		classified := classifyCheck(checkresult.Observation{Check: id, Source: checkresult.FocusedGoTest, PackageAbsent: true})
		switch classified.Outcome {
		case checkresult.Pass:
			return []HeavyCheck{{CheckID: id, Result: "pass", Detail: classified.Detail}}, nil
		case checkresult.Fail:
			return []HeavyCheck{{CheckID: id, Result: "fail", Detail: classified.Detail}}, nil
		default:
			return nil, Externalf("focused Go test %s gave no verdict: %s", name, classified.Detail)
		}
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
	var exitPtr *int
	if cmd.ProcessState != nil {
		v := cmd.ProcessState.ExitCode()
		exitPtr = &v
	}
	processErr := cmd.ProcessState == nil || (exitPtr != nil && *exitPtr == 0 && runErr != nil)
	classified := classifyCheck(checkresult.Observation{
		Check:        id,
		Source:       checkresult.FocusedGoTest,
		ExitCode:     exitPtr,
		ProcessError: processErr,
		Passed:       passed,
		Failed:       failed,
		Skipped:      skipped,
	})
	switch classified.Outcome {
	case checkresult.Pass:
		detail := classified.Detail
		if detail == checkresult.DetailPassed {
			detail = ""
		}
		return []HeavyCheck{{CheckID: id, Result: "pass", Detail: detail}}, nil
	case checkresult.Fail:
		check := HeavyCheck{CheckID: id, Result: "fail", Detail: stdout.String() + stderr.String()}
		if s.LaneRoot != "" && ctx.Value(heavyInverseKey{}) == nil {
			logPath := legacyLogPath(ctx, s.LaneRoot, dir, id)
			if logPath != "" && os.WriteFile(logPath, []byte(check.Detail), 0644) == nil {
				check.LogPath = logPath
			}
		}
		return []HeavyCheck{check}, nil
	default:
		return nil, Externalf("focused Go test %s gave no verdict: %v: %s%s", name, runErr, stderr.String(), stdout.String())
	}
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
