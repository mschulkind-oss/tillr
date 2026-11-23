package drain

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/checkresult"
)

// replayChildEnv builds the environment for every child process ProcessSteps.Replay
// spawns: it identifies the candidate run and item for ps, omits TILLR_LANE_ID (the
// lane is released and replay runs in .replay/<rev>) and TILLR_LOOP_STORE (so replay
// is never mistaken for a surviving worker process), forces GO_TEST_SHARDS_CLASS=ordinary
// so base replays queue behind trunk proof, and disables background git gc and
// maintenance on historical checkouts.
func (s *ProcessSteps) replayChildEnv(candidate Candidate, extra ...string) []string {
	env := withoutProcessEnv(
		s.checkProcessEnv(os.Environ()),
		"TILLR_RUN_ID",
		"TILLR_ITEM_ID",
		"TILLR_LANE_ID",
		"TILLR_LOOP_STORE",
		"GO_TEST_SHARDS_CLASS",
	)
	env = append(env, "GO_TEST_SHARDS_CLASS=ordinary")
	if candidate.RunID != "" {
		env = append(env, "TILLR_RUN_ID="+candidate.RunID)
	}
	if candidate.ItemID != "" {
		env = append(env, "TILLR_ITEM_ID="+candidate.ItemID)
	}
	env = appendGitConfigEnv(env,
		[2]string{"gc.auto", "0"},
		[2]string{"gc.autoDetach", "false"},
		[2]string{"maintenance.auto", "false"},
		[2]string{"maintenance.autoDetach", "false"},
	)
	return append(env, extra...)
}

func appendGitConfigEnv(env []string, pairs ...[2]string) []string {
	count := 0
	out := make([]string, 0, len(env)+len(pairs)*2+1)
	for _, kv := range env {
		if raw, ok := strings.CutPrefix(kv, "GIT_CONFIG_COUNT="); ok {
			if n, err := strconv.Atoi(raw); err == nil && n > 0 {
				count = n
			}
			continue
		}
		out = append(out, kv)
	}
	for _, pair := range pairs {
		out = append(out,
			"GIT_CONFIG_KEY_"+strconv.Itoa(count)+"="+pair[0],
			"GIT_CONFIG_VALUE_"+strconv.Itoa(count)+"="+pair[1],
		)
		count++
	}
	out = append(out, "GIT_CONFIG_COUNT="+strconv.Itoa(count))
	return out
}

// readGoAffectedFailingTests parses the `go-affected: failing packages and tests:`
// summary from the candidate run's gate logs. It returns (pkg -> tests, true) only
// when every listed failing package names concrete Go tests; any unnamed package or
// compile failure returns (nil, false) so the caller falls back to go_affected.py.
func readGoAffectedFailingTests(runDir string) (map[string][]string, bool) {
	if runDir == "" {
		return nil, false
	}
	logsDir := filepath.Join(runDir, "logs")
	var files []string
	for _, pattern := range []string{
		"fork-check-*-go-affected-affected-tillr-go-test-packages-pass.log",
		"*gate*.triage.log",
		"*gate*.log",
	} {
		matches, _ := filepath.Glob(filepath.Join(logsDir, pattern))
		sort.Strings(matches)
		for _, m := range matches {
			if pattern == "*gate*.log" && strings.HasSuffix(m, ".triage.log") {
				continue
			}
			files = append(files, m)
		}
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pkgs, found, concrete := parseGoAffectedSummary(string(raw))
		if found {
			return pkgs, concrete
		}
	}
	return nil, false
}

