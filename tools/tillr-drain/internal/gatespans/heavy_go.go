package gatespans

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr/checkresult"
)

const (
	GoTestAbsent  = "test not present at this commit"
	GoTestSkipped = "test or enclosing test skipped at this commit"
)

var goTestName = regexp.MustCompile(`^(Test|Example|Fuzz)[\pL\pN_]*(/[^[:space:]]*)*$`)
var goToolModule = regexp.MustCompile(`^tools/[a-z0-9-]+$`)
var goImport = regexp.MustCompile(`^[a-zA-Z0-9._~-]+(/[a-zA-Z0-9._~-]+)*$`)
var goBuildFailLineRe = regexp.MustCompile(`^FAIL\t\S+\s+\[(?:build|setup) failed\]`)

func validGoModuleDir(mod string) bool {
	if mod == "." {
		return true
	}
	return mod != "" && !strings.Contains(mod, ":") && filepath.IsLocal(mod) && filepath.Clean(mod) == mod
}

// GoSuiteProofID is the legacy declaration id for the suite that emitted a
// named test. A passing suite is positive evidence even though it does not
// enumerate tests. A red or unavailable suite provides no such evidence.
func GoSuiteProofID(id string) string {
	test, ok := strings.CutPrefix(id, GoTestPrefix)
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

func GoSuiteFailures(label string, evidence LegacyFailure, suite HeavyCheck) []HeavyCheck {
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
		check.CheckID = GoTestPrefix + module + ":" + test.Package + ":" + test.Name
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

// JudgeHeavyGo replays a single package-qualified case, including in the required
// gate of a previously reverted item. Its ID is self-contained across restarts.
func JudgeHeavyGo(ctx context.Context, cfg HeavyJudgeConfig, dir, id string) ([]HeavyCheck, error) {
	parts := strings.SplitN(strings.TrimPrefix(id, GoTestPrefix), ":", 3)
	if len(parts) != 3 || !validGoModuleDir(parts[0]) || !goImport.MatchString(parts[1]) || !goTestName.MatchString(parts[2]) {
		return nil, drainmodel.Externalf("invalid Go test selector %q", id)
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
			return nil, drainmodel.Externalf("Go package %q is not in module %q", pkg, modulePath)
		}
		rel = "./" + suffix
	}
	if _, err := os.Stat(filepath.Join(root, rel)); os.IsNotExist(err) {
		classified := drainmodel.ClassifyCheck(checkresult.Observation{Check: id, Source: checkresult.FocusedGoTest, PackageAbsent: true})
		switch classified.Outcome {
		case checkresult.Pass:
			return []HeavyCheck{{CheckID: id, Result: "pass", Detail: classified.Detail}}, nil
		case checkresult.Fail:
			return []HeavyCheck{{CheckID: id, Result: "fail", Detail: classified.Detail}}, nil
		default:
			return nil, drainmodel.Externalf("focused Go test %s gave no verdict: %s", name, classified.Detail)
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
	parent := cfg.LaneRoot
	if parent == "" {
		parent = filepath.Dir(dir)
	}
	scratch, err := os.MkdirTemp(parent, "go-replay-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	config := filepath.Join(scratch, "gitconfig")
	if err := PrivateGoReplayGitConfig(ctx, root, config); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-run", strings.Join(patterns, "/"), rel)
	cmd.Dir = root
	cmd.Env = cfg.env(os.Environ(), "CGO_ENABLED=0", "TMPDIR="+scratch, "YOLO_DURABLE_DIR="+scratch, "GIT_CONFIG_GLOBAL="+config)
	if err := cfg.applyPriority(ctx, cmd); err != nil {
		return nil, err
	}
	var stdout strings.Builder
	var stderr drainmodel.StderrTail
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := RunOwnedHeavy(ctx, cmd, "declaration")
	if CleanupLost(runErr) {
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
	classified := drainmodel.ClassifyCheck(checkresult.Observation{
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
		if cfg.LaneRoot != "" && ctx.Value(HeavyInverseKey{}) == nil {
			logPath := LegacyLogPath(ctx, cfg.LaneRoot, dir, id)
			if logPath != "" && os.WriteFile(logPath, []byte(check.Detail), 0644) == nil {
				check.LogPath = logPath
			}
		}
		return []HeavyCheck{check}, nil
	default:
		return nil, drainmodel.Externalf("focused Go test %s gave no verdict: %v: %s%s", name, runErr, stderr.String(), stdout.String())
	}
}

// PrivateGoReplayGitConfig flattens global includes so their values are visible
// but their files stay untouched.
func PrivateGoReplayGitConfig(ctx context.Context, dir, dest string) error {
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

// ExtractNamedGoFailingTests parses a failing suite check's log and resolves every
// failing Go test into a canonical `go-test:<module>:<pkg>:<TestName>` selector.
// It returns nil whenever any failure in the log cannot be named (build/setup failure,
// panic outside a test, unnamed shard failure, non-Go runner, or unresolvable module)
// so the next proof runs the whole check.
func ExtractNamedGoFailingTests(dir string, decl HeavyDeclaration, check HeavyCheck) []string {
	logRef := check.LogPath
	if logRef == "" {
		logRef = check.LogRef
	}
	if logRef == "" {
		return nil
	}
	if logHasUnnamedGoFailure(logRef) {
		return nil
	}
	goRes := runhistory.ReadGoTestFailures(logRef)
	if len(goRes.Failures) == 0 {
		return nil
	}
	modules := candidateGoModules(dir, decl)
	if len(modules) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(goRes.Failures))
	out := make([]string, 0, len(goRes.Failures))
	for _, gf := range goRes.Failures {
		if gf.Package == "" || gf.Test == "" || !goImport.MatchString(gf.Package) || !goTestName.MatchString(gf.Test) {
			return nil
		}
		mod := resolveGoModuleForPackage(dir, modules, gf.Package)
		if mod == "" {
			return nil
		}
		selector := GoTestPrefix + mod + ":" + gf.Package + ":" + gf.Test
		if _, ok := seen[selector]; !ok {
			seen[selector] = struct{}{}
			out = append(out, selector)
		}
	}
	sort.Strings(out)
	return out
}

func openCheckLogFile(logRef string) (*os.File, error) {
	whole := strings.TrimSuffix(logRef, ".triage.log")
	if whole != logRef {
		whole += ".log"
	}
	f, err := os.Open(whole)
	if err != nil && whole != logRef {
		f, err = os.Open(logRef)
	}
	return f, err
}

func logHasUnnamedGoFailure(logRef string) bool {
	f, err := openCheckLogFile(logRef)
	if err != nil {
		return true
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), runhistory.TriageScanMax)
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if goBuildFailLineRe.MatchString(trimmed) {
			return true
		}
		if shardLine, shardNames := runhistory.ParseShardFailureLine(trimmed); shardLine != "" && len(shardNames) == 0 {
			return true
		}
	}
	return scanner.Err() != nil
}

type goModuleCandidate struct {
	dir        string
	modulePath string
}

func candidateGoModules(dir string, decl HeavyDeclaration) []goModuleCandidate {
	seen := map[string]bool{}
	var dirs []string
	add := func(cand string) {
		cand = strings.TrimSpace(strings.Trim(cand, `"'`))
		cand = strings.TrimSuffix(cand, "/")
		if cand == "" {
			return
		}
		cand = filepath.Clean(cand)
		if !validGoModuleDir(cand) || seen[cand] {
			return
		}
		seen[cand] = true
		dirs = append(dirs, cand)
	}
	for _, suffix := range []string{"'s whole Go suite passes", "'s whole suite passes"} {
		if mod, ok := strings.CutSuffix(decl.Label, suffix); ok {
			add(mod)
		}
	}
	cmdText := strings.Join(decl.Command, " ")
	fields := strings.Fields(cmdText)
	for i := 0; i < len(fields); i++ {
		tok := fields[i]
		if (tok == "shards" || tok == "cd") && i+1 < len(fields) {
			add(fields[i+1])
		}
		if tok == "-C" && i+1 < len(fields) && i >= 2 && fields[i-2] == "go" && fields[i-1] == "test" {
			add(fields[i+1])
		}
	}
	add(".")
	var out []goModuleCandidate
	for _, d := range dirs {
		modPath := readGoModPath(filepath.Join(dir, d, "go.mod"))
		if modPath != "" {
			out = append(out, goModuleCandidate{dir: d, modulePath: modPath})
		}
	}
	return out
}

func readGoModPath(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], `"`)
		}
	}
	return ""
}

