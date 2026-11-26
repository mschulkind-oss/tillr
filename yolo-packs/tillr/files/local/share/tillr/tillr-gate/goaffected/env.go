package goaffected

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// StrippedOuterEnv returns a copy of the outer gate, drain, and git-override
// environment variable names stripped from child `go` and `git` processes.
func StrippedOuterEnv() []string {
	out := make([]string, len(strippedOuterEnv))
	copy(out, strippedOuterEnv)
	return out
}

// SharedCacheDir returns the resolved Go build cache directory for envMap
// (or os.Environ() when envMap is nil).
func SharedCacheDir(envMap map[string]string) string {
	if envMap == nil {
		envMap = envSliceToMap(os.Environ())
	}
	return sharedCacheDir(envMap)
}

// IsGitShim reports whether p is a yolo/tillr git wrapper or block shim.
func IsGitShim(p string) bool {
	return isGitShim(p)
}

// IsTillrGitGuard reports whether p is a tillr-git-guard shim script.
func IsTillrGitGuard(p string) bool {
	return isTillrGitGuard(p)
}

// ResolveRealGit finds the first non-shim git executable on pathValue and
// reports whether any git shim was encountered along the way.
func ResolveRealGit(pathValue string) (string, bool) {
	return resolveRealGit(pathValue)
}

// EnsureGitBinDir creates a stable symlink directory for realGit under baseDir.
func EnsureGitBinDir(baseDir, realGit string) (string, error) {
	return ensureGitBinDir(baseDir, realGit)
}

// BypassGitShimEnv returns a copy of baseEnv with git shims bypassed on PATH.
func BypassGitShimEnv(baseEnv map[string]string, candidateRoots ...string) map[string]string {
	cloned := make(map[string]string, len(baseEnv))
	for k, v := range baseEnv {
		cloned[k] = v
	}
	bypassGitShimEnv(cloned, candidateRoots...)
	return cloned
}

// ClearDirectoryContents removes all entries inside dir without removing dir itself.
func ClearDirectoryContents(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		_ = os.RemoveAll(filepath.Join(dir, ent.Name()))
	}
}

// AcquireInvocationScratch acquires an exclusive per-invocation scratch directory
// without losing Go test cache hits. Non-concurrent runs acquire slot-0 (a stable path);
// concurrent runs acquire distinct numbered slots (slot-0, slot-1, ...).
func AcquireInvocationScratch(durable, scratchKey string) (scratchDir, outDir string, release func(), err error) {
	if durable == "" {
		tmp, err := os.MkdirTemp("", "go-affected-scratch-")
		if err != nil {
			return "", "", nil, err
		}
		return tmp, tmp, func() { _ = os.RemoveAll(tmp) }, nil
	}

	sum := sha256.Sum256([]byte(scratchKey))
	slug := hex.EncodeToString(sum[:])[:12]
	poolDir := filepath.Join(durable, ".go-affected-scratch", slug)
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		return "", "", nil, err
	}

	slot := 0
	var lockFile *os.File
	for {
		lockPath := filepath.Join(poolDir, fmt.Sprintf("slot-%d.lock", slot))
		f, oerr := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o666)
		if oerr != nil {
			return "", "", nil, oerr
		}
		ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if ferr == nil {
			lockFile = f
			break
		}
		_ = f.Close()
		if errors.Is(ferr, syscall.EWOULDBLOCK) || errors.Is(ferr, syscall.EAGAIN) {
			slot++
			continue
		}
		return "", "", nil, ferr
	}

	scratch := filepath.Join(poolDir, fmt.Sprintf("slot-%d", slot))
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return "", "", nil, err
	}
	ClearDirectoryContents(scratch)

	rel := func() {
		ClearDirectoryContents(scratch)
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}
	return scratch, poolDir, rel, nil
}

// GlobalGitConfig reads global-scope git config key/value pairs, skipping include.path
// and includeIf.* entries.
func GlobalGitConfig(env []string) ([][2]string, error) {
	var envMap map[string]string
	if env == nil {
		envMap = envSliceToMap(os.Environ())
	} else {
		envMap = envSliceToMap(env)
	}
	gitEnvMap := BypassGitShimEnv(envMap, SharedCacheDir(envMap))
	gitBin, err := lookPathGit(gitEnvMap["PATH"])
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(gitBin, "config", "--list", "--includes", "--show-scope", "--null")
	cmd.Env = envMapToSlice(gitEnvMap)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimRight(out, "\x00")
	if len(trimmed) == 0 {
		return nil, nil
	}
	fields := bytes.Split(trimmed, []byte{0})
	var values [][2]string
	for i := 0; i+1 < len(fields); i += 2 {
		scope := string(fields[i])
		if scope != "global" {
			continue
		}
		entry := string(fields[i+1])
		key, val, hasSep := strings.Cut(entry, "\n")
		lower := strings.ToLower(key)
		if lower == "include.path" || strings.HasPrefix(lower, "includeif.") {
			continue
		}
		if !hasSep {
			val = "true"
		}
		values = append(values, [2]string{key, val})
	}
	return values, nil
}

// WritePrivateGitconfig writes a private gitconfig file in scratchDir containing
// gitValues while disabling maintenance.auto and gc.auto.
func WritePrivateGitconfig(scratchDir string, gitValues [][2]string, env []string) (string, error) {
	configPath := filepath.Join(scratchDir, "gitconfig")
	if err := os.WriteFile(configPath, nil, 0o644); err != nil {
		return "", err
	}
	var envMap map[string]string
	if env == nil {
		envMap = envSliceToMap(os.Environ())
	} else {
		envMap = envSliceToMap(env)
	}
	gitEnvMap := BypassGitShimEnv(envMap, SharedCacheDir(envMap), scratchDir)
	gitEnv := envMapToSlice(gitEnvMap)
	var gitBin string

	for _, kv := range gitValues {
		key, value := kv[0], kv[1]
		lower := strings.ToLower(key)
		if lower == "maintenance.auto" || lower == "gc.auto" {
			continue
		}
		if gitBin == "" {
			var lerr error
			gitBin, lerr = lookPathGit(gitEnvMap["PATH"])
			if lerr != nil {
				return "", lerr
			}
		}
		cmd := exec.Command(gitBin, "config", "--file", configPath, "--add", key, value)
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git config --add %s: %w: %s", key, err, strings.TrimSpace(string(out)))
		}
	}

	f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString("[maintenance]\n\tauto = false\n[gc]\n\tauto = 0\n")
	cerr := f.Close()
	if werr != nil {
		return "", werr
	}
	if cerr != nil {
		return "", cerr
	}
	return configPath, nil
}

func lookPathGit(pathValue string) (string, error) {
	for _, entry := range filepath.SplitList(pathValue) {
		if entry == "" {
			continue
		}
		candidate := filepath.Join(entry, "git")
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("git executable not found in bypassed PATH %q", pathValue)
}

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func envMapToSlice(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}