func parseGoAffectedSummary(content string) (map[string][]string, bool, bool) {
	const header = "go-affected: failing packages and tests:"
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := stripForkCheckPipe(lines[i])
		if strings.TrimSpace(line) != header {
			continue
		}
		pkgs := map[string][]string{}
		for j := i + 1; j < len(lines); j++ {
			entry := stripForkCheckPipe(lines[j])
			if !strings.HasPrefix(entry, "  ") {
				break
			}
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" || strings.HasPrefix(trimmed, "---") {
				break
			}
			pkg, rest, ok := strings.Cut(trimmed, ":")
			if !ok {
				return nil, true, false
			}
			pkg = strings.TrimSpace(pkg)
			rest = strings.TrimSpace(rest)
			if !goImport.MatchString(pkg) || rest == "" {
				return nil, true, false
			}
			if strings.Contains(rest, "(no failing test named)") || strings.Contains(rest, "(package failure with no named test)") {
				return nil, true, false
			}
			seen := map[string]bool{}
			for _, t := range pkgs[pkg] {
				seen[t] = true
			}
			for _, part := range strings.Split(rest, ",") {
				name := strings.TrimSpace(part)
				if !goTestName.MatchString(name) {
					return nil, true, false
				}
				if !seen[name] {
					seen[name] = true
					pkgs[pkg] = append(pkgs[pkg], name)
				}
			}
		}
		if len(pkgs) == 0 {
			return nil, true, false
		}
		return pkgs, true, true
	}
	return nil, false, false
}

func stripForkCheckPipe(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if rest, ok := strings.CutPrefix(trimmed, "| "); ok {
		return rest
	}
	if trimmed == "|" {
		return ""
	}
	return line
}

type goModuleInfo struct {
	dir        string
	modulePath string
}

func indexReplayGoModules(dir string) []goModuleInfo {
	var modFiles []string
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		modFiles = append(modFiles, filepath.Join(dir, "go.mod"))
	}
	toolMods, _ := filepath.Glob(filepath.Join(dir, "tools", "*", "go.mod"))
	sort.Strings(toolMods)
	modFiles = append(modFiles, toolMods...)

	var modules []goModuleInfo
	for _, modFile := range modFiles {
		raw, err := os.ReadFile(modFile)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "module" {
				modules = append(modules, goModuleInfo{
					dir:        filepath.Dir(modFile),
					modulePath: strings.Trim(fields[1], `"`),
				})
				break
			}
		}
	}
	// Longest modulePath first so nested modules match before outer ones.
	sort.Slice(modules, func(i, j int) bool {
		return len(modules[i].modulePath) > len(modules[j].modulePath)
	})
	return modules
}

func resolveReplayGoPackage(modules []goModuleInfo, pkg string) (modDir, rel string, ok bool) {
	for _, m := range modules {
		if pkg == m.modulePath {
			return m.dir, ".", true
		}
		if suffix, matched := strings.CutPrefix(pkg, m.modulePath+"/"); matched && filepath.IsLocal(suffix) && filepath.Clean(suffix) == suffix && suffix != "." {
			return m.dir, "./" + suffix, true
		}
	}
	return "", "", false
}

// replayGoAffected runs a scoped replay of go-affected on rev inside dir:
//   - If the candidate's gate log names concrete failing Go tests for every failing
//     package, it runs only those named tests via `go test -json -count=1 -run ...`.
//   - Otherwise, if candidate changed paths are available and tools/gate/go_affected.py
//     exists in dir, it runs `python3 tools/gate/go_affected.py --changed <paths...>`.
//   - Otherwise, handled is false so Replay falls back to DefaultReplayProgram.
func (s *ProcessSteps) replayGoAffected(ctx context.Context, dir string, candidate Candidate, rev, check string) (handled, red, known bool, output string, err error) {
	if s.Root != "" && candidate.RunID != "" {
		if pkgs, ok := readGoAffectedFailingTests(RunDir(s.Root, candidate.RunID)); ok {
			red, output, err := s.runNamedGoTestsInReplay(ctx, dir, candidate, rev, pkgs)
			if err != nil {
				return true, false, false, output, err
			}
			return true, red, true, output, nil
		}
	}
	changed := candidate.CandidateChangedPaths
	if len(changed) == 0 && s.Root != "" && candidate.BaseSHA != "" && candidate.HeadSHA != "" {
		changed = s.diffChangedPaths(ctx, candidate.BaseSHA, candidate.HeadSHA)
	}
	goAffectedScript := filepath.Join(dir, "tools", "gate", "go_affected.py")
	if len(changed) == 0 {
		return false, false, false, "", nil
	}
	if _, statErr := os.Stat(goAffectedScript); statErr != nil {
		return false, false, false, "", nil
	}
	args := append([]string{"tools/gate/go_affected.py", "--changed"}, changed...)
	cmd := exec.CommandContext(ctx, "python3", args...)
	cmd.Dir = dir
	cmd.Env = s.replayChildEnv(candidate, "TILLR_FORK_DRIFT=1")
	var stderr stderrTail
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = io.MultiWriter(&combined, &stderr)
	runErr := runCheckGroup(cmd)
	if ctx.Err() != nil {
		return true, false, false, combined.String(), ctx.Err()
	}
	if cmd.ProcessState == nil {
		return true, false, false, combined.String(), Externalf("replaying %s on %s: %v: %s",
			check, shortSHA(rev), runErr, stderr.String())
	}
	code := cmd.ProcessState.ExitCode()
	classified := classifyCheck(checkresult.Observation{
		Check:    check,
		Commit:   rev,
		Worktree: dir,
		Source:   checkresult.BinaryProgram,
		ExitCode: &code,
	})
	switch classified.Outcome {
	case checkresult.Pass:
		return true, false, true, combined.String(), nil
	case checkresult.Fail:
		return true, true, true, combined.String(), nil
	default:
		return true, false, false, combined.String(), nil
	}
}

