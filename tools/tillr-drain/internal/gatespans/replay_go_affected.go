package gatespans

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

	"github.com/mschulkind-oss/tillr-drain/internal/boundary"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/procguard"
	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr/checkresult"
)

const GoAffectedCheckID = "go-affected/affected-tillr-go-test-packages-pass"

// ReplayConfig supplies step-runner and candidate scalar inputs for scoped Go replay.
type ReplayConfig struct {
	Root                  string
	LaneRoot              string
	SelfExe               string
	Priority              *procguard.ProcessPriority
	CheckProcessEnv       func(base []string, extra ...string) []string
	RunID                 string
	ItemID                string
	BaseSHA               string
	HeadSHA               string
	CandidateChangedPaths []string
	ReplayCheckLogs       map[string]string
}

func (cfg ReplayConfig) baseEnv() []string {
	if cfg.CheckProcessEnv != nil {
		return cfg.CheckProcessEnv(os.Environ())
	}
	return os.Environ()
}

// ReplayChildEnv builds the environment for every child process ProcessSteps.Replay
// spawns: it identifies the candidate run and item for ps, omits TILLR_LANE_ID (the
// lane is released and replay runs in .replay/<rev>) and TILLR_LOOP_STORE (so replay
// is never mistaken for a surviving worker process), and disables background git gc and
// maintenance on historical checkouts.
func ReplayChildEnv(cfg ReplayConfig, extra ...string) []string {
	env := procguard.WithoutProcessEnv(
		cfg.baseEnv(),
		"TILLR_RUN_ID",
		"TILLR_ITEM_ID",
		"TILLR_LANE_ID",
		"TILLR_LOOP_STORE",
	)
	if cfg.RunID != "" {
		env = append(env, "TILLR_RUN_ID="+cfg.RunID)
	}
	if cfg.ItemID != "" {
		env = append(env, "TILLR_ITEM_ID="+cfg.ItemID)
	}
	env = AppendGitConfigEnv(env,
		[2]string{"gc.auto", "0"},
		[2]string{"gc.autoDetach", "false"},
		[2]string{"maintenance.auto", "false"},
		[2]string{"maintenance.autoDetach", "false"},
	)
	return append(env, extra...)
}

func AppendGitConfigEnv(env []string, pairs ...[2]string) []string {
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

// IsReplayGoCheck reports whether check is a Go test check whose named failing
// cases can be replayed directly with `go test -run` instead of re-running the
// whole suite.
func IsReplayGoCheck(check string) bool {
	return check == GoAffectedCheckID ||
		strings.HasPrefix(check, "go-tool/") ||
		strings.HasPrefix(check, "go-substrate/") ||
		strings.HasPrefix(check, "gate-runner/")
}

// ReadReplayGoFailingTests extracts concrete failing Go package -> test names for
// check from the candidate's gate logs: first the check's recorded CheckLogs entry,
// then per-check and gate logs under RunDir(root, runID)/logs. It accepts
// either `go-affected: failing packages and tests:` (for go-affected) or a matching
// `fork-check: triage --` section. Any unnamed, truncated, or non-Go failure returns
// (nil, false) so the caller falls back to replaying the whole check.
func ReadReplayGoFailingTests(root, runID string, replayCheckLogs map[string]string, check string) (map[string][]string, bool) {
	var files []string
	seen := map[string]bool{}
	addFile := func(path string) {
		if path == "" {
			return
		}
		if strings.HasSuffix(path, ".triage.log") {
			whole := strings.TrimSuffix(path, ".triage.log") + ".log"
			if !seen[whole] {
				seen[whole] = true
				files = append(files, whole)
			}
		}
		if !seen[path] {
			seen[path] = true
			files = append(files, path)
		}
	}
	if replayCheckLogs != nil {
		addFile(replayCheckLogs[check])
	}
	if root != "" && runID != "" {
		logsDir := filepath.Join(drainmodel.RunDir(root, runID), "logs")
		patterns := []string{
			"fork-check-*-" + remotesync.SanitizeLogComponent(check) + ".log",
			"*gate*.triage.log",
			"*gate*.log",
		}
		for _, pattern := range patterns {
			matches, _ := filepath.Glob(filepath.Join(logsDir, pattern))
			sort.Strings(matches)
			for _, m := range matches {
				if pattern == "*gate*.log" && strings.HasSuffix(m, ".triage.log") {
					continue
				}
				addFile(m)
			}
		}
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(raw)
		if check == GoAffectedCheckID {
			if pkgs, found, concrete := ParseGoAffectedSummary(text); found {
				return pkgs, concrete
			}
		}
		if pkgs, found, concrete := ParseTriageGoTests(text, check); found {
			return pkgs, concrete
		}
	}
	return nil, false
}

// ParseTriageGoTests parses a `fork-check: triage --` block for the section matching
// check. It returns (pkgs, found=true, concrete=true) only when the matching section
// is untruncated and every listed case names a concrete Go test and Go package import
// path. Any unnamed case, truncated list, or non-Go failure returns (nil, true, false).
func ParseTriageGoTests(content, check string) (map[string][]string, bool, bool) {
	lines := strings.Split(content, "\n")
	inBlock := false
	inCheck := false
	matched := false
	pkgs := map[string][]string{}
	seenByPkg := map[string]map[string]bool{}
	for _, rawLine := range lines {
		line := stripForkCheckPipe(rawLine)
		if !inBlock {
			if strings.HasPrefix(line, "fork-check: triage -- ") {
				inBlock = true
			}
			continue
		}
		if line == "" || (line[0] != ' ' && line[0] != '\t') {
			break
		}
		text := strings.TrimSpace(line)
		if strings.HasPrefix(line, "  triage ") {
			id, label, _ := strings.Cut(strings.TrimPrefix(text, "triage "), ": ")
			if id == check || remotesync.ForkCheckDeclID(id, label, "") == check {
				inCheck = true
				matched = true
			} else {
				inCheck = false
			}
			continue
		}
		if !inCheck {
			continue
		}
		switch {
		case strings.HasPrefix(text, "no case named in the output;"):
			return nil, true, false
		case strings.Contains(text, "more failing case(s) not listed"):
			return nil, true, false
		case strings.HasPrefix(text, "case: "):
			rest := strings.TrimPrefix(text, "case: ")
			name, where := rest, ""
			if i := strings.LastIndex(rest, " -- "); i >= 0 {
				name, where = strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+4:])
			}
			if !goTestName.MatchString(name) {
				return nil, true, false
			}
			pkg := where
			if idx := strings.Index(pkg, " ("); idx >= 0 {
				pkg = strings.TrimSpace(pkg[:idx])
			}
			if !goImport.MatchString(pkg) || strings.HasSuffix(pkg, ".go") {
				return nil, true, false
			}
			if seenByPkg[pkg] == nil {
				seenByPkg[pkg] = map[string]bool{}
			}
			if !seenByPkg[pkg][name] {
				seenByPkg[pkg][name] = true
				pkgs[pkg] = append(pkgs[pkg], name)
			}
		}
	}
	if !matched {
		return nil, false, false
	}
	if len(pkgs) == 0 {
		return nil, true, false
	}
	return pkgs, true, true
}

