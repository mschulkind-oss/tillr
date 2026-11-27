package native

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

const (
	defaultBashTimeout = 120 * time.Second
	maxBashTimeout     = 600 * time.Second
	maxBashOutputBytes = 64 * 1024
	gitStatusTimeout   = 5 * time.Second
)

// BashConfig holds optional subprocess confinement overrides for the native
// bash tool (Slice 4, D-NAB3 / D-NAB4). Zero values fall back to FencePolicy
// fields and process defaults.
type BashConfig struct {
	RepoRoot       string
	TraceDir       string
	BashShimPath   string
	NonLanding     bool
	BaseEnv        []string
	DefaultTimeout time.Duration
	MaxTimeout     time.Duration
	MaxOutputBytes int
	DisableMountNS bool
}

type bashArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// IsSecretEnvKey reports whether an environment variable name holds a provider
// credential or secret that must be scrubbed from bash tool children (D-NAB4).
func IsSecretEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return false
	}
	switch {
	case strings.HasPrefix(upper, "AWS_ACCESS_KEY_ID"),
		strings.HasPrefix(upper, "AWS_SECRET_ACCESS_KEY"),
		strings.HasPrefix(upper, "AWS_SESSION_TOKEN"),
		strings.HasPrefix(upper, "AWS_BEARER_TOKEN_BEDROCK"),
		upper == "GITHUB_TOKEN",
		upper == "GH_TOKEN",
		strings.HasPrefix(upper, "COPILOT_"),
		strings.Contains(upper, "_API_KEY"),
		strings.Contains(upper, "_SECRET"),
		strings.Contains(upper, "_TOKEN"):
		return true
	default:
		return false
	}
}

// ScrubBashEnv returns a copy of env with provider credentials and secret
// variables removed while preserving non-secret TILLR_* run markers.
func ScrubBashEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, hasEq := strings.Cut(entry, "=")
		if !hasEq {
			continue
		}
		if IsSecretEnvKey(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func setEnvKey(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, prefix+value)
}

func getEnvKey(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			val = entry[len(prefix):]
		}
	}
	return val
}