func resolveGoModuleForPackage(dir string, candidates []goModuleCandidate, pkg string) string {
	for _, c := range candidates {
		if pkg == c.modulePath {
			if info, err := os.Stat(filepath.Join(dir, c.dir)); err == nil && info.IsDir() {
				return c.dir
			}
			continue
		}
		if suffix, ok := strings.CutPrefix(pkg, c.modulePath+"/"); ok && filepath.IsLocal(suffix) && filepath.Clean(suffix) == suffix {
			if info, err := os.Stat(filepath.Join(dir, c.dir, suffix)); err == nil && info.IsDir() {
				return c.dir
			}
		}
	}
	return ""
}

func formatFocusedGoFailureLog(ctx context.Context, laneRoot, dir, checkID string, failed []HeavyCheck) (string, string) {
	var body strings.Builder
	var names []string
	for _, fc := range failed {
		parts := strings.SplitN(strings.TrimPrefix(fc.CheckID, GoTestPrefix), ":", 3)
		pkg, name := "", fc.CheckID
		if len(parts) == 3 {
			pkg, name = parts[1], parts[2]
		}
		names = append(names, name)
		fmt.Fprintf(&body, "--- FAIL: %s\n", name)
		for _, line := range strings.Split(fc.Detail, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var ev struct {
				Action string `json:"Action"`
				Output string `json:"Output"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.Action != "" {
				if ev.Action == "output" && ev.Output != "" {
					body.WriteString(ev.Output)
				}
				continue
			}
			body.WriteString(line)
			body.WriteByte('\n')
		}
		if pkg != "" {
			fmt.Fprintf(&body, "FAIL\t%s\t(focused rerun)\n", pkg)
		}
	}
	detail := "focused rerun failed: " + strings.Join(names, ", ")
	logPath := ""
	if laneRoot != "" {
		if rev, err := HeavyGitOutput(ctx, dir, "rev-parse", "HEAD"); err == nil && rev != "" {
			logs := filepath.Join(laneRoot, ".rolling", "logs", rev)
			if os.MkdirAll(logs, 0o755) == nil {
				safe := strings.Map(func(r rune) rune {
					if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
						return r
					}
					return '_'
				}, checkID)
				candidate := filepath.Join(logs, "focused-"+safe+".log")
				if os.WriteFile(candidate, []byte(body.String()), 0o644) == nil {
					logPath = candidate
				}
			}
		}
	}
	return logPath, detail
}