func ParseGoAffectedSummary(content string) (map[string][]string, bool, bool) {
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
			for _, rawName := range strings.Split(rest, ",") {
				name := strings.TrimSpace(rawName)
				if name == "" || !goTestName.MatchString(name) {
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

type GoModuleInfo struct {
	Dir        string
	ModulePath string
}

func IndexReplayGoModules(dir string) []GoModuleInfo {
	var modFiles []string
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		modFiles = append(modFiles, filepath.Join(dir, "go.mod"))
	}
	toolMods, _ := filepath.Glob(filepath.Join(dir, "tools", "*", "go.mod"))
	sort.Strings(toolMods)
	modFiles = append(modFiles, toolMods...)

	var modules []GoModuleInfo
	for _, modFile := range modFiles {
		raw, err := os.ReadFile(modFile)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "module" {
				modules = append(modules, GoModuleInfo{
					Dir:        filepath.Dir(modFile),
					ModulePath: strings.Trim(fields[1], `"`),
				})
				break
			}
		}
	}
	// Longest ModulePath first so nested modules match before outer ones.
	sort.Slice(modules, func(i, j int) bool {
		return len(modules[i].ModulePath) > len(modules[j].ModulePath)
	})
	return modules
}

func ResolveReplayGoPackage(modules []GoModuleInfo, pkg string) (modDir, rel string, ok bool) {
	for _, m := range modules {
		if pkg == m.ModulePath {
			return m.Dir, ".", true
		}
		if suffix, matched := strings.CutPrefix(pkg, m.ModulePath+"/"); matched && filepath.IsLocal(suffix) && filepath.Clean(suffix) == suffix && suffix != "." {
			return m.Dir, "./" + suffix, true
		}
	}
	return "", "", false
}

// ReplayGoCheck runs a scoped replay of a Go test check on rev inside dir:
//   - If the candidate's gate log names concrete failing Go tests for every failing
//     package (via go-affected summary or fork-check triage), it runs only those
//     named tests via `go test -json -count=1 -run ...`.
//   - Otherwise, for go-affected, if candidate changed paths are available and
//     tools/tillr-gate/goaffected or tools/gate/go_affected.py exists in dir, it runs
//     go-affected --changed <paths...>.
//   - Otherwise, handled is false so Replay falls back to DefaultReplayProgram.
func ReplayGoCheck(ctx context.Context, cfg ReplayConfig, dir, rev, check string) (handled, red, known bool, output string, err error) {
	if pkgs, ok := ReadReplayGoFailingTests(cfg.Root, cfg.RunID, cfg.ReplayCheckLogs, check); ok {
		red, output, err := RunNamedGoTestsInReplay(ctx, cfg, dir, rev, check, pkgs)
		if err != nil {
			return true, false, false, output, err
		}
		return true, red, true, output, nil
	}
	if check != GoAffectedCheckID {
		return false, false, false, "", nil
	}
	changed := cfg.CandidateChangedPaths
	if len(changed) == 0 && cfg.Root != "" && cfg.BaseSHA != "" && cfg.HeadSHA != "" {
		changed = DiffChangedPaths(ctx, cfg.Root, cfg.BaseSHA, cfg.HeadSHA)
	}
	goAffectedPkg := filepath.Join(dir, "tools", "tillr-gate", "goaffected")
	goAffectedScript := filepath.Join(dir, "tools", "gate", "go_affected.py")
	if len(changed) == 0 {
		return false, false, false, "", nil
	}
	var cmd *exec.Cmd
	if info, statErr := os.Stat(goAffectedPkg); statErr == nil && info.IsDir() {
		args := append([]string{"run", "-C", filepath.Join(dir, "tools", "tillr-gate"), ".", "go-affected", "--root", dir, "--changed"}, changed...)
		cmd = exec.CommandContext(ctx, "go", args...)
	} else if _, statErr := os.Stat(goAffectedScript); statErr == nil {
		args := append([]string{"tools/gate/go_affected.py", "--changed"}, changed...)
		cmd = exec.CommandContext(ctx, "python3", args...)
	} else {
		return false, false, false, "", nil
	}
	cmd.Dir = dir
	cmd.Env = ReplayChildEnv(cfg, "TILLR_FORK_DRIFT=1")
	replayClass := procguard.ProcessClassGate
	if drainmodel.IsRevertItem(cfg.ItemID) || drainmodel.IsTrunkFixer(cfg.ItemID) {
		replayClass = procguard.ProcessClassRepair
	}
	if err := cfg.Priority.ApplyToCmd(cmd, replayClass, cfg.SelfExe); err != nil {
		return true, false, false, "", err
	}
	var stderr drainmodel.StderrTail
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = io.MultiWriter(&combined, &stderr)
	runErr := RunCheckGroupStarted(cmd, nil)
	if ctx.Err() != nil {
		return true, false, false, combined.String(), ctx.Err()
	}
	if cmd.ProcessState == nil {
		return true, false, false, combined.String(), drainmodel.Externalf("replaying %s on %s: %v: %s",
			check, boundary.ShortSHA(rev), runErr, stderr.String())
	}
	code := cmd.ProcessState.ExitCode()
	classified := drainmodel.ClassifyCheck(checkresult.Observation{
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

func DiffChangedPaths(ctx context.Context, root, baseSHA, headSHA string) []string {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "diff", "--name-only", baseSHA, headSHA)
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

// LeafGoTests drops parent test names when a subtest of that parent is also listed
// (matching go_affected.py's _leaf_failed_tests), so replaying a failing subtest
// does not re-run every sibling subtest under its parent.
func LeafGoTests(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		hasChild := false
		prefix := name + "/"
		for _, other := range names {
			if strings.HasPrefix(other, prefix) {
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

func RunNamedGoTestsInReplay(ctx context.Context, cfg ReplayConfig, dir, rev, check string, pkgs map[string][]string) (bool, string, error) {
	modules := IndexReplayGoModules(dir)
	pkgNames := make([]string, 0, len(pkgs))
	for pkg := range pkgs {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	parent := cfg.LaneRoot
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
		modDir, rel, ok := ResolveReplayGoPackage(modules, pkg)
		if !ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(modDir, rel)); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return false, combined.String(), err
		}
		config := filepath.Join(scratch, "gitconfig")
		if err := PrivateGoReplayGitConfig(ctx, modDir, config); err != nil {
			return false, combined.String(), err
		}
		var topLevel []string
		var subtests []string
		for _, name := range LeafGoTests(pkgs[pkg]) {
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
			cmd.Env = ReplayChildEnv(cfg, "CGO_ENABLED=0", "TMPDIR="+scratch, "YOLO_DURABLE_DIR="+scratch, "GIT_CONFIG_GLOBAL="+config)
			replayClass := procguard.ProcessClassGate
			if drainmodel.IsRevertItem(cfg.ItemID) || drainmodel.IsTrunkFixer(cfg.ItemID) {
				replayClass = procguard.ProcessClassRepair
			}
			if err := cfg.Priority.ApplyToCmd(cmd, replayClass, cfg.SelfExe); err != nil {
				return false, combined.String(), err
			}
			var stderr drainmodel.StderrTail
			cmd.Stdout = &combined
			cmd.Stderr = io.MultiWriter(&combined, &stderr)
			runErr := RunCheckGroupStarted(cmd, nil)
			if ctx.Err() != nil {
				return false, combined.String(), ctx.Err()
			}
			if cmd.ProcessState == nil {
				return false, combined.String(), drainmodel.Externalf("replaying %s (%s %s) on %s: %v: %s",
					check, pkg, pattern, boundary.ShortSHA(rev), runErr, stderr.String())
			}
			if cmd.ProcessState.ExitCode() != 0 {
				red = true
			}
		}
	}
	return red, combined.String(), nil
}