func (s *ProcessSteps) diffChangedPaths(ctx context.Context, baseSHA, headSHA string) []string {
	cmd := exec.CommandContext(ctx, "git", "-C", s.Root, "diff", "--name-only", baseSHA, headSHA)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

func (s *ProcessSteps) runNamedGoTestsInReplay(ctx context.Context, dir string, candidate Candidate, rev string, pkgs map[string][]string) (bool, string, error) {
	modules := indexReplayGoModules(dir)
	pkgNames := make([]string, 0, len(pkgs))
	for pkg := range pkgs {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	parent := s.LaneRoot
	if parent == "" {
		parent = filepath.Dir(dir)
	}
	scratch, err := os.MkdirTemp(parent, "go-replay-")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(scratch)

	var combined bytes.Buffer
	red := false
	for _, pkg := range pkgNames {
		modDir, rel, ok := resolveReplayGoPackage(modules, pkg)
		if !ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(modDir, rel)); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return false, combined.String(), err
		}
		config := filepath.Join(scratch, "gitconfig")
		if err := privateGoReplayGitConfig(ctx, modDir, config); err != nil {
			return false, combined.String(), err
		}
		var topLevel []string
		var subtests []string
		for _, name := range pkgs[pkg] {
			if strings.Contains(name, "/") {
				subtests = append(subtests, name)
			} else {
				topLevel = append(topLevel, name)
			}
		}
		var runPatterns []string
		if len(topLevel) > 0 {
			quoted := make([]string, len(topLevel))
			for i, name := range topLevel {
				quoted[i] = regexp.QuoteMeta(name)
			}
			runPatterns = append(runPatterns, "^(?:"+strings.Join(quoted, "|")+")$")
		}
		for _, name := range subtests {
			parts := strings.Split(name, "/")
			for i, part := range parts {
				parts[i] = "^" + regexp.QuoteMeta(part) + "$"
			}
			runPatterns = append(runPatterns, strings.Join(parts, "/"))
		}
		for _, pattern := range runPatterns {
			cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-run", pattern, rel)
			cmd.Dir = modDir
			cmd.Env = s.replayChildEnv(candidate, "CGO_ENABLED=0", "TMPDIR="+scratch, "YOLO_DURABLE_DIR="+scratch, "GIT_CONFIG_GLOBAL="+config)
			var stderr stderrTail
			cmd.Stdout = &combined
			cmd.Stderr = io.MultiWriter(&combined, &stderr)
			runErr := runCheckGroup(cmd)
			if ctx.Err() != nil {
				return false, combined.String(), ctx.Err()
			}
			if cmd.ProcessState == nil {
				return false, combined.String(), Externalf("replaying %s (%s %s) on %s: %v: %s",
					goAffectedCheckID, pkg, pattern, shortSHA(rev), runErr, stderr.String())
			}
			if cmd.ProcessState.ExitCode() != 0 {
				red = true
			}
		}
	}
	return red, combined.String(), nil
}