func removeEnvKey(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// tailBuffer keeps the last maxBytes written to it.
type tailBuffer struct {
	mu        sync.Mutex
	maxBytes  int
	buf       []byte
	truncated bool
}

func newTailBuffer(maxBytes int) *tailBuffer {
	if maxBytes <= 0 {
		maxBytes = maxBashOutputBytes
	}
	return &tailBuffer{maxBytes: maxBytes}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	if n >= b.maxBytes {
		b.buf = append(b.buf[:0], p[n-b.maxBytes:]...)
		b.truncated = true
		return n, nil
	}
	if len(b.buf)+n > b.maxBytes {
		drop := len(b.buf) + n - b.maxBytes
		b.buf = append(b.buf[drop:], p...)
		b.truncated = true
		return n, nil
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.truncated {
		return string(b.buf)
	}
	return fmt.Sprintf("[output truncated; showing last %d bytes]\n%s", b.maxBytes, string(b.buf))
}

func (t *Toolset) execBash(ctx context.Context, args bashArgs) (string, bool) {
	if strings.TrimSpace(args.Command) == "" {
		return "bash: command is required", true
	}

	defaultTO := t.bash.DefaultTimeout
	if defaultTO <= 0 {
		defaultTO = defaultBashTimeout
	}
	maxTO := t.bash.MaxTimeout
	if maxTO <= 0 {
		maxTO = maxBashTimeout
	}
	timeout := defaultTO
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
		if timeout > maxTO {
			timeout = maxTO
		}
	}

	workDir := strings.TrimSpace(t.policy.WorkDir)
	if workDir == "" {
		if wd, err := os.Getwd(); err == nil {
			workDir = wd
		}
	}

	var baseEnv []string
	if t.bash.BaseEnv != nil {
		baseEnv = append([]string(nil), t.bash.BaseEnv...)
	} else {
		baseEnv = os.Environ()
	}

	// 1. Scrub provider secrets from child environment (D-NAB4).
	env := ScrubBashEnv(baseEnv)

	// 2. Assign a per-call TILLR_RUN_EXEC_ID so background grandchildren can be
	//    reaped deterministically when the bash call finishes or times out.
	callExecID, err := run.NewExecID()
	if err != nil {
		return fmt.Sprintf("bash: generating exec id: %v", err), true
	}
	env = setEnvKey(env, "TILLR_RUN_EXEC_ID", callExecID)

	// 3. Ensure trace env and GIT_CEILING_DIRECTORIES are propagated.
	traceDir := strings.TrimSpace(t.bash.TraceDir)
	if traceDir == "" {
		traceDir = strings.TrimSpace(getEnvKey(env, "TILLR_TRACE_DIR"))
	} else {
		env = setEnvKey(env, "TILLR_TRACE_DIR", traceDir)
	}
	repoRoot := strings.TrimSpace(t.bash.RepoRoot)
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(getEnvKey(env, "TILLR_REPO_ROOT"))
	} else {
		env = setEnvKey(env, "TILLR_REPO_ROOT", repoRoot)
	}
	if getEnvKey(env, "GIT_CEILING_DIRECTORIES") == "" && workDir != "" {
		ceiling := filepath.Dir(workDir)
		if repoRoot != "" {
			ceiling = filepath.Dir(repoRoot)
		}
		env = setEnvKey(env, "GIT_CEILING_DIRECTORIES", ceiling)
	}

	bashShim := strings.TrimSpace(t.bash.BashShimPath)
	if bashShim == "" {
		if existing := strings.TrimSpace(getEnvKey(env, "BASH_ENV")); existing != "" {
			if info, serr := os.Stat(existing); serr == nil && !info.IsDir() {
				bashShim = existing
			}
		}
	}
	if bashShim != "" {
		if info, serr := os.Stat(bashShim); serr == nil && !info.IsDir() {
			env = setEnvKey(env, "BASH_ENV", bashShim)
		} else {
			bashShim = ""
		}
	}

	// 4. Resolve shell binary (prefer bash so BASH_ENV / bash-shim.sh runs).
	shellPath := resolveShellPath(getEnvKey(env, "PATH"))
	if shellPath == "" {
		return "bash: neither bash nor sh found in PATH", true
	}

	// 5. Apply read-only or writer confinement.
	readOnly := t.policy.ReadOnly && !t.policy.planQuickWayUnlocked()
	durableDir := strings.TrimSpace(t.policy.DurableDir)
	artifactsDir := strings.TrimSpace(t.policy.ArtifactsDir)
	nonLanding := t.bash.NonLanding || getEnvKey(env, "TILLR_NON_LANDING") == "1"

	var tempGuardDir string
	guardDurableDir := durableDir
	if guardDurableDir == "" {
		if tmp, terr := os.MkdirTemp("", "tillr-native-bash-guard-*"); terr == nil {
			tempGuardDir = tmp
			guardDurableDir = tmp
		}
	}
	if tempGuardDir != "" {
		defer os.RemoveAll(tempGuardDir)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.Command(shellPath, "-c", args.Command)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if readOnly {
		usedMountNS := false
		if !t.bash.DisableMountNS && run.CanUseReadOnlyMountNS() {
			if !nonLanding && workDir != "" && guardDurableDir != "" {
				var gerr error
				env, gerr = run.InstallGitGuard(workDir, guardDurableDir, env)
				if gerr != nil {
					return fmt.Sprintf("bash: installing git guard: %v", gerr), true
				}
			}
			cmd.Env = env
			mounts := buildReadOnlyMounts(repoRoot, workDir, durableDir, artifactsDir, traceDir, env)
			ok, merr := run.PrepareReadOnlyMountCmd(cmd, run.ReadOnlyMountConfig{
				TargetPath: shellPath,
				TargetArgs: []string{"-c", args.Command},
				WorkDir:    workDir,
				Mounts:     mounts,
			})
			if merr != nil {
				return fmt.Sprintf("bash: preparing read-only mount namespace: %v", merr), true
			}
			usedMountNS = ok
		}
		if !usedMountNS {
			// Fallback when unprivileged CLONE_NEWNS is unavailable: install
			// preventative triage PATH and BASH_ENV shims.
			var terr error
			env, terr = run.InstallTriageGuard(guardDurableDir, env)
			if terr != nil {
				return fmt.Sprintf("bash: installing read-only guard: %v", terr), true
			}
			// If bash-shim.sh is active, wire BASH_ENV to it so both proc.exec
			// tracing and TILLR_SEAT_BASH_ENV (sourced by bash-shim.sh) run.
			if bashShim != "" {
				env = setEnvKey(env, "BASH_ENV", bashShim)
			}
			cmd.Env = env
		}
	} else {
		if !nonLanding && workDir != "" && guardDurableDir != "" {
			var gerr error
			env, gerr = run.InstallGitGuard(workDir, guardDurableDir, env)
			if gerr != nil {
				return fmt.Sprintf("bash: installing git guard: %v", gerr), true
			}
		}
		cmd.Env = env
	}

	// 6. Wire a direct OS pipe so Process.Wait returns as soon as the shell
	//    exits, allowing EndExecProcesses to reap any grandchildren still
	//    holding stdout/stderr open before the reader goroutine drains to EOF.
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Sprintf("bash: creating output pipe: %v", err), true
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return fmt.Sprintf("bash: starting shell: %v", err), true
	}
	pw.Close()

	pid := cmd.Process.Pid
	outBuf := newTailBuffer(t.bash.MaxOutputBytes)
	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		_, _ = io.Copy(outBuf, pr)
		_ = pr.Close()
	}()

	waitCh := make(chan struct {
		state *os.ProcessState
		err   error
	}, 1)
	go func() {
		st, werr := cmd.Process.Wait()
		waitCh <- struct {
			state *os.ProcessState
			err   error
		}{state: st, err: werr}
	}()

	var (
		procState *os.ProcessState
		waitErr   error
		timedOut  bool
	)
	select {
	case res := <-waitCh:
		procState = res.state
		waitErr = res.err
	case <-cmdCtx.Done():
		timedOut = true
		_ = run.EndExecProcesses(pid, callExecID)
		res := <-waitCh
		procState = res.state
		waitErr = res.err
	}

	// Always reap the process group and any background grandchildren carrying
	// TILLR_RUN_EXEC_ID=callExecID before draining the output pipe.
	_ = run.EndExecProcesses(pid, callExecID)
	<-copyDone

	output := outBuf.String()

	// 7. On writer runs, check git status against fence.txt and append a
	//    prominent warning if the command dirtied an out-of-fence path.
	warning := ""
	if !readOnly && workDir != "" {
		warning = t.checkOutOfFenceDirty(ctx, workDir)
	}

	var b strings.Builder
	if output != "" {
		b.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			b.WriteByte('\n')
		}
	}
	if warning != "" {
		b.WriteString(warning)
		if !strings.HasSuffix(warning, "\n") {
			b.WriteByte('\n')
		}
	}

	if timedOut {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(&b, "bash: command timed out after %s", timeout)
		} else {
			fmt.Fprintf(&b, "bash: command canceled: %v", cmdCtx.Err())
		}
		return strings.TrimRight(b.String(), "\n"), true
	}
	if waitErr != nil {
		fmt.Fprintf(&b, "bash: waiting for shell: %v", waitErr)
		return strings.TrimRight(b.String(), "\n"), true
	}
	if procState != nil && !procState.Success() {
		fmt.Fprintf(&b, "bash: exit status %d", procState.ExitCode())
		return strings.TrimRight(b.String(), "\n"), true
	}
	return strings.TrimRight(b.String(), "\n"), false
}

func resolveShellPath(pathVal string) string {
	for _, name := range []string{"bash", "sh"} {
		for _, dir := range filepath.SplitList(pathVal) {
			if dir == "" {
				continue
			}
			cand := filepath.Join(dir, name)
			info, err := os.Stat(cand)
			if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				if abs, aerr := filepath.Abs(cand); aerr == nil {
					return abs
				}
				return cand
			}
		}
		if p, err := exec.LookPath(name); err == nil {
			if abs, aerr := filepath.Abs(p); aerr == nil {
				return abs
			}
			return p
		}
	}
	return ""
}

func resolveExistingDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(abs)
}

func buildReadOnlyMounts(repoRoot, workDir, durableDir, artifactsDir, traceDir string, env []string) []run.MountOp {
	var mounts []run.MountOp

	resRepo := resolveExistingDir(repoRoot)
	resWork := resolveExistingDir(workDir)

	// 1. Mount repoRoot read-only first when distinct from workDir.
	if resRepo != "" && resRepo != resWork {
		mounts = append(mounts, run.MountOp{Src: resRepo, Dst: resRepo, ReadOnly: true})
	}

	// 2. Re-mount writable directories that live under repoRoot (outside workDir).
	if resRepo != "" && resRepo != resWork {
		var rwRepo []string
		addRWUnderRepo := func(dir string) {
			resolved := resolveExistingDir(dir)
			if resolved == "" || resolved == resWork {
				return
			}
			if _, ok := withinDir(resolved, resRepo); !ok {
				return
			}
			// Skip if strictly inside workDir; Step 4 handles that after workDir is mounted RO.
			if resWork != "" {
				if rel, ok := withinDir(resolved, resWork); ok && rel != "." {
					return
				}
			}
			for _, existing := range rwRepo {
				if existing == resolved {
					return
				}
			}
			rwRepo = append(rwRepo, resolved)
			mounts = append(mounts, run.MountOp{Src: resolved, Dst: resolved, ReadOnly: false})
		}
		addRWUnderRepo(getEnvKey(env, "TMPDIR"))
		addRWUnderRepo("/tmp")
		addRWUnderRepo(getEnvKey(env, "GOCACHE"))
		addRWUnderRepo(durableDir)
		addRWUnderRepo(artifactsDir)
		addRWUnderRepo(traceDir)
	}

	// 3. Mount workDir read-only after any ancestor writable mounts (such as TMPDIR)
	//    so workDir is always enforced read-only by the kernel.
	if resWork != "" {
		mounts = append(mounts, run.MountOp{Src: resWork, Dst: resWork, ReadOnly: true})

		// 4. Re-mount any writable dirs that are strictly inside workDir.
		var rwWork []string
		addRWUnderWork := func(dir string) {
			resolved := resolveExistingDir(dir)
			if resolved == "" {
				return
			}
			rel, ok := withinDir(resolved, resWork)
			if !ok || rel == "." {
				return
			}
			for _, existing := range rwWork {
				if existing == resolved {
					return
				}
			}
			rwWork = append(rwWork, resolved)
			mounts = append(mounts, run.MountOp{Src: resolved, Dst: resolved, ReadOnly: false})
		}
		addRWUnderWork(durableDir)
		addRWUnderWork(artifactsDir)
		addRWUnderWork(traceDir)
	}

	return mounts
}

func (t *Toolset) checkOutOfFenceDirty(ctx context.Context, workDir string) string {
	prefixes, err := t.policy.loadFencePrefixes()
	if err != nil || len(prefixes) == 0 {
		return ""
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	gCtx, cancel := context.WithTimeout(ctx, gitStatusTimeout)
	defer cancel()

	cmd := exec.CommandContext(gCtx, gitBin, "-C", workDir, "status", "--porcelain", "-z", "--untracked-files=all")
	env := os.Environ()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE"} {
		env = removeEnvKey(env, key)
	}
	env = setEnvKey(env, "GIT_OPTIONAL_LOCKS", "0")
	cmd.Env = env

	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	dirty := parsePorcelainZ(out)
	seen := make(map[string]bool)
	var outside []string
	for _, rel := range dirty {
		clean := filepath.ToSlash(filepath.Clean(rel))
		if clean == "" || clean == "." || seen[clean] {
			continue
		}
		seen[clean] = true
		if !InsideFence(clean, prefixes) {
			outside = append(outside, clean)
		}
	}
	if len(outside) == 0 {
		return ""
	}
	sort.Strings(outside)
	return fmt.Sprintf(
		"WARNING: out-of-fence dirty files detected after bash command: %s\nPermitted fence prefixes: %s\nRevert or remove out-of-fence files before committing.",
		strings.Join(outside, ", "),
		strings.Join(prefixes, ", "),
	)
}

func parsePorcelainZ(out []byte) []string {
	var paths []string
	parts := bytes.Split(out, []byte{0})
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		x, y := entry[0], entry[1]
		if p := string(entry[3:]); p != "" {
			paths = append(paths, p)
		}
		if (x == 'R' || x == 'C' || y == 'R' || y == 'C') && i+1 < len(parts) {
			i++
			if orig := string(parts[i]); orig != "" {
				paths = append(paths, orig)
			}
		}
	}
	return paths
}
